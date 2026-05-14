package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
)

// TestInboxList_JoinsContactLabel exercises the daemon-side join: the
// inbox.list response carries the per-row Label field so every surface
// (CLI, dashboard, future TUI) renders sender names consistently
// without hitting the contacts store again.
func TestInboxList_JoinsContactLabel(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	const knownPubkey = "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc"
	const unknownPubkey = "deadbeefcafebabefeedfacedeadbeefcafebabefeedfacedeadbeefcafebabe"

	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: knownPubkey, Label: "alice", Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	if err := d.Box.AppendInbox(inbox.Message{EventID: "e1", From: knownPubkey, Content: "hi", Kind: 14, RumorAt: now, ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := d.Box.AppendInbox(inbox.Message{EventID: "e2", From: unknownPubkey, Content: "hey", Kind: 14, RumorAt: now + 1, ReceivedAt: now + 1}); err != nil {
		t.Fatal(err)
	}

	// Pass sender="all" so we exercise label joining for BOTH the known
	// and the unknown sender row. The default sender filter is "known",
	// which would hide the unknown row (covered by
	// TestInboxList_SenderFilter_DefaultIsKnown).
	var out []inbox.Message
	if err := d.Call(ctx, "inbox.list", map[string]any{"sender": "all"}, &out); err != nil {
		t.Fatalf("inbox.list: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}

	byID := map[string]inbox.Message{out[0].EventID: out[0], out[1].EventID: out[1]}
	if got := byID["e1"].Label; got != "alice" {
		t.Errorf("known sender row: Label = %q, want %q", got, "alice")
	}
	if got := byID["e1"].Pending; got != false {
		t.Errorf("known sender row: Pending = %v, want false", got)
	}
	if got := byID["e2"].Label; got != "" {
		t.Errorf("unknown sender row: Label = %q, want empty", got)
	}
	if got := byID["e2"].Pending; got != true {
		t.Errorf("unknown sender row: Pending = %v, want true", got)
	}
}

func TestOutboxList_JoinsContactLabel(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	const knownPubkey = "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc"
	const unknownPubkey = "deadbeefcafebabefeedfacedeadbeefcafebabefeedfacedeadbeefcafebabe"

	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: knownPubkey, Label: "bob", Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	if err := d.Box.AppendOutbox(inbox.Sent{EventID: "x1", To: knownPubkey, Content: "hi", SentAt: now, Final: true, AcceptedBy: []string{"wss://r"}}); err != nil {
		t.Fatal(err)
	}
	if err := d.Box.AppendOutbox(inbox.Sent{EventID: "x2", To: unknownPubkey, Content: "hi", SentAt: now + 1, Final: true, AcceptedBy: []string{"wss://r"}}); err != nil {
		t.Fatal(err)
	}

	var out []inbox.Sent
	if err := d.Call(ctx, "outbox.list", map[string]any{}, &out); err != nil {
		t.Fatalf("outbox.list: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}
	byID := map[string]inbox.Sent{out[0].EventID: out[0], out[1].EventID: out[1]}
	if got := byID["x1"].Label; got != "bob" {
		t.Errorf("known recipient row: Label = %q, want %q", got, "bob")
	}
	if got := byID["x2"].Label; got != "" {
		t.Errorf("unknown recipient row: Label = %q, want empty", got)
	}
}

// TestInboxList_SenderFilter exercises the four cases of the new sender
// enum on the IPC handler: empty/"known" filters out Pending rows;
// "unknown" returns only Pending rows; "all" returns everything; and an
// invalid value rejects with INVALID_PARAMS.
func TestInboxList_SenderFilter(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	const known = "1111111111111111111111111111111111111111111111111111111111111111"
	const blocked = "2222222222222222222222222222222222222222222222222222222222222222"
	const unknown = "3333333333333333333333333333333333333333333333333333333333333333"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: known, Label: "K", Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: blocked, Label: "B", Tier: contacts.TierBlocked}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	for i, from := range []string{known, blocked, unknown} {
		if err := d.Box.AppendInbox(inbox.Message{
			EventID: string(rune('a' + i)), From: from, Kind: 14,
			Content: "x", RumorAt: now + int64(i), ReceivedAt: now + int64(i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Default ("known"): only the known-friend row.
	var def []inbox.Message
	if err := d.Call(ctx, "inbox.list", map[string]any{}, &def); err != nil {
		t.Fatalf("default: %v", err)
	}
	if len(def) != 1 || def[0].From != known {
		t.Errorf("default filter: got %d rows (want 1, from K): %+v", len(def), def)
	}

	// Explicit "known": same.
	var k []inbox.Message
	_ = d.Call(ctx, "inbox.list", map[string]any{"sender": "known"}, &k)
	if len(k) != 1 || k[0].From != known {
		t.Errorf(`sender="known": got %d (want 1, from K): %+v`, len(k), k)
	}

	// "unknown": blocked and stranger rows (both Pending=true).
	var u []inbox.Message
	_ = d.Call(ctx, "inbox.list", map[string]any{"sender": "unknown"}, &u)
	if len(u) != 2 {
		t.Errorf(`sender="unknown": got %d (want 2): %+v`, len(u), u)
	}
	for _, m := range u {
		if !m.Pending {
			t.Errorf("unknown-view row missing Pending=true: %+v", m)
		}
	}

	// "all": all three.
	var all []inbox.Message
	_ = d.Call(ctx, "inbox.list", map[string]any{"sender": "all"}, &all)
	if len(all) != 3 {
		t.Errorf(`sender="all": got %d (want 3)`, len(all))
	}

	// Invalid value rejects.
	var ignored []inbox.Message
	err := d.Call(ctx, "inbox.list", map[string]any{"sender": "bogus"}, &ignored)
	if err == nil {
		t.Error(`sender="bogus": expected INVALID_PARAMS`)
	}
}

// TestInboxList_SenderFilter_RetroactivePromote asserts that adding a
// contact later reclassifies past Pending rows as known on the next
// inbox.list call — no replay or migration needed.
func TestInboxList_SenderFilter_RetroactivePromote(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	const pk = "4444444444444444444444444444444444444444444444444444444444444444"
	now := time.Now().Unix()
	if err := d.Box.AppendInbox(inbox.Message{
		EventID: "e", From: pk, Kind: 14, Content: "first-breath",
		RumorAt: now, ReceivedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// Before promote: shows up in unknown view, not known.
	var before []inbox.Message
	_ = d.Call(ctx, "inbox.list", map[string]any{"sender": "known"}, &before)
	if len(before) != 0 {
		t.Errorf("before promote, known view should be empty; got %+v", before)
	}
	_ = d.Call(ctx, "inbox.list", map[string]any{"sender": "unknown"}, &before)
	if len(before) != 1 {
		t.Errorf("before promote, unknown view should have 1 row; got %+v", before)
	}

	// Promote.
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: pk, Label: "Alice", Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	// After promote: known view has the row; unknown view is empty.
	var afterKnown []inbox.Message
	_ = d.Call(ctx, "inbox.list", map[string]any{"sender": "known"}, &afterKnown)
	if len(afterKnown) != 1 || afterKnown[0].Label != "Alice" || afterKnown[0].Pending {
		t.Errorf("after promote, known view: got %+v", afterKnown)
	}
	var afterUnknown []inbox.Message
	_ = d.Call(ctx, "inbox.list", map[string]any{"sender": "unknown"}, &afterUnknown)
	if len(afterUnknown) != 0 {
		t.Errorf("after promote, unknown view should be empty; got %+v", afterUnknown)
	}
}
