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

// TestInitVolumeDirsIncludesRunDir is the regression guard for the
// "agent-runner can't write transcripts" bug: if a refactor drops
// /eidos/run from the volume-init list, the chownTree pass at the end
// of runInitVolume will skip it, and any later mkdir of /eidos/run by
// a process running as eidos (uid 1000) gets foreshadowed by some
// other process landing it root-owned. The fail mode is silent — the
// transcript fall-back swallows the EACCES — so this list-membership
// test is the cheapest way to anchor the contract.
func TestInitVolumeDirsIncludesRunDir(t *testing.T) {
	for _, d := range initVolumeDirs {
		if d == "/eidos/run" {
			return
		}
	}
	t.Errorf("/eidos/run must appear in initVolumeDirs so chownTree transfers it to eidos:eidos at volume bootstrap; got %v", initVolumeDirs)
}

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

// singleEntryTar produces an in-memory tar archive holding one header (and
// optional body). Used by the hardening tests below to feed extractTar a
// hostile entry without dragging in a fixture file.
func singleEntryTar(t *testing.T, h tar.Header, body []byte) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if body != nil {
		h.Size = int64(len(body))
	}
	if err := tw.WriteHeader(&h); err != nil {
		t.Fatal(err)
	}
	if body != nil {
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// noExtraSiblings asserts that target's parent directory has no entries
// other than target itself — i.e. nothing was written outside target.
func noExtraSiblings(t *testing.T, target string) {
	t.Helper()
	parent := filepath.Dir(target)
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Join(parent, e.Name()) != target {
			t.Errorf("unexpected sibling under %s: %s", parent, e.Name())
		}
	}
}

// TestExtractTarRejectsParentTraversal: a tar entry whose Name escapes the
// target dir via `..` must error and write nothing outside target.
// Regression guard for the path-traversal gap codex flagged on 2026-05-10.
func TestExtractTarRejectsParentTraversal(t *testing.T) {
	buf := singleEntryTar(t, tar.Header{
		Name:     "../escape.txt",
		Mode:     0o600,
		Typeflag: tar.TypeReg,
	}, []byte("pwn"))

	parent := t.TempDir()
	target := filepath.Join(parent, "victim")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractTar(buf, target); err == nil {
		t.Fatal("expected extractTar to reject parent traversal, got nil")
	}
	noExtraSiblings(t, target)
}

// TestExtractTarRejectsAbsolutePath: a tar entry with an absolute Name
// (`/etc/passwd`) must be rejected; absolute paths short-circuit
// filepath.Join's "rooted under target" intent.
func TestExtractTarRejectsAbsolutePath(t *testing.T) {
	buf := singleEntryTar(t, tar.Header{
		Name:     "/etc/passwd",
		Mode:     0o600,
		Typeflag: tar.TypeReg,
	}, []byte("root::0:0::/:/bin/sh\n"))

	target := t.TempDir()
	if err := extractTar(buf, target); err == nil {
		t.Fatal("expected extractTar to reject absolute path, got nil")
	}
	if _, err := os.Stat("/etc/passwd.tartest"); err == nil {
		t.Fatal("test leaked outside target")
	}
}

// TestExtractTarRejectsSneakyTraversal: an entry whose Name cleans to
// inside target but whose RAW form contains `..` segments
// (`good/../../escape.txt`) must still be rejected. Defense-in-depth
// against tools that strip `..` only on the outermost prefix.
func TestExtractTarRejectsSneakyTraversal(t *testing.T) {
	buf := singleEntryTar(t, tar.Header{
		Name:     "good/../../escape.txt",
		Mode:     0o600,
		Typeflag: tar.TypeReg,
	}, []byte("pwn"))

	parent := t.TempDir()
	target := filepath.Join(parent, "victim")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := extractTar(buf, target); err == nil {
		t.Fatal("expected extractTar to reject sneaky traversal, got nil")
	}
	noExtraSiblings(t, target)
}

// TestExtractTarRejectsSymlink: symlink and hardlink headers must error
// rather than silently dropping. Silent skip was prior behavior (only
// TypeDir/TypeReg were handled) — making it explicit prevents a malicious
// tar from sneaking a symlink past a future maintainer who adds Symlink
// handling without re-checking the path validation.
func TestExtractTarRejectsSymlink(t *testing.T) {
	buf := singleEntryTar(t, tar.Header{
		Name:     "trap",
		Mode:     0o600,
		Typeflag: tar.TypeSymlink,
		Linkname: "/etc/passwd",
	}, nil)

	target := t.TempDir()
	if err := extractTar(buf, target); err == nil {
		t.Fatal("expected extractTar to reject symlink header, got nil")
	}
}

// TestExtractTarRejectsHardlink: same fail-loud contract for hardlinks.
func TestExtractTarRejectsHardlink(t *testing.T) {
	buf := singleEntryTar(t, tar.Header{
		Name:     "trap",
		Mode:     0o600,
		Typeflag: tar.TypeLink,
		Linkname: "/etc/passwd",
	}, nil)

	target := t.TempDir()
	if err := extractTar(buf, target); err == nil {
		t.Fatal("expected extractTar to reject hardlink header, got nil")
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
