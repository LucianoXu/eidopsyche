package daemon

import (
	"context"
	"testing"
	"time"

	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
)

// TestBroadcastInbox_AnnotatesLabel exercises the live push path used by
// `gate inbox --tail` and the dashboard SSE thread. A known contact's
// freshly-arrived message must carry the contact label on the pushed
// event so the consumer renders the friendly name without waiting for
// the next inbox.list refresh.
func TestBroadcastInbox_AnnotatesLabel(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	const senderHex = "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: senderHex, Label: "alice", Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}

	a := dashboardAdapter{d: d}
	ch, cancel := a.SubscribeEvents()
	defer cancel()

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "hi"}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev-tail"}, makeRumor(senderHex, content))

	select {
	case ev := <-ch:
		if ev.Kind != "inbox.message" {
			t.Fatalf("kind %q want inbox.message", ev.Kind)
		}
		if ev.Message == nil {
			t.Fatal("missing message data")
		}
		if got := ev.Message.Label; got != "alice" {
			t.Errorf("broadcast inbox: Label = %q, want %q", got, "alice")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive inbox.message event")
	}
	_ = dashboard.Event{}
}
