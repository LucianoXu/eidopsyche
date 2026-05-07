package gate

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/service"
)

var purgeYes bool

var purgeCmd = &cobra.Command{
	Use:   "purge",
	Short: "Stop services, remove units, and delete the gate state directory",
	Long: `Wipes everything this host knows about the gate identity:
  1) stops the daemon and relay services if running
  2) disables and removes the systemd unit files
  3) reloads systemd
  4) deletes the gate state directory (key, state.db, config.toml, relay/)

Useful for tearing down test deployments and starting from a clean slate.

By default purge prompts for confirmation. Pass --yes to skip the prompt
(intended for scripts and CI).`,
	// v0.4 detection is skipped for purge by name in rootCmd's
	// PersistentPreRunE so v0.4 users can clean up before re-init.
	RunE: func(cmd *cobra.Command, args []string) error {
		stateDir, err := config.ResolveStateDir(globalStateDir)
		if err != nil {
			return err
		}
		mgr, _ := buildServiceManager(false) // may be nil on platforms without service support — fine, we still wipe state

		fmt.Println("This will permanently remove:")
		fmt.Printf("  - state directory: %s\n", stateDir)
		if mgr != nil {
			scope := "user"
			if useSystemServices {
				scope = "system"
			}
			fmt.Printf("  - %s systemd units: %s.service, %s.service\n", scope, service.DaemonUnitName, service.RelayUnitName)
		} else {
			fmt.Println("  - (system services not supported on this platform; skipping unit teardown)")
		}

		if !purgeYes {
			fmt.Print("Type 'yes' to proceed: ")
			r := bufio.NewReader(os.Stdin)
			line, err := r.ReadString('\n')
			if err != nil {
				return fmt.Errorf("read confirmation: %w", err)
			}
			if strings.TrimSpace(line) != "yes" {
				fmt.Println("aborted.")
				return nil
			}
		}

		ctx := context.Background()
		if mgr != nil {
			if err := mgr.Uninstall(ctx); err != nil {
				return fmt.Errorf("uninstall services: %w", err)
			}
			fmt.Println("✓ services uninstalled")
		}

		if err := os.RemoveAll(stateDir); err != nil {
			return fmt.Errorf("remove state dir %s: %w", stateDir, err)
		}
		fmt.Println("✓ state directory removed")

		return nil
	},
}

func init() {
	purgeCmd.Flags().BoolVar(&purgeYes, "yes", false, "skip the confirmation prompt")
	addSystemFlag(purgeCmd)
	rootCmd.AddCommand(purgeCmd)
}
