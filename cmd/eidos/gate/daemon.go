package gate

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
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

func init() { rootCmd.AddCommand(daemonCmd) }
