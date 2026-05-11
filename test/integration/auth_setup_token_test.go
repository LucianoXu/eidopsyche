//go:build integration

package integration

import (
	"bytes"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestForgeLogin_GenerateInstallsIsolatedCreds drives `eidos forge create`
// + `eidos forge login --generate` against a stubbed `claude` binary
// that writes a known fixture into $HOME/.claude/.credentials.json.
// Asserts:
//   - The volume's /eidos/claude/.claude/.credentials.json contains the
//     stub fixture (not the host's real credentials).
//   - The volume creds are NOT identical-by-content to the host's
//     ~/.claude/.credentials.json — the anti-regression check for the
//     old host-copy path.
func TestForgeLogin_GenerateInstallsIsolatedCreds(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "claude")
	fixture := `{"claudeAiOauth":{"accessToken":"stub-access","refreshToken":"stub-refresh","expiresAt":99999999999}}`
	body := `#!/bin/sh
mkdir -p "$HOME/.claude"
cat > "$HOME/.claude/.credentials.json" <<'CREDSEOF'
` + fixture + `
CREDSEOF
exit 0
`
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+":"+os.Getenv("PATH"))

	// Resolve repo root so `go build` finds ./cmd/eidos.
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))

	// Build a fresh eidos into the test's temp dir so the test is
	// self-contained and does not depend on a pre-built bin/eidos.
	binDir := t.TempDir()
	eidos := filepath.Join(binDir, "eidos")
	buildCmd := exec.Command("go", "build", "-o", eidos, "./cmd/eidos")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build eidos: %v\n%s", err, out)
	}

	// Slug: lower-case, docker-volume-safe name.
	slug := "auth-test-isolated"
	must := func(args ...string) {
		cmd := exec.Command(eidos, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("eidos %v: %v\n%s", args, err, out)
		}
	}

	// Best-effort purge in case a prior run left state behind.
	_ = exec.Command(eidos, "forge", "purge", slug, "--yes").Run()
	t.Cleanup(func() {
		_ = exec.Command(eidos, "forge", "purge", slug, "--yes").Run()
	})

	// A dummy owner npub and a syntactically-valid relay URL are required by
	// `forge create`. The npub is a real bech32-encoded pubkey (test-only);
	// neither it nor the relay URL needs to resolve for this test.
	const testOwnerNpub = "npub1w7syejupdm7alpyt9mz9kke283z946qw7wr84hyvd5j0xl6vq95q9x0rnd"
	must("forge", "create", slug,
		"--owner", testOwnerNpub,
		"--relay", "ws://127.0.0.1:19999",
		"--no-login",
	)
	must("forge", "login", slug, "--generate")

	out, err := exec.Command("docker", "run", "--rm",
		"--mount", "source=eidos-mindform-"+slug+",target=/eidos",
		"--entrypoint", "cat",
		"ghcr.io/lucianoxu/eidopsyche-mindform:dev",
		"/eidos/claude/.claude/.credentials.json",
	).Output()
	if err != nil {
		t.Fatalf("read volume creds: %v", err)
	}
	if !strings.Contains(string(out), "stub-access") {
		t.Errorf("volume creds missing stub fixture; got:\n%s", out)
	}

	// Anti-regression: volume creds MUST diverge from host creds.
	// The old host-copy path silently copied ~/.claude/.credentials.json
	// verbatim; if that path is ever re-introduced, this assertion catches it.
	hostHome, _ := os.UserHomeDir()
	hostCreds, _ := os.ReadFile(filepath.Join(hostHome, ".claude", ".credentials.json"))
	if len(hostCreds) > 0 && bytes.Equal(out, hostCreds) {
		t.Errorf("volume creds identical to host creds (regression: host-copy path re-introduced)")
		t.Logf("hash(volume)=%x hash(host)=%x", sha256.Sum256(out), sha256.Sum256(hostCreds))
	}
}
