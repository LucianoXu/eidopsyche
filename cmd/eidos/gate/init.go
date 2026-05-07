package gate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/store"
	"github.com/LucianoXu/eidopsyche/internal/version"
)

var initLabel string
var initListen string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a MindGate state directory and identity",
	RunE:  runInit,
}

func init() {
	initCmd.Flags().StringVar(&initLabel, "label", "", "label for this identity (default user@hostname)")
	initCmd.Flags().StringVar(&initListen, "listen", "", "relay listen address as host:port (e.g. 127.0.0.1:22896); sets relay.listen, relay.public_url, and the home relay row")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	// Validate --listen before writing any files.
	if initListen != "" {
		if _, _, err := net.SplitHostPort(initListen); err != nil {
			return fmt.Errorf("invalid --listen address %q: must be host:port (%w)", initListen, err)
		}
	}

	dir, err := config.ResolveStateDir(globalStateDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	keyPath := filepath.Join(dir, "key")
	if _, err := os.Stat(keyPath); err == nil {
		return fmt.Errorf("refusing to overwrite existing key at %s", keyPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	k, err := identity.Generate()
	if err != nil {
		return err
	}
	if err := identity.SaveKey(keyPath, k); err != nil {
		return err
	}
	dbPath := filepath.Join(dir, "state.db")
	db, err := store.Open(dbPath, false)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	if err := db.SetMeta(ctx, "owner_pubkey", k.PublicHex); err != nil {
		return err
	}
	if err := db.SetMeta(ctx, "created_at", strconv.FormatInt(time.Now().Unix(), 10)); err != nil {
		return err
	}
	if err := db.SetMeta(ctx, "mindgate_version", version.Version); err != nil {
		return err
	}
	label := initLabel
	if label == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "host"
		}
		label = "user@" + host
	}
	if err := db.SetMeta(ctx, "label", label); err != nil {
		return err
	}

	// Determine home relay URL from --listen or default.
	homeRelayURL := "ws://127.0.0.1:22895"
	if initListen != "" {
		homeRelayURL = "ws://" + initListen
	}
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO own_relays(relay_url,role,added_at) VALUES(?,?,?)`,
		homeRelayURL, "home", time.Now().Unix()); err != nil {
		return err
	}

	cfg := config.Defaults()
	if initListen != "" {
		cfg.Relay.Listen = initListen
		cfg.Relay.PublicURL = "ws://" + initListen
	}
	if err := config.Save(filepath.Join(dir, "config.toml"), cfg); err != nil {
		return err
	}
	fmt.Printf("✓ created %s\n", dir)
	fmt.Printf("✓ generated keypair → %s (0600)\n", keyPath)
	fmt.Printf("✓ wrote state.db (schema v%d)\n", store.SchemaVersion)
	fmt.Printf("✓ wrote config.toml\n")
	if initListen != "" {
		fmt.Printf("  home relay: %s\n", homeRelayURL)
	}
	fmt.Println("\nyour identity:")
	fmt.Printf("  npub: %s\n", k.Npub)
	fmt.Printf("  hex:  %s\n", k.PublicHex)
	fmt.Println("\nnext steps:")
	fmt.Println("  1) start daemon: mindgate daemon")
	fmt.Println("  2) start relay:  mindgate relay")
	fmt.Println("  3) share card:   mindgate card")
	return nil
}
