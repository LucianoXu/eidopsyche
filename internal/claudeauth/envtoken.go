// internal/claudeauth/envtoken.go
//
// Env-var injection path for setup-tokens — the structural answer to
// v0.13.1's WrapSetupToken, which fabricated a credentials.json blob
// matching a reverse-engineered claude runtime check.
//
// Operators with a raw sk-ant-oat01-... setup-token from
// claude.ai/setup install it via `eidos forge login <name>
// --setup-token-stdin`. WriteSetupTokenToVolume writes the bytes to
// /eidos/claude/setup_token (mode 600). The supervisor's claude
// spawn sites then call InjectSetupTokenEnv at each spawn to read the
// file and append CLAUDE_CODE_OAUTH_TOKEN=<token> to claude's
// environment. Claude treats that env var as a first-class
// authentication input — its in-memory credential object is
// constructed internally with the right shape, so we are not at the
// mercy of guard changes between claude releases.
//
// File-based paths (--token-file, --paste, --generate) still write
// /eidos/claude/.claude/.credentials.json. The two are mutually
// exclusive: WriteSetupTokenToVolume removes any stale credentials
// file (and writeToVolumeUsing removes any stale setup-token file)
// so claude doesn't see both and pick the wrong precedence.
package claudeauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Paths inside the mindform's volume, relative to /eidos. Both live
// under claude/ because Dockerfile sets HOME=/eidos/claude.
const (
	// SetupTokenFile is the raw setup-token (single line, no
	// JSON wrapper). Sibling of the .claude/ dir.
	SetupTokenFile = "claude/setup_token"
	// CredentialsFile is the on-disk OAuth blob claude reads via
	// its $HOME/.claude/.credentials.json default path.
	CredentialsFile = "claude/.claude/.credentials.json"
)

// EnvOAuthTokenVar is the env-var name claude reads to pick up a
// long-lived OAuth access token from process environment.
const EnvOAuthTokenVar = "CLAUDE_CODE_OAUTH_TOKEN"

// WriteSetupTokenToVolume installs a raw setup-token into the
// mindform's volume and removes any stale credentials file so the
// env-var path wins unambiguously at the next claude spawn.
func WriteSetupTokenToVolume(ctx context.Context, name, image, token string) error {
	w, err := NewForgectlVolumeWriter(ctx, name, image)
	if err != nil {
		return err
	}
	return writeSetupTokenToVolumeUsing(w, token)
}

// WriteSetupTokenToVolumeUsing is the test seam — production callers
// should use WriteSetupTokenToVolume.
func WriteSetupTokenToVolumeUsing(w VolumeWriter, token string) error {
	return writeSetupTokenToVolumeUsing(w, token)
}

func writeSetupTokenToVolumeUsing(w VolumeWriter, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("empty setup-token")
	}
	if err := w.Write(SetupTokenFile, []byte(token)); err != nil {
		return fmt.Errorf("write setup-token: %w", err)
	}
	// Avoid claude picking the file path over the env: if a previous
	// --token-file/--paste/--generate left a credentials.json behind,
	// claude's own LN5 guard prefers the file (with refreshToken) and
	// would never read our env var.
	if err := w.Remove(CredentialsFile); err != nil {
		return fmt.Errorf("remove stale credentials: %w", err)
	}
	if err := w.ClearAuthRequired(); err != nil {
		return fmt.Errorf("clear auth_required: %w", err)
	}
	return nil
}

// ReadSetupTokenFromHome reads $home/setup_token and returns its
// trimmed contents. Returns "" with a nil error when the file is
// absent so callers can branch on emptiness without distinguishing
// "no token installed" from "IO failure."
func ReadSetupTokenFromHome(home string) (string, error) {
	p := filepath.Join(home, "setup_token")
	body, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p, err)
	}
	return strings.TrimSpace(string(body)), nil
}

// InjectSetupTokenEnv reads $home/setup_token; if non-empty, returns
// base with CLAUDE_CODE_OAUTH_TOKEN=<token> appended. The supervisor
// calls this just before c.Env = ... at each claude spawn so a
// freshly-installed token takes effect on the next wake without a
// container restart.
//
// Read errors are returned to the caller (rather than silently
// swallowed) so the supervisor can log them — but the supervisor
// should continue to spawn claude on a read error: the
// .credentials.json fallback path may still succeed.
func InjectSetupTokenEnv(base []string, home string) ([]string, error) {
	tok, err := ReadSetupTokenFromHome(home)
	if err != nil {
		return base, err
	}
	if tok == "" {
		return base, nil
	}
	return append(base, EnvOAuthTokenVar+"="+tok), nil
}
