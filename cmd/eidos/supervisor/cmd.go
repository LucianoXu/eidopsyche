package supervisor

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "supervisor",
	Short: "Container PID 1 supervisor — cron + gate daemon + agent spawn (not yet implemented)",
	Long: `The supervisor subcommand is the entrypoint for the eidos container image.
It plays PID 1 and supervises three child processes: cron (HeartBeat
scheduling), the gate daemon (Nostr publish/subscribe), and the underlying
Agent runtime that drives the mind-form.

Reserved as a stub; implementation lands alongside MindForge.`,
}

// Command returns the root cobra.Command for the `eidos supervisor`
// subcommand. It is wired up by the parent eidos main package.
func Command() *cobra.Command { return rootCmd }
