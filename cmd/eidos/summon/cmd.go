// Package summon wires the First Contact wizard into a top-level
// `eidos summon` cobra command. Bare `eidos` (no subcommand, no
// state.db yet) auto-dispatches to summon.Run from main.go.
package summon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/cmd/eidos/forge"
	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/LucianoXu/eidopsyche/internal/store"
)

// Flags wired by Command(); read by Run().
var (
	flagMasterCard string
	flagKeyFile    string
)

// Command returns the cobra command. Registered in cmd/eidos/main.go.
// Bare `eidos` auto-dispatches via RunBare (different EntryMode).
func Command() *cobra.Command {
	c := &cobra.Command{
		Use:   "summon",
		Short: "Summon a mind-form via the First Contact ritual",
		Long: `Summon walks you through the First Contact ritual:

  1. Identity stage  (first run only): create / import / skip
  2. Mind-form stage: choose master source (your local identity, or a card)
  3. Describe the character you want to summon
  4. Watch a displaying paragraph take shape, name the to-be-summoned
  5. Seal the summoning book; the new mind-form awakens and replies

The ritual is one-shot: failure or interruption discards in-flight
state and you start over. claude (Claude Code) must be on PATH.

Flags:
  --master-card <path>   Use the holder of this v1 TOML card as the
                         new mind-form's master, regardless of any
                         local identity. Skips Phase 2's prompt.
  --key-file <path>      Read the operator's nsec1... or 32-byte hex
                         private key from a file (for the import
                         branch of Phase 1, when no local identity
                         exists yet). Avoids pasting nsec on stdin.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), firstcontact.EntrySummon)
		},
	}
	c.Flags().StringVar(&flagMasterCard, "master-card", "", "path to the master's identity card (overrides any local identity)")
	c.Flags().StringVar(&flagKeyFile, "key-file", "", "path to a file containing the operator's nsec1... or 32-byte hex private key (used in the import-identity branch)")
	return c
}

// Run is the entry point bare-eidos dispatch (cmd/eidos/main.go) calls.
// It runs the wizard with EntryBareEidos so Phase 2 keeps the 退出
// option (the mindgate-only flow needs to be reachable from `eidos`).
//
// `eidos summon` (the cobra command) goes through Command() → RunE →
// run() with EntrySummon so Phase 2 suppresses 退出 (the user already
// committed to summoning by typing `summon`).
func Run(ctx context.Context) error {
	return run(ctx, firstcontact.EntryBareEidos)
}

// run is the shared body, parameterised by entry mode.
//
// Note on dependency resolution: claude on PATH and the docker client
// are summon-only prerequisites. They are NOT validated up front,
// because the new decoupled wizard supports a mindgate-only flow
// (Phase 1 = create / import, Phase 2 = exit) on hosts that may not
// have claude or docker installed yet. We pass an EnsureSummonReady
// callback into the wizard; firstcontact.Run invokes it only after
// Phase 2 returns a summon action, populating Claude / DockerClient /
// the volume helpers in place.
func run(ctx context.Context, entry firstcontact.EntryMode) error {
	stateDir, err := config.ResolveStateDir("")
	if err != nil {
		return err
	}

	rend := render.NewAuto(os.Stdin, os.Stdout, firstcontact.TypewriterCPS)

	deps := firstcontact.Deps{
		StateDir:        stateDir,
		Renderer:        rend,
		EntryMode:       entry,
		MasterCardPath:  flagMasterCard,
		OperatorKeyPath: flagKeyFile,
		AddContact:      addContactDirect(stateDir),
		EnsureSummonReady: func(d *firstcontact.Deps) error {
			if _, err := exec.LookPath("claude"); err != nil {
				return errors.New("the `claude` command is not on PATH; install Claude Code (https://docs.anthropic.com/claude/claude-code) and run `claude /login`")
			}
			dock, err := forgectl.New()
			if err != nil {
				return fmt.Errorf("docker client: %w", err)
			}
			image := forge.DefaultImage
			d.Claude = &firstcontact.Claude{Run: firstcontact.ProductionRunner}
			d.DockerClient = dock
			d.Image = image
			d.WriteVolume = func(ctx context.Context, slug, relPath string, body []byte) error {
				return forgectl.WriteToVolume(ctx, dock, image, slug, relPath, body)
			}
			d.StartContainer = func(ctx context.Context, slug string) error {
				return dock.ContainerStart(ctx, forgectl.ContainerName(slug))
			}
			d.ResponseWait = (&volumeTailer{client: dock, image: image}).Wait
			d.ExistingSlugs = func() ([]string, error) {
				vols, err := dock.VolumeList(ctx, forgectl.VolumePrefix)
				if err != nil {
					return nil, err
				}
				out := make([]string, 0, len(vols))
				for _, v := range vols {
					out = append(out, strings.TrimPrefix(v, forgectl.VolumePrefix))
				}
				return out, nil
			}
			d.ReadyDeps = firstcontact.ReadyDeps{
				PullImage: func(ctx context.Context) error {
					if exists, _ := dock.ImageExists(ctx, image); exists {
						return nil
					}
					return dock.ImagePull(ctx, image, os.Stderr)
				},
				GenerateKey: func() (string, string, error) {
					k, err := identity.Generate()
					if err != nil {
						return "", "", err
					}
					return k.Npub, k.PrivateHex, nil
				},
				ProbeRelay: func(ctx context.Context, _ string) error {
					// Relay reachability is advisory in v1; the gate
					// daemon will surface dial failures at first publish.
					return nil
				},
				HomeRelayURL: firstcontact.PublicHomeRelay,
			}
			return nil
		},
	}
	if tui, ok := rend.(render.TUIRenderer); ok {
		return tui.RunWithPhases(ctx, func(ctx context.Context, r render.Renderer) error {
			deps.Renderer = r
			s, body, err := firstcontact.Run(ctx, deps)
			if err != nil {
				if errors.Is(err, firstcontact.ErrSelfHostExit) {
					return nil
				}
				return err
			}
			if body != nil {
				r.Show("")
				// Prefer markdown rendering when the renderer supports it
				// (TUI via glamour). CLI falls back to plain Typewriter.
				if md, ok := r.(render.MarkdownRenderer); ok {
					md.RenderMarkdown(ctx, string(body))
				} else {
					r.Typewriter(ctx, string(body))
				}
				r.Show("")
				r.Show(fmt.Sprintf(stringFor(s.Lang, "phase4_done"), s.Slug))
			}
			return nil
		})
	}

	// CLI fallback path — phase logic runs directly on the main goroutine.
	s, body, err := firstcontact.Run(ctx, deps)
	if err != nil {
		if errors.Is(err, firstcontact.ErrSelfHostExit) {
			return nil
		}
		return err
	}
	if body != nil {
		rend.Show("")
		rend.Typewriter(ctx, string(body))
		rend.Show("")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintf(os.Stdout, stringFor(s.Lang, "phase4_done"), s.Slug)
		fmt.Fprintln(os.Stdout)
	}
	return nil
}

// stringFor mirrors the firstcontact unexported lookup so the
// wrapping cmd-level summary respects language. Kept tiny here
// rather than exporting from internal/firstcontact.
func stringFor(lang, key string) string {
	if lang == "zh" && key == "phase4_done" {
		return "💠 仪式完成。`eidos forge logs %s` 看它呼吸。"
	}
	return "💠 Ritual complete. `eidos forge logs %s` to watch it breathe."
}

// addContactDirect returns a ContactAdder that prefers the live gate
// daemon's IPC `contact.add` method when the daemon's socket is
// reachable, and falls back to a direct state.db write ONLY when the
// daemon is unreachable (dial fails — typical first-run wizard case
// where no daemon has ever been started).
//
// If the daemon's socket dials but the IPC call returns an error, the
// error is surfaced — we do NOT silently bypass the daemon. Doing so
// would violate CLAUDE.md "Single Call Path": a running daemon owns
// the `contact.added` event, relay subscription refresh, and any
// future event-listener side effects; direct state.db writes while
// the daemon is up would diverge those state machines from the
// canonical handler.
//
// CONTACT_EXISTS is treated as success (idempotent re-summon).
func addContactDirect(stateDir string) func(context.Context, string, string, string) error {
	return func(ctx context.Context, npub, label, relay string) error {
		if cfg, err := config.Load(filepath.Join(stateDir, "config.toml")); err == nil {
			socket := filepath.Join(stateDir, cfg.Daemon.Socket)
			c, dialErr := ipc.Dial(socket)
			if dialErr == nil {
				defer c.Close()
				var resp map[string]bool
				ipcErr, callErr := c.Call("contact.add", map[string]any{
					"npub":   npub,
					"relays": []string{relay},
					"label":  label,
					"tier":   string(contacts.TierFriend),
				}, &resp)
				if callErr != nil {
					// Socket dialed but call had a transport error —
					// surface to the caller. Do NOT fall back to direct
					// write because the daemon is up and would race.
					return fmt.Errorf("contact.add IPC call: %w", callErr)
				}
				if ipcErr != nil &&
					!strings.Contains(ipcErr.Message, "exists") &&
					!strings.EqualFold(string(ipcErr.Code), "CONTACT_EXISTS") {
					return fmt.Errorf("contact.add: %s (%s)", ipcErr.Message, ipcErr.Code)
				}
				return nil
			}
			// dialErr != nil — daemon not running. Fall through to
			// direct write (sanctioned bootstrap exception).
		}
		hex, err := identity.DecodeNpub(npub)
		if err != nil {
			return fmt.Errorf("decode npub: %w", err)
		}
		dbPath := filepath.Join(stateDir, "state.db")
		db, err := store.Open(dbPath, false)
		if err != nil {
			return fmt.Errorf("open state.db: %w", err)
		}
		defer db.Close()
		repo := contacts.New(db)
		addErr := repo.Add(ctx, contacts.Contact{
			Pubkey: hex,
			Label:  label,
			Tier:   contacts.TierFriend,
			Relays: []string{relay},
		})
		if addErr != nil && !errors.Is(addErr, contacts.ErrExists) {
			return addErr
		}
		return nil
	}
}

// volumeTailer is the production ResponseWaiter: it polls the volume
// via a one-shot helper container that gates on essence/born_at and
// emits journal/0000-response.md once both are committed (per the
// agent's birth boot prompt: born_at is written LAST, after response).
type volumeTailer struct {
	client forgectl.Client
	image  string
}

func (v *volumeTailer) Wait(ctx context.Context, slug, gatePath, bodyPath string, timeout time.Duration) ([]byte, error) {
	gateTarget := "/eidos/" + strings.TrimPrefix(gatePath, "/")
	bodyTarget := "/eidos/" + strings.TrimPrefix(bodyPath, "/")
	deadline := time.Now().Add(timeout)
	// Single helper-container script: exit 0 + print body iff both
	// files are non-empty. Order matters: -s on gate first means we
	// don't even read body until the supervisor's authoritative
	// completion marker is set.
	script := fmt.Sprintf("test -s %q && test -s %q && cat %q",
		gateTarget, bodyTarget, bodyTarget)
	for {
		res, err := v.client.RunInit(ctx, forgectl.RunInitOpts{
			Image: v.image,
			Mount: forgectl.Mount{VolumeName: forgectl.VolumeName(slug), Target: "/eidos"},
			User:  "0:0",
			Cmd:   []string{"sh", "-c", script},
		})
		if err == nil && len(res.Stdout) > 0 {
			return res.Stdout, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for gate %s + body %s in volume", gatePath, bodyPath)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
