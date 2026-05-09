package forge

import (
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

func newWakeHostCmd() *cobra.Command {
	var reason, hint string
	cmd := &cobra.Command{
		Use:   "wake <name>",
		Short: "Manually wake a running mind-form (mostly for testing)",
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
			argv := []string{"eidos", "forge", "wake", "--reason", reason}
			if hint != "" {
				argv = append(argv, "--hint", hint)
			}
			res, err := c.ContainerExec(cmd.Context(), forgectl.ContainerName(name), argv)
			if err != nil {
				return err
			}
			cmd.Print(string(res.Stdout))
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "manual", "wake reason: manual|heartbeat (mindgate is gate-only)")
	cmd.Flags().StringVar(&hint, "hint", "", "human-readable single-line hint")
	return cmd
}
