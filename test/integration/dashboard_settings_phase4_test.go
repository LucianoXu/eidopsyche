//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// relaySlug mirrors internal/dashboard/handlers_relays.go:relaySlug.
// Duplicated here because internal/dashboard's slug helper isn't
// exported. The byte slice is the first 8 bytes of sha256(url) — once
// hex-encoded that produces a 16-character slug, matching the value
// the dashboard's row partial puts in `id="relay-<slug>"`.
func relaySlugForTest(rawURL string) string {
	h := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(h[:8])
}

// TestDashboardSettings_Phase4_RelayFlow covers the Relays tab end to
// end against a real daemon: the pane renders, an add lands a row in
// own_relays, fallback removal works without typed-confirm, the
// daemon refuses to remove the only home relay, and a stale slug
// returns 404.
func TestDashboardSettings_Phase4_RelayFlow(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)

	base := dashboardURL(alice)
	ctx := context.Background()

	// 1. Direct nav to /settings/relays renders the full page with
	//    Relays as the active tab.
	resp := mustGet(t, base+"/settings/relays")
	body := mustReadAll(t, resp)
	for _, want := range []string{
		`id="tab-relays" class="settings-tab is-active"`,
		`class="relays-pane"`,
		`Standing posts`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/settings/relays full page missing %q", want)
		}
	}

	// 2. Find existing rows (the bringUp helper seeds at least one
	//    home relay on the loopback test relay). Note their URLs —
	//    we'll use them for the slug → row resolution path.
	rows, err := alice.daemon.ListOwnRelays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one own_relay seeded by bringUp")
	}
	t.Logf("seeded own_relays: %+v", rows)

	// 3. POST to add a new fallback relay.
	addURL := "wss://test-fallback.example/"
	form := url.Values{"url": {addURL}, "role": {"fallback"}}
	req := mustNewReq(t, "POST", base+"/settings/relays", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp = mustDo(t, req)
	body = mustReadAll(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("POST add: status %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, addURL) {
		t.Errorf("relay row for %q should be in the rendered pane;\n%s", addURL, body)
	}

	// 4. Daemon's own_relays now contains the new URL with role=fallback.
	rows, err = alice.daemon.ListOwnRelays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.URL == addURL {
			found = true
			if r.Role != "fallback" {
				t.Errorf("added role=%q, want fallback", r.Role)
			}
		}
	}
	if !found {
		t.Errorf("added URL %q not in own_relays after POST; got %+v", addURL, rows)
	}

	// 5. Add the same URL again → handler renders an inline error
	//    flash (200), no panic, no duplicate row.
	resp = mustDo(t, mustReqWithForm(t, "POST", base+"/settings/relays",
		url.Values{"url": {addURL}, "role": {"fallback"}}, base))
	body = mustReadAll(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("duplicate add: status %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Add failed") {
		t.Errorf("duplicate add should surface error inline; body: %s", body)
	}

	// 6. Remove the fallback (no typed-confirm needed).
	slug := relaySlugForTest(addURL)
	req = mustNewReq(t, "POST", base+"/settings/relays/"+slug+"/remove", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp = mustDo(t, req)
	body = mustReadAll(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("POST fallback remove: status %d body %s", resp.StatusCode, body)
	}

	// 7. Daemon's own_relays no longer contains the removed URL.
	rows, err = alice.daemon.ListOwnRelays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.URL == addURL {
			t.Errorf("URL %q should be gone from own_relays after remove; got %+v", addURL, rows)
		}
	}
}

// TestDashboardSettings_Phase4_LastHomeRefused: the daemon side
// (errOwnRelayHomeRequired) refuses to remove the only home relay.
// The dashboard surfaces this as 400 with the explanatory message.
func TestDashboardSettings_Phase4_LastHomeRefused(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)
	base := dashboardURL(alice)
	ctx := context.Background()

	rows, err := alice.daemon.ListOwnRelays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var homeURL string
	homeCount := 0
	for _, r := range rows {
		if r.Role == "home" {
			homeCount++
			homeURL = r.URL
		}
	}
	if homeCount != 1 {
		t.Skipf("test requires exactly one home relay; got %d", homeCount)
	}
	slug := relaySlugForTest(homeURL)

	// POST with the correct typed-confirm phrase. The dashboard's
	// disabled-button defense doesn't apply — this is a hand-crafted
	// curl-equivalent request, so the daemon's refusal is what saves us.
	form := url.Values{"confirm": {homeURL}}
	req := mustNewReq(t, "POST", base+"/settings/relays/"+slug+"/remove",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp := mustDo(t, req)
	body := mustReadAll(t, resp)
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 on last-home remove, got %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "at least one home relay") {
		t.Errorf("body should mention the home-required guard; got: %s", body)
	}

	// Confirm the row is still in own_relays.
	stillThere, err := alice.daemon.ListOwnRelays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range stillThere {
		if r.URL == homeURL && r.Role == "home" {
			found = true
		}
	}
	if !found {
		t.Errorf("home relay %q should still be in own_relays after refused remove", homeURL)
	}
}

// TestDashboardSettings_Phase4_StaleSlug404: a slug that doesn't
// resolve to any current own_relays row returns 404 from both the
// confirm-modal GET and the remove POST.
func TestDashboardSettings_Phase4_StaleSlug404(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)
	base := dashboardURL(alice)

	bogus := relaySlugForTest("wss://nope.example/")
	resp := mustGet(t, base+"/settings/relays/"+bogus+"/confirm-remove")
	_ = mustReadAll(t, resp)
	if resp.StatusCode != 404 {
		t.Errorf("stale slug GET confirm-remove: expected 404, got %d", resp.StatusCode)
	}

	form := url.Values{"confirm": {"wss://nope.example/"}}
	req := mustNewReq(t, "POST", base+"/settings/relays/"+bogus+"/remove",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp = mustDo(t, req)
	_ = mustReadAll(t, resp)
	if resp.StatusCode != 404 {
		t.Errorf("stale slug POST remove: expected 404, got %d", resp.StatusCode)
	}
}

// mustReqWithForm is a tiny helper for url-encoded POSTs in this test
// file; the existing helpers in dashboard_settings_phase1_test.go
// require setting headers separately.
func mustReqWithForm(t *testing.T, method, urlStr string, form url.Values, origin string) *http.Request {
	t.Helper()
	req := mustNewReq(t, method, urlStr, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", origin)
	return req
}
