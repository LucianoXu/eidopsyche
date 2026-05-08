package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/dashboard"
)

// ErrLifecycleBusy is returned by LifecycleRun when a job is already in
// flight. The dashboard maps it to HTTP 409 so the operator sees a
// "another action is running" message rather than a queued request.
var ErrLifecycleBusy = errors.New("lifecycle: another job is already in flight")

// LifecycleSpawner is the seam tests inject. Production passes
// lifecycleSpawn from lifecycle_spawn.go (platform-aware); tests pass
// a stub that runs `/bin/sh -c "echo line1; echo line2; exit 3"` so
// the streaming + done-event path can be asserted without spawning a
// real `eidos` child.
type LifecycleSpawner func(ctx context.Context, args []string) (*exec.Cmd, error)

// lifecycleJob captures a single in-flight subprocess run.
type lifecycleJob struct {
	id      string
	args    []string
	cmd     *exec.Cmd
	started time.Time
	// done is closed by pumpLifecycle's deferred cleanup. shutdownLifecycle
	// waits on this (with a small timeout) so the final lifecycle.done +
	// service.status events flush before SSE subscribers tear down.
	done chan struct{}
}

// LifecycleStatus is a snapshot of the daemon's lifecycle state shown
// in the dashboard's Service tab.
type LifecycleStatus struct {
	JobID   string
	Args    []string
	Started time.Time
	Active  bool
}

// SwapLifecycleSpawner replaces the daemon's lifecycle spawner with
// the supplied function. Exposed for integration tests so they can
// substitute a fake spawner (`/bin/sh -c "echo line; exit 0"`) in
// place of the production fork-eidos-binary path. Preserves the
// existing lifeCtx so an in-flight job is unaffected.
func (d *Daemon) SwapLifecycleSpawner(spawner LifecycleSpawner) {
	d.installLifecycle(spawner)
}

// installLifecycle wires the daemon's lifecycle context, spawner, and
// active-job lock. Called from daemon.Run before any IPC handlers
// register; tests call it directly to inject a fake spawner.
//
// d.lifeCtx is the daemon-owned context that outlives any HTTP request
// — the request context is canceled the moment the dashboard handler
// returns the job id, but the subprocess (and its line-pump goroutine)
// must keep running until cmd.Wait returns. Cancelling lifeCtx happens
// only on daemon shutdown.
func (d *Daemon) installLifecycle(spawner LifecycleSpawner) {
	d.lifeMu.Lock()
	defer d.lifeMu.Unlock()
	if d.lifeCtx == nil {
		d.lifeCtx, d.lifeCancel = context.WithCancel(context.Background())
	}
	d.lifeSpawner = spawner
}

// LifecycleRun spawns a child process and returns immediately with the
// job id. Output is streamed line-by-line as dashboard.Event values
// (kind=lifecycle.line:<jobID>) until the process exits, then a final
// lifecycle.done:<jobID> carries the rendered status pill.
//
// args is the eidos sub-command argv (e.g. ["gate","reconnect"]); the
// daemon prepends `--state-dir <dir>` so the child finds the same IPC
// socket / config / state.db as the parent — without this, the child
// would use the default state-dir resolution and fail to dial the
// running daemon.
//
// At most one job runs at a time. A concurrent click returns
// ErrLifecycleBusy; the dashboard handler maps that to HTTP 409.
func (d *Daemon) LifecycleRun(args []string) (string, error) {
	args = lifecyclePrependStateDir(args, d.StateDir)
	d.lifeMu.Lock()
	if d.activeLife != nil {
		d.lifeMu.Unlock()
		return "", ErrLifecycleBusy
	}
	// Re-init lifeCtx if it's missing OR already cancelled. The latter
	// happens when a test calls shutdownLifecycle (via t.Cleanup) and
	// a follow-up test re-uses the same Daemon struct.
	if d.lifeCtx == nil || d.lifeCtx.Err() != nil {
		if d.lifeCancel != nil {
			d.lifeCancel()
		}
		d.lifeCtx, d.lifeCancel = context.WithCancel(context.Background())
	}
	spawner := d.lifeSpawner
	if spawner == nil {
		spawner = lifecycleSpawn
	}
	jobID, err := randomJobID()
	if err != nil {
		d.lifeMu.Unlock()
		return "", fmt.Errorf("job id: %w", err)
	}
	job := &lifecycleJob{
		id:      jobID,
		args:    append([]string(nil), args...),
		started: time.Now(),
		done:    make(chan struct{}),
	}
	d.activeLife = job
	d.lifeMu.Unlock()

	cmd, err := spawner(d.lifeCtx, args)
	if err != nil {
		d.clearActiveLife(job)
		d.logLifecycleErr("spawn", jobID, args, err)
		return "", fmt.Errorf("spawn: %w", err)
	}
	job.cmd = cmd
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		d.clearActiveLife(job)
		d.logLifecycleErr("stdout pipe", jobID, args, err)
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		d.clearActiveLife(job)
		d.logLifecycleErr("start", jobID, args, err)
		return "", fmt.Errorf("start: %w", err)
	}
	go d.pumpLifecycle(job, stdout)
	return jobID, nil
}

// logLifecycleErr surfaces lifecycle setup failures on the daemon-side
// log even when the dashboard handler is the only place that sees the
// returned error. Without this, a 502 in the operator's browser leaves
// no breadcrumb in the daemon log to debug the underlying spawn /
// pipe / start failure.
func (d *Daemon) logLifecycleErr(stage, jobID string, args []string, err error) {
	if d.Log == nil {
		return
	}
	d.Log.Error("lifecycle setup failed", "stage", stage, "job", jobID, "args", args, "err", err)
}

// LifecycleStatusSnapshot returns the current lifecycle status (active
// job + args, or empty Active=false). Reads under lifeMu so the caller
// always sees a consistent snapshot.
func (d *Daemon) LifecycleStatusSnapshot() LifecycleStatus {
	d.lifeMu.Lock()
	defer d.lifeMu.Unlock()
	if d.activeLife == nil {
		return LifecycleStatus{}
	}
	return LifecycleStatus{
		JobID:   d.activeLife.id,
		Args:    append([]string(nil), d.activeLife.args...),
		Started: d.activeLife.started,
		Active:  true,
	}
}

// pumpLifecycle reads the child's stdout one line at a time and emits
// each as a content-bearing SSE event. When cmd.Wait returns, the rc is
// rendered into a status pill and emitted as the lifecycle.done event,
// followed by a service.status signal so the rest of the page resyncs.
//
// The function runs in its own goroutine; cmd.Wait must run here, not
// in the HTTP handler, so the request returns immediately. The job.done
// channel is closed last so shutdownLifecycle can wait on it.
func (d *Daemon) pumpLifecycle(job *lifecycleJob, stdout io.ReadCloser) {
	defer close(job.done)
	defer d.clearActiveLife(job)
	scanner := bufio.NewScanner(stdout)
	// Allow long lines (some self-update output can include progress
	// blocks or long tracebacks). The default 64 KiB cap is too tight.
	scanner.Buffer(make([]byte, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		d.emitDashEvent(dashboard.Event{
			Kind: "lifecycle.line:" + job.id,
			HTML: lifecycleLineHTML(line),
		})
	}
	if err := scanner.Err(); err != nil {
		d.emitDashEvent(dashboard.Event{
			Kind: "lifecycle.line:" + job.id,
			HTML: lifecycleLineHTML("[stream error: " + err.Error() + "]"),
		})
	}
	waitErr := job.cmd.Wait()
	rc := exitCodeOf(waitErr)
	d.emitDashEvent(dashboard.Event{
		Kind: "lifecycle.done:" + job.id,
		HTML: lifecycleDoneHTML(job.id, rc),
	})
	d.emitDashEvent(dashboard.Event{Kind: "service.status"})
}

// clearActiveLife resets the active-job slot iff the slot still
// references job — so a second call from a background goroutine after
// the slot has already been replaced is a no-op.
func (d *Daemon) clearActiveLife(job *lifecycleJob) {
	d.lifeMu.Lock()
	defer d.lifeMu.Unlock()
	if d.activeLife == job {
		d.activeLife = nil
	}
}

// shutdownLifecycle cancels the daemon-owned lifecycle context, sends
// a best-effort kill to any in-flight child, then waits up to 2s for
// the pump goroutine to flush the final lifecycle.done + service.status
// events. After the wait the lifeCtx slot is nilled so a subsequent
// installLifecycle call (e.g. from a test that re-uses the same daemon)
// gets a fresh context.
//
// Called from daemon.Run's shutdown sequence; tests call it via
// t.Cleanup.
func (d *Daemon) shutdownLifecycle() {
	d.lifeMu.Lock()
	cancel := d.lifeCancel
	job := d.activeLife
	d.lifeCtx, d.lifeCancel = nil, nil
	d.lifeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if job != nil && job.cmd != nil && job.cmd.Process != nil {
		// Best-effort kill — log at debug if the kill itself fails so
		// "daemon hung on shutdown" investigations have a trail. The
		// spawner's session/cgroup isolation makes this a wakeup signal
		// rather than a guaranteed reap; ESRCH/already-reaped is fine.
		if err := job.cmd.Process.Kill(); err != nil && d.Log != nil {
			d.Log.Debug("lifecycle child kill failed", "job", job.id, "err", err)
		}
	}
	if job != nil {
		// Wait for the pump goroutine to drain the final events. 2s is
		// generous for a kill-induced exit; if the wait times out we
		// log and continue rather than hang shutdown.
		select {
		case <-job.done:
		case <-time.After(2 * time.Second):
			if d.Log != nil {
				d.Log.Warn("lifecycle pump did not drain in 2s; continuing shutdown",
					"job", job.id)
			}
		}
	}
}

// lifecycleLineHTML wraps a stdout/stderr line in the structured
// fragment the dashboard appends to the streaming <pre>. The line is
// HTML-escaped via templateText so a hostile child's output (an
// embedded "</pre>" or script tag) cannot escape the streaming block.
func lifecycleLineHTML(line string) string {
	return `<span class="lifecycle-line">` + htmlEscape(line) + "</span>"
}

// lifecycleDoneHTML returns the OOB-swap fragment that drops a status
// pill into the lifecycle log's status slot when the child exits.
func lifecycleDoneHTML(jobID string, rc int) string {
	klass := "is-ok"
	label := "exited cleanly"
	if rc != 0 {
		klass = "is-err"
		label = fmt.Sprintf("exited rc=%d", rc)
	}
	return `<span id="status-` + jobID + `" hx-swap-oob="outerHTML" class="lifecycle-status ` + klass + `">` + htmlEscape(label) + `</span>`
}

// lifecyclePrependStateDir inserts `--state-dir <dir>` at the start of
// the eidos argv when the daemon was started with a non-default state
// dir. The flag lives on `eidos gate`'s persistent flags (NOT on the
// eidos root), so it MUST go between "gate" and the gate-subcommand
// name — and it must NOT be added to non-gate subcommands like
// `self-update`, which would fail with "unknown flag".
//
// Empty stateDir → no-op. args[0] != "gate" → no-op (self-update,
// future top-level commands).
func lifecyclePrependStateDir(args []string, stateDir string) []string {
	if stateDir == "" || len(args) == 0 || args[0] != "gate" {
		return args
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, args[0], "--state-dir", stateDir)
	out = append(out, args[1:]...)
	return out
}

// exitCodeOf extracts the numeric exit code from a cmd.Wait error.
// Returns 0 on nil (success), the ExitError's ExitCode for normal
// non-zero exits, and -1 for signal kills / pre-start failures.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// randomJobID returns 8 lowercase hex characters. Per session a few
// dozen jobs is the upper bound; 32 bits of entropy is plenty.
func randomJobID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// htmlEscape is a local minimal escape for the small set of chars that
// matter in the streaming-log context. We don't use html/template here
// because the produced fragment is concatenated by the SSE encoder, not
// rendered through the renderer; using template would force an extra
// allocation per line for a trivial substitution set.
func htmlEscape(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range []byte(s) {
		switch r {
		case '<':
			out = append(out, []byte("&lt;")...)
		case '>':
			out = append(out, []byte("&gt;")...)
		case '&':
			out = append(out, []byte("&amp;")...)
		case '"':
			out = append(out, []byte("&#34;")...)
		case '\'':
			out = append(out, []byte("&#39;")...)
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
