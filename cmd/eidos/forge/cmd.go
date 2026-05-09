// Package forge implements the `eidos forge` subcommand tree. It serves
// two surfaces from the same binary:
//
//   - On the host: orchestrate mind-form Docker containers (create, start,
//     stop, status, list, logs, exec, wake, login, ontology, purge).
//   - In the container: reflect on self (whoami, inbox, send, memory,
//     ontology-status, wake).
//
// Selection is by the EIDOS_IN_CONTAINER environment variable; see
// incontainer.go.
package forge

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "forge",
	Short: "MindForge — mind-form lifecycle and self-reflection",
	Long: `eidos forge orchestrates mind-form containers from the host and exposes
self-reflection commands inside the container. Run on the host to manage
mind-forms; run inside a mind-form to inspect and act as the mind-form.`,
}

func init() {
	if InContainer() {
		registerInContainer(rootCmd)
	} else {
		registerHost(rootCmd)
	}
}

// Command returns the root cobra.Command for the `eidos forge` subcommand
// tree.
func Command() *cobra.Command { return rootCmd }
