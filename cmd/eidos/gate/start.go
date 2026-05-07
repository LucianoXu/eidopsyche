package gate

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Install (if needed) and start the gate daemon + relay as system services",
	Long: `Installs systemd units for the gate daemon and relay if they are not already
present, then enables and starts both. Idempotent — running 'start' again on
already-running services is a no-op.

By default this writes user-mode units to ~/.config/systemd/user/, which do
not require root. Pass --system to write to /etc/systemd/system/ instead;
that mode requires running 'eidos' under sudo.

After 'start' completes, 'eidos gate status' shows the current state of
both units, and the daemon and relay survive your shell exiting (user-mode
units may need 'loginctl enable-linger <user>' to survive a full logout).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := buildServiceManager()
		if err != nil {
			return err
		}
		ctx := context.Background()

		// Preflight the relay port unless our own relay already holds it.
		// This catches stale processes (e.g., the v0 mindgate binary
		// lingering from a prior deploy-test) before we hand off to systemd
		// / launchd, where a port conflict surfaces only as a unit going
		// straight to "failed" with the real reason buried in journalctl.
		if !relayAlreadyManaged(ctx, mgr) {
			stateDir, err := config.ResolveStateDir(globalStateDir)
			if err != nil {
				return err
			}
			if err := preflightRelayPort(stateDir); err != nil {
				return err
			}
		}

		if err := mgr.Start(ctx); err != nil {
			return err
		}
		fmt.Println("✓ gate services started")
		return printStatus(ctx, os.Stdout, mgr)
	},
}

func init() {
	addSystemFlag(startCmd)
	rootCmd.AddCommand(startCmd)
}
