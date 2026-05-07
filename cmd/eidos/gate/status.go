package gate

import (
	"context"
	"os"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the gate daemon + relay service state",
	Long: `Lists each gate unit with its current state:
  not-installed         no unit file present
  installed             unit file present, not enabled
  enabled-but-stopped   enabled (will start at login) but currently stopped
  active                running; PID is shown alongside

Pass --system to query system-mode units instead of user-mode.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := buildServiceManager(false)
		if err != nil {
			return err
		}
		return printStatus(context.Background(), os.Stdout, mgr)
	},
}

func init() {
	addSystemFlag(statusCmd)
	rootCmd.AddCommand(statusCmd)
}
