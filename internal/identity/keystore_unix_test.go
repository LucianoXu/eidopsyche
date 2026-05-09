//go:build !windows

package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeystoreRejectsInsecureMode(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key")

	k, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := SaveKey(keyPath, k); err != nil {
		t.Fatalf("SaveKey: %v", err)
	}
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if _, err := LoadKey(keyPath); err == nil {
		t.Fatal("expected error on insecure mode, got nil")
	}
}
