package daemon

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/dashboard"
)

// fakeSpawner returns a /bin/sh subprocess running the supplied
// shell snippet, ignoring the child argv entirely. Tests use this
// to assert the streaming + done-event paths without depending on
// the real `eidos` binary or systemd.
func fakeSpawner(snippet string) LifecycleSpawner {
	return func(ctx context.Context, _ []string) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "/bin/sh", "-c", snippet), nil
	}
}

func newLifecycleTestDaemon(t *testing.T, spawner LifecycleSpawner) *Daemon {
	t.Helper()
	// Force the SSE-listener wire delay to zero so unit tests don't pay
	// the 300 ms wallclock cost. The delay only matters in the browser
	// pipeline; the in-memory subscribeDashboard channel used by these
	// tests has no listener-attach race.
	zero := time.Duration(0)
	prev := testLifecycleSSEWireDelay
	testLifecycleSSEWireDelay = &zero
	t.Cleanup(func() { testLifecycleSSEWireDelay = prev })

	d := &Daemon{}
	d.installLifecycle(spawner)
	t.Cleanup(d.shutdownLifecycle)
	return d
}

// drainLifecycleEvents subscribes to the daemon's dashboard fanout and
// returns a func that consumes events until the lifecycle.done event
// for jobID arrives, or the timeout elapses. Returns the line events
// and the done event separately.
func drainLifecycleEvents(t *testing.T, d *Daemon, jobID string, timeout time.Duration) ([]dashboard.Event, *dashboard.Event) {
	t.Helper()
	ch, cancel := d.subscribeDashboard()
	t.Cleanup(cancel)
	var lines []dashboard.Event
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case ev := <-ch:
			switch {
			case ev.Kind == "lifecycle.line:"+jobID:
				lines = append(lines, ev)
			case ev.Kind == "lifecycle.done:"+jobID:
				return lines, &ev
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for lifecycle.done:%s; got %d lines", jobID, len(lines))
		}
	}
}

// TestLifecycleRun_HonorsSSEWireDelay pins the dashboard-race
// workaround: pumpLifecycle MUST hold off emitting events for at
// least lifecycleSSEWireDelay so the browser's htmx-ext-sse extension
// has time to addEventListener for `lifecycle.line:<jobID>` and
// `lifecycle.done:<jobID>` on the freshly-swapped lifecycle pane.
// EventSource.addEventListener has no replay; events that fire before
// the listener attaches are silently dropped — the symptom on PR #29's
// deploy test was the pill stuck on `is-running`.
//
// Override the package-level testLifecycleSSEWireDelay (which the
// helper sets to zero by default) to a measurable value and assert
// the first line event lands no earlier than that.
func TestLifecycleRun_HonorsSSEWireDelay(t *testing.T) {
	d := newLifecycleTestDaemon(t, fakeSpawner(`echo immediate-line`))

	delay := 200 * time.Millisecond
	prev := testLifecycleSSEWireDelay
	testLifecycleSSEWireDelay = &delay
	t.Cleanup(func() { testLifecycleSSEWireDelay = prev })

	ch, cancel := d.subscribeDashboard()
	t.Cleanup(cancel)

	start := time.Now()
	jobID, err := d.LifecycleRun([]string{"gate", "reconnect"})
	if err != nil {
		t.Fatalf("LifecycleRun: %v", err)
	}

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case ev := <-ch:
			if ev.Kind != "lifecycle.line:"+jobID {
				continue
			}
			elapsed := time.Since(start)
			if elapsed < delay {
				t.Fatalf("first lifecycle.line fired after %v, want >= %v (the SSE-wire delay)",
					elapsed, delay)
			}
			return
		case <-deadline.C:
			t.Fatal("timed out waiting for first lifecycle.line event")
		}
	}
}

func TestLifecycleRun_StreamsLines(t *testing.T) {
	d := newLifecycleTestDaemon(t, fakeSpawner(`echo line1; echo line2; echo line3`))
	jobID, err := d.LifecycleRun([]string{"gate", "reconnect"})
	if err != nil {
		t.Fatalf("LifecycleRun: %v", err)
	}
	lines, done := drainLifecycleEvents(t, d, jobID, 5*time.Second)
	if got, want := len(lines), 3; got != want {
		t.Errorf("expected %d line events, got %d", want, got)
	}
	for i, want := range []string{"line1", "line2", "line3"} {
		if i >= len(lines) {
			break
		}
		if !strings.Contains(lines[i].HTML, want) {
			t.Errorf("line %d: %q does not contain %q", i, lines[i].HTML, want)
		}
	}
	if done == nil || !strings.Contains(done.HTML, "is-ok") {
		t.Errorf("expected lifecycle.done with is-ok pill, got %+v", done)
	}
}

func TestLifecycleRun_NonZeroExit(t *testing.T) {
	d := newLifecycleTestDaemon(t, fakeSpawner(`echo before-fail; exit 7`))
	jobID, err := d.LifecycleRun([]string{"gate", "stop"})
	if err != nil {
		t.Fatalf("LifecycleRun: %v", err)
	}
	_, done := drainLifecycleEvents(t, d, jobID, 5*time.Second)
	if done == nil {
		t.Fatal("expected lifecycle.done event")
	}
	if !strings.Contains(done.HTML, "is-err") {
		t.Errorf("non-zero exit should render is-err pill; got %s", done.HTML)
	}
	if !strings.Contains(done.HTML, "rc=7") {
		t.Errorf("done event should mention rc=7; got %s", done.HTML)
	}
}

// TestLifecycleRun_Concurrent409 verifies that a second LifecycleRun
// while the first is still in flight returns ErrLifecycleBusy. Use a
// long-running first job so the second call observes activeLife != nil.
func TestLifecycleRun_Concurrent409(t *testing.T) {
	d := newLifecycleTestDaemon(t, fakeSpawner(`sleep 0.5; echo done`))
	id1, err := d.LifecycleRun([]string{"gate", "reconnect"})
	if err != nil {
		t.Fatalf("first LifecycleRun: %v", err)
	}
	// While job 1 is still running, job 2 must be rejected.
	id2, err := d.LifecycleRun([]string{"gate", "reconnect"})
	if !errors.Is(err, ErrLifecycleBusy) {
		t.Errorf("second LifecycleRun should return ErrLifecycleBusy, got id=%q err=%v", id2, err)
	}
	// Drain the first job to completion so cleanup is graceful.
	_, _ = drainLifecycleEvents(t, d, id1, 5*time.Second)
}

// TestLifecycleRun_SurvivesRequestCtxCancel verifies that the child
// process keeps running and emits its `lifecycle.done` event even
// when the caller's context (the HTTP request) was canceled before
// the child finished.
func TestLifecycleRun_SurvivesRequestCtxCancel(t *testing.T) {
	// The fake spawner ignores the ctx passed in; production
	// lifecycleSpawn uses d.lifeCtx (daemon-owned). To verify the
	// "survives request ctx cancel" guarantee, we wrap the spawner
	// to confirm it does NOT receive the request ctx.
	var seenCtx context.Context
	seenMu := sync.Mutex{}
	spawner := func(ctx context.Context, args []string) (*exec.Cmd, error) {
		seenMu.Lock()
		seenCtx = ctx
		seenMu.Unlock()
		return exec.CommandContext(ctx, "/bin/sh", "-c", `echo ok; sleep 0.2; echo done`), nil
	}
	d := newLifecycleTestDaemon(t, spawner)
	requestCtx, requestCancel := context.WithCancel(context.Background())
	jobID, err := d.LifecycleRun([]string{"gate", "reconnect"})
	if err != nil {
		t.Fatalf("LifecycleRun: %v", err)
	}
	requestCancel() // simulate the HTTP handler returning
	_ = requestCtx
	// The child should still complete and emit done.
	_, done := drainLifecycleEvents(t, d, jobID, 5*time.Second)
	if done == nil {
		t.Fatal("child did not complete after request ctx canceled")
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if seenCtx == nil {
		t.Fatal("spawner did not capture ctx")
	}
	// The captured ctx must NOT be the request ctx — it should be the
	// daemon's lifeCtx, which is still alive.
	select {
	case <-seenCtx.Done():
		t.Errorf("spawner ctx is cancelled; should be daemon-owned lifeCtx (still alive)")
	default:
		// expected: lifeCtx is alive
	}
}

// TestLifecycleRun_SpawnerError surfaces a spawner-side error (e.g.
// systemd-run not on PATH and Setsid unavailable) without leaving an
// active job behind.
func TestLifecycleRun_SpawnerError(t *testing.T) {
	want := errors.New("simulated spawn failure")
	spawner := func(ctx context.Context, args []string) (*exec.Cmd, error) {
		return nil, want
	}
	d := newLifecycleTestDaemon(t, spawner)
	id, err := d.LifecycleRun([]string{"gate", "reconnect"})
	if id != "" {
		t.Errorf("expected empty job id on spawn error, got %q", id)
	}
	if err == nil || !errors.Is(err, want) {
		t.Errorf("expected wrapped spawn error, got %v", err)
	}
	// active-job slot must be cleared so a retry can succeed.
	if d.LifecycleStatusSnapshot().Active {
		t.Error("active job slot not cleared after spawn error")
	}
}

// TestLifecyclePrependStateDir locks in the argv shape the daemon
// builds for the four lifecycle actions. The `--state-dir` flag is
// registered on `eidos gate`'s PersistentFlags, so it MUST sit
// between "gate" and the subcommand. self-update is a sibling of
// "gate" at the eidos root and accepts NO --state-dir flag — adding
// one would yield "error: unknown flag: --state-dir" on the child.
func TestLifecyclePrependStateDir(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		stateDir string
		want     []string
	}{
		{"empty state dir", []string{"gate", "reconnect"}, "", []string{"gate", "reconnect"}},
		{"gate reconnect", []string{"gate", "reconnect"}, "/var/eidos", []string{"gate", "--state-dir", "/var/eidos", "reconnect"}},
		{"gate stop", []string{"gate", "stop"}, "/var/eidos", []string{"gate", "--state-dir", "/var/eidos", "stop"}},
		{"gate purge --yes", []string{"gate", "purge", "--yes"}, "/var/eidos", []string{"gate", "--state-dir", "/var/eidos", "purge", "--yes"}},
		{"self-update untouched", []string{"self-update"}, "/var/eidos", []string{"self-update"}},
		{"empty args untouched", []string{}, "/var/eidos", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := lifecyclePrependStateDir(c.args, c.stateDir)
			if len(got) != len(c.want) {
				t.Fatalf("len got=%v want=%v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("argv[%d] = %q, want %q (full got=%v)", i, got[i], c.want[i], got)
				}
			}
		})
	}
}

func TestExitCodeOf(t *testing.T) {
	if got := exitCodeOf(nil); got != 0 {
		t.Errorf("nil err: got %d, want 0", got)
	}
	// /bin/sh -c 'exit 5'
	cmd := exec.Command("/bin/sh", "-c", "exit 5")
	err := cmd.Run()
	if got := exitCodeOf(err); got != 5 {
		t.Errorf("exit 5: got %d, want 5", got)
	}
}

func TestHTMLEscapeAndLineHTML(t *testing.T) {
	// Streaming-log lines must escape `<`, `>`, `&` so a hostile
	// child can't smuggle script tags into the page via stdout.
	in := `<script>alert("&pwn")</script>`
	out := lifecycleLineHTML(in)
	if strings.Contains(out, "<script>") {
		t.Errorf("escape failed: %s", out)
	}
	if !strings.Contains(out, `&lt;script&gt;`) {
		t.Errorf("expected escaped <script>; got %s", out)
	}
}
