package forge

import (
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	var grace int
	cmd := &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop (sleep) a mind-form",
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
			if err := stopMindform(cmd.Context(), c, name, grace); err != nil {
				return err
			}
			cmd.Printf("✓ %s is asleep.\n", name)
			return nil
		},
	}
	cmd.Flags().IntVar(&grace, "grace", 10, "seconds to wait before SIGKILL")
	return cmd
}

func stopMindform(ctx context.Context, c forgectl.Client, name string, grace int) error {
	cont := forgectl.ContainerName(name)
	state, err := c.ContainerInspectState(ctx, cont)
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	if state != "running" {
		return nil
	}
	return c.ContainerStop(ctx, cont, grace)
}
