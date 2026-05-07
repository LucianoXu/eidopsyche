package gate

import (
	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

var rootCmd = &cobra.Command{
	Use:           "gate",
	Short:         "MindGate — Eidopsyche identity and communication CLI",
	SilenceUsage:  true,
	SilenceErrors: true,
	// PersistentPreRunE runs before every gate subcommand. It refuses to
	// proceed when the state directory looks like a pre-v0.5 install (no
	// [relay].enabled field in config.toml). `purge` is exempt — it's the
	// command v0.4 users need to clean up before re-init — so we skip
	// detection by name here. Doing the opt-out at the root keeps v0.4
	// behavior consistent regardless of cobra's EnableTraverseRunHooks
	// global, which would otherwise change whether a child's own
	// PersistentPreRunE overrides the parent's.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Name() == "purge" {
			return nil
		}
		stateDir, err := config.ResolveStateDir(globalStateDir)
		if err != nil {
			return err
		}
		return detectV04State(stateDir)
	},
}

var globalStateDir string

func init() {
	rootCmd.PersistentFlags().StringVar(&globalStateDir, "state-dir", "", "MindGate state directory")
}

// Command returns the root cobra.Command for the `eidos gate` subcommand tree.
// It is wired up by the parent eidos main package.
func Command() *cobra.Command { return rootCmd }
