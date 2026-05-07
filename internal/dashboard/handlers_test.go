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
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d body %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_Send_EmptyText_Rejected(t *testing.T) {
	deps := fakeDeps{pubkey: "selfpubkey"}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	form := strings.NewReader("text=")
	req := httptest.NewRequest("POST", "/thread/abcdef/send", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
