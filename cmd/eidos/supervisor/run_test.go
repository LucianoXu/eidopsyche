//go:build !windows

package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/wake"
)

// fakeChildren is a test double for ChildSpawner. It records how many children
// were spawned and can be configured to return an error on a specific call.
type fakeChildren struct {
	started   int
	failAfter int   // if > 0, return errFail on the failAfter-th Spawn call (1-indexed)
	errFail   error // error to return when failAfter is triggered
}

// newFakeChildren creates a fakeChildren that succeeds for all spawns.
func newFakeChildren() *fakeChildren {
	return &fakeChildren{}
}

func (f *fakeChildren) Spawn(_ context.Context, _ ChildPolicy, _ string, _ ...string) error {
	f.started++
	if f.failAfter > 0 && f.started == f.failAfter {
		return f.errFail
	}
	return nil
}

func (f *fakeChildren) Started() int { return f.started }

// withFakeCrontabInstaller swaps crontabInstaller to a no-op for the
// duration of a test so tests don't actually try to sudo.
func withFakeCrontabInstaller(t *testing.T) {
	t.Helper()
	prev := crontabInstaller
	crontabInstaller = func(_ context.Context, _ string) error { return nil }
	t.Cleanup(func() { crontabInstaller = prev })
}

func TestStartChildrenLaunchesBoth(t *testing.T) {
	withFakeCrontabInstaller(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracker := newFakeChildren()
	if err := startChildren(ctx, tracker); err != nil {
		t.Fatalf("startChildren returned unexpected error: %v", err)
	}
	if got := tracker.Started(); got != 2 {
		t.Errorf("started %d children, want 2 (crond + gate daemon)", got)
	}
}

func TestStartChildrenPropagatesFirstError(t *testing.T) {
	withFakeCrontabInstaller(t)
	ctx := context.Background()
	sentinel := errors.New("spawn failed")

	// Fail on first spawn (crond).
	fc := &fakeChildren{failAfter: 1, errFail: sentinel}
	if err := startChildren(ctx, fc); !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got: %v", err)
	}
	if fc.Started() != 1 {
		t.Errorf("expected exactly 1 spawn attempt, got %d", fc.Started())
	}

	// Fail on second spawn (gate daemon).
	fc2 := &fakeChildren{failAfter: 2, errFail: sentinel}
	if err := startChildren(ctx, fc2); !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error on second spawn, got: %v", err)
	}
	if fc2.Started() != 2 {
		t.Errorf("expected exactly 2 spawn attempts, got %d", fc2.Started())
	}
}

func TestWatchPromotesPendingAndSpawns(t *testing.T) {
	dir := t.TempDir()
	spawned := make(chan wake.Signal, 4)
	spawn := func(_ context.Context, sig wake.Signal) error {
		spawned <- sig
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loopErr := make(chan error, 1)
	go func() { loopErr <- watchWakesIn(ctx, dir, spawn) }()
	// Wait briefly for watcher to install.
	time.Sleep(50 * time.Millisecond)
	// Atomically write pending.json.
	tmp := filepath.Join(dir, "pending.tmp")
	if err := os.WriteFile(tmp, []byte(`{"v":1,"id":"abc","reason":"manual","triggered_at":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "pending.json")); err != nil {
		t.Fatal(err)
	}
	select {
	case sig := <-spawned:
		if sig.ID != "abc" {
			t.Errorf("got id=%q", sig.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent not spawned within 2s")
	}
	// Cancel and wait for the watcher goroutine to exit before the test
	// returns — otherwise t.TempDir's cleanup races with the still-active
	// fsnotify watcher and intermittently sees "directory not empty".
	cancel()
	select {
	case <-loopErr:
	case <-time.After(time.Second):
		t.Logf("watcher goroutine did not exit within 1s of cancel")
	}
}
