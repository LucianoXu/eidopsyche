package supervisor

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "supervisor",
	Short: "Container PID 1 — cron + gate daemon + long-lived agent-loop",
}

// Command returns the root cobra.Command for the `eidos supervisor` subcommand tree.
//
// Subcommands (run, agent-loop) are registered only on platforms where
// the supervisor can actually execute — see cmd_unix.go. On Windows the
// command tree exists but has no children: the supervisor is container
// PID 1 inside Linux mind-form containers and never runs on the host's
// Windows OS, so the cross-compiled binary keeps the namespace but
// can't dispatch into it.
func Command() *cobra.Command { return rootCmd }
