package forge

import (
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

func newPurgeCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "purge <name>",
		Short: "Remove a mind-form (container + volume). Destructive.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			if !yes {
				return fmt.Errorf("refusing to purge without --yes (this destroys mind-form %q)", name)
			}
			c, err := forgectl.New()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			cont := forgectl.ContainerName(name)
			vol := forgectl.VolumeName(name)
			_ = c.ContainerStop(ctx, cont, 5)
			_ = c.ContainerRemove(ctx, cont)
			if err := c.VolumeRemove(ctx, vol); err != nil {
				return err
			}
			cmd.Printf("✓ purged %s\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm destructive removal")
	return cmd
}
