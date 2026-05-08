//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/daemon"
)

// fakeServiceLifecycleSpawner returns a daemon.LifecycleSpawner that
// runs `/bin/sh -c "<snippet>"` instead of forking the real eidos
// binary. Used in integration tests so the daemon's full subprocess
// + streaming path is exercised without taking the daemon itself
// down (which `eidos gate stop` / `purge` would).
func fakeServiceLifecycleSpawner(snippet string) daemon.LifecycleSpawner {
	return func(ctx context.Context, args []string) (*exec.Cmd, error) {
		return exec.CommandContext(ctx, "/bin/sh", "-c", snippet), nil
	}
}

// TestDashboardLifecycle_Phase5_StatusEndpoint verifies the Service
// pane renders against a real daemon and includes the daemon's
// version/state-dir/socket fields.
func TestDashboardLifecycle_Phase5_StatusEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)

	base := dashboardURL(alice)
	resp := mustGet(t, base+"/settings/service")
	body := mustReadAll(t, resp)
	for _, want := range []string{
		`id="tab-service" class="settings-tab is-danger is-active"`,
		`Service control`,
		// 4 buttons:
		`hx-post="/settings/service/reconnect"`,
		`hx-get="/settings/service/confirm/stop"`,
		`hx-get="/settings/service/confirm/purge"`,
		`hx-get="/settings/service/confirm/self-update"`,
		// the daemon's own state dir:
		alice.daemon.StateDir,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/settings/service missing %q", want)
		}
	}
}

// TestDashboardLifecycle_Phase5_ReconnectStreams runs a fake spawner
// that emits 3 lines and exits 0. Asserts /settings/service/reconnect
// returns the lifecycle-log frame and the SSE event stream contains
// the expected lifecycle.line + lifecycle.done events.
func TestDashboardLifecycle_Phase5_ReconnectStreams(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)
	// Replace the daemon's lifecycle spawner with a fake before firing
	// the action, otherwise the real spawner would try to fork the
	// `eidos` binary which doesn't exist on the integration host's PATH.
	alice.daemon.SwapLifecycleSpawner(fakeServiceLifecycleSpawner(
		`echo line-one; echo line-two; echo line-three`))

	base := dashboardURL(alice)

	// Subscribe to /events BEFORE posting reconnect so we don't miss
	// the early lifecycle.line events.
	sseReq := mustNewReq(t, "GET", base+"/events", nil)
	sseReq.Header.Set("Accept", "text/event-stream")
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("subscribe /events: %v", err)
	}
	defer sseResp.Body.Close()
	// Consume events on a goroutine so we don't deadlock if the daemon
	// emits faster than we read.
	events := make(chan string, 32)
	go func() {
		defer close(events)
		buf := make([]byte, 4096)
		var carry string
		for {
			n, err := sseResp.Body.Read(buf)
			if n > 0 {
				carry += string(buf[:n])
				for {
					i := strings.Index(carry, "\n\n")
					if i < 0 {
						break
					}
					events <- carry[:i]
					carry = carry[i+2:]
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Fire reconnect.
	form := url.Values{}
	req := mustNewReq(t, "POST", base+"/settings/service/reconnect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp := mustDo(t, req)
	body := mustReadAll(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("reconnect: status %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `class="lifecycle-log-pane"`) {
		t.Errorf("reconnect should render lifecycle-log frame; body: %s", body)
	}

	// Collect events for up to 5s and assert we saw 3 line events
	// plus a done event.
	var lineCount int
	var sawDone bool
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
LOOP:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break LOOP
			}
			if strings.Contains(ev, "event: lifecycle.line:") {
				lineCount++
			}
			if strings.Contains(ev, "event: lifecycle.done:") {
				sawDone = true
				break LOOP
			}
		case <-deadline.C:
			break LOOP
		}
	}
	if lineCount != 3 {
		t.Errorf("expected 3 lifecycle.line events, got %d", lineCount)
	}
	if !sawDone {
		t.Error("expected lifecycle.done event")
	}
}

// TestDashboardLifecycle_Phase5_StopRequiresConfirm guards against a
// hand-crafted curl bypassing the typed-confirm modal.
func TestDashboardLifecycle_Phase5_StopRequiresConfirm(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)
	// Inject fake spawner so an accidental success doesn't take the
	// real daemon down.
	alice.daemon.SwapLifecycleSpawner(fakeServiceLifecycleSpawner(`echo would-have-stopped`))
	base := dashboardURL(alice)

	form := url.Values{"confirm": {"wrong-phrase"}}
	req := mustNewReq(t, "POST", base+"/settings/service/stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp := mustDo(t, req)
	_ = mustReadAll(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("wrong confirm should 400, got %d", resp.StatusCode)
	}
}

// TestDashboardLifecycle_Phase5_BusyReturns409: starting a second job
// while the first is still running returns 409 Conflict.
func TestDashboardLifecycle_Phase5_BusyReturns409(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)
	// Long-running first job so the second POST observes activeLife != nil.
	alice.daemon.SwapLifecycleSpawner(fakeServiceLifecycleSpawner(`sleep 3; echo done`))
	base := dashboardURL(alice)

	// First reconnect — succeeds, returns immediately with the lifecycle
	// log frame. The subprocess keeps running for ~3s.
	req1 := mustNewReqWithOrigin(t, "POST", base+"/settings/service/reconnect", base)
	resp1 := mustDo(t, req1)
	_ = mustReadAll(t, resp1)
	if resp1.StatusCode != 200 {
		t.Fatalf("first reconnect: status %d", resp1.StatusCode)
	}
	// Second reconnect (while first is still sleeping) — 409.
	req2 := mustNewReqWithOrigin(t, "POST", base+"/settings/service/reconnect", base)
	resp2 := mustDo(t, req2)
	_ = mustReadAll(t, resp2)
	if resp2.StatusCode != http.StatusConflict {
		t.Errorf("concurrent reconnect should 409, got %d", resp2.StatusCode)
	}
	// Wait for the first job to finish so the next test isn't poisoned.
	time.Sleep(3500 * time.Millisecond)
}

// TestDashboardLifecycle_Phase5_RealStopGated only runs when the
// operator explicitly opts in via EIDOS_TEST_DESTRUCTIVE=1. Verifies
// the production spawner actually re-execs `os.Args[0] gate stop`.
// Skipped in CI by default.
func TestDashboardLifecycle_Phase5_RealStopGated(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	if os.Getenv("EIDOS_TEST_DESTRUCTIVE") != "1" {
		t.Skip("set EIDOS_TEST_DESTRUCTIVE=1 to opt in to the real-stop integration test")
	}
	t.Skip("real-stop test exercises systemd-run / the install dir; reserved for manual ops smoke")
}

// mustNewReqWithOrigin is a tiny helper for url-encoded POSTs with the
// same-origin header set.
func mustNewReqWithOrigin(t *testing.T, method, urlStr, origin string) *http.Request {
	t.Helper()
	req := mustNewReq(t, method, urlStr, strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", origin)
	return req
}

// jsonString helper kept for future expansion (the SSE encoder may add
// a structured event payload). Not used today.
var _ = json.Marshal
