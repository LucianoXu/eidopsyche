package forge

import (
	"os"
	"os/exec"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newExecCmd() *cobra.Command {
	var asUser string
	cmd := &cobra.Command{
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
			// Only attach a TTY when stdin and stdout are themselves TTYs.
			// In a non-interactive context (scripts, awk pipelines, CI),
			// `docker exec -it` fails with "cannot attach stdin to a
			// TTY-enabled container because stdin is not a terminal".
			// Always pass -i (so stdin is wired through) but pass -t only
			// when both ends look interactive.
			flags := "-i"
			if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
				flags = "-it"
			}
			argv := []string{"exec", flags}
			if asUser != "" {
				argv = append(argv, "-u", asUser)
			}
			argv = append(argv, forgectl.ContainerName(name))
			argv = append(argv, rest...)
			c := exec.Command("docker", argv...) //nolint:gosec // argv built from validated inputs
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
	cmd.Flags().StringVar(&asUser, "user", "", `run as a specific user (e.g. "root" or "1000:1000"); default is the image's USER`)
	return cmd
}
