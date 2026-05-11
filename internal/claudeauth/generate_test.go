// internal/claudeauth/generate_test.go
package claudeauth

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubScript writes a shell script at path that:
//   - asserts $HOME points at a temp directory with the eidos-setup- prefix
//     (not the operator's real $HOME)
//   - writes fixture as $HOME/.claude/.credentials.json
//   - exits with exitCode
func stubScript(t *testing.T, path, fixture string, exitCode int) {
	t.Helper()
	body := fmt.Sprintf(`#!/bin/sh
set -eu
if [ -z "${HOME:-}" ]; then echo "stub: HOME unset" >&2; exit 99; fi
case "$(basename "$HOME")" in eidos-setup-*) : ;; *) echo "stub: bad HOME=$HOME" >&2; exit 98 ;; esac
mkdir -p "$HOME/.claude"
cat > "$HOME/.claude/.credentials.json" <<EOF
%s
EOF
exit %d
`, fixture, exitCode)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
}

func TestGenerate_HappyPath(t *testing.T) {
	isolated := t.TempDir()
	t.Setenv("TMPDIR", isolated)

	dir := t.TempDir()
	stub := filepath.Join(dir, "claude")
	fixture := `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}`
	stubScript(t, stub, fixture, 0)

	var stdout, stderr bytes.Buffer
	blob, err := Generate(nil, &stdout, &stderr, stub)
	if err != nil {
		t.Fatalf("Generate: %v\nstderr:\n%s", err, stderr.String())
	}
	if !strings.Contains(string(blob), `"accessToken":"a"`) {
		t.Errorf("blob missing expected content: %q", blob)
	}
}

func TestGenerate_NonZeroExit_ReturnsError(t *testing.T) {
	isolated := t.TempDir()
	t.Setenv("TMPDIR", isolated)

	dir := t.TempDir()
	stub := filepath.Join(dir, "claude")
	stubScript(t, stub, `bogus`, 5)

	var stdout, stderr bytes.Buffer
	if _, err := Generate(nil, &stdout, &stderr, stub); err == nil {
		t.Fatalf("expected error on stub exit 5")
	}
}

func TestGenerate_TempDirCleaned(t *testing.T) {
	isolated := t.TempDir()
	t.Setenv("TMPDIR", isolated)

	dir := t.TempDir()
	stub := filepath.Join(dir, "claude")
	fixture := `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}`
	stubScript(t, stub, fixture, 0)

	var out, errb bytes.Buffer
	_, err := Generate(nil, &out, &errb, stub)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(isolated, "eidos-setup-*"))
	if len(matches) > 0 {
		t.Errorf("temp dir not cleaned: %v", matches)
	}
}

func TestGenerate_MissingCredsAfterRun_ReturnsError(t *testing.T) {
	isolated := t.TempDir()
	t.Setenv("TMPDIR", isolated)

	dir := t.TempDir()
	stub := filepath.Join(dir, "claude")
	// exit 0 but DON'T write the file.
	body := `#!/bin/sh
exit 0
`
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	var out, errb bytes.Buffer
	if _, err := Generate(nil, &out, &errb, stub); err == nil {
		t.Fatalf("expected error when stub leaves no .credentials.json")
	}
}
