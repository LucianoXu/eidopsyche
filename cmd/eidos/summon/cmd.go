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

	rend := render.NewCLI(os.Stdin, os.Stdout, firstcontact.TypewriterCPS)
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

// addContactDirect is the bootstrap-exception ContactAdder: when the
// host gate daemon is not running (the wizard does not start it),
// the new MindForm is added to the operator's contacts by writing
// directly to state.db. Documented as an exception per
// internal/firstcontact/doc.go and CLAUDE.md "Single Call Path".
func addContactDirect(stateDir string) func(context.Context, string, string, string) error {
	return func(ctx context.Context, npub, label, relay string) error {
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
		err = repo.Add(ctx, contacts.Contact{
			Pubkey: hex,
			Label:  label,
			Tier:   contacts.TierFriend,
			Relays: []string{relay},
		})
		if err != nil && !errors.Is(err, contacts.ErrExists) {
			return err
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
