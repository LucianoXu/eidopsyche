package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/cmd/eidos/forge"
	"github.com/LucianoXu/eidopsyche/cmd/eidos/gate"
	"github.com/LucianoXu/eidopsyche/cmd/eidos/relay"
	"github.com/LucianoXu/eidopsyche/cmd/eidos/supervisor"
	"github.com/LucianoXu/eidopsyche/internal/update"
)

var rootCmd = &cobra.Command{
	Use:   "eidos",
	Short: "Eidopsyche — mind-form social network framework",
	Long: `Eidopsyche is a 心智体 (mind-form) social network framework.
The single eidos binary delivers three component roles via subcommands:

  eidos forge       MindForge: mind-form lifecycle and self-reflection
  eidos gate        MindGate: decentralized comms over Nostr
  eidos supervisor  Container PID 1: cron + gate daemon + agent spawn
  eidos relay       Stand-alone Nostr relay (infrastructure, not an entity)

See https://github.com/LucianoXu/eidopsyche for documentation.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(gate.Command())
	rootCmd.AddCommand(forge.Command())
	rootCmd.AddCommand(supervisor.Command())
	rootCmd.AddCommand(relay.Command())
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(selfUpdateCmd)
}

func main() {
	// Kick off an async update-check refresh; never blocks the user's command.
	// The result lands in the cache for the next invocation to read.
	update.MaybeRefreshAsync(buildInfo())

	err := rootCmd.Execute()

	// Print the prompt after the command runs. version always prints (handled
	// inside versionCmd); other commands print at most once per 24h.
	update.MaybePrompt(buildInfo(), os.Stderr, false)

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
