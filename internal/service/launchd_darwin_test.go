//go:build darwin

package service

import (
	"context"
	"errors"
	"testing"
)

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

// TestLaunchdStop_BestEffort: regression for `eidos gate stop` failing on
// macOS when launchctl returned exit 3 with empty output on a running
// unit. Stop must swallow launchctl errors and rely on `gate status` to
// report whether the units actually stopped.
func TestLaunchdStop_BestEffort(t *testing.T) {
	calls := 0
	l := &launchd{
		cfg: Config{Scope: ScopeUser},
		run: func(ctx context.Context, args ...string) ([]byte, error) {
			calls++
			// Simulate the failure mode that motivated this test:
			// launchctl returned an error (no output) on a unit that was
			// in fact loaded and running.
			return []byte(""), errors.New("synthetic launchctl exit 3")
		},
	}
	if err := l.Stop(context.Background()); err != nil {
		t.Fatalf("Stop should swallow launchctl errors, got: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 launchctl invocations (daemon + relay), got %d", calls)
	}
}

func TestLaunchdStop_NoErrorOnSuccess(t *testing.T) {
	l := &launchd{
		cfg: Config{Scope: ScopeUser},
		run: func(ctx context.Context, args ...string) ([]byte, error) {
			return []byte(""), nil
		},
	}
	if err := l.Stop(context.Background()); err != nil {
		t.Fatalf("Stop should succeed when launchctl returns no error, got: %v", err)
	}
}
