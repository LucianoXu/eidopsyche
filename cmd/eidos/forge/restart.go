package forge

import (
	"context"
	"fmt"
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

	if err := c.ContainerStop(ctx, cont, graceSeconds); err != nil {
		return fmt.Errorf("stop %s: %w", cont, err)
	}

	// ContainerInspectImage returns (image-id, image-ref, err). We
	// only need the ref (the tag) to recreate the container with the
	// same image; the id is used by forge upgrade for change detection.
	_, image, err := c.ContainerInspectImage(ctx, cont)
	if err != nil {
		return fmt.Errorf("inspect image %s: %w", cont, err)
	}
	if image == "" {
		return fmt.Errorf("inspect image %s: container has no image ref (does it exist?)", cont)
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
			stateDir, err := config.ResolveStateDir("")
			if err != nil {
				return err
			}
			cfg, err := config.Load(filepath.Join(stateDir, "config.toml"))
			if err != nil {
				return err
			}
			if err := Restart(cmd.Context(), c, args[0], &cfg, grace); err != nil {
				return err
			}
			cmd.Printf("restarted %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().IntVar(&grace, "grace", 10, "seconds before SIGKILL during stop")
	return cmd
}
