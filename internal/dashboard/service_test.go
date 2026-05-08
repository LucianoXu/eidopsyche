package dashboard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newPhase5Deps() fakeDeps {
	return fakeDeps{
		pubkey: validHex32,
		label:  "alice",
		statusSnapshot: ServiceStatus{
			Version:      "v0.4.0",
			Commit:       "abcdef1",
			BuildDate:    "2026-05-08T12:00:00Z",
			StartedAt:    time.Now().Add(-30 * time.Minute),
			StateDir:     "/var/lib/eidos/gate",
			DashboardURL: "http://127.0.0.1:22893",
			IPCSocket:    "/var/lib/eidos/gate/sock",
			RelayEnabled: true,
			RelayMode:    "paired",
			RelayListen:  "0.0.0.0:22895",
		},
	}
}

func TestSettingsService_FragmentForHTMX(t *testing.T) {
	srv := newTestServer(t, newPhase5Deps())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/service", nil)
	req.Header.Set("HX-Request", "true")
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "<html") {
		t.Error("HX-Request should produce a fragment, not a full document")
	}
	for _, want := range []string{
		`class="service-pane"`,
		`hx-trigger="sse:service.status from:body"`,
		`Service control`,
		// status fields:
		`v0.4.0`,
		`abcdef1`,
		`paired`,
		`/var/lib/eidos/gate/sock`,
		// 4 action cards:
		`hx-post="/settings/service/reconnect"`,
		`hx-get="/settings/service/confirm/stop"`,
		`hx-get="/settings/service/confirm/purge"`,
		`hx-get="/settings/service/confirm/self-update"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("service pane missing %q", want)
		}
	}
}

func TestSettingsService_DisablesActionsWhenJobInFlight(t *testing.T) {
	deps := newPhase5Deps()
	deps.statusSnapshot.ActiveJobID = "abcd1234"
	deps.statusSnapshot.ActiveJobArgs = []string{"gate", "reconnect"}
	deps.statusSnapshot.ActiveJobAt = time.Now().Add(-10 * time.Second)
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/service", nil)
	req.Header.Set("HX-Request", "true")
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		`Job <code>abcd1234</code>`,
		`disabled`,
		`is-busy`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("active-job render missing %q", want)
		}
	}
}

func TestPostService_ReconnectFiresLifecycleRun(t *testing.T) {
	deps := newPhase5Deps()
	calls := [][]string{}
	deps.lifecycleRunLog = &calls
	srv := newTestServer(t, deps)
	form := url.Values{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/service/reconnect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 || strings.Join(calls[0], " ") != "gate reconnect" {
		t.Errorf("LifecycleRun called with %v, want [gate reconnect]", calls)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="lifecycle-log-pane"`,
		`sse-swap="lifecycle.line:`,
		`stub-job-id`, // fakeDeps.LifecycleRun default jobID
	} {
		if !strings.Contains(body, want) {
			t.Errorf("reconnect response missing %q", want)
		}
	}
}

func TestPostService_BusyReturns409(t *testing.T) {
	deps := newPhase5Deps()
	deps.lifecycleRunFn = func(args []string) (string, error) {
		return "", ErrLifecycleBusy
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/service/reconnect", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("busy lifecycle should 409, got %d", rec.Code)
	}
}

func TestPostService_StopRequiresOwnLabelConfirm(t *testing.T) {
	deps := newPhase5Deps()
	calls := [][]string{}
	deps.lifecycleRunLog = &calls
	srv := newTestServer(t, deps)

	// Wrong phrase → 400, no LifecycleRun.
	form := url.Values{"confirm": {"wrong"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/service/stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("wrong confirm should 400, got %d", rec.Code)
	}
	if len(calls) != 0 {
		t.Errorf("LifecycleRun must NOT fire on wrong confirm; got %v", calls)
	}

	// Correct phrase (own label = "alice") → 200 + lifecycle log.
	form = url.Values{"confirm": {"alice"}}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/settings/service/stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("matching confirm: status %d body %s", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 || strings.Join(calls[0], " ") != "gate stop" {
		t.Errorf("LifecycleRun should fire with [gate stop], got %v", calls)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `Daemon will exit`) {
		t.Errorf("daemon-killing banner missing in stop response: %s", body)
	}
}

func TestPostService_PurgeRequiresLabelAndAck(t *testing.T) {
	deps := newPhase5Deps()
	calls := [][]string{}
	deps.lifecycleRunLog = &calls
	srv := newTestServer(t, deps)

	// Phrase ok, ack missing → 400.
	form := url.Values{"confirm": {"alice"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/service/purge", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("purge without ack should 400, got %d", rec.Code)
	}
	if len(calls) != 0 {
		t.Errorf("LifecycleRun must NOT fire on missing ack; got %v", calls)
	}

	// Phrase ok, ack=1 → 200, fires `gate purge --yes`.
	form = url.Values{"confirm": {"alice"}, "ack-backup": {"1"}}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/settings/service/purge", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 || strings.Join(calls[0], " ") != "gate purge --yes" {
		t.Errorf("LifecycleRun should fire with [gate purge --yes], got %v", calls)
	}
}

func TestPostService_SelfUpdateConfirmsVersion(t *testing.T) {
	deps := newPhase5Deps()
	calls := [][]string{}
	deps.lifecycleRunLog = &calls
	srv := newTestServer(t, deps)
	// Wrong version → 400.
	form := url.Values{"confirm": {"v0.99.0"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/service/self-update", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("wrong version confirm should 400, got %d", rec.Code)
	}
	// Correct version → 200, fires `self-update`.
	form = url.Values{"confirm": {"v0.4.0"}}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/settings/service/self-update", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 || strings.Join(calls[0], " ") != "self-update" {
		t.Errorf("LifecycleRun should fire with [self-update], got %v", calls)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Binary update in progress") {
		t.Errorf("self-update banner missing: %s", body)
	}
}

func TestGetService_ConfirmModalRenders(t *testing.T) {
	for _, action := range []string{"stop", "purge", "self-update"} {
		t.Run(action, func(t *testing.T) {
			srv := newTestServer(t, newPhase5Deps())
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/settings/service/confirm/"+action, nil)
			srv.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, `hx-post="/settings/service/`+action+`"`) {
				t.Errorf("confirm modal for %s missing action POST URL", action)
			}
			expected := "alice"
			if action == "self-update" {
				expected = "v0.4.0"
			}
			if !strings.Contains(body, `data-expected="`+expected+`"`) {
				t.Errorf("confirm modal for %s missing data-expected=%q", action, expected)
			}
		})
	}
}

func TestPostService_BusyReturns409OnConfirmedAction(t *testing.T) {
	deps := newPhase5Deps()
	deps.lifecycleRunFn = func(args []string) (string, error) {
		return "", ErrLifecycleBusy
	}
	srv := newTestServer(t, deps)
	form := url.Values{"confirm": {"alice"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/service/stop", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("busy on confirmed stop should 409, got %d", rec.Code)
	}
}

func TestPostService_OperationalErrorReturns502(t *testing.T) {
	deps := newPhase5Deps()
	deps.lifecycleRunFn = func(args []string) (string, error) {
		return "", errors.New("spawn: cgroup setup failed")
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/service/reconnect", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("non-busy operational error should 502, got %d body %s", rec.Code, rec.Body.String())
	}
}
