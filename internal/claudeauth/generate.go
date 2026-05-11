// internal/claudeauth/generate.go
package claudeauth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Generate drives `claude setup-token` against an isolated HOME so the
// resulting OAuth token never lands in the operator's host claude
// install. Stdin/stdout/stderr are wired to the operator's terminal
// (caller passes os.Stdin / os.Stdout / os.Stderr) so they see the
// device-authorization URL and any progress prompts directly.
//
// On exit 0, reads $HOME/.claude/.credentials.json and returns its
// contents. The temporary HOME is removed before returning, so the
// credentials live only in memory until the caller writes them into the
// mindform's volume.
//
// claudeBin defaults to "claude" but is parameterised so tests can
// substitute a stub script.
func Generate(stdin io.Reader, stdout, stderr io.Writer, claudeBin string) ([]byte, error) {
	if claudeBin == "" {
		claudeBin = "claude"
	}
	if _, err := exec.LookPath(claudeBin); err != nil {
		// Allow absolute paths to stubs that aren't on PATH.
		if _, statErr := os.Stat(claudeBin); statErr != nil {
			return nil, fmt.Errorf("locate %q: %w", claudeBin, err)
		}
	}

	var idBytes [8]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, fmt.Errorf("random suffix: %w", err)
	}
	suffix := hex.EncodeToString(idBytes[:])
	home := filepath.Join("/tmp", "eidos-setup-"+suffix)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		return nil, fmt.Errorf("temp HOME: %w", err)
	}
	defer os.RemoveAll(home)

	cmd := exec.Command(claudeBin, "setup-token")
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
	)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("claude setup-token: %w", err)
	}

	credsPath := filepath.Join(home, ".claude", ".credentials.json")
	body, err := os.ReadFile(credsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", credsPath, err)
	}
	return body, nil
}
