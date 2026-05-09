package forge

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestExtractTarRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	body := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "greet.txt",
		Mode:     0o600,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := extractTar(&buf, dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "greet.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

// TestChownTreeIdempotent walks a tmpdir and asserts every entry
// post-chownTree has the requested uid/gid, both first call and second.
// Tests don't run as root, so we chown to the current uid/gid.
func TestChownTreeIdempotent(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.txt"), "a")
	mustMkdir(t, filepath.Join(root, "sub"))
	mustWriteFile(t, filepath.Join(root, "sub", "b.txt"), "b")

	uid := os.Getuid()
	gid := os.Getgid()
	if err := chownTree(root, uid, gid); err != nil {
		t.Fatal(err)
	}
	// Idempotent: second call must succeed.
	if err := chownTree(root, uid, gid); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		filepath.Join(root, "a.txt"),
		filepath.Join(root, "sub"),
		filepath.Join(root, "sub", "b.txt"),
	} {
		st, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			t.Skipf("non-syscall stat on %s; cannot verify ownership", p)
		}
		if int(sys.Uid) != uid || int(sys.Gid) != gid {
			t.Errorf("%s: uid/gid = %d:%d, want %d:%d", p, sys.Uid, sys.Gid, uid, gid)
		}
	}
}

// TestApplyModelEnv writes EIDOS_FORGE_MODEL through to config.toml
// when set, errors on bad input, and is a no-op when unset.
func TestApplyModelEnv(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	mustWriteFile(t, cfgPath, "log_level = \"info\"\n")

	if err := applyModelEnv(cfgPath, "claude-sonnet-4-7"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(body), `model = "claude-sonnet-4-7"`) {
		t.Errorf("config.toml missing model line; got:\n%s", body)
	}

	// Empty model: file unchanged.
	mustWriteFile(t, cfgPath, "log_level = \"info\"\n")
	before, _ := os.ReadFile(cfgPath)
	if err := applyModelEnv(cfgPath, ""); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(cfgPath)
	if !bytes.Equal(before, after) {
		t.Errorf("empty model should leave config unchanged; before=%q after=%q", before, after)
	}

	// Invalid model id: error.
	if err := applyModelEnv(cfgPath, "garbage"); err == nil {
		t.Error("invalid model id should error")
	}
}

func mustWriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
