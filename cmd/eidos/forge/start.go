package forge

import (
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start <name>",
		Short: "Start (wake) a mind-form",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			c, err := forgectl.New()
			if err != nil {
				return err
			}
			if err := startMindform(cmd.Context(), c, name); err != nil {
				return err
			}
			cmd.Printf("✓ %s is awake.\n", name)
			return nil
		},
	}
}

func startMindform(ctx context.Context, c forgectl.Client, name string) error {
	cont := forgectl.ContainerName(name)
	state, err := c.ContainerInspectState(ctx, cont)
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	switch state {
	case "absent":
		// `forge create` provisions both volume and container (stopped),
		// so reaching this branch implies the container was removed out
		// of band (manual `docker rm`, partial purge, or a v0 install
		// from before forge create created the persistent container).
		return fmt.Errorf("mind-form %q has no container (the volume may still exist; try `eidos forge purge %s --yes` then re-create)", name, name)
	case "running":
		return nil
	}
	return c.ContainerStart(ctx, cont)
}
