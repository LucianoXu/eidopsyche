package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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
			ctx := cmd.Context()
			children := processSpawner{}
			startChildren(ctx, children)
			return watchWakes(ctx)
		},
	}
}

// startChildren spawns long-running children (crond + gate daemon).
func startChildren(ctx context.Context, sp ChildSpawner) {
	_ = sp.Spawn(ctx, "crond", "-f", "-c", "/etc/crontabs")
	_ = sp.Spawn(ctx, "eidos", "gate", "daemon", "--state-dir", gateDir)
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
	if _, err := promoteAndSpawn(ctx, dir, spawn); err != nil {
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
			if _, err := promoteAndSpawn(ctx, dir, spawn); err != nil {
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

// promoteAndSpawn picks up pending.json (if any), promotes to active,
// invokes spawn, then clears active. If more pending arrives during spawn,
// it is processed recursively at the end (chained, not concurrent).
//
// If active.json already exists when called, this is a no-op (the previous
// spawn is still in flight; the supervisor will pick up pending after).
func promoteAndSpawn(ctx context.Context, dir string, spawn SpawnAgent) (*wake.Signal, error) {
	if cur, _ := wake.ReadActive(dir); cur != nil {
		return nil, nil
	}
	sig, err := wake.PromoteToActive(dir)
	if err != nil || sig == nil {
		return sig, err
	}
	if err := spawn(ctx, *sig); err != nil {
		_ = wake.ClearActive(dir)
		return sig, err
	}
	_ = wake.ClearActive(dir)
	// After active clears, check if more pending arrived and process.
	if _, err := promoteAndSpawn(ctx, dir, spawn); err != nil {
		return sig, err
	}
	return sig, nil
}

// runAgentForWake is the production spawner: it runs `eidos supervisor
// agent-runner --wake-file <active.json>` as a child process and waits.
func runAgentForWake(ctx context.Context, _ wake.Signal) error {
	cmd := exec.CommandContext(ctx, "eidos", "supervisor", "agent-runner",
		"--wake-file", filepath.Join(wakeDir, "active.json"),
		"--ontology", "/eidos/ontology",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
