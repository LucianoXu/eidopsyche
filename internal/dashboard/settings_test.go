package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// 64 hex chars (32 raw bytes) — a valid Nostr pubkey shape so the
// identity handler's hex→npub encoding succeeds. The exact npub is
// computed by the handler; tests just assert non-empty + npub1 prefix.
const validHex32 = "98e1d96b036f9eda0bf2e95b39e7e7d09f0a5dafd4b76879c2c14e2bd1d51d8c"

func phase1Origin(req *http.Request) {
	req.Header.Set("Origin", "http://"+req.Host)
}

func newPhase1Deps() fakeDeps {
	return fakeDeps{
		pubkey:     validHex32,
		label:      "alice",
		cardURI:    "mindgate://npub1yc8wktqde7…@ws://localhost:22895/?label=alice",
		configSnap: config.Defaults(),
	}
}

func TestSettingsShell_RendersIdentityByDefault(t *testing.T) {
	srv := newTestServer(t, newPhase1Deps())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="settings-tab is-active"`, // active tab
		`Identity`,
		`Config`,
		`alice`, // own label
		`hx-get="/settings/identity"`,
		`hx-get="/settings/config"`,
		validHex32, // hex on identity colophon
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings shell missing %q", want)
		}
	}
}

func TestSettingsShell_NotFoundOnSubpath(t *testing.T) {
	// /settings is exact-match; /settings/something-unknown should 404
	// (our /settings/identity and /settings/config patterns catch the
	// known sub-paths; everything else falls through to the catch-all
	// shellHandler at "/", which 404s on non-root paths).
	srv := newTestServer(t, newPhase1Deps())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/notatab", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on unknown sub-path, got %d", rec.Code)
	}
}

func TestSettingsIdentity_FragmentForHTMX(t *testing.T) {
	srv := newTestServer(t, newPhase1Deps())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/identity", nil)
	req.Header.Set("HX-Request", "true")
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Fragment, not the full shell.
	if strings.Contains(body, "<html") {
		t.Error("HX-Request should produce a fragment, not a full document")
	}
	if !strings.Contains(body, `class="identity-pane"`) {
		t.Errorf("identity fragment missing pane: %s", body)
	}
	if !strings.Contains(body, "Colophon") {
		t.Error("identity fragment missing 'Colophon' heading")
	}
}

func TestSettingsIdentity_FullPageOnDirectNav(t *testing.T) {
	// Without the HX-Request header (e.g. address-bar reload of
	// /settings/identity), the handler must render the whole page so
	// the operator doesn't see a fragment hanging in space.
	srv := newTestServer(t, newPhase1Deps())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/identity", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<html") {
		t.Error("direct-nav response should be a full document")
	}
	if !strings.Contains(body, "settings-tab is-active") {
		t.Error("direct-nav response should include the active tab marker")
	}
}

func TestSettingsLabelPost_Valid(t *testing.T) {
	calls := []string{}
	deps := newPhase1Deps()
	deps.setLabelCalls = &calls
	srv := newTestServer(t, deps)

	rec := httptest.NewRecorder()
	form := strings.NewReader("label=mallory")
	req := httptest.NewRequest("POST", "/settings/identity/label", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	phase1Origin(req)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 || calls[0] != "mallory" {
		t.Fatalf("expected SetOwnLabel(\"mallory\"), got %v", calls)
	}
	if !strings.Contains(rec.Body.String(), "form-flash") {
		t.Error("expected form-flash on success")
	}
	if strings.Contains(rec.Body.String(), "form-flash is-error") {
		t.Error("success response should not carry form-flash is-error chip")
	}
}

func TestSettingsLabelPost_Empty(t *testing.T) {
	srv := newTestServer(t, newPhase1Deps())
	rec := httptest.NewRecorder()
	form := strings.NewReader("label=  ")
	req := httptest.NewRequest("POST", "/settings/identity/label", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	phase1Origin(req)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d, expected 200 with inline error", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "form-flash is-error") {
		t.Errorf("expected form-flash is-error chip, got: %s", body)
	}
	if !strings.Contains(body, "empty") {
		t.Errorf("expected 'empty' message, got: %s", body)
	}
}

func TestSettingsLabelPost_TooLong(t *testing.T) {
	srv := newTestServer(t, newPhase1Deps())
	rec := httptest.NewRecorder()
	tooLong := strings.Repeat("a", labelMaxLen+1)
	form := strings.NewReader("label=" + tooLong)
	req := httptest.NewRequest("POST", "/settings/identity/label", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	phase1Origin(req)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "too long") {
		t.Errorf("expected 'too long' error, got: %s", rec.Body.String())
	}
}

// TestSettingsLabelPost_RejectedDoesNotPoisonColophon asserts that a
// failed-validation label renders only inside the form input and does
// NOT replace the persisted label shown in the colophon. Regression
// for codex review: previously the same Label field rendered both
// surfaces, so a rejected entry briefly posed as the current identity.
func TestSettingsLabelPost_RejectedDoesNotPoisonColophon(t *testing.T) {
	srv := newTestServer(t, newPhase1Deps()) // current label = "alice"
	rec := httptest.NewRecorder()
	rejected := strings.Repeat("z", labelMaxLen+5)
	form := strings.NewReader("label=" + rejected)
	req := httptest.NewRequest("POST", "/settings/identity/label", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	phase1Origin(req)
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "form-flash is-error") {
		t.Errorf("expected error chip, got: %s", body)
	}
	// The rejected input must appear ONLY inside an input element's
	// value attribute, not as the colophon's serif italic label.
	if !strings.Contains(body, `value="`+rejected+`"`) {
		t.Error("rejected input should preserve in the form input value")
	}
	if !strings.Contains(body, `<dd class="serif">alice</dd>`) {
		t.Errorf("colophon must still show the persisted label \"alice\"; body: %s", body)
	}
	if strings.Contains(body, `<dd class="serif">`+rejected+`</dd>`) {
		t.Error("rejected input must NOT appear in the colophon")
	}
}

func TestSettingsConfig_FragmentForHTMX(t *testing.T) {
	srv := newTestServer(t, newPhase1Deps())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/config", nil)
	req.Header.Set("HX-Request", "true")
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="config-pane"`,
		`dashboard.enabled`, // a known key from the registry
		`log_level`,
		`id="cfg-dashboard-enabled"`, // slugified id (path with dots → dashes)
		`Restart required`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("config fragment missing %q", want)
		}
	}
}

func TestSettingsConfig_PostValidatesAndCalls(t *testing.T) {
	calls := []configSetCall{}
	deps := newPhase1Deps()
	deps.configSetCalls = &calls
	srv := newTestServer(t, deps)

	rec := httptest.NewRecorder()
	form := strings.NewReader("path=log_level&value=debug")
	req := httptest.NewRequest("POST", "/settings/config", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	phase1Origin(req)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 || calls[0] != (configSetCall{Path: "log_level", Value: "debug"}) {
		t.Fatalf("expected ConfigSet call, got %v", calls)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="cfg-log_level"`) {
		t.Errorf("response should be the log_level row partial: %s", body)
	}
	if strings.Contains(body, `class="row-error"`) {
		t.Errorf("success response should not include row-error: %s", body)
	}
}

func TestSettingsConfig_PostUnknownKey(t *testing.T) {
	srv := newTestServer(t, newPhase1Deps())
	rec := httptest.NewRecorder()
	form := strings.NewReader("path=fake.key&value=1")
	req := httptest.NewRequest("POST", "/settings/config", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	phase1Origin(req)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown key, got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestSettingsConfig_DepsErrorIsRendered(t *testing.T) {
	deps := newPhase1Deps()
	deps.configSetErr = errStr("log_level must be one of debug|info|warn|error")
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	form := strings.NewReader("path=log_level&value=bogus")
	req := httptest.NewRequest("POST", "/settings/config", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	phase1Origin(req)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="row-error"`) {
		t.Errorf("expected row-error chip on validation failure: %s", body)
	}
	if !strings.Contains(body, "debug") {
		t.Errorf("error message should reach the row: %s", body)
	}
}

// errStr is a stable error type for table tests; keeping it local avoids
// importing "errors" in just this file.
type errStr string

func (e errStr) Error() string { return string(e) }
