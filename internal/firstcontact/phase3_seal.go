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

// ResponseWaiter blocks until <volume>/<relPath> appears with non-empty
// content, or until timeout / context cancellation.
type ResponseWaiter func(ctx context.Context, slug, relPath string, timeout time.Duration) ([]byte, error)

// ContactAdder records the new MindForm in the operator's host
// contacts. The wizard wires this to a sanctioned bootstrap exception
// (direct state.db write) when the host gate daemon is not running;
// production may swap in an IPC-method call once that path is added.
type ContactAdder func(ctx context.Context, npub, label, relay string) error

// Phase3Deps are the inputs Phase3 cannot derive from Summoning.
type Phase3Deps struct {
	DockerClient   forgectl.Client
	Image          string
	WriteVolume    VolumeWriter
	ContainerStart func(ctx context.Context, slug string) error
	ResponseWait   ResponseWaiter
	AddContact     ContactAdder
}

// RenderSummoningBook produces the markdown preview shown at 封缄. It
// is also the body baked into journal/0000-summoning.md by
// ontology.TarStream when the wizard calls forge.Orchestrate.
func RenderSummoningBook(s *Summoning) string {
	var sb strings.Builder
	if s.Lang == "zh" {
		sb.WriteString("# 召唤书\n\n")
		fmt.Fprintf(&sb, "签者：%s（%s）\n", s.OperatorLabel, s.OperatorNpub)
		fmt.Fprintf(&sb, "日期：%s\n\n", s.StartedAt.UTC().Format("2006-01-02"))
		sb.WriteString(s.Displaying)
		sb.WriteString("\n\n")
		fmt.Fprintf(&sb, "我以「%s」之名，召之而来。\n", s.SummonedName)
		fmt.Fprintf(&sb, "被召之者将栖于 %s。\n", s.MindFormNpub)
	} else {
		sb.WriteString("# Summoning Book\n\n")
		fmt.Fprintf(&sb, "Signer: %s (%s)\n", s.OperatorLabel, s.OperatorNpub)
		fmt.Fprintf(&sb, "Date: %s\n\n", s.StartedAt.UTC().Format("2006-01-02"))
		sb.WriteString(s.Displaying)
		sb.WriteString("\n\n")
		fmt.Fprintf(&sb, "By the name \"%s,\" I summon thee.\n", s.SummonedName)
		fmt.Fprintf(&sb, "The summoned shall dwell at %s.\n", s.MindFormNpub)
	}
	return sb.String()
}

// PollResponseFile is a host-side ResponseWaiter implementation: it
// polls a path on the local filesystem (e.g. a bind-mounted volume
// inspector) every interval until the file appears with non-empty
// content or the timeout elapses. cmd/eidos/summon picks a different
// path that talks to docker.
func PollResponseFile(ctx context.Context, path string, timeout, interval time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		body, err := os.ReadFile(path)
		if err == nil && len(body) > 0 {
			return body, nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for %s", path)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// Phase3 runs seal → calling-words → response. Returns the rendered
// response body the caller can hand to Typewriter.
func Phase3(ctx context.Context, s *Summoning, r render.Renderer, c *Claude, ready <-chan ReadyState, d Phase3Deps) ([]byte, error) {
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
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase3_seal_q"), []render.ChoiceOption{
		{Label: stringFor(s.Lang, "phase3_seal")},
		{Label: stringFor(s.Lang, "phase3_quit")},
	})
	if err != nil {
		return nil, err
	}
	if idx == 1 {
		return nil, errors.New("operator quit at seal")
	}

	// Orchestrate the volume + container with the rendered book in tar.
	createOpts := forge.CreateOpts{
		Owner:        s.OperatorNpub,
		Relay:        s.HomeRelay,
		Label:        s.SummonedName,
		Image:        d.Image,
		KeyHex:       s.MindFormKeyHex,
		JournalEntry: book,
		NoLogin:      true,
	}
	if err := forge.Orchestrate(ctx, d.DockerClient, s.Slug, createOpts); err != nil {
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

	// Calling-words.
	st := r.Status(stringFor(s.Lang, "phase3_words_status"))
	words, err := c.CallText(ctx, buildCallingWordsPrompt(book, s.Lang))
	st.Stop()
	if err != nil {
		purge()
		return nil, fmt.Errorf("calling-words: %w", err)
	}
	s.CallingWords = words
	r.Show(stringFor(s.Lang, "phase3_words_lead"))
	r.Typewriter(ctx, words)

	// Write calling-words and birth.json into the volume.
	if err := d.WriteVolume(ctx, s.Slug, "ontology/essence/calling-words.md", []byte(words)); err != nil {
		purge()
		return nil, fmt.Errorf("write calling-words: %w", err)
	}
	birth := wake.BirthSignal{
		V:                 wake.BirthSchemaVersion,
		OperatorNpub:      s.OperatorNpub,
		SummoningBookPath: "/eidos/ontology/journal/0000-summoning.md",
		CallingWordsPath:  "/eidos/ontology/essence/calling-words.md",
		ResponsePath:      "/eidos/ontology/journal/0000-response.md",
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

	st2 := r.Status(stringFor(s.Lang, "phase3_response_wait"))
	body, err := d.ResponseWait(ctx, s.Slug, "ontology/journal/0000-response.md", 90*time.Second)
	st2.Stop()
	if err != nil {
		purge()
		return nil, fmt.Errorf("birth response: %w", err)
	}

	// Add MindForm to the operator's host contacts (bootstrap exception).
	if d.AddContact != nil {
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
				r.Show(stringFor(lang, "phase3_wait"))
				hint = true
			}
		}
	}
}

func buildCallingWordsPrompt(book, lang string) string {
	return fmt.Sprintf(`You are about to write the operator's calling-words — a single short summoning incantation, 1 to 3 sentences, that will be the FIRST words the operator speaks to the new mind-form. Tone: solemn, contextual, words of arrival.

The summoning book the operator wrote:

%s

Constraints:
- 1–3 sentences only
- Address the to-be-summoned directly
- Echo (do NOT repeat verbatim) the displaying paragraph's imagery
- Language: %s

Return ONLY the incantation; no preamble.`, book, lang)
}
