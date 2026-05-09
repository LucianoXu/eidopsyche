package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/wake"
)

func TestStartChildrenLaunchesBoth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracker := &fakeChildren{}
	go startChildren(ctx, tracker)
	time.Sleep(50 * time.Millisecond)
	if got := tracker.Started(); got != 2 {
		t.Errorf("started %d children, want 2 (crond + gate daemon)", got)
	}
}

type fakeChildren struct{ started int }

func (f *fakeChildren) Spawn(_ context.Context, _ string, _ ...string) error {
	f.started++
	return nil
}
func (f *fakeChildren) Started() int { return f.started }

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
}
