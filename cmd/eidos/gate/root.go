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
	// [relay].enabled field in config.toml). Subcommands that must keep
	// working on v0.4 state — purge — set their own PersistentPreRunE
	// returning nil to override.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
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
