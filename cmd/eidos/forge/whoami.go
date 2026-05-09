package forge

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Print self/identity.md + npub + master + relay",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if body, err := os.ReadFile("/eidos/ontology/self/identity.md"); err == nil {
				fmt.Fprintln(cmd.OutOrStdout(), string(body))
			}
			c := exec.Command("eidos", "gate", "whoami", "--state-dir", "/eidos/gate")
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			return c.Run()
		},
	}
}
