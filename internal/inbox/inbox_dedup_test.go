package inbox

import (
	"testing"
	"time"
)

// TestListInboxCollapsesByEventID covers the "duplicate event_id on disk
// must show once in the dashboard" guarantee. Daemon-restart relay
// re-delivery loses the in-memory dedupe map (see Daemon.dedupe) and
// AppendInbox writes are unconditional, so the same wrap can land in
// inbox.jsonl multiple times. ListInbox now collapses by EventID with
// newest-wins semantics.
func TestListInboxCollapsesByEventID(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()
	first := Message{EventID: "dup", From: "alice", Content: "hi", Kind: 14, RumorAt: now, ReceivedAt: now}
	again := Message{EventID: "dup", From: "alice", Content: "hi", Kind: 14, RumorAt: now, ReceivedAt: now + 100}
	if err := s.AppendInbox(first); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendInbox(again); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListInbox(nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 collapsed row, got %d: %+v", len(got), got)
	}
	// Newest-wins: the surviving row carries the most recent
	// ReceivedAt so the dashboard ordering stays consistent with the
	// underlying file order. The streaming scan stops at the first
	// occurrence newest-first; older replays are skipped.
	if got[0].ReceivedAt != now+100 {
		t.Errorf("collapsed ReceivedAt = %d, want %d (newest-wins)", got[0].ReceivedAt, now+100)
	}
}

// TestListInboxKeepsRowsWithoutEventID covers a defensive corner:
// historical inbox.jsonl rows from before EventID was set, or any
// row where unmarshal left it empty, should not be silently dropped.
func TestListInboxKeepsRowsWithoutEventID(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()
	withID := Message{EventID: "e1", From: "alice", Content: "a", ReceivedAt: now}
	withoutID := Message{EventID: "", From: "alice", Content: "b", ReceivedAt: now + 1}
	if err := s.AppendInbox(withID); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendInbox(withoutID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListInbox(nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("rows without EventID should pass through, got %d: %+v", len(got), got)
	}
}

// TestListInboxFromFilterAfterDedup verifies the from= filter still
// works after the collapse layer.
func TestListInboxFromFilterAfterDedup(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()
	dups := []Message{
		{EventID: "a", From: "alice", Content: "1", ReceivedAt: now},
		{EventID: "a", From: "alice", Content: "1", ReceivedAt: now + 100},
		{EventID: "b", From: "bob", Content: "2", ReceivedAt: now + 1},
	}
	for _, m := range dups {
		if err := s.AppendInbox(m); err != nil {
			t.Fatal(err)
		}
	}
	gotAlice, err := s.ListInbox(nil, "alice", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotAlice) != 1 || gotAlice[0].EventID != "a" {
		t.Errorf("alice filter after dedup: %+v", gotAlice)
	}
	gotBob, err := s.ListInbox(nil, "bob", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotBob) != 1 || gotBob[0].EventID != "b" {
		t.Errorf("bob filter after dedup: %+v", gotBob)
	}
}

// TestEventIDs returns the distinct set of EventIDs persisted in
// inbox.jsonl. Used by Daemon.New to rebuild dedupe across restart.
func TestEventIDs(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()
	rows := []Message{
		{EventID: "a", From: "alice", ReceivedAt: now},
		{EventID: "a", From: "alice", ReceivedAt: now + 100}, // duplicate of "a"
		{EventID: "b", From: "bob", ReceivedAt: now + 1},
		{EventID: "", From: "noid", ReceivedAt: now + 2}, // missing id — skipped
	}
	for _, m := range rows {
		if err := s.AppendInbox(m); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := s.EventIDs()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ids["a"]; !ok {
		t.Errorf("expected a in set: %v", ids)
	}
	if _, ok := ids["b"]; !ok {
		t.Errorf("expected b in set: %v", ids)
	}
	if _, ok := ids[""]; ok {
		t.Errorf("empty EventID must be skipped, got: %v", ids)
	}
	if len(ids) != 2 {
		t.Errorf("expected exactly 2 distinct ids, got %d: %v", len(ids), ids)
	}
}

// TestSelfWrapIDs returns ONLY the self-copy wrap ids (Sent.SelfEventID),
// mirroring the runtime recordSelfWrap behavior. The recipient-bound
// Sent.EventID must NOT enter the set: for a self-addressed send it IS
// the legitimate delivery the subscription dispatches as a self-chat /
// self-command, and dropping it would silently lose the operator's own
// messages across restart.
func TestSelfWrapIDs(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()
	rows := []Sent{
		{EventID: "wrap-bob1", SelfEventID: "self1", To: "bob", SentAt: now},
		{EventID: "wrap-alice1", SelfEventID: "self2", To: "alice", SentAt: now + 1},
		{EventID: "wrap-self-addressed", SelfEventID: "self3", To: "self", SentAt: now + 2},
	}
	for _, o := range rows {
		if err := s.AppendOutbox(o); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := s.SelfWrapIDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"self1", "self2", "self3"} {
		if _, ok := ids[want]; !ok {
			t.Errorf("expected self-copy %q in set: %v", want, ids)
		}
	}
	for _, mustNot := range []string{"wrap-bob1", "wrap-alice1", "wrap-self-addressed"} {
		if _, ok := ids[mustNot]; ok {
			t.Errorf("recipient-bound wrap %q must not be in selfWrapIDs (would drop legitimate self-addressed delivery): %v", mustNot, ids)
		}
	}
	if len(ids) != 3 {
		t.Errorf("expected 3 distinct self-copy ids, got %d: %v", len(ids), ids)
	}
}

// TestListInboxLimitAfterDedup verifies the limit applies to the
// collapsed (post-dedup) view, not the raw row count.
func TestListInboxLimitAfterDedup(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()
	// 4 distinct events, each persisted twice.
	for i := 0; i < 4; i++ {
		m := Message{EventID: string(rune('a' + i)), From: "alice", Content: "x", ReceivedAt: now + int64(i)}
		_ = s.AppendInbox(m)
		_ = s.AppendInbox(m) // duplicate
	}
	got, err := s.ListInbox(nil, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("limit=2 should return 2 distinct rows after dedup, got %d", len(got))
	}
}
