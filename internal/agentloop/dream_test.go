//go:build !windows

// internal/agentloop/dream_test.go
package agentloop

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

func TestDreamWatcher_FlipsFlagOnBegin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")

	w := NewDreamWatcher(DreamWatcherConfig{Path: path})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)

	if w.Dreaming() {
		t.Fatal("flag should start false")
	}

	if err := dreamstate.Begin(path, time.Now(), "test"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return w.Dreaming() })
}

func TestDreamWatcher_EnqueuesRotationOnEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")

	rotations := make(chan struct{}, 4)
	w := NewDreamWatcher(DreamWatcherConfig{
		Path:                path,
		OnRotationRequested: func() { rotations <- struct{}{} },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)

	if err := dreamstate.Begin(path, time.Now(), "test"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return w.Dreaming() })

	if err := dreamstate.End(path, time.Now(), "done", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rotations:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("rotation not requested after dream end")
	}
}

func TestDreamWatcher_BacklogDrainPreservesOrder(t *testing.T) {
	w := NewDreamWatcher(DreamWatcherConfig{Path: "/tmp/unused-eidos-dream-test"})
	w.AppendToBacklog(wake.Signal{ID: "a"})
	w.AppendToBacklog(wake.Signal{ID: "b"})
	w.AppendToBacklog(wake.Signal{ID: "c"})
	drained := w.DrainBacklog()
	if len(drained) != 3 || drained[0].ID != "a" || drained[2].ID != "c" {
		t.Errorf("drain order: got %v", drained)
	}
}

// waitFor polls pred until it returns true or the timeout elapses.
// Shared helper for agentloop tests.
func waitFor(t *testing.T, timeout time.Duration, pred func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitFor timed out after %s", timeout)
}
