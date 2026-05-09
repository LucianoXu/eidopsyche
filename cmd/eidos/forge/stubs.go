package forge

import "github.com/spf13/cobra"

// stub returns a cobra.Command that prints "not yet implemented" and exits 0.
func stub(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.PrintErrf("%s: not yet implemented\n", use)
			return nil
		},
	}
}

func newWhoamiCmd() *cobra.Command { return stub("whoami", "print self/identity.md + npub") }
func newInboxCmd() *cobra.Command  { return stub("inbox", "list inbox messages") }
func newSendCmd() *cobra.Command   { return stub("send <to> <text>", "send a message") }
func newMemoryCmd() *cobra.Command { return stub("memory", "memory list/show") }
func newOntologyStatusCmd() *cobra.Command {
	return stub("ontology-status", "git status + log on /eidos/ontology")
}
func newWakeInContainerCmd() *cobra.Command { return stub("wake", "in-container wake submission") }
