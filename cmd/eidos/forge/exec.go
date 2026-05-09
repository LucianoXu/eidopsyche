package forge

import (
	"os"
	"os/exec"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

func newExecCmd() *cobra.Command {
	return &cobra.Command{
		Use:                   "exec <name> [-- <cmd...>]",
		Short:                 "Run an interactive shell or command inside the mind-form container",
		Args:                  cobra.MinimumNArgs(1),
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			rest := args[1:]
			if len(rest) == 0 {
				rest = []string{"sh"}
			}
			argv := append([]string{"exec", "-it", forgectl.ContainerName(name)}, rest...)
			c := exec.Command("docker", argv...) //nolint:gosec // argv built from validated inputs
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
}
