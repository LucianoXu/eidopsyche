package fileops_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/fileops"
)

func TestAtomicWrite_WritesBodyWithPerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := fileops.AtomicWrite(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("body = %q want %q", got, "hello")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v want 0o600", info.Mode().Perm())
	}
}

func TestAtomicWrite_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fileops.AtomicWrite(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Fatalf("body = %q want %q", got, "new")
	}
}

func TestAtomicWrite_CleansTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := fileops.AtomicWrite(path, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in dir after AtomicWrite, got %d: %v", len(entries), entries)
	}
}

func TestAtomicWrite_ParentDirMissingReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-dir", "x.txt")
	if err := fileops.AtomicWrite(path, []byte("hi"), 0o600); err == nil {
		t.Fatal("expected error when parent dir missing")
	}
}
