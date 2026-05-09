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

// TestLaunchdRestartDaemonBootstrapsAfterBootout verifies the restart
// path matches startOne's bootout-then-bootstrap sequence: launchctl is
// invoked exactly twice, first to evict any prior instance and then to
// bring the plist back up. RunAtLoad=true in the rendered plist makes
// the bootstrap call also (re)launch the process, so this is the
// minimum cross-platform "restart" semantics the gate cli relies on.
func TestLaunchdRestartDaemonBootstrapsAfterBootout(t *testing.T) {
	tmp := t.TempDir()
	var calls [][]string
	l := &launchd{
		cfg: Config{
			BinaryPath: "/usr/local/bin/eidos",
			Scope:      ScopeUser,
			UnitDir:    tmp,
		},
		run: func(ctx context.Context, args ...string) ([]byte, error) {
			calls = append(calls, append([]string(nil), args...))
			return nil, nil
		},
	}
	if err := l.RestartDaemon(context.Background()); err != nil {
		t.Fatalf("RestartDaemon: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 launchctl invocations, got %d (%v)", len(calls), calls)
	}
	if calls[0][0] != "bootout" {
		t.Errorf("first call: got %v, want bootout first", calls[0])
	}
	if calls[1][0] != "bootstrap" {
		t.Errorf("second call: got %v, want bootstrap second", calls[1])
	}
}
