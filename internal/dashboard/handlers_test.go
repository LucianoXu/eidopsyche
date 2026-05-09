package dashboard

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

func newTestServer(t *testing.T, deps DashboardDeps) http.Handler {
	t.Helper()
	r, err := newRenderer()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerHandlersWithRenderer(mux, deps, r, slogDiscard())
	return sameOriginGuard(mux)
}

func slogDiscard() *slog.Logger {
	var b strings.Builder
	return slog.New(slog.NewTextHandler(stringWriter{&b}, nil))
}

func TestHandler_Shell_OK(t *testing.T) {
	deps := fakeDeps{pubkey: "abc123def456ghi789", label: "alice"}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "alice") {
		t.Errorf("shell missing label: %s", body)
	}
	// Toast region + global error-handling script must be present so
	// htmx:responseError / htmx:sendError don't drop silently.
	for _, want := range []string{
		`id="toasts"`,
		`htmx:responseError`,
		`htmx:sendError`,
		`window.eidosToast`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("shell missing toast wiring %q", want)
		}
	}
}

// TestHandler_Shell_BusyPathRegexAnchored guards against regressing
// the PR #14 review finding: an unanchored alternation made
// /settings/relays* user submits silently dropped alongside the
// background /relays poll. The literal regex shipped to the browser
// must contain the anchor.
//
// We assert on the served literal (string match) — running the regex
// inside an in-process JS engine is overkill for a one-line guard.
func TestHandler_Shell_BusyPathRegexAnchored(t *testing.T) {
	deps := fakeDeps{pubkey: "abc123def456ghi789", label: "alice"}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	const want = `var BUSY_PATH_RE     = /^\/(events|relays|topbar|sidebar\/)/;`
	if !strings.Contains(body, want) {
		t.Errorf("BUSY_PATH_RE must be path-start anchored to avoid swallowing "+
			"user-initiated /settings/relays* errors; expected literal %q in shell", want)
	}
}

func TestHandler_Sidebar_RendersContacts(t *testing.T) {
	deps := fakeDeps{
		contactsL: []*contacts.Contact{
			{Pubkey: "p1", Label: "Bob", Tier: contacts.TierMaster},
			{Pubkey: "p2", Label: "Alice", Tier: contacts.TierFriend},
		},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/sidebar/contacts", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Bob") || !strings.Contains(body, "Alice") {
		t.Errorf("sidebar missing contacts: %s", body)
	}
}

func TestHandler_Thread_FindsContactAndRendersBubbles(t *testing.T) {
	from := "0123456789abcdef"
	deps := fakeDeps{
		pubkey:    "selfpubkey",
		contactsL: []*contacts.Contact{{Pubkey: from, Label: "Bob", Tier: contacts.TierFriend}},
		inbox: []inbox.Message{
			{From: from, Content: mustEncode(t, envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "hello"}), ReceivedAt: time.Now().Unix()},
		},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/thread/"+from, nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Errorf("thread missing message text: %s", rec.Body.String())
	}
}

func TestHandler_Messages_AllShowsRows(t *testing.T) {
	deps := fakeDeps{
		inbox: []inbox.Message{
			{From: "p1", Content: mustEncode(t, envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "ping"}), ReceivedAt: time.Now().Unix()},
		},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/messages", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ping") {
		t.Errorf("messages missing row text: %s", rec.Body.String())
	}
}

func TestHandler_Messages_MalformedFilter(t *testing.T) {
	deps := fakeDeps{
		inbox: []inbox.Message{
			{From: "p1", Content: "garbage", Malformed: true, RejectReason: "not_envelope", ReceivedAt: time.Now().Unix()},
			{From: "p2", Content: mustEncode(t, envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "ok"}), ReceivedAt: time.Now().Unix()},
		},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/messages?malformed=1", nil)
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "not_envelope") {
		t.Errorf("malformed view missing reason: %s", body)
	}
	// The non-malformed row's content "ok" must not appear.
	if strings.Contains(body, ">ok<") {
		t.Errorf("malformed view should not contain non-malformed row text: %s", body)
	}
}

func TestHandler_Send_ToContact_OK(t *testing.T) {
	deps := fakeDeps{
		pubkey:    "selfpubkey",
		contactsL: []*contacts.Contact{{Pubkey: "abcdef", Label: "Bob", Tier: contacts.TierFriend}},
		sendID:    "evt-1",
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	form := strings.NewReader("text=hello+bob")
	req := httptest.NewRequest("POST", "/thread/abcdef/send", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello bob") {
		t.Errorf("response missing bubble text: %s", rec.Body.String())
	}
}

func TestHandler_Send_RelayFailure_Returns502(t *testing.T) {
	deps := fakeDeps{
		pubkey:    "selfpubkey",
		contactsL: []*contacts.Contact{{Pubkey: "abcdef", Label: "Bob", Tier: contacts.TierFriend}},
		sendErr:   errors.New("stub: no relay accepted"),
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	form := strings.NewReader("text=oops")
	req := httptest.NewRequest("POST", "/thread/abcdef/send", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body %s", rec.Code, rec.Body.String())
	}
	// The body must include the underlying error message in a form the
	// global toast handler in shell.html can surface — without this,
	// htmx 2.x silently drops the 502 and the operator sees nothing.
	// Use a plain substring check; http.Error appends a newline.
	if !strings.Contains(rec.Body.String(), "send failed: stub: no relay accepted") {
		t.Errorf("502 body must include human-readable error for the toast layer; got: %q",
			rec.Body.String())
	}
}

// TestHandler_Compose_SendToUnknown_ReturnsContactNotFound asserts that
// the dashboard refuses sends to an npub that is not in contacts. The
// check itself lives in the IPC send handler (resolveTarget plus
// repo.Get); this test verifies the dashboard surface routes through
// that handler and surfaces CONTACT_NOT_FOUND back to the operator
// instead of silently going through fallback relays.
//
// Pins the fix from
// docs/superpowers/specs/2026-05-09-unified-call-path-design.md
// (Phase 1: dashboard.Send routes through *Daemon.Call).
func TestHandler_Compose_SendToUnknown_ReturnsContactNotFound(t *testing.T) {
	deps := fakeDeps{
		pubkey: "selfpubkey",
		sendErr: &ipc.Error{
			Code:    ipc.ErrContactNotFound,
			Message: "deadbeef",
		},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	form := strings.NewReader("to=deadbeef&text=hi")
	req := httptest.NewRequest("POST", "/compose/send", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body %s", rec.Code, rec.Body.String())
	}
	// The body must include CONTACT_NOT_FOUND so the toast layer surfaces
	// the typed code (operator can act on it: "add this npub first").
	if !strings.Contains(rec.Body.String(), "CONTACT_NOT_FOUND") {
		t.Errorf("502 body must contain CONTACT_NOT_FOUND for the toast layer; got: %q",
			rec.Body.String())
	}
}

func TestGuard_PostWithoutOrigin_Rejected(t *testing.T) {
	deps := fakeDeps{pubkey: "selfpubkey"}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	form := strings.NewReader("text=hi")
	req := httptest.NewRequest("POST", "/thread/abc/send", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Deliberately NO Origin header.
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestGuard_OriginNull_Rejected(t *testing.T) {
	deps := fakeDeps{pubkey: "selfpubkey"}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "null")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for Origin: null, got %d", rec.Code)
	}
}

func TestSendRoute_GET_Rejected(t *testing.T) {
	deps := fakeDeps{pubkey: "selfpubkey"}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/thread/abc/send?text=hi", nil)
	srv.ServeHTTP(rec, req)
	// GET to /thread/{pk}/send should NOT trigger send. The route falls
	// into threadHandler which strips the trailing /send and returns the
	// thread for pubkey "abc/send" — either way no send occurs and
	// status is 200 (rendering succeeds). The contract here is that no
	// 200 with a "you" bubble is returned.
	if strings.Contains(rec.Body.String(), `class="bubble self`) {
		t.Fatal("GET should not produce a sent-bubble fragment")
	}
}

func TestComposeSendRoute_GET_Rejected(t *testing.T) {
	deps := fakeDeps{pubkey: "selfpubkey"}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/compose/send?to=npub1abc&text=hi", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 on GET to /compose/send, got %d", rec.Code)
	}
}

func TestHandler_Send_EmptyText_Rejected(t *testing.T) {
	deps := fakeDeps{pubkey: "selfpubkey"}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	form := strings.NewReader("text=")
	req := httptest.NewRequest("POST", "/thread/abcdef/send", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+req.Host)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func mustEncode(t *testing.T, e envelope.Envelope) string {
	t.Helper()
	s, err := envelope.Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
