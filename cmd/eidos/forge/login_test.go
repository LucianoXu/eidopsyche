package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/claudeauth"
)

// fakeForgeVolumeWriter implements claudeauth.VolumeWriter with in-memory storage.
type fakeForgeVolumeWriter struct {
	writes  []struct{ relPath, body string }
	cleared bool
}

func (f *fakeForgeVolumeWriter) Write(relPath string, body []byte) error {
	f.writes = append(f.writes, struct{ relPath, body string }{relPath, string(body)})
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
	if len(w.writes) != 1 {
		t.Errorf("want 1 write, got %d", len(w.writes))
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
	if len(w.writes) != 1 {
		t.Errorf("want 1 write, got %d", len(w.writes))
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
	if len(w.writes) != 1 {
		t.Fatalf("want 1 write, got %d", len(w.writes))
	}
	body := w.writes[0].body
	if !strings.Contains(body, `"accessToken": "sk-ant-oat01-XYZ"`) {
		t.Errorf("wrapped credentials missing accessToken; got: %s", body)
	}
	// Round-trip guard: if WrapSetupToken ever drifts from Validate's
	// requirements, WriteToVolumeUsing would fail before reaching the
	// writer. This explicit check catches the same regression with a
	// clearer error message.
	if err := claudeauth.Validate([]byte(body)); err != nil {
		t.Errorf("wrapped credentials fail Validate: %v", err)
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
	if len(w.writes) != 1 {
		t.Fatalf("want 1 write, got %d", len(w.writes))
	}
	if !strings.Contains(w.writes[0].body, `"accessToken": "sk-ant-oat01-ABC"`) {
		t.Errorf("setup-token not wrapped into accessToken; got: %s", w.writes[0].body)
	}
}
