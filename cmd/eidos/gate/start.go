package gate

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Install (if needed) and start the gate daemon as a system service",
	Long: `Installs a systemd unit for the gate daemon if not already present, then
enables and starts it. Idempotent — running 'start' again on an already-running
service is a no-op.

By default this writes a user-mode unit to ~/.config/systemd/user/, which does
not require root. Pass --system to write to /etc/systemd/system/ instead;
that mode requires running 'eidos' under sudo.

After 'start' completes, 'eidos gate status' shows the current state of the
daemon unit. The daemon survives your shell exiting (user-mode units may need
'loginctl enable-linger <user>' to survive a full logout).

Use 'eidos relay service install' to manage the relay service separately.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, stateDir, err := loadGateConfig()
		if err != nil {
			return err
		}
		mgr, err := buildServiceManager()
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

		if err := mgr.StartDaemon(ctx); err != nil {
			return err
		}
		fmt.Println("✓ gate daemon started")
		return printStatus(ctx, os.Stdout, mgr)
	},
}

func init() {
	addSystemFlag(startCmd)
	rootCmd.AddCommand(startCmd)
}
