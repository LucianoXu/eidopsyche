package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeystoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key")

	k, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := SaveKey(keyPath, k); err != nil {
		t.Fatalf("SaveKey: %v", err)
	}

	loaded, err := LoadKey(keyPath)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if loaded.PrivateHex != k.PrivateHex {
		t.Fatalf("private mismatch: got %q want %q", loaded.PrivateHex, k.PrivateHex)
	}
	if loaded.PublicHex != k.PublicHex {
		t.Fatalf("public mismatch: got %q want %q", loaded.PublicHex, k.PublicHex)
	}
	if loaded.Npub != k.Npub {
		t.Fatalf("npub mismatch: got %q want %q", loaded.Npub, k.Npub)
	}
}

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
