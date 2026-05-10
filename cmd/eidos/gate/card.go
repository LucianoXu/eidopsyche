package gate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/store"
)

// cardCmd is both a leaf command (default RunE prints the mindgate://
// URI form, preserving the existing v0.x surface) AND a parent for
// `export` (write a TOML card file) and `show` (validate + pretty-print
// a card file). The two forms are two serializations of the same
// underlying Card struct in internal/card.
var cardCmd = &cobra.Command{
	Use:   "card",
	Short: "Print this entity's mindgate:// card URI (or use 'export' / 'show' for the TOML form)",
	Long: `An identity card introduces an entity (human or mind-form)
to others — bundles label, public-key hex, npub, and home relay.

  eidos gate card           Print this entity's URI form (one-line, paste-friendly)
  eidos gate card export    Write the TOML form to a file (~/eidos-cards/<label>.eidos-card.toml)
  eidos gate card show      Validate + pretty-print any card file`,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp struct {
			URI string `json:"uri"`
		}
		if err := mustOK(c.Call("card.export", nil, &resp)); err != nil {
			return err
		}
		fmt.Println(resp.URI)
		return nil
	},
}

var scanCmd = &cobra.Command{
	Use:   "scan <uri>",
	Short: "Parse a mindgate:// URI without storing",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClient()
		if err != nil {
			return err
		}
		defer c.Close()
		var resp map[string]string
		if err := mustOK(c.Call("card.parse", map[string]string{"uri": args[0]}, &resp)); err != nil {
			return err
		}
		for k, v := range resp {
			fmt.Printf("%s: %s\n", k, v)
		}
		return nil
	},
}

var (
	cardExportOut   string
	cardExportLabel string
)

var cardExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Write this gate's identity as a TOML card file",
	Long: `Reads local gate state directly (no IPC round trip, works
whether the daemon is running or not) and writes a v1 identity card
to --out (default: ~/eidos-cards/<label>.eidos-card.toml).

--label overrides the label written into the file without changing
the gate's stored label.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		stateDir, err := config.ResolveStateDir(globalStateDir)
		if err != nil {
			return err
		}
		out, err := resolveCardExportPath(cardExportOut, cardExportLabel, stateDir)
		if err != nil {
			return err
		}
		return exportCardToFile(stateDir, out, cardExportLabel, time.Now().UTC())
	},
}

var cardShowCmd = &cobra.Command{
	Use:   "show <path>",
	Short: "Pretty-print and validate a TOML card file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := card.Read(args[0])
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "schema_version : %d\n", c.SchemaVersion)
		fmt.Fprintf(out, "label          : %s\n", c.Label)
		fmt.Fprintf(out, "pubkey_hex     : %s\n", c.PubkeyHex)
		fmt.Fprintf(out, "npub           : %s\n", c.Npub)
		fmt.Fprintf(out, "home_relay     : %s\n", c.Relay)
		fmt.Fprintf(out, "created_at     : %s\n", c.CreatedAt.Format(time.RFC3339))
		return nil
	},
}

// resolveCardExportPath computes the default output path when --out is
// empty: ~/eidos-cards/<label>.eidos-card.toml. mkdir -p the parent.
func resolveCardExportPath(outFlag, labelOverride, stateDir string) (string, error) {
	if outFlag != "" {
		return outFlag, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	label := labelOverride
	if label == "" {
		label, err = readGateLabel(stateDir)
		if err != nil {
			return "", err
		}
	}
	if label == "" {
		return "", fmt.Errorf("gate has no label in state.db; pass --label or set one before export")
	}
	return filepath.Join(home, "eidos-cards", label+".eidos-card.toml"), nil
}

// readGateLabel returns the gate's label meta. Empty string + nil error
// when the state.db is reachable but the meta key is unset.
func readGateLabel(stateDir string) (string, error) {
	db, err := store.Open(filepath.Join(stateDir, "state.db"), true)
	if err != nil {
		return "", fmt.Errorf("open state.db: %w", err)
	}
	defer db.Close()
	return db.GetMeta(context.Background(), "label")
}

// exportCardToFile reads gate state from stateDir and writes a v1 Card
// to out. labelOverride, if non-empty, replaces the stored label in
// the card (the gate's own state.db label is NOT modified).
func exportCardToFile(stateDir, out, labelOverride string, now time.Time) error {
	keyPath := filepath.Join(stateDir, "key")
	k, err := identity.LoadKey(keyPath)
	if err != nil {
		return fmt.Errorf("load key: %w", err)
	}
	dbPath := filepath.Join(stateDir, "state.db")
	db, err := store.Open(dbPath, true)
	if err != nil {
		return fmt.Errorf("open state.db: %w", err)
	}
	defer db.Close()
	ctx := context.Background()
	label := labelOverride
	if label == "" {
		label, err = db.GetMeta(ctx, "label")
		if err != nil {
			return fmt.Errorf("read label: %w", err)
		}
	}
	if label == "" {
		return fmt.Errorf("gate has no label; pass --label or set one before export")
	}
	var home string
	if err := db.QueryRowContext(ctx,
		`SELECT relay_url FROM own_relays WHERE role='home' LIMIT 1`).Scan(&home); err != nil {
		return fmt.Errorf("read home relay: %w", err)
	}
	c := card.Card{
		SchemaVersion: 1,
		Label:         label,
		PubkeyHex:     k.PublicHex,
		Npub:          k.Npub,
		Relay:         home,
		CreatedAt:     now,
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("validate card: %w", err)
	}
	if err := card.Write(out, c); err != nil {
		return fmt.Errorf("write card: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ wrote %s\n", out)
	return nil
}

func init() {
	cardExportCmd.Flags().StringVar(&cardExportOut, "out", "", "output path (default ~/eidos-cards/<label>.eidos-card.toml)")
	cardExportCmd.Flags().StringVar(&cardExportLabel, "label", "", "override the label written into the card (does not change local state)")
	cardCmd.AddCommand(cardExportCmd)
	cardCmd.AddCommand(cardShowCmd)
	rootCmd.AddCommand(cardCmd)
	rootCmd.AddCommand(scanCmd)
}
