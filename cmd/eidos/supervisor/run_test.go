package supervisor

import (
	"context"
	"testing"
	"time"
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
