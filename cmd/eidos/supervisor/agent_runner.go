package supervisor

import "github.com/spf13/cobra"

func newAgentRunnerCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "agent-runner",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.PrintErrln("agent-runner: not yet implemented (Task 4.3)")
			return nil
		},
	}
}
