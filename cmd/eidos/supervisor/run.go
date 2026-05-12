//go:build !windows

package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/cron"
	"github.com/LucianoXu/eidopsyche/internal/wake"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

const (
	wakeDir     = "/eidos/run/wake"
	gateDir     = "/eidos/gate"
	ontologyDir = "/eidos/ontology"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run as PID 1: spawn crond + gate daemon, watch wake dir",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			children := newProcessSpawner(cancel)
			fwd, err := startChildren(ctx, children)
			if err != nil {
				return err
			}
			return watchWakes(ctx, fwd.forward)
		},
	}
}

// startChildren renders the crontab from config, spawns long-running
// children (crond + gate daemon + agent-loop), and starts the planner
// goroutine that fires due plans.
//
// Returns a forwarder whose forward method delivers wake signals to
// agent-loop's stdin, or the first non-nil error so the caller can abort
// before entering the wake loop.
func startChildren(ctx context.Context, sp ChildSpawner) (*forwarder, error) {
	// Render and install the crontab from per-mindform config. A bad
	// config falls back to config.DefaultHeartbeatInterval (currently 2h)
	// with a logged warning rather than leaving the mind-form silent —
	// heartbeat is rhythm, not authority.
	cfg, err := config.Load(filepath.Join(gateDir, "config.toml"))
	if err != nil {
		log.Printf("supervisor: config load: %v (continuing with defaults)", err)
		cfg = config.Defaults()
	}
	if err := config.ValidateMindFormConfig(cfg); err != nil {
		log.Printf("supervisor: config validation: %v (continuing with defaults where possible)", err)
	}
	body, _, rerr := cron.RenderOrDefault(cfg.Heartbeat.Interval)
	if rerr != nil {
		log.Printf("supervisor: crontab render: %v (using default 2h)", rerr)
	}
	// Installer requires root because /var/spool/cron/crontabs is
	// root-owned. The supervisor runs as eidos, so production routes
	// through sudo (internal/cron handles that). Tests substitute
	// crontabInstaller to skip the real sudo invocation.
	if err := crontabInstaller(ctx, body); err != nil {
		return nil, fmt.Errorf("install crontab: %w", err)
	}

	// busybox crond requires its user crontab files to be root-owned
	// (it silently ignores user-owned spool entries, presumably to
	// prevent privilege escalation by tampering with the spool) AND
	// crond itself must be root so it can setuid into the user named
	// by the spool filename. The supervisor runs as eidos, so we use
	// sudo (NOPASSWD per /etc/sudoers.d/eidos) to spawn crond as root.
	// The heartbeat command then runs as eidos via crond's setuid
	// because the crontab file is named "eidos".
	if err := sp.Spawn(ctx, ChildPolicy{OnExit: CancelSupervisor}, "sudo", "-n", "crond", "-f", "-c", "/var/spool/cron/crontabs"); err != nil {
		return nil, err
	}
	if err := sp.Spawn(ctx, ChildPolicy{OnExit: CancelSupervisor}, "eidos", "gate", "daemon", "--state-dir", gateDir); err != nil {
		return nil, err
	}

	// Spawn agent-loop as a long-lived child with ClassifyAndRestart policy.
	// The PreStart hook wires a fresh stdin pipe into fwd on each (re)spawn.
	fwd := &forwarder{}
	if err := sp.Spawn(ctx, ChildPolicy{
		OnExit: ClassifyAndRestart,
		Classify: func(err error, exitCode int) RestartDecision {
			// EXIT_AUTH_REQUIRED (47) is set by agent-loop's claude exit
			// classifier when claude needs /login — supervisor must NOT
			// restart-loop; operator runs `eidos forge login` and then
			// `eidos forge restart <name>`.
			if exitCode == 47 {
				log.Printf("supervisor: agent-loop EXIT_AUTH_REQUIRED; not restarting until login")
				return RestartDecision{Halt: true}
			}
			return RestartDecision{Restart: true, Backoff: time.Second}
		},
		PreStart: func(cmd *exec.Cmd) error {
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			stdin, err := cmd.StdinPipe()
			if err != nil {
				return err
			}
			fwd.setStdin(stdin)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return nil
		},
	}, "eidos", "supervisor", "agent-loop"); err != nil {
		return nil, err
	}

	// Start the planner goroutine. Errors are logged inside; cancellation
	// of ctx is the only way out.
	go func() {
		if err := plannerLoop(ctx, supervisorPlansDir, PlanScanInterval, wake.Submit); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("planner exited: %v", err)
		}
	}()
	return fwd, nil
}

// crontabInstaller is the function startChildren calls to write the
// crontab file. Tests swap this to a no-op; production routes through
// internal/cron's Installer which handles sudo.
var crontabInstaller = func(ctx context.Context, body string) error {
	return cron.DefaultInstaller().Install(ctx, body)
}

// Forward is the fire-and-forget callback used in the always-on model.
// It writes one wake.Signal JSONL line to agent-loop's stdin and
// returns immediately. Errors mean the pipe is broken; supervisor's
// child-restart logic for agent-loop will trigger a respawn, and
// recoverStaleActive folds the stranded active.json back into pending.
type Forward func(ctx context.Context, sig wake.Signal) error

// forwarder owns the live stdin pipe to agent-loop. setStdin is called
// by the PreStart hook on each (re)spawn; forward marshals a Signal
// to JSONL and writes it under a mutex so concurrent producers don't
// interleave bytes mid-record.
type forwarder struct {
	mu    sync.Mutex
	stdin io.WriteCloser
}

func (f *forwarder) setStdin(w io.WriteCloser) {
	f.mu.Lock()
	if f.stdin != nil {
		_ = f.stdin.Close()
	}
	f.stdin = w
	f.mu.Unlock()
}

func (f *forwarder) forward(_ context.Context, sig wake.Signal) error {
	body, err := json.Marshal(sig)
	if err != nil {
		return fmt.Errorf("marshal wake: %w", err)
	}
	body = append(body, '\n')
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stdin == nil {
		return fmt.Errorf("agent-loop stdin not connected")
	}
	if _, err := f.stdin.Write(body); err != nil {
		return fmt.Errorf("write to agent-loop stdin: %w", err)
	}
	return nil
}

// watchWakes is the supervisor's main loop in production.
func watchWakes(ctx context.Context, forward Forward) error {
	if err := os.MkdirAll(wakeDir, 0o700); err != nil {
		return err
	}
	if err := recoverStaleActive(wakeDir); err != nil {
		log.Printf("recover stale active wake: %v", err)
	}
	return watchWakesIn(ctx, wakeDir, forward)
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
// one agent runs at a time (single-instance is enforced by agent-loop via
// its internal sequencing, but the supervisor also serializes here to avoid
// forwarding into the void).
//
// On every iteration (and once at startup) it also checks for a one-shot
// birth.json — the First Contact wizard's hand-off — and runs the birth
// handler before any normal wake. See cmd/eidos/supervisor/birth.go.
func watchWakesIn(ctx context.Context, dir string, forward Forward) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	if err := w.Add(dir); err != nil {
		return err
	}
	// Birth-wake: at most once per MindForm; consumed before any pending wake.
	if err := drainBirthIfPresent(ctx, dir, ontologyDir, birthHandlerForProduction); err != nil {
		log.Printf("birth drain (startup): %v", err)
	}
	// Drain any pre-existing pending.json.
	if _, err := drainPending(ctx, dir, forward); err != nil {
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
			base := filepath.Base(ev.Name)
			if base != "pending.json" && base != "birth.json" {
				continue
			}
			if ev.Op&(fsnotify.Create|fsnotify.Rename|fsnotify.Write) == 0 {
				continue
			}
			if base == "birth.json" {
				if err := drainBirthIfPresent(ctx, dir, ontologyDir, birthHandlerForProduction); err != nil {
					log.Printf("birth drain: %v", err)
				}
				continue
			}
			// Pending wakes are also a retry opportunity for a stranded
			// birth.json (its handler error left it in place; no new fsnotify
			// event would otherwise fire). Cheap when birth.json is absent.
			if err := drainBirthIfPresent(ctx, dir, ontologyDir, birthHandlerForProduction); err != nil {
				log.Printf("birth drain (pending event): %v", err)
			}
			if _, err := drainPending(ctx, dir, forward); err != nil {
				// Forward errors (pipe broken, agent-loop restarting) are
				// runtime conditions, not supervisor-fatal. Log and continue
				// watching — letting the supervisor crash here would loop with
				// docker's restart-policy and produce a flapping container.
				// Only inotify / fsnotify errors below are fatal.
				log.Printf("wake forward: %v", err)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("watcher error: %w", err)
		}
	}
}

// drainPending promotes and forwards all pending wake signals in an iterative
// loop, processing one at a time until no pending.json remains. This replaces
// the previous recursive promoteAndSpawn to avoid unbounded stack growth under
// sustained producer pressure.
//
// In the always-on model, active.json is written to signal that a wake has
// been forwarded to agent-loop. Because Forward is fire-and-forget, the active
// marker is cleared immediately after the write succeeds. If the write fails
// (pipe broken while agent-loop is restarting), active.json is left in place;
// recoverStaleActive on the next supervisor boot folds it back into pending.
//
// If active.json already exists, this is a no-op (a previous forward is still
// unacknowledged; the supervisor will retry after the next pending event).
func drainPending(ctx context.Context, dir string, forward Forward) (*wake.Signal, error) {
	var last *wake.Signal
	for {
		if cur, _ := wake.ReadActive(dir); cur != nil {
			return last, nil
		}
		sig, err := wake.PromoteToActive(dir)
		if err != nil || sig == nil {
			return last, err
		}
		if err := forward(ctx, *sig); err != nil {
			// Leave active.json in place; recoverStaleActive on next
			// iteration / agent-loop restart will fold it back.
			return sig, err
		}
		_ = wake.ClearActive(dir)
		last = sig
		// After clearing active, loop to check for more pending signals.
	}
}
