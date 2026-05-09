// Package relay implements the `eidos relay` subcommand tree:
// stateless infrastructure operations distinct from `eidos gate`.
package relay

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "relay",
	Short: "Run and manage an eidos Nostr relay",
	Long: `eidos relay manages a stand-alone Nostr relay process.

The relay is infrastructure, not an entity: it has no contacts,
no inbox, no NIP-17 messaging state. A relay-only host runs
` + "`eidos relay init` then `eidos relay service install/start`" + ` and
never invokes ` + "`eidos gate`" + `.`,
}

// Command returns the cobra root for `eidos relay`. Registered in
// cmd/eidos/main.go alongside forge, gate, and supervisor.
func Command() *cobra.Command { return rootCmd }
