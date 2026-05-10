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

// Command returns the cobra command. Registered in cmd/eidos/main.go.
func Command() *cobra.Command {
	return &cobra.Command{
		Use:   "summon",
		Short: "Summon a mind-form via the First Contact ritual",
		Long: `Summon walks you through the First Contact ritual:

  1. Pick a language and your operator label
  2. Describe the character you want to summon
  3. Watch a displaying paragraph take shape, name the to-be-summoned
  4. Seal the summoning book; the new mind-form awakens and replies

The ritual is one-shot: failure or interruption discards in-flight
state and you start over. claude (Claude Code) must be on PATH.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Run(cmd.Context())
		},
	}
}

// Run is the entry point both Command() and bare-eidos dispatch call.
func Run(ctx context.Context) error {
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("the `claude` command is not on PATH; install Claude Code (https://docs.anthropic.com/claude/claude-code) and run `claude /login`")
	}

	stateDir, err := config.ResolveStateDir("")
	if err != nil {
		return err
	}

	dock, err := forgectl.New()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	rend := render.NewAuto(os.Stdin, os.Stdout, firstcontact.TypewriterCPS)
	cl := &firstcontact.Claude{Run: firstcontact.ProductionRunner}
	image := forge.DefaultImage

	deps := firstcontact.Deps{
		StateDir:     stateDir,
		Renderer:     rend,
		Claude:       cl,
		DockerClient: dock,
		Image:        image,
		WriteVolume: func(ctx context.Context, slug, relPath string, body []byte) error {
			return forgectl.WriteToVolume(ctx, dock, image, slug, relPath, body)
		},
		StartContainer: func(ctx context.Context, slug string) error {
			return dock.ContainerStart(ctx, forgectl.ContainerName(slug))
		},
		ResponseWait: (&volumeTailer{client: dock, image: image}).Wait,
		AddContact:   addContactDirect(stateDir),
		ExistingSlugs: func() ([]string, error) {
			vols, err := dock.VolumeList(ctx, forgectl.VolumePrefix)
			if err != nil {
				return nil, err
			}
			out := make([]string, 0, len(vols))
			for _, v := range vols {
				out = append(out, strings.TrimPrefix(v, forgectl.VolumePrefix))
			}
			return out, nil
		},
		ReadyDeps: firstcontact.ReadyDeps{
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
				// Accept any reachable ws/wss URL by treating relay
				// reachability as advisory (the real probe is wired
				// post-merge once we have an internal/relay/probe
				// helper). For now: rely on user-supplied URL being
				// well-formed; the gate daemon will surface dial
				// failures at first publish.
				return nil
			},
			HomeRelayURL: firstcontact.PublicHomeRelay,
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
				r.Show(fmt.Sprintf(stringFor(s.Lang, "phase3_done"), s.Slug))
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
		fmt.Fprintf(os.Stdout, stringFor(s.Lang, "phase3_done"), s.Slug)
		fmt.Fprintln(os.Stdout)
	}
	return nil
}

// stringFor mirrors the firstcontact unexported lookup so the
// wrapping cmd-level summary respects language. Kept tiny here
// rather than exporting from internal/firstcontact.
func stringFor(lang, key string) string {
	if lang == "zh" && key == "phase3_done" {
		return "💠 仪式完成。`eidos forge logs %s` 看它呼吸。"
	}
	return "💠 Ritual complete. `eidos forge logs %s` to watch it breathe."
}

// addContactDirect returns a ContactAdder that prefers the live gate
// daemon's IPC `contact.add` method when the daemon is reachable, and
// falls back to a direct state.db write only when the daemon is down
// (the typical wizard run). The fallback is the second sanctioned
// bootstrap exception per CLAUDE.md "Single Call Path"; using IPC when
// available keeps the daemon's `contact.added` event + relay
// subscription refresh in the loop.
func addContactDirect(stateDir string) func(context.Context, string, string, string) error {
	return func(ctx context.Context, npub, label, relay string) error {
		// Prefer IPC if the daemon is reachable.
		if cfg, err := config.Load(filepath.Join(stateDir, "config.toml")); err == nil {
			socket := filepath.Join(stateDir, cfg.Daemon.Socket)
			if c, dialErr := ipc.Dial(socket); dialErr == nil {
				defer c.Close()
				var resp map[string]bool
				ipcErr, callErr := c.Call("contact.add", map[string]any{
					"npub":   npub,
					"relays": []string{relay},
					"label":  label,
					"tier":   string(contacts.TierFriend),
				}, &resp)
				if callErr == nil && (ipcErr == nil || strings.Contains(ipcErr.Message, "exists") || strings.EqualFold(string(ipcErr.Code), "CONTACT_EXISTS")) {
					return nil
				}
				// IPC failed for some reason — fall through to direct write
				// rather than block ritual completion.
			}
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
// for the response file via a one-shot helper container.
type volumeTailer struct {
	client forgectl.Client
	image  string
}

func (v *volumeTailer) Wait(ctx context.Context, slug, relPath string, timeout time.Duration) ([]byte, error) {
	target := "/eidos/" + strings.TrimPrefix(relPath, "/")
	deadline := time.Now().Add(timeout)
	script := fmt.Sprintf("test -s %q && cat %q", target, target)
	for {
		// Run a short-lived container that exits 0 + prints the file
		// when present. Exits non-zero otherwise.
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
			return nil, fmt.Errorf("timed out waiting for %s in volume", relPath)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
