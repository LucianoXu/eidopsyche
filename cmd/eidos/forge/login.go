package forge

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

// newLoginCmd authenticates Claude Code inside the mind-form's volume.
//
// Prior versions invoked `claude /login` (the in-TUI slash command). That
// flow spins up a localhost OAuth callback server in the container and
// expects a browser running on the same host to reach it — which never
// works for `docker run -it` over SSH and frequently emits an OAuth URL
// missing the redirect_uri parameter, leaving operators stranded with
// "Invalid OAuth Request — Missing redirect_uri parameter".
//
// The headless-friendly equivalents are:
//   - `claude setup-token`   — long-lived token, ideal for unattended
//     daemons; requires a Claude Pro/Max subscription.
//   - `claude auth login`    — interactive OAuth, designed for the CLI
//     (browser opens locally; the user pastes the resulting code back).
//
// Flag --method picks between them. Default is `setup-token` because
// long-lived tokens match the mind-form's "wake unattended forever"
// shape; --method auth-login falls back to the OAuth path.
func newLoginCmd() *cobra.Command {
	var image, method string
	cmd := &cobra.Command{
		Use:   "login <name>",
		Short: "Authenticate Claude Code inside a mind-form's volume",
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

			var claudeArgv []string
			switch method {
			case "setup-token":
				claudeArgv = []string{"claude", "setup-token"}
			case "auth-login":
				claudeArgv = []string{"claude", "auth", "login"}
			default:
				return fmt.Errorf(`--method must be "setup-token" or "auth-login" (got %q)`, method)
			}

			argv := append([]string{
				"run", "-it", "--rm",
				"--mount", "source=" + forgectl.VolumeName(name) + ",target=/eidos",
				"-e", "EIDOS_IN_CONTAINER=1",
				img,
			}, claudeArgv...)
			c := exec.Command("docker", argv...) //nolint:gosec // argv built from validated inputs
			c.Stdin = os.Stdin
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			return c.Run()
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "override container image")
	cmd.Flags().StringVar(&method, "method", "setup-token", "auth method: setup-token (long-lived token, requires Claude subscription) | auth-login (browser OAuth)")
	return cmd
}
