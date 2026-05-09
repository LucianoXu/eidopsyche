package supervisor

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "supervisor",
	Short: "Container PID 1 — cron + gate daemon + per-wake agent spawn",
}

func init() {
	rootCmd.AddCommand(newRunCmd(), newAgentRunnerCmd())
}

// Command returns the root cobra.Command for the `eidos supervisor` subcommand tree.
func Command() *cobra.Command { return rootCmd }
