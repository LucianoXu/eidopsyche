package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/LucianoXu/eidopsyche/internal/wake"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

const (
	wakeDir = "/eidos/run/wake"
	gateDir = "/eidos/gate"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run as PID 1: spawn crond + gate daemon, watch wake dir",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			children := newProcessSpawner(cancel)
			if err := startChildren(ctx, children); err != nil {
				return err
			}
			return watchWakes(ctx)
		},
	}
}

// startChildren spawns long-running children (crond + gate daemon).
// Returns the first non-nil error so the caller can abort before entering
// the wake loop.
func startChildren(ctx context.Context, sp ChildSpawner) error {
	if err := sp.Spawn(ctx, "crond", "-f", "-c", "/etc/crontabs"); err != nil {
		return err
	}
	return sp.Spawn(ctx, "eidos", "gate", "daemon", "--state-dir", gateDir)
}

// SpawnAgent is invoked when the supervisor picks up a pending wake. The
// signal has already been promoted to active.json.
type SpawnAgent func(ctx context.Context, sig wake.Signal) error

// watchWakes is the supervisor's main loop in production.
func watchWakes(ctx context.Context) error {
	if err := os.MkdirAll(wakeDir, 0o700); err != nil {
		return err
	}
	return watchWakesIn(ctx, wakeDir, runAgentForWake)
}

// watchWakesIn is the testable seam. It serializes wake processing: only
// one agent runs at a time (single-instance is enforced by agent-runner via
// flock, but the supervisor also serializes here to avoid spawning into
// the void).
func watchWakesIn(ctx context.Context, dir string, spawn SpawnAgent) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	if err := w.Add(dir); err != nil {
		return err
	}
	// Drain any pre-existing pending.json.
	if _, err := drainPending(ctx, dir, spawn); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if filepath.Base(ev.Name) != "pending.json" {
				continue
			}
			if ev.Op&(fsnotify.Create|fsnotify.Rename|fsnotify.Write) == 0 {
				continue
			}
			if _, err := drainPending(ctx, dir, spawn); err != nil {
				return err
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("watcher error: %w", err)
		}
	}
}

// drainPending promotes and spawns all pending wake signals in an iterative
// loop, processing one at a time until no pending.json remains. This replaces
// the previous recursive promoteAndSpawn to avoid unbounded stack growth under
// sustained producer pressure.
//
// If active.json already exists, this is a no-op (the previous spawn is still
// in flight; the supervisor will pick up pending after it completes).
func drainPending(ctx context.Context, dir string, spawn SpawnAgent) (*wake.Signal, error) {
	var last *wake.Signal
	for {
		if cur, _ := wake.ReadActive(dir); cur != nil {
			return last, nil
		}
		sig, err := wake.PromoteToActive(dir)
		if err != nil || sig == nil {
			return last, err
		}
		if err := spawn(ctx, *sig); err != nil {
			_ = wake.ClearActive(dir)
			return sig, err
		}
		_ = wake.ClearActive(dir)
		last = sig
		// After clearing active, loop to check for more pending signals.
	}
}

// runAgentForWake is the production spawner: it runs `eidos supervisor
// agent-runner --wake-file <active.json>` as a child process and waits.
// The child is started in its own process group (Setpgid) so that on context
// cancellation we can SIGTERM the entire group, including any grandchild
// `claude` process started by agent-runner.
func runAgentForWake(ctx context.Context, _ wake.Signal) error {
	cmd := exec.Command("eidos", "supervisor", "agent-runner",
		"--wake-file", filepath.Join(wakeDir, "active.json"),
		"--ontology", "/eidos/ontology",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if cmd.Process != nil {
			// Negative pid targets the process group. Best-effort.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return <-done
	}
}
