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
