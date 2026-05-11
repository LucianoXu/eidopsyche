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

func TestLogin_TokenFileFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")
	if err := os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	w := &fakeForgeVolumeWriter{}
	installVolume = func(name, image string) (claudeauth.VolumeWriter, error) { return w, nil }
	defer func() { installVolume = nil }()

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
	w := &fakeForgeVolumeWriter{}
	installVolume = func(name, image string) (claudeauth.VolumeWriter, error) { return w, nil }
	defer func() { installVolume = nil }()

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
