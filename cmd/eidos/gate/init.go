package gate

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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

var (
	initLabel string
	initHome  string
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a MindGate state directory and identity",
	RunE:  runInit,
}

func init() {
	initCmd.Flags().StringVar(&initLabel, "label", "", "label for this identity (required; how others see your card by default — change later with `eidos gate set-label`)")
	initCmd.Flags().StringVar(&initHome, "home", "", "home relay URL (required; ws:// or wss://) — the URL peers will dial to reach you")
	if err := initCmd.MarkFlagRequired("label"); err != nil {
		panic(err) // Cobra returns nil for known flags; surfacing a panic here is appropriate for a setup bug.
	}
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	// --home is required and must be ws:// or wss://.
	if initHome == "" {
		return errors.New("--home is required (the inbound relay URL peers will dial); see docs/USAGE.md for topology choices")
	}
	parsed, err := url.Parse(initHome)
	if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" {
		return fmt.Errorf("--home must start with ws:// or wss:// and include a host, got %q", initHome)
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
	if err := db.SetMeta(ctx, "label", initLabel); err != nil {
		return err
	}

	// own_relays(role='home') uses --home: the URL peers dial to reach this gate.
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO own_relays(relay_url,role,added_at) VALUES(?,?,?)`,
		initHome, "home", time.Now().Unix()); err != nil {
		return err
	}

	cfg := config.Defaults()
	if err := config.Save(filepath.Join(dir, "config.toml"), cfg); err != nil {
		return err
	}

	fmt.Printf("✓ created %s\n", dir)
	fmt.Printf("✓ generated keypair → %s (0600)\n", keyPath)
	fmt.Printf("✓ wrote state.db (schema v%d)\n", store.SchemaVersion)
	fmt.Printf("✓ wrote config.toml\n")
	fmt.Printf("  home relay: %s\n", initHome)
	fmt.Println("\nyour identity:")
	fmt.Printf("  npub: %s\n", k.Npub)
	fmt.Printf("  hex:  %s\n", k.PublicHex)
	fmt.Println("\nnext steps:")
	fmt.Println("  1) start gate:     eidos gate start")
	fmt.Println("  2) start relay:    eidos relay init && eidos relay service install && eidos relay service start")
	fmt.Println("  3) share card:     eidos gate card")
	return nil
}
