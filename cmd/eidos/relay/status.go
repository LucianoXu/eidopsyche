package relay

import (
	"context"
	"fmt"
	"strings"

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

		// Reading the count requires opening the eventstore, which Badger
		// locks exclusively. When `eidos relay start` (or the systemd unit)
		// is already running, that lock is taken — surface the count as
		// "unavailable" instead of failing the whole status command. Any
		// other open error is a real problem and is propagated.
		count, err := readEventCount(relaycfg.EventStorePath(dir))
		switch {
		case err == nil:
			fmt.Fprintf(out, "events:     %d stored\n", count)
		case isEventStoreLocked(err):
			fmt.Fprintf(out, "events:     unavailable (relay process is running and holds the store lock)\n")
		default:
			return err
		}
		return nil
	},
}

// readEventCount opens the eventstore at storePath, counts all events, and
// closes it. Returns (count, nil) on success or (-1, err) on failure.
func readEventCount(storePath string) (int64, error) {
	store, err := relayd.OpenEventStore(storePath)
	if err != nil {
		return -1, err
	}
	defer store.Close()
	count, err := store.CountEvents(context.Background(), gnostr.Filter{})
	if err != nil {
		return -1, fmt.Errorf("count events: %w", err)
	}
	return count, nil
}

// isEventStoreLocked reports whether err looks like Badger's exclusive
// directory lock failing — the typical case when the relay process has
// the store open. Match against the substring rather than a typed error
// because Badger surfaces the lock failure as a wrapped fmt.Errorf.
func isEventStoreLocked(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Another process is using this Badger database") ||
		strings.Contains(msg, "resource temporarily unavailable")
}

func init() {
	statusCmd.Flags().StringVar(&statusDir, "dir", "", "config dir (default: ~/.config/eidos/relay)")
	rootCmd.AddCommand(statusCmd)
}
