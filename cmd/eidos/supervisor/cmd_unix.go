//go:build !windows

package supervisor

func init() {
	rootCmd.AddCommand(newRunCmd(), newAgentRunnerCmd())
}
