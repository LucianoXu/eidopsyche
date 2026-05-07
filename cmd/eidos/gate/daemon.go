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
		fmt.Fprintf(os.Stderr, "mindgate-daemon starting state_dir=%s\n", dir)
		return d.Run(ctx)
	},
}

func init() { rootCmd.AddCommand(daemonCmd) }
