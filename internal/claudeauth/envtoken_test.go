package claudeauth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeWriter records every operation so we can assert ordering and
// arguments. Mirrors what production forgectlWriter does, in memory.
type fakeWriter struct {
	writes  map[string][]byte
	removed []string
	cleared bool
}

func newFakeWriter() *fakeWriter {
	return &fakeWriter{writes: map[string][]byte{}}
}

func (f *fakeWriter) Write(p string, b []byte) error {
	f.writes[p] = append([]byte{}, b...)
	return nil
}
func (f *fakeWriter) Remove(p string) error    { f.removed = append(f.removed, p); return nil }
func (f *fakeWriter) ClearAuthRequired() error { f.cleared = true; return nil }

func TestWriteSetupTokenToVolume_HappyPath(t *testing.T) {
	w := newFakeWriter()
	const token = "sk-ant-oat01-XYZ"
	if err := WriteSetupTokenToVolumeUsing(w, token); err != nil {
		t.Fatalf("WriteSetupTokenToVolumeUsing: %v", err)
	}
	body, ok := w.writes[SetupTokenFile]
	if !ok {
		t.Fatalf("setup-token not written; writes=%v", w.writes)
	}
	if string(body) != token {
		t.Errorf("token bytes = %q, want %q", body, token)
	}
	// Stale credentials.json must be removed so claude's LN5 guard does
	// not pick the file path over our env var on the next spawn.
	foundCreds := false
	for _, r := range w.removed {
		if r == CredentialsFile {
			foundCreds = true
		}
	}
	if !foundCreds {
		t.Errorf("CredentialsFile not removed; removed=%v", w.removed)
	}
	if !w.cleared {
		t.Errorf("auth_required not cleared")
	}
}

func TestWriteSetupTokenToVolume_TrimsAndRejectsEmpty(t *testing.T) {
	w := newFakeWriter()
	if err := WriteSetupTokenToVolumeUsing(w, "  sk-ant-oat01-ABC\n"); err != nil {
		t.Fatalf("trim case: %v", err)
	}
	if string(w.writes[SetupTokenFile]) != "sk-ant-oat01-ABC" {
		t.Errorf("token not trimmed: %q", w.writes[SetupTokenFile])
	}
	for _, in := range []string{"", "   ", "\n\t"} {
		if err := WriteSetupTokenToVolumeUsing(newFakeWriter(), in); err == nil {
			t.Errorf("empty token %q accepted; want error", in)
		}
	}
}

func TestWriteToVolume_RemovesStaleSetupToken(t *testing.T) {
	w := newFakeWriter()
	blob := []byte(`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1}}`)
	if err := WriteToVolumeUsing(w, blob); err != nil {
		t.Fatalf("WriteToVolumeUsing: %v", err)
	}
	// File path wins -> stale setup-token must be removed so the env-var
	// path doesn't quietly stay attached.
	foundTok := false
	for _, r := range w.removed {
		if r == SetupTokenFile {
			foundTok = true
		}
	}
	if !foundTok {
		t.Errorf("SetupTokenFile not removed; removed=%v", w.removed)
	}
}

func TestReadSetupTokenFromHome(t *testing.T) {
	home := t.TempDir()
	// Absent -> empty, no error (callers branch on emptiness).
	got, err := ReadSetupTokenFromHome(home)
	if err != nil {
		t.Fatalf("absent: unexpected error %v", err)
	}
	if got != "" {
		t.Errorf("absent: got %q, want \"\"", got)
	}

	// Present, with trailing whitespace -> trimmed.
	if err := os.WriteFile(filepath.Join(home, "setup_token"),
		[]byte("  sk-ant-oat01-XYZ\n  "), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = ReadSetupTokenFromHome(home)
	if err != nil {
		t.Fatalf("present: %v", err)
	}
	if got != "sk-ant-oat01-XYZ" {
		t.Errorf("present: got %q, want trimmed token", got)
	}
}

func TestInjectSetupTokenEnv(t *testing.T) {
	home := t.TempDir()
	base := []string{"PATH=/usr/bin", "HOME=" + home}

	// No file -> base unchanged.
	got, err := InjectSetupTokenEnv(base, home)
	if err != nil {
		t.Fatalf("absent: %v", err)
	}
	if len(got) != len(base) {
		t.Errorf("absent: env length = %d, want %d", len(got), len(base))
	}

	// File present -> CLAUDE_CODE_OAUTH_TOKEN appended.
	if err := os.WriteFile(filepath.Join(home, "setup_token"),
		[]byte("sk-ant-oat01-XYZ\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = InjectSetupTokenEnv(base, home)
	if err != nil {
		t.Fatalf("present: %v", err)
	}
	last := got[len(got)-1]
	want := EnvOAuthTokenVar + "=sk-ant-oat01-XYZ"
	if last != want {
		t.Errorf("present: tail = %q, want %q", last, want)
	}
	// Confirm we appended (didn't replace) so HOME etc. survive.
	if !strings.HasPrefix(strings.Join(got, "\n"), strings.Join(base, "\n")) {
		t.Errorf("base env was mutated: %v", got)
	}
}
