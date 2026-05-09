package forge

import (
	"os"
	"os/exec"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var image string
	cmd := &cobra.Command{
		Use:   "login <name>",
		Short: "Run `claude /login` interactively in a mind-form's volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			img := image
			if img == "" {
				img = DefaultImage
			}
			argv := []string{
				"run", "-it", "--rm",
				"--mount", "source=" + forgectl.VolumeName(name) + ",target=/eidos",
				"-e", "EIDOS_IN_CONTAINER=1",
				img,
				"claude", "/login",
			}
			c := exec.Command("docker", argv...) //nolint:gosec // argv built from validated inputs
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "override container image")
	return cmd
}
