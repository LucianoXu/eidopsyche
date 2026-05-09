//go:build darwin

package service

import (
	"context"
	"errors"
	"testing"
)

// TestLaunchdStopDaemon_BestEffort: regression for `eidos gate stop` failing
// on macOS when launchctl returned exit 3 with empty output on a running
// unit. StopDaemon must swallow launchctl errors and rely on `gate status`
// to report whether the units actually stopped.
func TestLaunchdStopDaemon_BestEffort(t *testing.T) {
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
	if err := l.StopDaemon(context.Background()); err != nil {
		t.Fatalf("StopDaemon should swallow launchctl errors, got: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 launchctl invocation (daemon only), got %d", calls)
	}
}

func TestLaunchdStopRelay_BestEffort(t *testing.T) {
	calls := 0
	l := &launchd{
		cfg: Config{Scope: ScopeUser},
		run: func(ctx context.Context, args ...string) ([]byte, error) {
			calls++
			return []byte(""), errors.New("synthetic launchctl exit 3")
		},
	}
	if err := l.StopRelay(context.Background()); err != nil {
		t.Fatalf("StopRelay should swallow launchctl errors, got: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 launchctl invocation (relay only), got %d", calls)
	}
}

func TestLaunchdStopDaemon_NoErrorOnSuccess(t *testing.T) {
	l := &launchd{
		cfg: Config{Scope: ScopeUser},
		run: func(ctx context.Context, args ...string) ([]byte, error) {
			return []byte(""), nil
		},
	}
	if err := l.StopDaemon(context.Background()); err != nil {
		t.Fatalf("StopDaemon should succeed when launchctl returns no error, got: %v", err)
	}
}

func TestLaunchdRelayUnitName(t *testing.T) {
	if RelayUnitName != "eidos-relay" {
		t.Errorf("RelayUnitName = %q, want %q", RelayUnitName, "eidos-relay")
	}
}
