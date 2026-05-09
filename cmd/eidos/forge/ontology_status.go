package forge

import (
	"os/exec"

	"github.com/spf13/cobra"
)

func newOntologyStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ontology-status",
		Short: "git status + git log -5 on /eidos/ontology",
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, argv := range [][]string{
				{"git", "-C", "/eidos/ontology", "status", "-s"},
				{"git", "-C", "/eidos/ontology", "log", "--oneline", "-5"},
			} {
				c := exec.Command(argv[0], argv[1:]...)
				c.Stdout = cmd.OutOrStdout()
				c.Stderr = cmd.ErrOrStderr()
				_ = c.Run()
			}
			return nil
		},
	}
}
