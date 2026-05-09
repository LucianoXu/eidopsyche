package relay

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
	"github.com/LucianoXu/eidopsyche/internal/relayd"
	"github.com/LucianoXu/eidopsyche/internal/service"
)

var startDir string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Run the relay in the foreground",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := startDir
		if dir == "" {
			d, err := relaycfg.DefaultDir()
			if err != nil {
				return err
			}
			dir = d
		}
		cfg, err := relaycfg.Load(dir)
		if err != nil {
			return fmt.Errorf("load relay config: %w (have you run `eidos relay init`?)", err)
		}

		srv, err := relayd.New(relayd.Config{
			Mode:           relayd.Mode(cfg.Relay.Mode),
			Listen:         cfg.Relay.Listen,
			OwnerHex:       cfg.Relay.OwnerPubkey,
			TLS:            relayd.TLSConfig{CertFile: cfg.Relay.TLS.CertFile, KeyFile: cfg.Relay.TLS.KeyFile},
			Auth:           relayd.AuthConfig{Required: cfg.Relay.Auth.Required, ServiceURL: cfg.Relay.Auth.ServiceURL},
			EventStorePath: relaycfg.EventStorePath(dir),
		})
		if err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()

		fmt.Fprintf(os.Stderr, "eidos-relay listening on %s mode=%s\n", cfg.Relay.Listen, cfg.Relay.Mode)
		// On Windows, when launched by SCM, RunSupervised dispatches via
		// golang.org/x/sys/windows/svc and translates SERVICE_CONTROL_STOP
		// into a context cancellation. Elsewhere it is a pass-through that
		// just runs the closure with the signal-driven context above.
		return service.RunSupervised(ctx, service.RelayUnitName, func(ctx context.Context) error {
			errc := make(chan error, 1)
			go func() { errc <- srv.ListenAndServe() }()
			select {
			case err := <-errc:
				return err
			case <-ctx.Done():
				shutdown, scancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer scancel()
				return srv.Shutdown(shutdown)
			}
		})
	},
}

func init() {
	startCmd.Flags().StringVar(&startDir, "dir", "", "config dir (default: ~/.config/eidos/relay)")
	rootCmd.AddCommand(startCmd)
}
