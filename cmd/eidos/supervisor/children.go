//go:build !windows

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// ChildExitPolicy enumerates what to do when a spawned child exits.
type ChildExitPolicy int

const (
	// CancelSupervisor cancels the supervisor's root context on exit,
	// letting docker's restart-policy bring the container back. Used
	// for crond and gate-daemon (pre-existing behavior).
	CancelSupervisor ChildExitPolicy = iota
	// ClassifyAndRestart hands the exit to a Classify callback and
	// either restarts (with optional backoff) or halts without
	// cancelling the supervisor. Used for agent-loop.
	ClassifyAndRestart
)

// RestartDecision is returned by ChildPolicy.Classify.
type RestartDecision struct {
	Restart bool
	Backoff time.Duration
	Halt    bool // stop restarting; do NOT cancel supervisor
}

// ChildPolicy describes how a spawned child is managed.
type ChildPolicy struct {
	OnExit   ChildExitPolicy
	Classify func(err error, exitCode int) RestartDecision

	// PreStart, if non-nil, runs just before each (re)spawn — used by
	// agent-loop's caller to reconnect the stdin pipe to a new process.
	PreStart func(cmd *exec.Cmd) error
}

// ChildSpawner is the supervisor's interface for launching long-running
// children. Production uses processSpawner; tests substitute fakes.
type ChildSpawner interface {
	Spawn(ctx context.Context, policy ChildPolicy, name string, args ...string) error
}

// processSpawner is the production ChildSpawner.
type processSpawner struct {
	cancel context.CancelFunc
}

// newProcessSpawner constructs a processSpawner. cancel is called when
// a CancelSupervisor-policy child exits while ctx is still live.
func newProcessSpawner(cancel context.CancelFunc) ChildSpawner {
	return &processSpawner{cancel: cancel}
}

// Spawn launches name+args and supervises it according to policy.
func (s *processSpawner) Spawn(ctx context.Context, policy ChildPolicy, name string, args ...string) error {
	switch policy.OnExit {
	case CancelSupervisor:
		return s.spawnCancel(ctx, policy, name, args...)
	case ClassifyAndRestart:
		go s.spawnClassify(ctx, policy, name, args...)
		return nil
	default:
		return fmt.Errorf("unknown OnExit policy: %d", policy.OnExit)
	}
}

func (s *processSpawner) spawnCancel(ctx context.Context, policy ChildPolicy, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if policy.PreStart != nil {
		if err := policy.PreStart(cmd); err != nil {
			return err
		}
	}
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

func (s *processSpawner) spawnClassify(ctx context.Context, policy ChildPolicy, name string, args ...string) {
	for {
		if ctx.Err() != nil {
			return
		}
		cmd := exec.CommandContext(ctx, name, args...)
		if policy.PreStart != nil {
			if err := policy.PreStart(cmd); err != nil {
				return
			}
		}
		startErr := cmd.Start()
		if startErr != nil {
			decision := classifyOrDefault(policy.Classify, startErr, -1)
			if decision.Halt || !decision.Restart {
				return
			}
			time.Sleep(decision.Backoff)
			continue
		}
		waitErr := cmd.Wait()
		var exitErr *exec.ExitError
		exitCode := 0
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		decision := classifyOrDefault(policy.Classify, waitErr, exitCode)
		if decision.Halt || !decision.Restart {
			return
		}
		time.Sleep(decision.Backoff)
	}
}

func classifyOrDefault(fn func(error, int) RestartDecision, err error, code int) RestartDecision {
	if fn == nil {
		return RestartDecision{Restart: true, Backoff: time.Second}
	}
	return fn(err, code)
}
