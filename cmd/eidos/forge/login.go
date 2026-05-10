package forge

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

// newLoginCmd authenticates Claude Code inside the mind-form's volume.
//
// Per SPEC, MindForge prefers Claude Pro/Max subscription auth over
// Anthropic Console (API-billing). In headless containers the in-TUI
// subscription OAuth flow (browser + localhost callback) is broken, so
// we route the operator through the host's claude — where browser +
// localhost work — and copy the resulting credentials into the volume.
//
// Resolution order (default → fallbacks):
//
//  1. Host-claude + setup-token (default). Run `claude setup-token` on
//     the host: real browser opens, long-lived subscription token gets
//     written to the host's ~/.claude.json. Copy ~/.claude.json (and
//     ~/.claude/.credentials.json if present) into the mind-form's
//     volume. Subsequent agent-runner spawns find the credentials
//     under $HOME = /eidos/claude inside the container.
//
//  2. --from-host: skip setup-token, copy whatever ~/.claude.json the
//     operator already has from a prior login. For operators who use
//     Claude Code routinely.
//
//  3. --from-file <path>: copy a specific .claude.json file. For
//     operators driving login from a different machine than where
//     credentials were obtained (e.g., scp from laptop to a server).
//
//  4. --method <console|subscription|setup-token>: in-container OAuth.
//     Only "console" actually works under SSH — the others need a
//     local browser on the container host. Last-resort path.
//
// Empirical notes about the in-container flows we observed (Claude Code
// 2.1.138 against the eidopsyche-mindform image):
//   - `claude /login` (slash): localhost-callback in container; fails.
//   - `claude setup-token`: same.
//   - `claude auth login` (default --claudeai): same.
//   - `claude auth login --console`: hosted callback at
//     platform.claude.com — works under SSH, but bills via API usage,
//     not subscription quota.
func newLoginCmd() *cobra.Command {
	var image, method, fromFile string
	var fromHost bool
	cmd := &cobra.Command{
		Use:   "login <name>",
		Short: "Authenticate Claude Code inside a mind-form's volume (host-claude + setup-token by default)",
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

			// Explicit file path wins over everything.
			if fromFile != "" {
				return installCredentialsFromFile(name, img, fromFile)
			}
			// In-container OAuth flow — opt-in via --method.
			if cmd.Flags().Changed("method") {
				return runInContainerLogin(name, img, method)
			}
			// --from-host: copy host's existing creds, no setup-token.
			if fromHost {
				return installFromHost(name, img, false)
			}
			// Default: drive setup-token on the host, then install.
			return installFromHost(name, img, true)
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "override container image")
	cmd.Flags().BoolVar(&fromHost, "from-host", false,
		"copy host's existing ~/.claude.json into the mind-form (skip setup-token; assumes the operator is already logged in to Claude Code on this machine)")
	cmd.Flags().StringVar(&fromFile, "from-file", "",
		"path to a .claude.json file (e.g., scp'd from a laptop) to copy into the mind-form")
	cmd.Flags().StringVar(&method, "method", "console",
		`fallback in-container OAuth: "console" (works under SSH, bills via API) | "subscription" | "setup-token". Both subscription and setup-token need a real browser on the container host`)
	return cmd
}

// InstallLoginFromHost is the wizard-callable shim around the unexported
// installFromHost. It copies the host's existing ~/.claude.json (and
// ~/.claude/.credentials.json if present) into the new mind-form's
// volume so the in-container claude is logged in before the supervisor
// runs the birth-wake handler. Used by internal/firstcontact/phase3.
//
// Equivalent to running `eidos forge login <name> --from-host`.
func InstallLoginFromHost(name, image string) error {
	return installFromHost(name, image, false)
}

// installFromHost drives the host's claude (optionally running
// setup-token first) and copies the resulting credentials into the
// mind-form's volume.
func installFromHost(name, image string, runSetupToken bool) error {
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf(`no "claude" binary on host PATH; install Claude Code on this machine, or pass --from-file <path> with a credentials file scp'd from a host that does, or use --method console for the in-container Console OAuth flow`)
	}

	if runSetupToken {
		fmt.Printf("running `%s setup-token` on host (a browser will open; complete the subscription login)…\n", claudePath)
		c := exec.Command(claudePath, "setup-token")
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("claude setup-token: %w", err)
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate $HOME: %w", err)
	}
	clauseJSONPath := filepath.Join(homeDir, ".claude.json")
	credsBytes, err := os.ReadFile(clauseJSONPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no credentials at %s; run `claude setup-token` on this host first or use --from-file", clauseJSONPath)
		}
		return fmt.Errorf("read %s: %w", clauseJSONPath, err)
	}

	if err := writeIntoVolume(name, image, "/eidos/claude/.claude.json", bytes.NewReader(credsBytes)); err != nil {
		return err
	}
	// On Linux, subscription tokens may live in ~/.claude/.credentials.json
	// rather than ~/.claude.json. Copy if present; ignore if not.
	credsExtraPath := filepath.Join(homeDir, ".claude", ".credentials.json")
	if extra, err := os.ReadFile(credsExtraPath); err == nil {
		if err := writeIntoVolume(name, image, "/eidos/claude/.claude/.credentials.json", bytes.NewReader(extra)); err != nil {
			return err
		}
	}
	if err := clearAuthRequiredInVolume(name, image); err != nil {
		// Hard error. Without the marker cleared, agent-runner's
		// self-gate keeps refusing to invoke claude — so a "login OK"
		// message followed by a permanently-locked mind-form is much
		// worse than failing login here. Operator can re-run after
		// fixing the underlying issue (typically docker not running).
		return fmt.Errorf("login: clear auth_required marker: %w", err)
	}
	fmt.Printf("✓ credentials installed into eidos-mindform-%s\n", name)
	return nil
}

// installCredentialsFromFile is --from-file's implementation.
func installCredentialsFromFile(name, image, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if err := writeIntoVolume(name, image, "/eidos/claude/.claude.json", f); err != nil {
		return err
	}
	if err := clearAuthRequiredInVolume(name, image); err != nil {
		return fmt.Errorf("login: clear auth_required marker: %w", err)
	}
	fmt.Printf("✓ credentials installed into eidos-mindform-%s from %s\n", name, path)
	return nil
}

// clearAuthRequiredInVolume removes /eidos/run/auth_required.json
// inside the mind-form's volume after a successful login. Uses a
// one-shot helper container (works regardless of whether the
// long-running container is up) and tolerates absence (rm -f).
func clearAuthRequiredInVolume(name, image string) error {
	c := exec.Command("docker", "run", "--rm",
		"--mount", "source="+forgectl.VolumeName(name)+",target=/eidos",
		"--entrypoint", "sh",
		image,
		"-c", "rm -f /eidos/run/auth_required.json",
	) //nolint:gosec // argv built from validated inputs
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("clear auth_required marker: %w", err)
	}
	return nil
}

// writeIntoVolume copies a file into the mind-form's volume by piping
// the content through a one-shot `docker run` that does
// `mkdir -p $(dirname dst) && cat > dst`.
func writeIntoVolume(name, image, dst string, content io.Reader) error {
	parent := filepath.Dir(dst)
	script := fmt.Sprintf(`mkdir -p %q && cat > %q && chmod 600 %q`, parent, dst, dst)
	c := exec.Command("docker", "run", "--rm", "-i",
		"--mount", "source="+forgectl.VolumeName(name)+",target=/eidos",
		"--entrypoint", "sh",
		image,
		"-c", script,
	) //nolint:gosec // argv built from validated inputs
	c.Stdin = content
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("docker write to volume %s: %w", dst, err)
	}
	return nil
}

// runInContainerLogin is the legacy in-container OAuth path, retained
// as a fallback when the host has no claude.
func runInContainerLogin(name, image, method string) error {
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
		image,
	}, claudeArgv...)
	c := exec.Command("docker", argv...) //nolint:gosec // argv built from validated inputs
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return err
	}
	if err := clearAuthRequiredInVolume(name, image); err != nil {
		return fmt.Errorf("login: clear auth_required marker: %w", err)
	}
	return nil
}
