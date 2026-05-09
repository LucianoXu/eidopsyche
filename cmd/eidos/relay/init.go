package relay

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
	"github.com/LucianoXu/eidopsyche/internal/relayd"
)

// resolveOwnerPubkey returns the hex form of an --owner argument that may
// be either a bech32 npub or a 64-char hex string. Hex is what relayd's
// paired-mode address-match compares against, so accepting npub here saves
// users from having to convert by hand.
func resolveOwnerPubkey(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "npub1") {
		hex, err := identity.DecodeNpub(s)
		if err != nil {
			return "", fmt.Errorf("decode npub: %w", err)
		}
		return hex, nil
	}
	if len(s) != 64 {
		return "", fmt.Errorf("--owner must be a 64-char hex pubkey or an npub1… string (got %d chars)", len(s))
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return "", fmt.Errorf("--owner is 64 chars but not hex (non-hex char %q)", r)
		}
	}
	return strings.ToLower(s), nil
}

type initOpts struct {
	dir    string
	mode   string
	listen string
	owner  string
	force  bool
}

var initFlags initOpts

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a relay config dir at ~/.config/eidos/relay (or --dir)",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := initFlags.dir
		if dir == "" {
			d, err := relaycfg.DefaultDir()
			if err != nil {
				return err
			}
			dir = d
		}
		opts := initFlags
		opts.dir = dir
		return runInit(opts)
	},
}

func runInit(o initOpts) error {
	if strings.TrimSpace(o.mode) == "" {
		return fmt.Errorf("--mode is required (\"paired\" or \"public\")")
	}
	if strings.TrimSpace(o.listen) == "" {
		return fmt.Errorf("--listen is required (host:port)")
	}
	ownerHex := ""
	switch o.mode {
	case "paired":
		if strings.TrimSpace(o.owner) == "" {
			return fmt.Errorf("--owner is required when --mode=paired (npub1… or 64-char hex)")
		}
		hex, err := resolveOwnerPubkey(o.owner)
		if err != nil {
			return err
		}
		ownerHex = hex
	case "public":
		if strings.TrimSpace(o.owner) != "" {
			return fmt.Errorf("--owner must not be set when --mode=public")
		}
	default:
		return fmt.Errorf("--mode must be \"paired\" or \"public\" (got %q)", o.mode)
	}

	cfgPath := filepath.Join(o.dir, relaycfg.FileName)
	if _, err := os.Stat(cfgPath); err == nil {
		if !o.force {
			return fmt.Errorf("relay already initialized at %s (re-run with --force to overwrite)", cfgPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", cfgPath, err)
	}
	if err := os.MkdirAll(o.dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", o.dir, err)
	}

	cfg := relaycfg.Defaults()
	cfg.Relay.Mode = o.mode
	cfg.Relay.Listen = o.listen
	cfg.Relay.OwnerPubkey = ownerHex
	if err := relaycfg.Save(o.dir, cfg); err != nil {
		return err
	}

	// Touch the event store now so a fresh `relay start` doesn't have
	// to create-on-first-write. Also catches FS permission issues at init.
	store, err := relayd.OpenEventStore(relaycfg.EventStorePath(o.dir))
	if err != nil {
		return fmt.Errorf("create event store: %w", err)
	}
	store.Close()

	fmt.Printf("relay initialized at %s\n", o.dir)
	return nil
}

func init() {
	initCmd.Flags().StringVar(&initFlags.dir, "dir", "", "config dir (default: ~/.config/eidos/relay)")
	initCmd.Flags().StringVar(&initFlags.mode, "mode", "", "paired | public (required)")
	initCmd.Flags().StringVar(&initFlags.listen, "listen", "", "address to bind, e.g. 0.0.0.0:7777 (required)")
	initCmd.Flags().StringVar(&initFlags.owner, "owner", "", "owner pubkey, npub1… or 64-char hex (required iff --mode=paired)")
	initCmd.Flags().BoolVar(&initFlags.force, "force", false, "overwrite existing config")
	rootCmd.AddCommand(initCmd)
}
