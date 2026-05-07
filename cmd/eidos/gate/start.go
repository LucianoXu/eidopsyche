package gate

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
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
		cfg, stateDir, err := loadGateConfig()
		if err != nil {
			return err
		}
		mgr, err := buildServiceManager(cfg.RelayEnabled())
		if err != nil {
			return err
		}
		ctx := context.Background()

		// Preflight the relay port only when we're going to install / start
		// the relay unit. Daemon-only deployments don't bind any port from
		// our binary, so port-in-use isn't a meaningful failure mode here.
		if cfg.RelayEnabled() && !relayAlreadyManaged(ctx, mgr) {
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
