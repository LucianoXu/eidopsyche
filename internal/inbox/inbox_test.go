package inbox

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndListInbox(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()
	m1 := Message{EventID: "e1", From: "alice", Content: "hi", Kind: 14, RumorAt: now, ReceivedAt: now}
	m2 := Message{EventID: "e2", From: "bob", Content: "hey", Kind: 14, RumorAt: now + 1, ReceivedAt: now + 1}
	if err := s.AppendInbox(m1); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendInbox(m2); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListInbox(nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d", len(got))
	}
	if got[0].EventID != "e2" {
		t.Fatalf("expected newest first, got %v", got)
	}
	gotBob, err := s.ListInbox(nil, "bob", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotBob) != 1 || gotBob[0].EventID != "e2" {
		t.Fatalf("from filter: %v", gotBob)
	}
}

func TestOutboxCollapseAndFinal(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()
	pre := Sent{EventID: "x", To: "bob", Content: "hi", SentAt: now, AcceptedBy: nil}
	final := Sent{EventID: "x", To: "bob", Content: "hi", SentAt: now, AcceptedBy: []string{"wss://r"}, Final: true}
	if err := s.AppendOutbox(pre); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendOutbox(final); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListOutbox(nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 collapsed row, got %d", len(got))
	}
	if !got[0].Final || len(got[0].AcceptedBy) != 1 {
		t.Fatalf("collapse picked wrong row: %+v", got[0])
	}
}

func TestDailyPath(t *testing.T) {
	ts := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	if got := dailyPath("/x/inbox", ts); got != filepath.Join("/x/inbox", "2026", "05", "06.jsonl") {
		t.Fatalf("got %q", got)
	}
}

func TestMessage_LegacyRowReadsAsZero(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	// Pre-envelope (legacy) message: Malformed/RejectReason absent.
	legacy := Message{
		EventID:    "abc",
		From:       "deadbeef",
		Kind:       14,
		Content:    "hello world",
		ReceivedAt: 1700000000,
	}
	if err := s.AppendInbox(legacy); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListInbox(nil, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Malformed != false || got[0].RejectReason != "" {
		t.Fatalf("legacy row got non-zero new fields: %+v", got[0])
	}
}
