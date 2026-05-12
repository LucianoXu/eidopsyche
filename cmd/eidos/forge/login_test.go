package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/claudeauth"
)

// fakeForgeVolumeWriter implements claudeauth.VolumeWriter with in-memory
// storage. Records every write and remove so tests can assert that the
// setup-token and credentials.json paths land at distinct on-disk
// locations and that each install clears the other.
type fakeForgeVolumeWriter struct {
	writes  []struct{ relPath, body string }
	removed []string
	cleared bool
}

func (f *fakeForgeVolumeWriter) Write(relPath string, body []byte) error {
	f.writes = append(f.writes, struct{ relPath, body string }{relPath, string(body)})
	return nil
}
func (f *fakeForgeVolumeWriter) Remove(relPath string) error {
	f.removed = append(f.removed, relPath)
	return nil
}
func (f *fakeForgeVolumeWriter) ClearAuthRequired() error { f.cleared = true; return nil }

// installFakeWriter swaps in a fresh fake VolumeWriter and returns it
// plus a restore func. Centralises the test seam wiring all login tests
// need.
func installFakeWriter(t *testing.T) (*fakeForgeVolumeWriter, func()) {
	t.Helper()
	w := &fakeForgeVolumeWriter{}
	prev := installVolume
	installVolume = func(name, image string) (claudeauth.VolumeWriter, error) { return w, nil }
	return w, func() { installVolume = prev }
}

// findWriteAt returns the (recorded) body written at relPath, or empty
// string when there was no such write.
func findWriteAt(w *fakeForgeVolumeWriter, relPath string) string {
	for _, wr := range w.writes {
		if wr.relPath == relPath {
			return wr.body
		}
	}
	return ""
}

func contains(slice []string, want string) bool {
	for _, s := range slice {
		if s == want {
			return true
		}
	}
	return false
}

func TestLogin_TokenFileFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	w, restore := installFakeWriter(t)
	defer restore()

	cmd := newLoginCmd()
	cmd.SetArgs([]string{"alice", "--token-file", path, "--image", "img"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !w.cleared {
		t.Errorf("auth_required not cleared after login")
	}
	// File path -> credentials.json written, stale setup-token removed.
	if findWriteAt(w, claudeauth.CredentialsFile) == "" {
		t.Errorf("credentials.json not written; writes=%v", w.writes)
	}
	if !contains(w.removed, claudeauth.SetupTokenFile) {
		t.Errorf("stale setup-token not removed; removed=%v", w.removed)
	}
}

func TestLogin_PasteFlag(t *testing.T) {
	w, restore := installFakeWriter(t)
	defer restore()

	cmd := newLoginCmd()
	cmd.SetArgs([]string{"alice", "--paste", "--image", "img"})
	cmd.SetIn(strings.NewReader(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}`))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if findWriteAt(w, claudeauth.CredentialsFile) == "" {
		t.Errorf("credentials.json not written; writes=%v", w.writes)
	}
}

func TestLogin_SetupTokenStdinFlag(t *testing.T) {
	w, restore := installFakeWriter(t)
	defer restore()

	const token = "sk-ant-oat01-XYZ"
	cmd := newLoginCmd()
	cmd.SetArgs([]string{"alice", "--setup-token-stdin", "--image", "img"})
	cmd.SetIn(strings.NewReader(token + "\n"))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !w.cleared {
		t.Errorf("auth_required not cleared after login")
	}
	// Env-var path -> setup-token file written with the raw bytes, no
	// credentials.json written, stale credentials.json removed.
	body := findWriteAt(w, claudeauth.SetupTokenFile)
	if body != token {
		t.Errorf("setup-token bytes = %q, want %q", body, token)
	}
	if findWriteAt(w, claudeauth.CredentialsFile) != "" {
		t.Errorf("env-var path must not write credentials.json; writes=%v", w.writes)
	}
	if !contains(w.removed, claudeauth.CredentialsFile) {
		t.Errorf("stale credentials.json not removed; removed=%v", w.removed)
	}
}

func TestLogin_SetupTokenStdinFlag_EmptyRejected(t *testing.T) {
	_, restore := installFakeWriter(t)
	defer restore()

	cmd := newLoginCmd()
	cmd.SetArgs([]string{"alice", "--setup-token-stdin", "--image", "img"})
	cmd.SetIn(strings.NewReader("   \n\t\n"))
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for empty setup-token, got nil")
	}
}

// Pipes from `printf %s ... | eidos ...` have no trailing newline.
// Reading one line via bufio.ReadString('\n') returns io.EOF in that
// case, but the accumulated bytes still carry the token. The handler
// must not treat io.EOF as failure.
func TestLogin_SetupTokenStdinFlag_NoTrailingNewline(t *testing.T) {
	w, restore := installFakeWriter(t)
	defer restore()

	cmd := newLoginCmd()
	cmd.SetArgs([]string{"alice", "--setup-token-stdin", "--image", "img"})
	cmd.SetIn(strings.NewReader("sk-ant-oat01-ABC"))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if findWriteAt(w, claudeauth.SetupTokenFile) != "sk-ant-oat01-ABC" {
		t.Errorf("token from EOF-terminated stdin missing or wrong; writes=%v", w.writes)
	}
}

func TestLogin_InteractiveSetupTokenPath(t *testing.T) {
	w, restore := installFakeWriter(t)
	defer restore()

	// Choose [1] setup-token, then paste token line.
	stdin := strings.NewReader("1\nsk-ant-oat01-ABC\n")
	var stdout strings.Builder
	cmd := newLoginCmd()
	cmd.SetArgs([]string{"alice", "--image", "img"})
	cmd.SetIn(stdin)
	cmd.SetOut(&stdout)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if findWriteAt(w, claudeauth.SetupTokenFile) != "sk-ant-oat01-ABC" {
		t.Errorf("interactive setup-token path didn't write expected bytes; writes=%v", w.writes)
	}
	if findWriteAt(w, claudeauth.CredentialsFile) != "" {
		t.Errorf("interactive setup-token path must not write credentials.json; writes=%v", w.writes)
	}
}
