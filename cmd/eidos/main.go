package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/cmd/eidos/forge"
	"github.com/LucianoXu/eidopsyche/cmd/eidos/gate"
	"github.com/LucianoXu/eidopsyche/cmd/eidos/relay"
	"github.com/LucianoXu/eidopsyche/cmd/eidos/summon"
	"github.com/LucianoXu/eidopsyche/cmd/eidos/supervisor"
	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/firstcontact"
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
  eidos summon      First Contact wizard: guided ritual to summon a new mind-form

When run with no subcommand AND no existing state, eidos auto-launches
the First Contact wizard. Returning users see this help message instead.

See https://github.com/LucianoXu/eidopsyche for documentation.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(gate.Command())
	rootCmd.AddCommand(forge.Command())
	rootCmd.AddCommand(supervisor.Command())
	rootCmd.AddCommand(relay.Command())
	rootCmd.AddCommand(summon.Command())
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(selfUpdateCmd)
}

func main() {
	// Kick off an async update-check refresh; never blocks the user's command.
	// The result lands in the cache for the next invocation to read.
	update.MaybeRefreshAsync(buildInfo())

	if shouldDispatchToWizard() {
		if err := summon.Run(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		update.MaybePrompt(buildInfo(), os.Stderr, false)
		return
	}

	err := rootCmd.Execute()

	// Print the prompt after the command runs. version always prints (handled
	// inside versionCmd); other commands print at most once per 24h.
	update.MaybePrompt(buildInfo(), os.Stderr, false)

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// shouldDispatchToWizard reports whether bare `eidos` (no subcommand,
// no flags) should auto-launch the First Contact wizard. Triggers when
// argv has no subcommand AND the state directory is not yet
// initialized. "Initialized" uses the same predicate the wizard itself
// uses to decide whether the host is identity-initialized
// (firstcontact.IsIdentityInitialized) so the two layers cannot drift:
// an operator with a complete identity always sees cobra's help on
// bare `eidos`, never the wizard.
func shouldDispatchToWizard() bool {
	if len(os.Args) != 1 {
		return false
	}
	dir, err := config.ResolveStateDir("")
	if err != nil {
		return false
	}
	return !firstcontact.IsIdentityInitialized(dir)
}
