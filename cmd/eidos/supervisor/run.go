//go:build !windows

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

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
			if err := startChildren(ctx, children); err != nil {
				return err
			}
			return watchWakes(ctx)
		},
	}
}

// startChildren renders the crontab from config, spawns long-running
// children (crond + gate daemon), and starts the planner goroutine that
// fires due plans.
//
// Returns the first non-nil error so the caller can abort before entering
// the wake loop.
func startChildren(ctx context.Context, sp ChildSpawner) error {
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
		return fmt.Errorf("install crontab: %w", err)
	}

	// busybox crond requires its user crontab files to be root-owned
	// (it silently ignores user-owned spool entries, presumably to
	// prevent privilege escalation by tampering with the spool) AND
	// crond itself must be root so it can setuid into the user named
	// by the spool filename. The supervisor runs as eidos, so we use
	// sudo (NOPASSWD per /etc/sudoers.d/eidos) to spawn crond as root.
	// The heartbeat command then runs as eidos via crond's setuid
	// because the crontab file is named "eidos".
	if err := sp.Spawn(ctx, "sudo", "-n", "crond", "-f", "-c", "/var/spool/cron/crontabs"); err != nil {
		return err
	}
	if err := sp.Spawn(ctx, "eidos", "gate", "daemon", "--state-dir", gateDir); err != nil {
		return err
	}

	// Start the planner goroutine. Errors are logged inside; cancellation
	// of ctx is the only way out.
	go func() {
		if err := plannerLoop(ctx, supervisorPlansDir, PlanScanInterval, wake.Submit); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("planner exited: %v", err)
		}
	}()
	return nil
}

// crontabInstaller is the function startChildren calls to write the
// crontab file. Tests swap this to a no-op; production routes through
// internal/cron's Installer which handles sudo.
var crontabInstaller = func(ctx context.Context, body string) error {
	return cron.DefaultInstaller().Install(ctx, body)
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
//
// On every iteration (and once at startup) it also checks for a one-shot
// birth.json — the First Contact wizard's hand-off — and runs the birth
// handler before any normal wake. See cmd/eidos/supervisor/birth.go.
func watchWakesIn(ctx context.Context, dir string, spawn SpawnAgent) error {
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
