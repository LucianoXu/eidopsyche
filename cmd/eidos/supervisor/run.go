//go:build !windows

package supervisor

import (
	"context"
	"fmt"
	"log"
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
	// busybox crond reads each file in the spool dir as a user's
	// crontab keyed by filename. We use /var/spool/cron/crontabs
	// (the per-user spool); the image installs the heartbeat
	// crontab as eidos's spool entry.
	if err := sp.Spawn(ctx, "crond", "-f", "-c", "/var/spool/cron/crontabs"); err != nil {
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
	if err := recoverStaleActive(wakeDir); err != nil {
		log.Printf("recover stale active wake: %v", err)
	}
	return watchWakesIn(ctx, wakeDir, runAgentForWake)
}

// recoverStaleActive handles a leftover active.json from a previous
// supervisor invocation — typically caused by a hard container shutdown
// during a wake (docker stop --time=0, host reboot, OOM kill). Without
// this, a stale active.json blocks every future wake forever because
// drainPending sees "another wake is in progress" and skips.
//
// Recovery rule: if active.json exists at startup, treat it as an
// aborted wake. Merge a "previous wake was interrupted" hint into
// pending.json so the next agent spawn knows what happened, then
// remove the active marker. The agent will pick up where things left
// off via the usual inbox-since-last-wake-ts read.
func recoverStaleActive(dir string) error {
	stale, err := wake.ReadActive(dir)
	if err != nil {
		return fmt.Errorf("read stale active: %w", err)
	}
	if stale == nil {
		return nil
	}
	log.Printf("supervisor: recovering aborted wake %q (was active across restart)", stale.ID)
	hint := wake.Signal{
		V:           wake.SchemaVersion,
		ID:          fmt.Sprintf("%d-recover-%s", stale.TriggeredAt, stale.ID),
		Reason:      stale.Reason,
		TriggeredAt: stale.TriggeredAt,
		Hint:        "previous wake was interrupted; this is the recovery follow-up",
		Context:     stale.Context,
	}
	if err := wake.Submit(dir, hint); err != nil {
		return fmt.Errorf("submit recovery wake: %w", err)
	}
	return wake.ClearActive(dir)
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
				// Spawn errors (claude exit non-zero, OAuth missing,
				// transient runtime issues) are runtime conditions, not
				// supervisor-fatal. Log and continue watching — letting
				// the supervisor crash here would loop with docker's
				// restart-policy and produce a flapping container.
				// Only inotify / fsnotify errors below are fatal.
				log.Printf("wake spawn: %v", err)
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
