package relay

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
	"github.com/LucianoXu/eidopsyche/internal/relayd"
)

var statusDir string

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show relay configuration and event-store size",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := resolveConfigDir(statusDir)
		if err != nil {
			return err
		}
		cfg, err := relaycfg.Load(dir)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "config dir: %s\n", dir)
		fmt.Fprintf(out, "mode:       %s\n", cfg.Relay.Mode)
		fmt.Fprintf(out, "listen:     %s\n", cfg.Relay.Listen)
		if cfg.Relay.Mode == "paired" {
			fmt.Fprintf(out, "owner:      %s\n", cfg.Relay.OwnerPubkey)
		}
		fmt.Fprintf(out, "tls:        cert=%q key=%q\n", cfg.Relay.TLS.CertFile, cfg.Relay.TLS.KeyFile)
		fmt.Fprintf(out, "auth:       required=%t service_url=%q\n", cfg.Relay.Auth.Required, cfg.Relay.Auth.ServiceURL)

		store, err := relayd.OpenEventStore(relaycfg.EventStorePath(dir))
		if err != nil {
			return err
		}
		defer store.Close()
		count, err := store.CountEvents(context.Background(), gnostr.Filter{})
		if err != nil {
			return fmt.Errorf("count events: %w", err)
		}
		fmt.Fprintf(out, "events:     %d stored\n", count)
		return nil
	},
}

func init() {
	statusCmd.Flags().StringVar(&statusDir, "dir", "", "config dir (default: ~/.config/eidos/relay)")
	rootCmd.AddCommand(statusCmd)
}
