package supervisor

import (
	"context"
	"fmt"
	"os/exec"
)

// ChildSpawner is the supervisor's interface for launching long-running
// children. Production uses processSpawner; tests substitute fakes.
type ChildSpawner interface {
	Spawn(ctx context.Context, name string, args ...string) error
}

// processSpawner is the production ChildSpawner. It starts each child in a
// goroutine that calls Wait; when any child exits while the context is still
// live it fires cancel, letting docker's restart-policy bring the container
// back up.
type processSpawner struct {
	cancel context.CancelFunc
}

// newProcessSpawner creates a processSpawner that calls cancel when any
// spawned child exits unexpectedly (while ctx is not yet done).
func newProcessSpawner(cancel context.CancelFunc) ChildSpawner {
	return &processSpawner{cancel: cancel}
}

func (s *processSpawner) Spawn(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn %s: %w", name, err)
	}
	go func() {
		_ = cmd.Wait()
		// If the context is still live, the child died unexpectedly — cancel so
		// docker's restart-policy can bring the whole container back.
		if ctx.Err() == nil {
			s.cancel()
		}
	}()
	return nil
}
