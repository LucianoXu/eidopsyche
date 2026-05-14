package dashboard

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
)

// TestHandler_Messages_RendersLabelWhenPresent asserts the dashboard
// /messages view renders the contact label that the daemon-side join
// attached to inbox.list rows. The CLI and dashboard share one display
// helper so an inbox row from "alice" reads as "alice" in both places.
func TestHandler_Messages_RendersLabelWhenPresent(t *testing.T) {
	const senderHex = "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc"
	deps := fakeDeps{
		inbox: []inbox.Message{{
			From:       senderHex,
			Label:      "alice",
			Content:    mustEncode(t, envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "ping"}),
			ReceivedAt: time.Now().Unix(),
		}},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/messages", nil)
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, body)
	}
	if !strings.Contains(body, "alice") {
		t.Errorf("expected label 'alice' in messages view; got %s", body)
	}
}

// TestHandler_Messages_FallsBackToShortHexWithEllipsis asserts the
// label-less code path now matches the CLI's short-hex+ellipsis format
// (the shared helper) instead of the dashboard's previous bespoke
// prefix-and-suffix shape.
func TestHandler_Messages_FallsBackToShortHexWithEllipsis(t *testing.T) {
	const senderHex = "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc"
	deps := fakeDeps{
		inbox: []inbox.Message{{
			From:       senderHex,
			Content:    mustEncode(t, envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "ping"}),
			ReceivedAt: time.Now().Unix(),
		}},
	}
	srv := newTestServer(t, deps)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/messages", nil)
	srv.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "abc1234…") {
		t.Errorf("expected short-hex prefix with ellipsis, got %s", body)
	}
}

// TestHandler_Messages_PendingView verifies /messages?view=pending
// surfaces Pending=true rows under the new Pending tab. The default
// /messages view must hide them; the count chip shows the pending count.
func TestHandler_Messages_PendingView(t *testing.T) {
	const knownHex = "1111111111111111111111111111111111111111111111111111111111111111"
	const strangerHex = "2222222222222222222222222222222222222222222222222222222222222222"
	deps := fakeDeps{
		inbox: []inbox.Message{
			{
				From:       knownHex,
				Label:      "alice",
				Content:    mustEncode(t, envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "from alice"}),
				ReceivedAt: time.Now().Unix(),
				Pending:    false,
			},
			{
				From:       strangerHex,
				Content:    mustEncode(t, envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "cold-call"}),
				ReceivedAt: time.Now().Unix() + 1,
				Pending:    true,
			},
		},
	}
	srv := newTestServer(t, deps)

	// Default view: known only.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest("GET", "/messages", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "from alice") {
		t.Errorf("default view should include known sender's message; body=%s", body)
	}
	if strings.Contains(body, "cold-call") {
		t.Errorf("default view leaked Pending row; body=%s", body)
	}
	if !strings.Contains(body, "Pending (1)") {
		t.Errorf("expected 'Pending (1)' chip in default view; body=%s", body)
	}

	// Pending view: strangers only.
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, httptest.NewRequest("GET", "/messages?view=pending", nil))
	body2 := rec2.Body.String()
	if !strings.Contains(body2, "cold-call") {
		t.Errorf("pending view should include stranger's message; body=%s", body2)
	}
	if strings.Contains(body2, "from alice") {
		t.Errorf("pending view leaked known row; body=%s", body2)
	}
}
