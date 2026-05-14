package forge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/ontology"
	"github.com/LucianoXu/eidopsyche/internal/version"
	"github.com/spf13/cobra"
)

// MindFormImageRepo is the container repository on ghcr.io that the
// release pipeline pushes mind-form images to (release.yml's
// `mindform-image` job tags `<repo>:<release-tag>` and `<repo>:latest`).
const MindFormImageRepo = "ghcr.io/lucianoxu/eidopsyche-mindform"

// DefaultImage returns the container image tag a fresh `forge create`
// pulls when `--image` is not given.
//
// The tag is derived from the host binary's version so a v0.11.2 host
// and a v0.11.2 mind-form image are paired automatically (the release
// pipeline pushes `<repo>:v0.11.2` on each tag push). Source builds
// (`version.Version == "dev"`) fall back to the `:dev` tag, which is
// what `make image IMAGE_TAG=dev` produces locally — preserving the
// developer workflow where you `make image && eidos forge create`.
//
// A function (rather than a package-level var) so tests can override
// `version.Version` and observe the tag flip without ldflags.
func DefaultImage() string {
	tag := version.Version
	if tag == "" || tag == "dev" {
		tag = "dev"
	}
	return MindFormImageRepo + ":" + tag
}

// orchestrateStep is one reversible action in the create pipeline.
// undo == nil means the step has nothing to roll back (e.g. checks,
// image pull which is shared across mind-forms).
type orchestrateStep struct {
	name string
	do   func(ctx context.Context) error
	undo func(ctx context.Context) error
}

// runSteps executes steps in order. On the first error, it runs the
// undo of every step that completed successfully (in reverse) and
// returns the original error. Rollback failures are logged to stderr
// and joined onto the returned error so the operator knows when
// orphan state may need manual cleanup; the do-failure remains the
// primary cause so existing error-string contracts at call sites are
// preserved.
func runSteps(ctx context.Context, steps []orchestrateStep) error {
	done := make([]orchestrateStep, 0, len(steps))
	for _, s := range steps {
		if err := s.do(ctx); err != nil {
			rollbackErrs := []error{err}
			for i := len(done) - 1; i >= 0; i-- {
				if done[i].undo == nil {
					continue
				}
				if uerr := done[i].undo(ctx); uerr != nil {
					fmt.Fprintf(os.Stderr, "orchestrate: rollback of %s failed: %v\n", done[i].name, uerr)
					rollbackErrs = append(rollbackErrs, fmt.Errorf("rollback %s: %w", done[i].name, uerr))
				}
			}
			if len(rollbackErrs) == 1 {
				return err
			}
			return errors.Join(rollbackErrs...)
		}
		done = append(done, s)
	}
	return nil
}

// Orchestrate is the testable seam for `eidos forge create` and the
// First Contact wizard's phase 3. The wizard imports it directly as a
// sanctioned bootstrap exception (see CLAUDE.md "Single Call Path"):
// container creation does not flow through the daemon's methodTable
// today, and adding an IPC method just to wrap docker is out of scope
// for this milestone.
//
// Mid-flight failures (init-volume, container-create) reverse-undo any
// completed steps so the operator can retry cleanly without manual
// `docker volume rm` / `docker rm` housekeeping.
func Orchestrate(ctx context.Context, c forgectl.Client, name string, o CreateOpts) error {
	vol := forgectl.VolumeName(name)
	cont := forgectl.ContainerName(name)

	if exists, err := c.VolumeExists(ctx, vol); err != nil {
		return fmt.Errorf("check volume: %w", err)
	} else if exists {
		return fmt.Errorf("volume %s already exists", vol)
	}
	if exists, err := c.ContainerExists(ctx, cont); err != nil {
		return fmt.Errorf("check container: %w", err)
	} else if exists {
		return fmt.Errorf("container %s already exists", cont)
	}

	image := o.Image
	if image == "" {
		image = DefaultImage()
	}

	steps := []orchestrateStep{
		{
			name: "ensure-image",
			// Skip the pull when the image is already present locally
			// (common in dev / staging where `make image` produced a
			// tag the registry doesn't host yet). When the image is
			// missing we still attempt the pull and surface its error.
			do: func(ctx context.Context) error {
				exists, err := c.ImageExists(ctx, image)
				if err != nil {
					return fmt.Errorf("inspect image %s: %w", image, err)
				}
				if exists {
					return nil
				}
				if err := c.ImagePull(ctx, image, os.Stderr); err != nil {
					return fmt.Errorf("pull %s: %w", image, err)
				}
				return nil
			},
			// undo intentionally nil: images are shared infrastructure.
		},
		{
			name: "create-volume",
			do: func(ctx context.Context) error {
				if err := c.VolumeCreate(ctx, vol); err != nil {
					return fmt.Errorf("create volume: %w", err)
				}
				return nil
			},
			undo: func(ctx context.Context) error { return c.VolumeRemove(ctx, vol) },
		},
		{
			name: "init-volume",
			do: func(ctx context.Context) error {
				// Build the template tar to pipe into init-volume.
				// PrefabID, if set, switches the source to prefab/<id>/.
				pipeR, pipeW := io.Pipe()
				go func() {
					defer pipeW.Close()
					params := ontology.Params{
						Label:         o.Label,
						OwnerNpub:     o.Owner,
						OwnerLabel:    o.OwnerLabel,
						MindFormNpub:  o.MindFormNpub,
						HomeRelay:     o.Relay,
						CreatedDate:   time.Now().UTC().Format("2006-01-02"),
						SummoningBook: o.SummoningBook,
						RoleResearch:  o.RoleResearch,
					}
					var perr error
					if o.PrefabID != "" {
						perr = ontology.TarStreamPrefab(pipeW, o.PrefabID, params)
					} else {
						perr = ontology.TarStream(pipeW, params)
					}
					if perr != nil {
						_ = pipeW.CloseWithError(perr)
					}
				}()

				env := []string{
					"EIDOS_IN_CONTAINER=1",
					"EIDOS_FORGE_NAME=" + name,
					"EIDOS_FORGE_LABEL=" + o.Label,
					"EIDOS_FORGE_OWNER=" + o.Owner,
					"EIDOS_FORGE_RELAY=" + o.Relay,
					"EIDOS_FORGE_MODEL=" + o.Model,
					"EIDOS_FORGE_HEARTBEAT_INTERVAL=" + o.HeartbeatInterval,
				}
				if o.KeyHex != "" {
					env = append(env, "EIDOS_FORGE_KEY_HEX="+o.KeyHex)
				}
				res, err := c.RunInit(ctx, forgectl.RunInitOpts{
					Image: image,
					Mount: forgectl.Mount{Type: forgectl.MountVolume, Source: vol, Target: "/eidos"},
					// init-volume runs as root so it can extract the
					// template tar, clone the bundle, and git-init the
					// parent ontology with full privileges. Its last
					// step chowns /eidos to 1000:1000; the persistent
					// container then starts as the image's USER (eidos).
					User:  "0:0",
					Env:   env,
					Cmd:   []string{"eidos", "forge", "init-volume"},
					Stdin: pipeR,
				})
				if err != nil {
					return fmt.Errorf("init-volume: %w (stderr: %s)", err, string(res.Stderr))
				}
				return nil
			},
			// init-volume writes into the volume; create-volume's undo
			// (VolumeRemove) is what cleans up on failure here.
		},
		{
			name: "create-container",
			// Create the persistent container in stopped state.
			// `forge start` is then a thin docker-start. Using the
			// image's default entrypoint (tini → entrypoint.sh →
			// eidos supervisor run); no Cmd / Entrypoint override.
			do: func(ctx context.Context) error {
				if err := c.ContainerCreate(ctx, forgectl.CreateOpts{
					Name:   cont,
					Image:  image,
					Mounts: []forgectl.Mount{{Type: forgectl.MountVolume, Source: vol, Target: "/eidos"}},
				}); err != nil {
					return fmt.Errorf("create container: %w", err)
				}
				return nil
			},
			undo: func(ctx context.Context) error { return c.ContainerRemove(ctx, cont) },
		},
	}
	return runSteps(ctx, steps)
}

// runCreate2 is the cobra-level entry: creates a real Docker client,
// orchestrates, and prints post-create UX.
func runCreate2(cmd *cobra.Command, name string, o CreateOpts) error {
	c, err := forgectl.New()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if err := Orchestrate(ctx, c, name, o); err != nil {
		return err
	}
	cmd.Printf("created mind-form %q (label %q)\n", name, o.Label)
	cmd.Printf("  volume: %s\n", forgectl.VolumeName(name))
	cmd.Printf("  master: %s\n", o.Owner)
	cmd.Printf("  relay : %s\n", o.Relay)
	cmd.Printf("\nNext: print its card with `eidos forge status %s`, start it with `eidos forge start %s`.\n", name, name)
	if !o.NoLogin {
		cmd.Printf("\nRun `eidos forge login %s` now to log Claude Code into this mind-form.\n", name)
	}
	return nil
}
