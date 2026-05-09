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
// Auth-method ergonomics in a headless container are subtle. Empirically
// (Claude Code 2.1.138 against the eidopsyche-mindform image):
//
//   - `claude /login`           — in-TUI slash command. Spins up a
//     localhost callback server in the container and tries to open a
//     browser. Fails over `docker run -it`/SSH; URLs emitted often lack
//     the redirect_uri param, producing "Invalid OAuth Request —
//     Missing redirect_uri parameter" at Anthropic's auth gateway.
//   - `claude setup-token`      — long-lived token; also TUI-driven and
//     also relies on browser-open + localhost callback. Same failure
//     mode in containers.
//   - `claude auth login`       — defaults to the Claude subscription
//     flow, which is also browser+localhost-callback. Same failure.
//   - `claude auth login --console` — Anthropic Console (API billing)
//     OAuth. Prints a clean URL whose redirect_uri points at
//     platform.claude.com's hosted callback page. The operator opens
//     the URL, authorizes, copies the code from the resulting page, and
//     pastes it back in the TTY. Works under `docker run -it` from any
//     SSH session.
//
// We default to --method=console because it is the only one that
// actually works in this deployment shape. The other paths remain
// reachable for operators with environments where they do work
// (e.g., a desktop-installed eidos invoking forge login on a local
// container with a real browser).
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
			case "console":
				claudeArgv = []string{"claude", "auth", "login", "--console"}
			case "subscription":
				claudeArgv = []string{"claude", "auth", "login"}
			case "setup-token":
				claudeArgv = []string{"claude", "setup-token"}
			default:
				return fmt.Errorf(`--method must be "console" | "subscription" | "setup-token" (got %q)`, method)
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
	cmd.Flags().StringVar(&method, "method", "console",
		`auth method: "console" (Console OAuth, hosted callback — works under SSH; default) | `+
			`"subscription" (Claude Pro/Max OAuth — needs a real browser on the same host) | `+
			`"setup-token" (long-lived token — same browser requirement)`)
	return cmd
}
