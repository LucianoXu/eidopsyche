//go:build windows

package identity

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// TestKeystoreRejectsWideACL verifies that LoadKey refuses a key file whose
// DACL grants any access to a "wide" SID (Everyone in this case). We re-use
// SaveKey to seed the file then deliberately broaden its DACL through the
// same ACL APIs `protectKeyFile` uses.
func TestKeystoreRejectsWideACL(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key")

	k, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := SaveKey(keyPath, k); err != nil {
		t.Fatalf("SaveKey: %v", err)
	}
	// Sanity check: SaveKey + protectKeyFile must produce a file LoadKey
	// accepts. If this fails, the rest of the test cannot conclude that
	// the rejection below was caused by the deliberate widening.
	if _, err := LoadKey(keyPath); err != nil {
		t.Fatalf("LoadKey on freshly-saved key: %v", err)
	}

	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid(World): %v", err)
	}
	wide := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(world),
		},
	}}
	dacl, err := windows.ACLFromEntries(wide, nil)
	if err != nil {
		t.Fatalf("ACLFromEntries: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(
		keyPath,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	); err != nil {
		t.Fatalf("SetNamedSecurityInfo: %v", err)
	}

	if _, err := LoadKey(keyPath); err == nil {
		t.Fatal("expected error after widening the DACL to Everyone, got nil")
	}
}
