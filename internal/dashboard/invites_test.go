package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/invitedb"
)

// newPhase3Deps returns a fakeDeps preloaded with three invites — one
// active, one expired, one revoked — so list-rendering and bucketing
// can both be asserted without per-test boilerplate.
func newPhase3Deps() fakeDeps {
	now := time.Now()
	invs := []*invitedb.Invite{
		{
			ID:            "11111111aaaaaaaaaaaaaaaa11111111",
			CreatedAt:     now.Add(-1 * time.Hour),
			ExpiresAt:     now.Add(48 * time.Hour),
			MaxUses:       1,
			Uses:          0,
			IssuerLabel:   "alice",
			RedeemerLabel: "bob",
			Status:        invitedb.StatusActive,
		},
		{
			ID:            "22222222bbbbbbbbbbbbbbbb22222222",
			CreatedAt:     now.Add(-72 * time.Hour),
			ExpiresAt:     now.Add(-1 * time.Hour),
			MaxUses:       1,
			Uses:          0,
			IssuerLabel:   "alice",
			RedeemerLabel: "carol",
			Status:        invitedb.StatusExpired,
		},
		{
			ID:            "33333333cccccccccccccccc33333333",
			CreatedAt:     now.Add(-24 * time.Hour),
			MaxUses:       0, // unlimited
			Uses:          3,
			IssuerLabel:   "alice",
			RedeemerLabel: "",
			Status:        invitedb.StatusRevoked,
		},
	}
	return fakeDeps{
		pubkey: validHex32,
		label:  "alice",
		listInvitesFn: func(ctx context.Context, status string) ([]*invitedb.Invite, error) {
			if status == "" {
				return invs, nil
			}
			out := []*invitedb.Invite{}
			for _, inv := range invs {
				if string(inv.Status) == status {
					out = append(out, inv)
				}
			}
			return out, nil
		},
	}
}

func TestSettingsInvites_FragmentForHTMX(t *testing.T) {
	srv := newTestServer(t, newPhase3Deps())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/invites", nil)
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
		`class="invites-pane"`,
		`hx-trigger="sse:invite.created`,
		`Tokens of welcome`,
		// Active section pulls the active row only:
		`id="invite-11111111"`,
		// History details element shows expired+revoked counts:
		`<details class="invites-history"`,
		`(1 expired, 1 revoked)`,
		// Issue + redeem forms:
		`hx-post="/settings/invites"`,
		`hx-post="/settings/invites/redeem"`,
		`name="redeemer_label"`,
		`name="expires"`,
		`name="max_uses"`,
		`name="token"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("invites pane missing %q\nfull body length=%d", want, len(body))
		}
	}
}

func TestSettingsInvites_FullPageOnDirectNav(t *testing.T) {
	srv := newTestServer(t, newPhase3Deps())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/invites", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<html") {
		t.Error("direct-nav GET should produce the full page")
	}
	// Tab nav should reflect Invites as the active tab.
	if !strings.Contains(body, `id="tab-invites" class="settings-tab is-active"`) {
		t.Error("Invites tab should be marked active on direct nav")
	}
}

func TestSettingsInvites_ListBuckets(t *testing.T) {
	deps := newPhase3Deps()
	view := buildSettingsInvites(context.Background(), deps)
	if got, want := len(view.Active), 1; got != want {
		t.Errorf("Active bucket: got %d, want %d", got, want)
	}
	if got, want := len(view.Expired), 1; got != want {
		t.Errorf("Expired bucket: got %d, want %d", got, want)
	}
	if got, want := len(view.Revoked), 1; got != want {
		t.Errorf("Revoked bucket: got %d, want %d", got, want)
	}
	if view.Active[0].IDShort != "11111111" {
		t.Errorf("Active[0].IDShort = %q, want 11111111", view.Active[0].IDShort)
	}
}

func TestPostInvite_CreateValid(t *testing.T) {
	deps := newPhase3Deps()
	captured := struct{ opts InviteCreateOpts }{}
	deps.createInviteFn = func(ctx context.Context, opts InviteCreateOpts) (*invitedb.Invite, string, error) {
		captured.opts = opts
		return &invitedb.Invite{
			ID:            "deadbeef" + strings.Repeat("0", 24),
			CreatedAt:     time.Now(),
			ExpiresAt:     time.Now().Add(7 * 24 * time.Hour),
			MaxUses:       1,
			Status:        invitedb.StatusActive,
			IssuerLabel:   "alice",
			RedeemerLabel: opts.RedeemerLabel,
		}, "mindgate-invite://stub", nil
	}
	srv := newTestServer(t, deps)
	form := url.Values{
		"redeemer_label": {"bob"},
		"expires":        {"7d"},
		"max_uses":       {"1"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/invites", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !captured.opts.SingleUse {
		t.Errorf("expected SingleUse=true on max_uses=1, got %+v", captured.opts)
	}
	if captured.opts.ExpiresSeconds != int64((7 * 24 * time.Hour).Seconds()) {
		t.Errorf("expected ExpiresSeconds=7d, got %d", captured.opts.ExpiresSeconds)
	}
	if captured.opts.RedeemerLabel != "bob" {
		t.Errorf("expected RedeemerLabel=bob, got %q", captured.opts.RedeemerLabel)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="invite-slip"`,
		`Pass <code>deadbeef</code> sealed`,
		`mindgate-invite://stub`,
		`class="copy-btn"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("create-success render missing %q", want)
		}
	}
}

func TestPostInvite_CreateUnlimitedAndNever(t *testing.T) {
	deps := newPhase3Deps()
	captured := InviteCreateOpts{}
	deps.createInviteFn = func(ctx context.Context, opts InviteCreateOpts) (*invitedb.Invite, string, error) {
		captured = opts
		return &invitedb.Invite{
			ID:        "ff" + strings.Repeat("0", 30),
			CreatedAt: time.Now(),
			MaxUses:   0,
			Status:    invitedb.StatusActive,
		}, "mindgate-invite://x", nil
	}
	srv := newTestServer(t, deps)
	form := url.Values{
		"redeemer_label": {""},
		"expires":        {"never"},
		"max_uses":       {"unlimited"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/invites", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !captured.Unlimited {
		t.Errorf("expected Unlimited=true, got %+v", captured)
	}
	if captured.ExpiresSeconds != -1 {
		t.Errorf("expected ExpiresSeconds=-1 (never), got %d", captured.ExpiresSeconds)
	}
}

func TestPostInvite_CreateBadExpiry(t *testing.T) {
	srv := newTestServer(t, newPhase3Deps())
	form := url.Values{
		"redeemer_label": {""},
		"expires":        {"banana"},
		"max_uses":       {"1"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/invites", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="form-flash is-error"`) {
		t.Error("bad expires should render an error flash, not a success slip")
	}
	if !strings.Contains(body, `Invalid expires value`) {
		t.Errorf("bad expires error message missing; body: %s", body)
	}
}

func TestPostInvite_CreateBadMaxUses(t *testing.T) {
	srv := newTestServer(t, newPhase3Deps())
	form := url.Values{
		"redeemer_label": {""},
		"expires":        {"7d"},
		"max_uses":       {"-3"},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/invites", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `Max uses must be a positive integer`) {
		t.Errorf("bad max_uses should render validation error; body: %s", body)
	}
}

func TestPostInvite_RevokeRequiresConfirm(t *testing.T) {
	id := "11111111aaaaaaaaaaaaaaaa11111111"
	calls := []string{}
	deps := newPhase3Deps()
	deps.revokeCalls = &calls
	srv := newTestServer(t, deps)
	// No confirm field on the POST.
	form := url.Values{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/invites/"+id+"/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing confirm should 400, got %d", rec.Code)
	}
	if len(calls) != 0 {
		t.Errorf("RevokeInvite should NOT be called without confirm; got calls=%v", calls)
	}
}

func TestPostInvite_RevokeWrongPhrase(t *testing.T) {
	id := "11111111aaaaaaaaaaaaaaaa11111111"
	calls := []string{}
	deps := newPhase3Deps()
	deps.revokeCalls = &calls
	srv := newTestServer(t, deps)
	form := url.Values{"confirm": {"22222222"}} // mismatch
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/invites/"+id+"/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("wrong confirm should 400, got %d", rec.Code)
	}
	if len(calls) != 0 {
		t.Errorf("RevokeInvite should NOT be called on wrong phrase; got calls=%v", calls)
	}
}

func TestPostInvite_RevokeSuccess(t *testing.T) {
	id := "11111111aaaaaaaaaaaaaaaa11111111"
	calls := []string{}
	deps := newPhase3Deps()
	deps.revokeCalls = &calls
	srv := newTestServer(t, deps)
	form := url.Values{"confirm": {"11111111"}} // matches IDShort
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/invites/"+id+"/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if got, want := calls, []string{id}; !equalStringSlice(got, want) {
		t.Errorf("RevokeInvite called with %v, want %v", got, want)
	}
	body := rec.Body.String()
	// Must close the modal via OOB swap and re-render the pane.
	if !strings.Contains(body, `<div id="modal" hx-swap-oob="innerHTML"></div>`) {
		t.Error("revoke success should OOB-clear the modal slot")
	}
	if !strings.Contains(body, `class="invites-pane"`) {
		t.Error("revoke success should re-render the invites pane")
	}
}

func TestPostInvite_RevokeNotFound(t *testing.T) {
	deps := newPhase3Deps()
	deps.revokeInviteFn = func(ctx context.Context, idPrefix string) (string, error) {
		return "", invitedb.ErrNotFound
	}
	srv := newTestServer(t, deps)
	form := url.Values{"confirm": {"deadbeef"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/invites/deadbeef/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("ErrNotFound should map to 404, got %d", rec.Code)
	}
}

func TestPostInvite_RedeemValid(t *testing.T) {
	deps := newPhase3Deps()
	deps.redeemInviteFn = func(ctx context.Context, token string) (RedeemResult, error) {
		if token != "mindgate-invite://something" {
			t.Errorf("unexpected token: %q", token)
		}
		return RedeemResult{
			IssuerNpub:  "npub1issuer",
			IssuerRelay: "wss://relay.example",
			AcceptedBy:  []string{"wss://relay.example"},
		}, nil
	}
	srv := newTestServer(t, deps)
	form := url.Values{"token": {"mindgate-invite://something"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/invites/redeem", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="invite-slip is-redeem"`,
		`Welcome accepted`,
		`npub1issuer`,
		`wss://relay.example`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("redeem success render missing %q", want)
		}
	}
}

func TestPostInvite_RedeemEmptyToken(t *testing.T) {
	srv := newTestServer(t, newPhase3Deps())
	form := url.Values{"token": {""}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/invites/redeem", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `Invite URI is required.`) {
		t.Errorf("empty token should render validation error; body: %s", body)
	}
}

func TestPostInvite_RedeemFailureSurfacesError(t *testing.T) {
	deps := newPhase3Deps()
	deps.redeemInviteFn = func(ctx context.Context, token string) (RedeemResult, error) {
		return RedeemResult{}, errors.New("invite: expired")
	}
	srv := newTestServer(t, deps)
	form := url.Values{"token": {"mindgate-invite://stale"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/settings/invites/redeem", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `Redeem failed: invite: expired`) {
		t.Errorf("redeem failure should surface error in flash; body: %s", body)
	}
}

func TestGetInvite_ConfirmRevokeRendersModal(t *testing.T) {
	id := "11111111aaaaaaaaaaaaaaaa11111111"
	srv := newTestServer(t, newPhase3Deps())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/settings/invites/"+id+"/confirm-revoke", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`hx-post="/settings/invites/` + id + `/revoke"`,
		`Type <code>11111111</code>`,
		`data-expected="11111111"`,
		// %q produces double-quotes which html/template escapes to &#34;
		`Issued for redeemer &#34;bob&#34;`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("confirm-revoke modal missing %q", want)
		}
	}
}

func TestParseInviteDuration(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"7d", 7 * 24 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"0d", 0, true},
		{"-1d", 0, true},
		{"banana", 0, true},
		{"-5m", 0, true},
	}
	for _, c := range cases {
		got, err := parseInviteDuration(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseInviteDuration(%q) = %v, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseInviteDuration(%q) returned error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("parseInviteDuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
