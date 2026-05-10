package gate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/store"
)

var (
	initLabel           string
	initHome            string
	initService         bool
	initFromExistingKey bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a MindGate state directory and identity",
	RunE:  runInit,
}

func init() {
	initCmd.Flags().StringVar(&initLabel, "label", "", "label for this identity (required; how others see your card by default — change later with `eidos gate set-label`)")
	initCmd.Flags().StringVar(&initHome, "home", "", "home relay URL (required; ws:// or wss://) — the URL peers will dial to reach you")
	initCmd.Flags().BoolVar(&initService, "service", false, "after initializing, install and start the gate daemon service in one step")
	initCmd.Flags().BoolVar(&initFromExistingKey, "key-from-existing", false, "use an already-written <state-dir>/key (the caller wrote it; skip key generation). Used by the First Contact wizard's keypair-injection path.")
	addSystemFlag(initCmd) // --system writes to the same useSystemServices var as start/stop/etc.
	if err := initCmd.MarkFlagRequired("label"); err != nil {
		panic(err) // Cobra returns nil for known flags; surfacing a panic here is appropriate for a setup bug.
	}
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	if initHome == "" {
		return errors.New("--home is required (the inbound relay URL peers will dial); see docs/USAGE.md for topology choices")
	}

	dir, err := config.ResolveStateDir(globalStateDir)
	if err != nil {
		return err
	}

	// Idempotent guard: if the host is already identity-initialized,
	// report it and exit 0. Spec § 4.3 — gate init delegates the
	// "already initialized" decision to the same predicate the wizard
	// and main.go's auto-dispatch consult, so the three layers cannot
	// drift on what counts as initialized.
	if firstcontact.IsIdentityInitialized(dir) {
		label, _ := readGateLabel(dir)
		if label == "" {
			label = "(unset)"
		}
		fmt.Fprintf(cmd.OutOrStdout(),
			"this host already has a gate identity at %s (label: %s)\nnothing to do\n", dir, label)
		return nil
	}

	var npub string
	if initFromExistingKey {
		npub, err = identity.BootstrapWithExistingKey(dir, initLabel, initHome)
	} else {
		npub, err = identity.Bootstrap(dir, initLabel, initHome)
	}
	if err != nil {
		if errors.Is(err, identity.ErrAlreadyInitialized) {
			return fmt.Errorf("refusing to overwrite existing identity at %s", dir)
		}
		return err
	}

	keyPath := filepath.Join(dir, "key")
	fmt.Printf("✓ created %s\n", dir)
	fmt.Printf("✓ generated keypair → %s (0600)\n", keyPath)
	fmt.Printf("✓ wrote state.db (schema v%d)\n", store.SchemaVersion)
	fmt.Printf("✓ wrote config.toml\n")
	fmt.Printf("  home relay: %s\n", initHome)
	fmt.Println("\nyour identity:")
	fmt.Printf("  npub: %s\n", npub)
	if k, lerr := identity.LoadKey(keyPath); lerr == nil {
		fmt.Printf("  hex:  %s\n", k.PublicHex)
	}

	if initService {
		ctx := context.Background()
		mgr, err := buildServiceManager()
		if err != nil {
			return fmt.Errorf("install service: %w", err)
		}
		if err := mgr.StartDaemon(ctx); err != nil {
			return fmt.Errorf("start service: %w", err)
		}
		fmt.Println("\n✓ gate daemon service installed and started")
		fmt.Println("  next: eidos gate card           # show your identity card")
		return nil
	}

	fmt.Println("\nnext steps:")
	fmt.Println("  eidos gate start                # install and start the daemon (re-run with `eidos gate init --service` to do both at init time)")
	fmt.Println("  eidos gate card                 # show your identity card")
	fmt.Println("\noptional — run a self-hosted relay on this host:")
	fmt.Println("  eidos relay init --mode public --listen 0.0.0.0:7777 --service")
	fmt.Println("  # or: eidos relay init --mode paired --owner <npub> --listen 0.0.0.0:7777 --service")
	return nil
}
