package forge

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "forge",
	Short: "MindForge — mind-form lifecycle and self-reflection (not yet implemented)",
	Long: `MindForge is the mind-form lifecycle and self-reflection framework.
It manages mind-form Docker instances on the host (create / start / stop / status
/ list / logs / exec / wake) and exposes self-reflection commands inside the
container (whoami / memory / skills / config / ontology).

The forge subcommand is reserved here as a stub; implementation lands in a
future release.`,
}

// Command returns the root cobra.Command for the `eidos forge` subcommand
// tree. It is wired up by the parent eidos main package.
func Command() *cobra.Command { return rootCmd }
