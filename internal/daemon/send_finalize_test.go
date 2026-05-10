package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/inbox"
)

// TestFinalizeOutboxOrLog_FailureLogged drives the post-publish finalize path
// when the underlying outbox write fails (here: today's daily JSONL path is
// pre-created as a directory so open-for-append errors). The RPC caller must
// not see the failure — publish already succeeded — but the operator must
// see an ERROR log breadcrumb naming the event_id and recipient. Regression
// guard for the silent `_ = d.Box.AppendOutbox(final)` that codex flagged
// on 2026-05-10.
func TestFinalizeOutboxOrLog_FailureLogged(t *testing.T) {
	d := newTestDaemon(t)
	var buf bytes.Buffer
	d.Log = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// AppendOutbox writes to outbox/YYYY/MM/DD.jsonl (UTC). Pre-create the
	// exact daily file path as a directory so OpenFile in O_APPEND|O_CREATE
	// mode fails with "is a directory" on the underlying append.
	sentAt := time.Now().Unix()
	utc := time.Unix(sentAt, 0).UTC()
	dailyDir := filepath.Join(d.StateDir, "outbox",
		utc.Format("2006"), utc.Format("01"))
	if err := os.MkdirAll(dailyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bogusPath := filepath.Join(dailyDir, utc.Format("02")+".jsonl")
	if err := os.Mkdir(bogusPath, 0o755); err != nil {
		t.Fatal(err)
	}

	sent := inbox.Sent{V: 1, EventID: "ev1", To: "bob", SentAt: sentAt, Final: true}
	d.finalizeOutboxOrLog(context.Background(), sent, "ev1", "bob-pubkey")

	var rec map[string]any
	dec := json.NewDecoder(&buf)
	if err := dec.Decode(&rec); err != nil {
		t.Fatalf("expected one JSON log record, got decode error: %v (buf=%q)", err, buf.String())
	}
	if got, _ := rec["level"].(string); got != "ERROR" {
		t.Errorf("log level = %q, want ERROR", got)
	}
	if msg, _ := rec["msg"].(string); !strings.Contains(msg, "finalize outbox write failed") {
		t.Errorf("log message = %q, want substring 'finalize outbox write failed'", msg)
	}
	if id, _ := rec["event_id"].(string); id != "ev1" {
		t.Errorf("event_id = %q, want %q", id, "ev1")
	}
	if to, _ := rec["to"].(string); to != "bob-pubkey" {
		t.Errorf("to = %q, want %q", to, "bob-pubkey")
	}
}

// TestFinalizeOutboxOrLog_SuccessNoLog: when the outbox append succeeds, no
// error log is produced — confirms the helper doesn't spam logs on the happy
// path.
func TestFinalizeOutboxOrLog_SuccessNoLog(t *testing.T) {
	d := newTestDaemon(t)
	var buf bytes.Buffer
	d.Log = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sent := inbox.Sent{V: 1, EventID: "ev1", To: "bob", SentAt: time.Now().Unix(), Final: true}
	d.finalizeOutboxOrLog(context.Background(), sent, "ev1", "bob-pubkey")

	if strings.Contains(buf.String(), "finalize outbox write failed") {
		t.Errorf("unexpected error log on success: %q", buf.String())
	}
}
