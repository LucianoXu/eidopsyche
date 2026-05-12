//go:build !windows

package supervisor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestSpawner_CancelOnExit_FiresCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var canceled atomic.Bool
	s := newProcessSpawner(func() { canceled.Store(true) })

	if err := s.Spawn(ctx, ChildPolicy{OnExit: CancelSupervisor}, "true"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !canceled.Load() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !canceled.Load() {
		t.Errorf("cancel not fired after child exit")
	}
}

func TestSpawner_ClassifyAndRestart_RestartsOnNonAuthExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var canceled atomic.Bool
	s := newProcessSpawner(func() { canceled.Store(true) })

	var calls atomic.Int32
	policy := ChildPolicy{
		OnExit: ClassifyAndRestart,
		Classify: func(err error, exitCode int) RestartDecision {
			calls.Add(1)
			if calls.Load() >= 3 {
				return RestartDecision{Halt: true}
			}
			return RestartDecision{Restart: true, Backoff: 10 * time.Millisecond}
		},
	}
	if err := s.Spawn(ctx, policy, "/bin/false"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if calls.Load() < 3 {
		t.Errorf("expected at least 3 classify calls, got %d", calls.Load())
	}
	if canceled.Load() {
		t.Errorf("cancel should NOT fire for classify-and-restart policy")
	}
}
