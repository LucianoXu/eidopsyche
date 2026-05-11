package gate

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/cron"
	"github.com/LucianoXu/eidopsyche/internal/daemon"
	"github.com/LucianoXu/eidopsyche/internal/service"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run the MindGate daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := config.ResolveStateDir(globalStateDir)
		if err != nil {
			return err
		}
		d, err := daemon.Start(dir)
		if err != nil {
			return err
		}
		// Container-side daemon: take ContainerCtx and register apply
		// hooks for keys that need in-container side effects beyond the
		// config.toml write. heartbeat.interval re-renders + installs
		// the busybox crontab so the new cadence takes effect without
		// a `docker restart`. The supervisor's PID-1 startup-time
		// render handles the cold path (process boot); this apply hook
		// handles the hot path (config change on a running mindform).
		if os.Getenv("EIDOS_IN_CONTAINER") == "1" {
			d.SetContext(config.ContainerCtx)
			d.RegisterApply("config.heartbeat.interval", heartbeatApplyHook)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		fmt.Fprintf(os.Stderr, "eidos-gate-daemon starting state_dir=%s\n", dir)
		// On Windows, when launched by SCM, RunSupervised dispatches via
		// `golang.org/x/sys/windows/svc` and translates SERVICE_CONTROL_STOP
		// into a context cancellation. Elsewhere it is a pass-through that
		// just runs the closure with the signal-driven context above.
		return service.RunSupervised(ctx, service.DaemonUnitName, func(ctx context.Context) error {
			return d.Run(ctx)
		})
	},
}

// heartbeatInstaller is the production installer the apply hook uses.
// Tests substitute a tempdir-backed Installer so they don't shell out
// to sudo or mutate the host's /var/spool/cron.
var heartbeatInstaller = cron.DefaultInstaller

// heartbeatApplyHook re-renders the busybox crontab from the new
// heartbeat.interval value and writes it to /var/spool/cron/crontabs/eidos
// atomically via sudo. busybox crond detects the spool mtime change and
// re-reads on its next scan. Synchronous: the IPC call that triggered
// the config.set returns only after the new crontab is in place.
//
// Empty `new` is valid (operator clears the override, falls back to the
// 2h default). cron.Render handles "" → DefaultHeartbeatInterval.
//
// Type assertion on newVal is checked: a non-string value signals a
// registry-level bug (config.set passed something the Key.Get didn't
// stringify) and should fail loudly rather than silently render the
// default.
func heartbeatApplyHook(ctx context.Context, _ any, _ any, newVal any) error {
	interval, ok := newVal.(string)
	if !ok {
		return fmt.Errorf("heartbeat apply: expected string interval, got %T", newVal)
	}
	body, err := cron.Render(interval)
	if err != nil {
		return fmt.Errorf("heartbeat apply: render: %w", err)
	}
	if err := heartbeatInstaller().Install(ctx, body); err != nil {
		return fmt.Errorf("heartbeat apply: install: %w", err)
	}
	return nil
}

func init() { rootCmd.AddCommand(daemonCmd) }
