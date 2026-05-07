package gate

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/relayd"
	"github.com/LucianoXu/eidopsyche/internal/store"
)

var (
	relayMode   string
	relayListen string
)

var relayCmd = &cobra.Command{
	Use:   "relay",
	Short: "Run the embedded MindGate relay",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := config.ResolveStateDir(globalStateDir)
		if err != nil {
			return err
		}
		cfg, _ := config.Load(filepath.Join(dir, "config.toml"))
		mode := relayMode
		if mode == "" {
			mode = cfg.Relay.Mode
		}
		listen := relayListen
		if listen == "" {
			listen = cfg.Relay.Listen
		}
		dbPath := filepath.Join(dir, "state.db")
		db, err := store.Open(dbPath, true)
		if err != nil {
			return err
		}
		owner, err := db.GetMeta(context.Background(), "owner_pubkey")
		if err != nil {
			return err
		}
		wl := relayd.NewWhitelistSource(db, time.Second)
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		go wl.Run(ctx)
		srv, err := relayd.New(relayd.Config{
			Mode:      relayd.Mode(mode),
			Listen:    listen,
			OwnerHex:  owner,
			Whitelist: wl,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "mindgate-relay listening %s mode=%s\n", listen, mode)
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
	},
}

func init() {
	relayCmd.Flags().StringVar(&relayMode, "mode", "", "paired or public")
	relayCmd.Flags().StringVar(&relayListen, "listen", "", "address to listen on")
	rootCmd.AddCommand(relayCmd)
}
