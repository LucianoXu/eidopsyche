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

func newListCmd() *cobra.Command { return stub("list", "list mind-forms") }
func newLogsCmd() *cobra.Command     { return stub("logs <name>", "show mind-form logs") }
func newExecCmd() *cobra.Command     { return stub("exec <name>", "exec into mind-form container") }
func newWakeHostCmd() *cobra.Command { return stub("wake <name>", "wake a mind-form") }
func newLoginCmd() *cobra.Command {
	return stub("login <name>", "(re)login Claude Code in the mind-form")
}
func newOntologyCmd() *cobra.Command { return stub("ontology", "ontology export/import") }
func newPurgeCmd() *cobra.Command    { return stub("purge <name>", "remove a mind-form") }
func newWhoamiCmd() *cobra.Command   { return stub("whoami", "print self/identity.md + npub") }
func newInboxCmd() *cobra.Command    { return stub("inbox", "list inbox messages") }
func newSendCmd() *cobra.Command     { return stub("send <to> <text>", "send a message") }
func newMemoryCmd() *cobra.Command   { return stub("memory", "memory list/show") }
func newOntologyStatusCmd() *cobra.Command {
	return stub("ontology-status", "git status + log on /eidos/ontology")
}
func newWakeInContainerCmd() *cobra.Command { return stub("wake", "in-container wake submission") }
