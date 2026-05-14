package forge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// Restart performs the recreate cycle on the named mind-form's
// container: stop → inspect-image → rm → create (with preserved image
// + latest config mounts) → start. The /eidos named volume is
// unaffected. Image preservation reads from the existing container's
// Config.Image rather than re-resolving DefaultImage(); this ensures
// a previous `forge upgrade` is not silently reverted.
func Restart(ctx context.Context, c forgectl.Client, name string, cfg *config.Config, graceSeconds int) error {
	cont := forgectl.ContainerName(name)
	vol := forgectl.VolumeName(name)

	// Preflight every configured workspace's host path before we touch
	// the running container. If any path was deleted or renamed since
	// the operator ran `forge workspace add`, ContainerCreate would
	// later reject the bind mount and leave the mind-form with no
	// container at all (we've already done stop+rm by then). Fail
	// loudly here, before the destructive steps, so the operator can
	// fix the path or `forge workspace remove` it without losing
	// container state.
	if cfg != nil {
		for _, w := range cfg.Forge[name].Workspaces {
			if err := config.ValidateWorkspaceHostPath(w.HostPath); err != nil {
				return fmt.Errorf("workspace %q: %w (fix the path or run 'eidos forge workspace remove %s %s')", w.Name, err, name, w.Name)
			}
		}
	}

	// Inspect the container's image BEFORE stopping. Image inspect is a
	// read-only docker query and doesn't require the container to be
	// stopped; doing it as part of preflight avoids stranding the
	// mind-form down when docker returns a transient error here
	// (daemon hiccup, permission glitch). Prefer the content-addressable
	// ID over the ref so a mutable tag like `:dev` or `:latest` can't
	// silently move the mind-form to newer image bits between create
	// and restart. Fall back to the ref if the ID is missing.
	imageID, imageRef, err := c.ContainerInspectImage(ctx, cont)
	if err != nil {
		return fmt.Errorf("inspect image %s: %w", cont, err)
	}
	image := imageID
	if image == "" {
		image = imageRef
	}
	if image == "" {
		return fmt.Errorf("inspect image %s: container has no image ref (does it exist?)", cont)
	}

	// Only stop if the container is actually running. Operators may
	// legitimately run `forge stop alice && forge workspace add … &&
	// forge restart alice` — without the state check, docker returns
	// an "already stopped" error and the restart aborts mid-flow.
	// Matches the upgrade flow's pre-stop state check.
	state, err := c.ContainerInspectState(ctx, cont)
	if err != nil {
		return fmt.Errorf("inspect state %s: %w", cont, err)
	}
	if state == "running" {
		if err := c.ContainerStop(ctx, cont, graceSeconds); err != nil {
			return fmt.Errorf("stop %s: %w", cont, err)
		}
	}

	if err := c.ContainerRemove(ctx, cont); err != nil {
		return fmt.Errorf("remove %s: %w", cont, err)
	}

	mounts := []forgectl.Mount{
		{Type: forgectl.MountVolume, Source: vol, Target: "/eidos"},
	}
	if cfg != nil {
		for _, w := range cfg.Forge[name].Workspaces {
			mounts = append(mounts, forgectl.Mount{
				Type:     forgectl.MountBind,
				Source:   w.HostPath,
				Target:   "/workspace/" + w.Name,
				ReadOnly: w.EffectiveMode() == "ro",
			})
		}
	}

	if err := c.ContainerCreate(ctx, forgectl.CreateOpts{
		Name:   cont,
		Image:  image,
		Mounts: mounts,
	}); err != nil {
		return fmt.Errorf("create %s: %w", cont, err)
	}

	if err := c.ContainerStart(ctx, cont); err != nil {
		return fmt.Errorf("start %s: %w", cont, err)
	}
	return nil
}

// newRestartCmd returns the cobra binding for `eidos forge restart <name>`.
func newRestartCmd() *cobra.Command {
	var grace int
	cmd := &cobra.Command{
		Use:   "restart <name>",
		Short: "Recreate the mind-form container, picking up any workspace config changes",
		Long: `Recreate the mind-form's container with the latest configured mounts.
The /eidos volume (ontology, identity, transcripts, claudeauth) is
preserved; the current container's image is preserved across the
recreate cycle. To change the image, use 'eidos forge upgrade'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := forgectl.New()
			if err != nil {
				return err
			}
			// Missing config.toml is fine — the container will be
			// recreated with /eidos only. Operators who configured
			// workspaces presumably also have a config.toml.
			var cfgPtr *config.Config
			stateDir, err := config.ResolveStateDir("")
			if err != nil {
				return err
			}
			if cfg, lerr := config.Load(filepath.Join(stateDir, "config.toml")); lerr == nil {
				cfgPtr = &cfg
			} else if !os.IsNotExist(lerr) {
				return lerr
			}
			if err := Restart(cmd.Context(), c, args[0], cfgPtr, grace); err != nil {
				return err
			}
			cmd.Printf("restarted %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().IntVar(&grace, "grace", 10, "seconds before SIGKILL during stop")
	return cmd
}
