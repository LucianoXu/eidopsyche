package gate

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the gate daemon service",
	Long: `Stops the gate daemon unit without uninstalling or disabling it. Re-running
'eidos gate start' will bring it back up; 'eidos gate purge' removes the
unit entirely (and the state directory).

To stop the relay service, use 'eidos relay service stop'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := buildServiceManager()
		if err != nil {
			return err
		}
		ctx := context.Background()
		if err := mgr.StopDaemon(ctx); err != nil {
			return err
		}
		fmt.Println("✓ gate daemon stopped")
		return printStatus(ctx, os.Stdout, mgr)
	},
}

func init() {
	addSystemFlag(stopCmd)
	rootCmd.AddCommand(stopCmd)
}
