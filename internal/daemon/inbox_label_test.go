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

	var out []inbox.Message
	if err := d.Call(ctx, "inbox.list", map[string]any{}, &out); err != nil {
		t.Fatalf("inbox.list: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}

	byID := map[string]inbox.Message{out[0].EventID: out[0], out[1].EventID: out[1]}
	if got := byID["e1"].Label; got != "alice" {
		t.Errorf("known sender row: Label = %q, want %q", got, "alice")
	}
	if got := byID["e2"].Label; got != "" {
		t.Errorf("unknown sender row: Label = %q, want empty", got)
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
