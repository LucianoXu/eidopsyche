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

func TestListOutboxMergesAckDelta(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	now := time.Now().Unix()
	pre := Sent{V: 1, EventID: "ev1", InnerID: "rumor1", To: "bob", SentAt: now}
	final := Sent{V: 1, EventID: "ev1", InnerID: "rumor1", To: "bob", SentAt: now,
		AcceptedBy: []string{"wss://r"}, Final: true}
	ackDelta := Sent{V: 1, EventID: "ev1", SentAt: now,
		AckedAt: now + 5, AckEventID: "ackwrap1"}

	if err := s.AppendOutbox(pre); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendOutbox(final); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendOutbox(ackDelta); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ListOutbox(nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d, want 1", len(rows))
	}
	got := rows[0]
	if !got.Final || len(got.AcceptedBy) != 1 {
		t.Errorf("non-ack fields not preserved: %+v", got)
	}
	if got.AckedAt != now+5 || got.AckEventID != "ackwrap1" {
		t.Errorf("ack fields not merged: %+v", got)
	}
}

func TestListOutboxAckBeforeFinal(t *testing.T) {
	// Ack delta arriving BEFORE the publish-finalization row must still
	// survive into the merged result (defends against the regression
	// where Final=true row would clobber the ack fields).
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()

	_ = s.AppendOutbox(Sent{V: 1, EventID: "ev1", InnerID: "rumor1", To: "bob", SentAt: now})
	_ = s.AppendOutbox(Sent{V: 1, EventID: "ev1", SentAt: now, AckedAt: now + 1, AckEventID: "ack-early"})
	_ = s.AppendOutbox(Sent{V: 1, EventID: "ev1", InnerID: "rumor1", To: "bob", SentAt: now,
		AcceptedBy: []string{"wss://r"}, Final: true})

	rows, _ := s.ListOutbox(nil, "", 0)
	if len(rows) != 1 || rows[0].AckedAt == 0 || !rows[0].Final {
		t.Errorf("merge lost data: %+v", rows)
	}
}

func TestListOutboxAckFirstWins(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()
	_ = s.AppendOutbox(Sent{V: 1, EventID: "ev1", InnerID: "rumor1", To: "bob", SentAt: now})
	_ = s.AppendOutbox(Sent{V: 1, EventID: "ev1", SentAt: now, AckedAt: now + 1, AckEventID: "ack-first"})
	_ = s.AppendOutbox(Sent{V: 1, EventID: "ev1", SentAt: now, AckedAt: now + 9, AckEventID: "ack-second"})

	rows, _ := s.ListOutbox(nil, "", 0)
	if len(rows) != 1 || rows[0].AckEventID != "ack-first" {
		t.Errorf("first-ack-wins violated: %+v", rows)
	}
}

func TestListInboxKeepFilter(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()
	for i, from := range []string{"alice", "bob", "carol", "alice"} {
		if err := s.AppendInbox(Message{
			EventID: string(rune('a' + i)), From: from, Kind: 14,
			Content: "x", RumorAt: now + int64(i), ReceivedAt: now + int64(i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// keep only carol — should produce exactly one row.
	keep := func(m Message) bool { return m.From == "carol" }
	got, err := s.ListInbox(nil, "", 10, keep)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].From != "carol" {
		t.Fatalf("keep-filter: got %+v", got)
	}
	// nil keep == omitted keep — both behave identically.
	gotNil, _ := s.ListInbox(nil, "", 10, nil)
	gotOmitted, _ := s.ListInbox(nil, "", 10)
	if len(gotNil) != 4 || len(gotOmitted) != 4 {
		t.Fatalf("nil/omitted keep: got %d / %d, want 4 / 4", len(gotNil), len(gotOmitted))
	}
	// keep + limit interaction: limit counts AFTER keep.
	keepNotBob := func(m Message) bool { return m.From != "bob" }
	gotLim, _ := s.ListInbox(nil, "", 2, keepNotBob)
	if len(gotLim) != 2 {
		t.Fatalf("keep+limit: got %d rows, want 2", len(gotLim))
	}
	for _, m := range gotLim {
		if m.From == "bob" {
			t.Fatalf("keep+limit leaked rejected row: %+v", m)
		}
	}
}

func TestAppendInboxStripsTransitFields(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()
	in := Message{
		EventID: "e", From: "x", Kind: 14, Content: "y",
		RumorAt: now, ReceivedAt: now,
		Label:   "should-not-persist",
		Pending: true,
	}
	if err := s.AppendInbox(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListInbox(nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Label != "" {
		t.Errorf("Label persisted: %q", got[0].Label)
	}
	if got[0].Pending != false {
		t.Errorf("Pending persisted: %v", got[0].Pending)
	}
}
