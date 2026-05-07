package daemon

import (
	"context"
	"strings"
	"testing"

	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
)

// makeRumor builds a minimal rumor (kind:14) with the given pubkey & content,
// matching what nostr.Unwrap returns to handleIncoming.
func makeRumor(pubkey, content string) *gnostr.Event {
	return &gnostr.Event{Kind: 14, PubKey: pubkey, Content: content, ID: "rumor-" + pubkey}
}

func TestDispatch_ChatFromContact_PersistsCleanRow(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	from := "from-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: from, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "hi"}
	content, _ := envelope.Encode(env)

	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev1"}, makeRumor(from, content))

	got, _ := d.Box.ListInbox(nil, "", 10)
	if len(got) != 1 || got[0].Malformed || got[0].RejectReason != "" {
		t.Fatalf("expected clean chat row; got %+v", got)
	}
}

func TestDispatch_PlainTextSoftRejects(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	from := "from-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: from, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev2"}, makeRumor(from, "hello (not envelope)"))

	got, _ := d.Box.ListInbox(nil, "", 10)
	if len(got) != 1 || !got[0].Malformed || got[0].RejectReason != "not_envelope" {
		t.Fatalf("expected soft-reject not_envelope; got %+v", got)
	}
}

func TestDispatch_FutureVersionSoftRejects(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	from := "from-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: from, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev3"}, makeRumor(from, `{"v":2,"type":"chat","text":"hi"}`))

	got, _ := d.Box.ListInbox(nil, "", 10)
	if len(got) != 1 || !got[0].Malformed || got[0].RejectReason != "unsupported_version" {
		t.Fatalf("expected soft-reject unsupported_version; got %+v", got)
	}
}

func TestDispatch_CommandFromForeignSoftRejects(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	from := "stranger-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: from, Tier: contacts.TierMaster}); err != nil {
		t.Fatal(err)
	}
	env := envelope.Envelope{V: 1, Type: envelope.TypeCommand, Command: &envelope.Command{Name: "status", Args: map[string]any{}}}
	content, _ := envelope.Encode(env)

	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev4"}, makeRumor(from, content))

	got, _ := d.Box.ListInbox(nil, "", 10)
	if len(got) != 1 || !got[0].Malformed || got[0].RejectReason != "unauthorized_command" {
		t.Fatalf("expected unauthorized_command; got %+v", got)
	}
}

func TestDispatch_CommandFromSelfExecutes(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	env := envelope.Envelope{V: 1, Type: envelope.TypeCommand, Command: &envelope.Command{Name: "status", Args: map[string]any{}}}
	content, _ := envelope.Encode(env)

	var captured string
	d.testSendChatReply = func(_ context.Context, _ string, text string) error {
		captured = text
		return nil
	}
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev5"}, makeRumor(d.Key.PublicHex, content))

	if !strings.Contains(captured, "uptime") {
		t.Fatalf("expected status reply with uptime, got %q", captured)
	}
	got, _ := d.Box.ListInbox(nil, "", 10)
	if len(got) != 0 {
		t.Fatalf("authorized command should not append to inbox; got %d rows: %+v", len(got), got)
	}
}

func TestDispatch_UnknownCommandFromSelfSoftRejects(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	env := envelope.Envelope{V: 1, Type: envelope.TypeCommand, Command: &envelope.Command{Name: "frobnicate", Args: map[string]any{}}}
	content, _ := envelope.Encode(env)
	d.testSendChatReply = func(_ context.Context, _ string, _ string) error {
		t.Fatal("unknown command should not trigger reply")
		return nil
	}

	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev6"}, makeRumor(d.Key.PublicHex, content))

	got, _ := d.Box.ListInbox(nil, "", 10)
	if len(got) != 1 || !got[0].Malformed || got[0].RejectReason != "unknown_command" {
		t.Fatalf("expected unknown_command soft-reject; got %+v", got)
	}
}
