package forge

import (
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func newInboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "inbox [-- <gate-inbox-args...>]",
		Short:              "List inbox messages (proxies `eidos gate inbox`)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := append([]string{"gate", "inbox", "--state-dir", "/eidos/gate"}, args...)
			c := exec.Command("eidos", argv...)
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			c.Stdin = os.Stdin
			return c.Run()
		},
	}
	return cmd
}
