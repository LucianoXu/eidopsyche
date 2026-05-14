//go:build unix

package config

import (
	"os"
	"testing"
)

// TestHostPathOwnerUID exercises the helper against tempdirs the test
// process owns (uid == os.Geteuid() under any test runner). Distinct
// uid scenarios are tested in the IPC handler test with a stat-stub.
func TestHostPathOwnerUID(t *testing.T) {
	dir := t.TempDir()
	uid, err := HostPathOwnerUID(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := uint32(os.Geteuid())
	if uid != want {
		t.Errorf("HostPathOwnerUID(%q)=%d, want %d", dir, uid, want)
	}

	if _, err := HostPathOwnerUID("/no/such/path/" + t.Name()); err == nil {
		t.Error("nonexistent path should error")
	}
}
