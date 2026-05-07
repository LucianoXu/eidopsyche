package gate

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the gate daemon + relay services",
	Long: `Stops both gate units without uninstalling or disabling them. Re-running
'eidos gate start' will bring them back up; 'eidos gate purge' removes the
units entirely (and the state directory).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := buildServiceManager(false)
		if err != nil {
			return err
		}
		ctx := context.Background()
		if err := mgr.Stop(ctx); err != nil {
			return err
		}
		fmt.Println("✓ gate services stopped")
		return printStatus(ctx, os.Stdout, mgr)
	},
}

func init() {
	addSystemFlag(stopCmd)
	rootCmd.AddCommand(stopCmd)
}
