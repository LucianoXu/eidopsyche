package gate

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "gate",
	Short:         "MindGate — Eidopsyche identity and communication CLI",
	SilenceUsage:  true,
	SilenceErrors: true,
}

var globalStateDir string

func init() {
	rootCmd.PersistentFlags().StringVar(&globalStateDir, "state-dir", "", "MindGate state directory")
}

// Command returns the root cobra.Command for the `eidos gate` subcommand tree.
// It is wired up by the parent eidos main package.
func Command() *cobra.Command { return rootCmd }
