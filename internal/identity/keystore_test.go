package identity

import (
	"path/filepath"
	"testing"
)

// TestKeystoreRoundtrip verifies the cross-platform happy path: SaveKey
// writes a file that LoadKey accepts and reproduces. The platform-specific
// LoadKey-rejects-wide-permissions tests live in keystore_unix_test.go and
// keystore_windows_test.go.
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
