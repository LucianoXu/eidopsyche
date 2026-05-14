package forgectl

import "testing"

// TestMountTypeConstants is a smoke check on the public enums; the
// translation to the Docker SDK's mount.Type lives behind the SDK
// client and is exercised by the integration tests.
func TestMountTypeConstants(t *testing.T) {
	if MountVolume == MountBind {
		t.Fatal("MountVolume and MountBind must be distinct values")
	}
	if MountVolume != "volume" || MountBind != "bind" {
		t.Fatalf("unexpected enum values: volume=%q bind=%q", MountVolume, MountBind)
	}
}
