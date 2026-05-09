package forge

import (
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func newSendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "send <to> [<content>]",
		Short:              "Send a NIP-17 message (proxies `eidos gate send`)",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			argv := append([]string{"gate", "send", "--state-dir", "/eidos/gate"}, args...)
			c := exec.Command("eidos", argv...)
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			c.Stdin = os.Stdin
			return c.Run()
		},
	}
	return cmd
}
