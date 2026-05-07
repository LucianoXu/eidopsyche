//go:build darwin

package service

import "testing"

func TestLaunchdInstallUnitsDaemonOnly(t *testing.T) {
	l := &launchd{cfg: Config{WithRelay: false}}
	got := l.installUnits()
	if len(got) != 1 || got[0].label != DaemonUnitName {
		t.Fatalf("installUnits() with WithRelay=false = %+v, want [%s]", got, DaemonUnitName)
	}
}

func TestLaunchdInstallUnitsBoth(t *testing.T) {
	l := &launchd{cfg: Config{WithRelay: true}}
	got := l.installUnits()
	if len(got) != 2 {
		t.Fatalf("installUnits() with WithRelay=true returned %d entries, want 2", len(got))
	}
	if got[0].label != DaemonUnitName || got[1].label != RelayUnitName {
		t.Fatalf("installUnits() order = [%s, %s], want [%s, %s]",
			got[0].label, got[1].label, DaemonUnitName, RelayUnitName)
	}
}

func TestLaunchdUnitsAlwaysReturnsBoth(t *testing.T) {
	// units() (used by Stop / Uninstall / Status) always returns both names
	// so residual relay plists stay reachable for cleanup, regardless of
	// WithRelay.
	for _, withRelay := range []bool{false, true} {
		l := &launchd{cfg: Config{WithRelay: withRelay}}
		got := l.units()
		if len(got) != 2 {
			t.Fatalf("units(WithRelay=%v) returned %d entries, want 2", withRelay, len(got))
		}
	}
}
