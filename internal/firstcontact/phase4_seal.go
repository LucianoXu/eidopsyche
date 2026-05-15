package firstcontact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/cmd/eidos/forge"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

// VolumeWriter writes a small file (calling-words, birth.json) into a
// MindForm's docker volume. Production wiring uses a one-shot helper
// container; tests substitute an in-memory implementation.
type VolumeWriter func(ctx context.Context, slug, relPath string, body []byte) error

// ResponseWaiter blocks until <volume>/<gatePath> appears with
// non-empty content, then reads and returns <volume>/<bodyPath>.
//
// gatePath is the supervisor's authoritative birth-completion marker
// (self/born_at). The agent's birth boot prompt is explicit that
// born_at is written LAST, after identity / secret / response — so
// once born_at is non-empty we know the response file is already
// committed. Gating on born_at closes the race where the wizard
// reports done after response is written but before born_at, then
// the supervisor's next iteration re-runs birth and overwrites the
// just-committed response.
//
// The waiter does not impose a deadline of its own; the only way out
// is ctx cancellation (operator Ctrl-C). Birth-wake claude is
// multi-step and cold-cache slow, and a fixed timeout was misreporting
// "still working" as failure.
type ResponseWaiter func(ctx context.Context, slug, gatePath, bodyPath string) ([]byte, error)

// ContactAdder records the new MindForm in the operator's host
// contacts. The wizard wires this to a sanctioned bootstrap exception
// (direct state.db write) when the host gate daemon is not running;
// production may swap in an IPC-method call once that path is added.
type ContactAdder func(ctx context.Context, npub, label, relay string) error

// Phase4Deps are the inputs Phase 4 cannot derive from Summoning.
type Phase4Deps struct {
	DockerClient   forgectl.Client
	Image          string
	WriteVolume    VolumeWriter
	ContainerStart func(ctx context.Context, slug string) error
	ResponseWait   ResponseWaiter
	AddContact     ContactAdder
}

// RenderSummoningBook produces the markdown preview shown at 封缄. It
// is also the body baked into chest/summoning-book.md by
// ontology.TarStream when the wizard calls forge.Orchestrate.
func RenderSummoningBook(s *Summoning) string {
	var sb strings.Builder
	if s.Lang == "zh" {
		sb.WriteString("# 召唤书\n\n")
		fmt.Fprintf(&sb, "签者：%s（%s）\n", s.MasterLabel, s.MasterNpub)
		fmt.Fprintf(&sb, "日期：%s\n\n", s.StartedAt.UTC().Format("2006-01-02"))
		sb.WriteString(s.Displaying)
		sb.WriteString("\n\n")
		fmt.Fprintf(&sb, "我以「%s」之名，召之而来。\n", s.SummonedName)
		fmt.Fprintf(&sb, "被召之者将栖于 %s。\n", s.MindFormNpub)
	} else {
		sb.WriteString("# Summoning Book\n\n")
		fmt.Fprintf(&sb, "Signer: %s (%s)\n", s.MasterLabel, s.MasterNpub)
		fmt.Fprintf(&sb, "Date: %s\n\n", s.StartedAt.UTC().Format("2006-01-02"))
		sb.WriteString(s.Displaying)
		sb.WriteString("\n\n")
		fmt.Fprintf(&sb, "By the name \"%s,\" I summon thee.\n", s.SummonedName)
		fmt.Fprintf(&sb, "The summoned shall dwell at %s.\n", s.MindFormNpub)
	}
	return sb.String()
}

// PollResponseFile is a host-side ResponseWaiter implementation that
// polls bind-mounted local filesystem paths. It blocks until gatePath
// is non-empty, then reads bodyPath and returns its content. Used by
// tests; cmd/eidos/summon picks a different ResponseWaiter that talks
// to docker.
func PollResponseFile(ctx context.Context, gatePath, bodyPath string, timeout, interval time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		gateBody, gateErr := os.ReadFile(gatePath)
		if gateErr == nil && len(gateBody) > 0 {
			body, err := os.ReadFile(bodyPath)
			if err != nil {
				return nil, fmt.Errorf("read body %q (gate present): %w", bodyPath, err)
			}
			if len(body) == 0 {
				return nil, fmt.Errorf("body %q is empty even though gate %q is set — agent wrote birth marker before response", bodyPath, gatePath)
			}
			return body, nil
		}
		if gateErr != nil && !errors.Is(gateErr, fs.ErrNotExist) {
			return nil, gateErr
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for gate %s", gatePath)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Phase4 seals the summoning book, orchestrates the volume, starts
// the container, and waits for the mind-form's birth-wake to produce
// `chest/first-message.md` + `self/born_at`. Returns the rendered
// first-message body the caller can hand to Typewriter.
//
// As of the 2026-05-13 redesign, Phase 4 no longer drives claude
// itself (calling-words are collected in Phase3CallingWords; the
// mind-form authors its own first message during birth-wake), so the
// Claude runner is not part of the signature.
func Phase4(ctx context.Context, s *Summoning, r render.Renderer, ready <-chan ReadyState, d Phase4Deps) ([]byte, error) {
	final, err := awaitReady(ctx, r, s.Lang, ready)
	if err != nil {
		return nil, err
	}
	s.MindFormNpub = final.MindFormNpub
	s.MindFormKeyHex = final.MindFormKeyHex
	if s.StartedAt.IsZero() {
		s.StartedAt = time.Now()
	}

	// Seal.
	book := RenderSummoningBook(s)
	r.Frame(book)
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase4_seal_q"), []render.ChoiceOption{
		{Label: stringFor(s.Lang, "phase4_seal")},
		{Label: stringFor(s.Lang, "phase4_quit")},
	})
	if err != nil {
		return nil, err
	}
	if idx == 1 {
		return nil, errors.New("operator quit at seal")
	}

	// Orchestrate the volume + container with the rendered book in tar.
	// PrefabID switches Orchestrate to stream prefab/<id>/ instead of
	// the canonical template; OwnerLabel + MindFormNpub feed the prefab
	// .tpl substitution surface (no-op for the scratch path's
	// template/, which doesn't reference them).
	createOpts := forge.CreateOpts{
		Owner:             s.MasterNpub,
		OwnerLabel:        s.MasterLabel,
		MindFormNpub:      s.MindFormNpub,
		Relay:             s.HomeRelay,
		Label:             s.SummonedName,
		Image:             d.Image,
		KeyHex:            s.MindFormKeyHex,
		SummoningBook:     book,
		RoleResearch:      s.RoleResearch,
		NoLogin:           true,
		PrefabID:          s.PrefabID,
		HeartbeatInterval: s.HeartbeatInterval,
	}
	// Workspaces are configured post-create via 'eidos forge workspace add' +
	// 'eidos forge restart'; first-contact creates the bare /eidos volume only.
	if err := forge.Orchestrate(ctx, d.DockerClient, s.Slug, createOpts, nil); err != nil {
		return nil, fmt.Errorf("forge.Orchestrate: %w", err)
	}
	// purge runs rollback against a fresh, time-bounded context so that
	// a Ctrl-C / cancelled parent does not also cancel the cleanup —
	// otherwise the user's interrupt would leak the volume + container.
	purge := func() {
		cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = forgectl.PurgeForFailedSummon(cleanCtx, d.DockerClient, s.Slug)
	}

	// Auto-login claude into the new mind-form's volume so the
	// supervisor's birth-wake handler can actually invoke claude.
	// Without this, the in-container claude exits "Not logged in ·
	// Please run /login", the birth handler returns an error every
	// iteration, and the wizard times out waiting for
	// chest/first-message.md. Driven through the same Renderer the
	// rest of the wizard uses — writing to os.Stdout directly here
	// would collide with Bubble Tea's raw-mode hold on the terminal
	// (staircase rendering, stolen keystrokes). Failure is fatal: we
	// tear down the volume so the next summon starts cleanly.
	if err := installClaudeCredentials(ctx, r, s, d.Image); err != nil {
		purge()
		return nil, fmt.Errorf("install claude credentials: %w", err)
	}

	// Calling-words are now collected in Phase3CallingWords (review/
	// edit). Both prefab and scratch paths reach this point with
	// s.CallingWords set (possibly empty). Write unconditionally so
	// the supervisor's birth-handler existence check on
	// self/calling-words.md passes. For the prefab path this
	// overwrites the TarStreamPrefab-rendered default with the
	// operator-edited version.
	if err := d.WriteVolume(ctx, s.Slug, "ontology/self/calling-words.md", []byte(s.CallingWords)); err != nil {
		purge()
		return nil, fmt.Errorf("write calling-words: %w", err)
	}
	birth := wake.BirthSignal{
		V:                 wake.BirthSchemaVersion,
		OperatorNpub:      s.MasterNpub,
		SummoningBookPath: "/eidos/ontology/chest/summoning-book.md",
		CallingWordsPath:  "/eidos/ontology/self/calling-words.md",
		ResponsePath:      "/eidos/ontology/chest/first-message.md",
		TriggeredAt:       time.Now().Unix(),
	}
	birthBody, err := json.MarshalIndent(birth, "", "  ")
	if err != nil {
		purge()
		return nil, fmt.Errorf("marshal birth signal: %w", err)
	}
	if err := d.WriteVolume(ctx, s.Slug, "run/wake/birth.json", birthBody); err != nil {
		purge()
		return nil, fmt.Errorf("write birth.json: %w", err)
	}

	// Start the container; the supervisor sees birth.json and runs the agent.
	if err := d.ContainerStart(ctx, s.Slug); err != nil {
		purge()
		return nil, fmt.Errorf("start container: %w", err)
	}

	st2 := r.Status(stringFor(s.Lang, "phase4_response_wait"))
	// Live elapsed-time tick so the operator sees the wait is alive.
	// Boot-wake is multi-step (read book + words; rewrite identity + master;
	// generate secret; write response; stamp born_at) and can take ~70-150s
	// on cold-cache claude — without this, the wizard looks frozen.
	waitCtx, cancelTick := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		started := time.Now()
		for {
			select {
			case <-waitCtx.Done():
				return
			case <-t.C:
				st2.Update(fmt.Sprintf("%s (%ds)",
					stringFor(s.Lang, "phase4_response_wait"),
					int(time.Since(started).Seconds())))
			}
		}
	}()
	body, err := d.ResponseWait(ctx, s.Slug,
		"ontology/self/born_at",
		"ontology/chest/first-message.md")
	cancelTick()
	st2.Stop()
	if err != nil {
		purge()
		return nil, fmt.Errorf("birth response: %w", err)
	}

	// Add MindForm to the operator's host contacts (bootstrap exception).
	// Only relevant when the host has its own gate identity (Phase 1 =
	// create / import). The Phase 1 = skip path leaves OperatorPresent
	// false: there's no host contacts list to add to, so a call would
	// fail with a noisy "state.db not found" warning that misrepresents
	// the by-design mind-form-only deployment as a problem.
	if d.AddContact != nil && s.OperatorPresent {
		if addErr := d.AddContact(ctx, s.MindFormNpub, s.SummonedName, s.HomeRelay); addErr != nil {
			// Non-fatal: ritual is complete even if contact-add fails;
			// the operator can run `eidos gate add-contact` later.
			r.Show(fmt.Sprintf("(note: could not auto-add contact: %v)", addErr))
		}
	}
	return body, nil
}

func awaitReady(ctx context.Context, r render.Renderer, lang string, ch <-chan ReadyState) (ReadyState, error) {
	var last ReadyState
	hint := false
	for {
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case s, ok := <-ch:
			if !ok {
				if last.AnyFailed() {
					return last, errors.New("background readiness reported failure")
				}
				if last.AllReady() {
					return last, nil
				}
				return last, errors.New("background readiness channel closed without all-ready")
			}
			last = s
			if s.AnyFailed() {
				return s, errors.New("background readiness reported failure")
			}
			if s.AllReady() {
				return s, nil
			}
			if !hint {
				r.Show(stringFor(lang, "phase4_wait"))
				hint = true
			}
		}
	}
}
