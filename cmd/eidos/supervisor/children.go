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

type processSpawner struct{}

func (processSpawner) Spawn(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn %s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }() // best-effort reap; supervisor.Run's loop monitors restarts
	return nil
}
