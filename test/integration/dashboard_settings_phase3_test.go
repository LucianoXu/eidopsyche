//go:build integration

package integration

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestDashboardSettings_Phase3_InviteFlow covers the Invites tab end to
// end against a real daemon: shell renders the new tab, fragment
// renders sections, POST creates an invite that lands in the daemon's
// invitedb, the create-success slip shows the URI, and POST revoke (with
// the typed-confirm phrase) marks the invite revoked in the DB.
func TestDashboardSettings_Phase3_InviteFlow(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)

	base := dashboardURL(alice)

	// 1. Shell with /settings/invites is full page; tab marked active.
	resp := mustGet(t, base+"/settings/invites")
	body := mustReadAll(t, resp)
	for _, want := range []string{
		`id="tab-invites" class="settings-tab is-active"`,
		`class="invites-pane"`,
		`Tokens of welcome`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/settings/invites (full page) missing %q", want)
		}
	}

	// 2. Fragment for HX-Request.
	req := mustNewReq(t, "GET", base+"/settings/invites", nil)
	req.Header.Set("HX-Request", "true")
	resp = mustDo(t, req)
	body = mustReadAll(t, resp)
	if strings.Contains(body, "<html") {
		t.Error("/settings/invites (HX-Request): expected fragment, got full page")
	}
	if !strings.Contains(body, `class="invites-pane"`) {
		t.Error("/settings/invites: missing invites-pane class")
	}

	// 3. POST a create with a 24h expiry, single-use.
	form := url.Values{
		"redeemer_label": {"Bob"},
		"expires":        {"24h"},
		"max_uses":       {"1"},
	}
	req = mustNewReq(t, "POST", base+"/settings/invites", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp = mustDo(t, req)
	body = mustReadAll(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("POST invite: status %d body %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`class="invite-slip"`,
		`Pass <code>`,
		`mindgate-invite://`,
		`class="copy-btn"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("create-success render missing %q\nbody: %s", want, body[:min(2000, len(body))])
		}
	}

	// 4. The invite landed in the daemon's invitedb with status='active'
	// and the redeemer label we typed.
	ctx := context.Background()
	invs, err := alice.daemon.Invites.List(ctx, "active")
	if err != nil {
		t.Fatalf("Invites.List: %v", err)
	}
	if len(invs) != 1 {
		t.Fatalf("expected 1 active invite after create, got %d", len(invs))
	}
	created := invs[0]
	if created.RedeemerLabel != "Bob" {
		t.Errorf("redeemer_label persisted as %q, want Bob", created.RedeemerLabel)
	}
	if created.MaxUses != 1 {
		t.Errorf("max_uses = %d, want 1", created.MaxUses)
	}

	// 5. POST revoke with the typed-confirm phrase (the 8-char ID prefix).
	confirm := created.ID[:8]
	form = url.Values{"confirm": {confirm}}
	req = mustNewReq(t, "POST", base+"/settings/invites/"+created.ID+"/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp = mustDo(t, req)
	body = mustReadAll(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("POST revoke: status %d body %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `<div id="modal" hx-swap-oob="innerHTML"></div>`) {
		t.Error("revoke success: missing OOB modal-clear")
	}

	// 6. Confirm the daemon flipped status to revoked.
	stillActive, err := alice.daemon.Invites.List(ctx, "active")
	if err != nil {
		t.Fatal(err)
	}
	if len(stillActive) != 0 {
		t.Errorf("after revoke: expected 0 active, got %d", len(stillActive))
	}
	revoked, err := alice.daemon.Invites.List(ctx, "revoked")
	if err != nil {
		t.Fatal(err)
	}
	if len(revoked) != 1 || revoked[0].ID != created.ID {
		t.Errorf("after revoke: expected 1 revoked invite with ID=%s, got %+v", created.ID, revoked)
	}
}

// TestDashboardSettings_Phase3_RevokeRequiresMatchingPhrase confirms the
// server-side defense-in-depth check on the typed-confirm phrase. A
// hand-crafted curl with a wrong phrase must NOT mark the invite
// revoked even when the URL path correctly identifies it.
func TestDashboardSettings_Phase3_RevokeRequiresMatchingPhrase(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)
	base := dashboardURL(alice)
	ctx := context.Background()

	// Create one invite via the dashboard.
	form := url.Values{"redeemer_label": {"X"}, "expires": {"7d"}, "max_uses": {"1"}}
	req := mustNewReq(t, "POST", base+"/settings/invites", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp := mustDo(t, req)
	_ = mustReadAll(t, resp)

	invs, err := alice.daemon.Invites.List(ctx, "active")
	if err != nil || len(invs) != 1 {
		t.Fatalf("setup: expected 1 active invite, got %d (err: %v)", len(invs), err)
	}
	id := invs[0].ID

	// Wrong confirm phrase ⇒ 400, status untouched.
	form = url.Values{"confirm": {"deadbeef"}}
	req = mustNewReq(t, "POST", base+"/settings/invites/"+id+"/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp = mustDo(t, req)
	_ = mustReadAll(t, resp)
	if resp.StatusCode != 400 {
		t.Errorf("wrong confirm: expected 400, got %d", resp.StatusCode)
	}

	// Empty confirm ⇒ 400 too.
	form = url.Values{"confirm": {""}}
	req = mustNewReq(t, "POST", base+"/settings/invites/"+id+"/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp = mustDo(t, req)
	_ = mustReadAll(t, resp)
	if resp.StatusCode != 400 {
		t.Errorf("empty confirm: expected 400, got %d", resp.StatusCode)
	}

	// State unchanged.
	still, err := alice.daemon.Invites.List(ctx, "active")
	if err != nil || len(still) != 1 || still[0].ID != id {
		t.Errorf("invite state should be unchanged after rejected revokes; got %+v (err: %v)", still, err)
	}
}

// TestDashboardSettings_Phase3_RevokeNotFound confirms a wrong path
// (an ID that doesn't exist in the DB) surfaces 404 from the dashboard
// — defense-in-depth against a stale row ID still in the operator's
// browser after a concurrent admin-tool revoke from the CLI.
func TestDashboardSettings_Phase3_RevokeNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	time.Sleep(300 * time.Millisecond)
	base := dashboardURL(alice)

	bogus := strings.Repeat("ff", 16) // 32 chars, valid prefix shape, no match
	form := url.Values{"confirm": {bogus[:8]}}
	req := mustNewReq(t, "POST", base+"/settings/invites/"+bogus+"/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	resp := mustDo(t, req)
	_ = mustReadAll(t, resp)
	if resp.StatusCode != 404 {
		t.Errorf("revoke nonexistent invite: expected 404, got %d", resp.StatusCode)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
