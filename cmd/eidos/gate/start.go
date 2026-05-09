package gate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
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
		_, stateDir, err := loadGateConfig()
		if err != nil {
			return err
		}
		mgr, err := buildServiceManager()
		if err != nil {
			return err
		}
		ctx := context.Background()

		// Warn if a residual [relay] section is present in the gate config.
		// Since v0.5 the relay runs as a separate process managed by
		// `eidos relay service`; the gate no longer reads or acts on [relay].
		cfgPath := filepath.Join(stateDir, "config.toml")
		if _, meta, loadErr := config.LoadWithMeta(cfgPath); loadErr == nil {
			for _, key := range meta.Undecoded() {
				if len(key) > 0 && key[0] == "relay" {
					fmt.Fprintln(os.Stderr,
						"warning: [relay] section is no longer read by gate; configure the relay via `eidos relay init`. "+
							"See CHANGELOG.md for the migration recipe.")
					break
				}
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
