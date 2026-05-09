package relay

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
	"github.com/LucianoXu/eidopsyche/internal/service"
)

var (
	svcSystem bool
	svcDir    string
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Install and manage the eidos-relay system service",
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the eidos-relay unit (does not start it)",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveRelayServiceDir()
		if err != nil {
			return err
		}
		mgr, err := buildRelayServiceManager(dir)
		if err != nil {
			return err
		}
		return mgr.InstallRelay(context.Background(), dir)
	},
}

var serviceStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Enable and start eidos-relay",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveRelayServiceDir()
		if err != nil {
			return err
		}
		mgr, err := buildRelayServiceManager(dir)
		if err != nil {
			return err
		}
		return mgr.StartRelay(context.Background(), dir)
	},
}

var serviceStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop eidos-relay (leaves the unit installed)",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveRelayServiceDir()
		if err != nil {
			return err
		}
		mgr, err := buildRelayServiceManager(dir)
		if err != nil {
			return err
		}
		return mgr.StopRelay(context.Background())
	},
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print the eidos-relay service status",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveRelayServiceDir()
		if err != nil {
			return err
		}
		mgr, err := buildRelayServiceManager(dir)
		if err != nil {
			return err
		}
		statuses, err := mgr.Status(context.Background())
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		for _, st := range statuses {
			if st.Name != service.RelayUnitName {
				continue
			}
			state := "not-installed"
			switch {
			case st.Active:
				state = "active"
			case st.Enabled:
				state = "enabled-but-stopped"
			case st.Installed:
				state = "installed"
			}
			pid := ""
			if st.PID > 0 {
				pid = fmt.Sprintf("  pid=%d", st.PID)
			}
			fmt.Fprintf(out, "  %-22s %s%s\n", st.Name, state, pid)
			return nil
		}
		fmt.Fprintf(out, "  %-22s not-installed\n", service.RelayUnitName)
		return nil
	},
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop, disable, and remove the eidos-relay unit",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveRelayServiceDir()
		if err != nil {
			return err
		}
		mgr, err := buildRelayServiceManager(dir)
		if err != nil {
			return err
		}
		return mgr.UninstallRelay(context.Background())
	},
}

func resolveRelayServiceDir() (string, error) {
	if svcDir != "" {
		return svcDir, nil
	}
	return relaycfg.DefaultDir()
}

// buildRelayServiceManager constructs a service.Manager scoped to the
// running binary, the resolved relay config directory, and the --system
// flag. Returns a typed error when the host platform has no service-
// manager support so callers can surface a friendly message.
func buildRelayServiceManager(relayDir string) (service.Manager, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate eidos binary: %w", err)
	}
	scope := service.ScopeUser
	if svcSystem {
		scope = service.ScopeSystem
	}
	mgr, err := service.New(service.Config{
		BinaryPath: exe,
		StateDir:   relayDir,
		Scope:      scope,
	})
	if err != nil {
		if errors.Is(err, service.ErrUnsupported) {
			return nil, fmt.Errorf(`%w
Run the relay manually instead. Linux/macOS:
  eidos relay start &
PowerShell:
  Start-Job -Name eidos-relay -ScriptBlock { eidos.exe relay start }`, err)
		}
		return nil, err
	}
	return mgr, nil
}

func init() {
	serviceCmd.PersistentFlags().BoolVar(&svcSystem, "system", false,
		"manage host-wide units (Linux: /etc/systemd/system, root; macOS: /Library/LaunchDaemons, root; Windows: no-op, SCM is host-wide)")
	serviceCmd.PersistentFlags().StringVar(&svcDir, "dir", "",
		"relay config dir (default: ~/.config/eidos/relay)")
	serviceCmd.AddCommand(serviceInstallCmd, serviceStartCmd, serviceStopCmd, serviceStatusCmd, serviceUninstallCmd)
	rootCmd.AddCommand(serviceCmd)
}
