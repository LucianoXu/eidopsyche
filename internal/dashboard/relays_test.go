package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newPhase4Deps returns a fakeDeps preloaded with one home and one
// fallback relay plus matching health snapshots so list-rendering and
// the merge between own_relays and relay_health can both be asserted.
func newPhase4Deps() fakeDeps {
	return fakeDeps{
		pubkey: validHex32,
		label:  "alice",
		ownRelays: []OwnRelay{
			{URL: "wss://home.example/", Role: "home", AddedAt: 1715000000},
			{URL: "wss://fallback.example/", Role: "fallback", AddedAt: 1715100000},
		},
		relayHealthRows: []RelayState{
			{URL: "wss://home.example/", Role: "home", State: "connected", LastEventAt: 1715200000},
			{URL: "wss://fallback.example/", Role: "fallback", State: "connecting"},
		},
	}
}

func TestSettingsRelays_FragmentForHTMX(t *testing.T) {
	srv := newTestServer(t, newPhase4Deps())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/relays", nil)
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
		`class="relays-pane"`,
		`hx-trigger="sse:relay.added,sse:relay.removed,sse:relay.state from:body"`,
		`Standing posts`,
		`wss://home.example/`,
		`wss://fallback.example/`,
		`class="role-pill is-home"`,
		`class="role-pill is-fallback"`,
		`class="state-pill is-connected"`,
		`class="state-pill is-connecting"`,
		// add-form anchor:
		`hx-post="/settings/relays"`,
		`name="url"`,
		`name="role"`,
		`<option value="fallback" selected>fallback</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("relays pane missing %q", want)
		}
	}
}

// TestSettingsRelays_PublishFailingPillRendered: when a relay's sub
// connection is alive but every publish has been rejected, the row's
// state-pill must carry the `is-publish-failing` modifier class AND
// the failure reason must appear in the title attribute so an operator
// hovering the cell sees why publishes aren't landing — without this,
// the dashboard reports a bare `connected` while real send attempts
// fail with NO_RELAYS_REACHABLE (the original alice-thinks-the-relay-
// is-offline incident on 2026-05-12).
func TestSettingsRelays_PublishFailingPillRendered(t *testing.T) {
	deps := fakeDeps{
		ownRelays: []OwnRelay{
			{URL: "wss://home.example/", Role: "home", AddedAt: 1715000000},
		},
		relayHealthRows: []RelayState{
			{
				URL: "wss://home.example/", Role: "home", State: "connected",
				LastEventAt:    1715200000,
				LastPublishOk:  false,
				LastPublishErr: "blocked: spam",
				LastPublishAt:  1715200500,
			},
		},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/relays", nil)
	req.Header.Set("HX-Request", "true")
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{
		`is-publish-failing`,
		`blocked: spam`,
		`publish ✗`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("publish-failing relay row missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestSettingsRelays_PublishOkOmitsFailingPill: positive case — when the
// most recent publish succeeded, the failing modifier must NOT appear,
// otherwise every healthy relay would render with a false alarm.
func TestSettingsRelays_PublishOkOmitsFailingPill(t *testing.T) {
	deps := fakeDeps{
		ownRelays: []OwnRelay{
			{URL: "wss://home.example/", Role: "home"},
		},
		relayHealthRows: []RelayState{
			{
				URL: "wss://home.example/", Role: "home", State: "connected",
				LastPublishOk: true,
				LastPublishAt: 1715200500,
			},
		},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/relays", nil)
	req.Header.Set("HX-Request", "true")
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, "is-publish-failing") {
		t.Errorf("healthy publish should not render publish-failing modifier\n%s", body)
	}
}

func TestSettingsRelays_FullPageOnDirectNav(t *testing.T) {
	srv := newTestServer(t, newPhase4Deps())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/relays", nil)
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "<html") {
		t.Error("direct-nav GET should produce the full page")
	}
	if !strings.Contains(body, `id="tab-relays" class="settings-tab is-active"`) {
		t.Error("Relays tab should be marked active on direct nav")
	}
}

// TestSettingsRelays_LastHomeButtonDisabled exercises the
// IsLastHome calculation: when there's exactly one home relay,
// the Remove button on it must render as disabled (defense in
// depth — the daemon also refuses, but the dashboard shouldn't
// even try to fire the request).
func TestSettingsRelays_LastHomeButtonDisabled(t *testing.T) {
	deps := fakeDeps{
		ownRelays: []OwnRelay{
			{URL: "wss://only-home.example/", Role: "home"},
		},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/relays", nil)
	req.Header.Set("HX-Request", "true")
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `disabled`) ||
		!strings.Contains(body, `only home relay`) {
		t.Errorf("last-home Remove must render disabled with the explanatory title;\n%s", body)
	}
	// Crucially the disabled button must not carry an hx-get/hx-post.
	if strings.Contains(body, `hx-get="/settings/relays/`) ||
		strings.Contains(body, `hx-post="/settings/relays/`) {
		t.Error("disabled last-home Remove must not carry hx-* attributes")
	}
}

func TestPostRelay_AddValid(t *testing.T) {
	deps := newPhase4Deps()
	calls := []addOwnRelayCall{}
	deps.addOwnRelayCalls = &calls
	srv := newTestServer(t, deps)
	form := url.Values{"url": {"wss://new.example/"}, "role": {"fallback"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/relays", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 || calls[0].URL != "wss://new.example/" || calls[0].Role != "fallback" {
		t.Errorf("AddOwnRelay called with %+v, want url=wss://new.example/ role=fallback", calls)
	}
}

func TestPostRelay_AddDefaultsToFallback(t *testing.T) {
	deps := newPhase4Deps()
	calls := []addOwnRelayCall{}
	deps.addOwnRelayCalls = &calls
	srv := newTestServer(t, deps)
	// Form omits role — handler should default to fallback.
	form := url.Values{"url": {"wss://new.example/"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/relays", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if len(calls) != 1 || calls[0].Role != "fallback" {
		t.Errorf("expected default role=fallback, got %+v", calls)
	}
}

func TestPostRelay_AddSurfacesValidationError(t *testing.T) {
	deps := newPhase4Deps()
	deps.addOwnRelayFn = func(ctx context.Context, rawURL, role string) error {
		// The adapter wraps the daemon-internal sentinel into the
		// dashboard-public ErrRelayInvalidURL; tests that fake the
		// adapter must do the same so the handler routes through the
		// inline-error branch rather than the operational 502.
		return fmt.Errorf("%w: relay: url must be a non-empty ws:// or wss:// URL",
			ErrRelayInvalidURL)
	}
	srv := newTestServer(t, deps)
	form := url.Values{"url": {"http://wrong-scheme.example/"}, "role": {"fallback"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/relays", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `class="form-flash is-error"`) ||
		!strings.Contains(body, `Add failed`) {
		t.Errorf("validation error must surface inline; got: %s", body)
	}
}

// TestPostRelay_AddOperationalErrorReturns502 ensures the handler
// distinguishes typed validation errors (rendered inline) from raw
// operational errors (logged + 502). Without this distinction, a DB
// outage would masquerade as "Add failed: ..." in the operator's UI.
func TestPostRelay_AddOperationalErrorReturns502(t *testing.T) {
	deps := newPhase4Deps()
	deps.addOwnRelayFn = func(ctx context.Context, rawURL, role string) error {
		return errors.New("disk full") // not wrapping any ErrRelay* sentinel
	}
	srv := newTestServer(t, deps)
	form := url.Values{"url": {"wss://valid.example/"}, "role": {"fallback"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/relays", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("operational error should 502, got %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "disk full") {
		t.Errorf("502 body should include the underlying error; got: %s", rec.Body.String())
	}
}

func TestPostRelay_RemoveFallbackNoConfirmRequired(t *testing.T) {
	deps := newPhase4Deps()
	calls := []string{}
	deps.removeOwnRelayLog = &calls
	srv := newTestServer(t, deps)
	slug := relaySlug("wss://fallback.example/")
	// Form has no confirm field — fallback removal does not require one.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/relays/"+slug+"/remove",
		strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 || calls[0] != "wss://fallback.example/" {
		t.Errorf("expected RemoveOwnRelay called once with the fallback URL, got %+v", calls)
	}
}

func TestPostRelay_RemoveHomeRequiresConfirm(t *testing.T) {
	deps := newPhase4Deps()
	// Add a second home relay so removal isn't blocked by IsLastHome.
	deps.ownRelays = append(deps.ownRelays, OwnRelay{
		URL: "wss://home2.example/", Role: "home",
	})
	calls := []string{}
	deps.removeOwnRelayLog = &calls
	srv := newTestServer(t, deps)
	slug := relaySlug("wss://home.example/")
	// Wrong confirm phrase ⇒ 400, no underlying call.
	form := url.Values{"confirm": {"https://wrong.example/"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/relays/"+slug+"/remove",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("home-relay remove with wrong confirm should 400; got %d", rec.Code)
	}
	if len(calls) != 0 {
		t.Errorf("RemoveOwnRelay must not be called on wrong confirm; calls=%+v", calls)
	}
	// Now with the matching URL as confirm.
	form = url.Values{"confirm": {"wss://home.example/"}}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/settings/relays/"+slug+"/remove",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("home-relay remove with matching confirm: status %d body %s", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 || calls[0] != "wss://home.example/" {
		t.Errorf("expected RemoveOwnRelay called once with home URL, got %+v", calls)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<div id="modal" hx-swap-oob="innerHTML"></div>`) {
		t.Error("home-relay remove success must OOB-clear the modal slot")
	}
}

func TestPostRelay_RemoveStaleSlugReturns404(t *testing.T) {
	srv := newTestServer(t, newPhase4Deps())
	bogus := relaySlug("wss://nope.example/")
	form := url.Values{"confirm": {"wss://nope.example/"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/relays/"+bogus+"/remove",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("stale slug should 404, got %d", rec.Code)
	}
}

func TestGetRelay_ConfirmRemoveModalForHome(t *testing.T) {
	deps := newPhase4Deps()
	deps.ownRelays = append(deps.ownRelays, OwnRelay{
		URL: "wss://home2.example/", Role: "home",
	})
	srv := newTestServer(t, deps)
	slug := relaySlug("wss://home.example/")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/relays/"+slug+"/confirm-remove", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`hx-post="/settings/relays/` + slug + `/remove"`,
		`data-expected="wss://home.example/"`,
		`Type <code>wss://home.example/</code>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("confirm-remove modal missing %q", want)
		}
	}
}

func TestRelaySlug_Stable(t *testing.T) {
	a := relaySlug("wss://example.com/")
	b := relaySlug("wss://example.com/")
	if a != b {
		t.Errorf("relaySlug not deterministic: %q vs %q", a, b)
	}
	if relaySlug("wss://a.example/") == relaySlug("wss://b.example/") {
		t.Error("relaySlug collision on different URLs")
	}
	if len(a) != 16 {
		t.Errorf("relaySlug length = %d, want 16", len(a))
	}
}

func TestBuildSettingsRelays_MergesHealth(t *testing.T) {
	view := buildSettingsRelays(context.Background(), newPhase4Deps(), "")
	if got, want := len(view.Rows), 2; got != want {
		t.Fatalf("expected %d rows, got %d", want, got)
	}
	if view.HomeCount != 1 {
		t.Errorf("HomeCount = %d, want 1", view.HomeCount)
	}
	// home row should be marked IsLastHome (only one home in the fixture).
	for _, r := range view.Rows {
		if r.Role == "home" && !r.IsLastHome {
			t.Errorf("home row should be IsLastHome=true with HomeCount=1; got row %+v", r)
		}
		if r.URL == "wss://home.example/" && r.State != "connected" {
			t.Errorf("home row state mismatch: got %q, want connected", r.State)
		}
	}
}
