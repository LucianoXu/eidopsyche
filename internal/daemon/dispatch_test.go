package daemon

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/dashboard"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
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

func TestDashboardHub_FanOutOnInbox(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	a := dashboardAdapter{d: d}
	ch1, cancel1 := a.SubscribeEvents()
	defer cancel1()
	ch2, cancel2 := a.SubscribeEvents()
	defer cancel2()

	from := "from-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: from, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "hi"}
	content, _ := envelope.Encode(env)

	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev-hub"}, makeRumor(from, content))

	for i, ch := range []<-chan dashboard.Event{ch1, ch2} {
		// Each subscriber may receive both an inbox.message (from
		// broadcastInbox) and an outbox.message (from emitAck's ack
		// publish — but only if Pool.Publish reaches a real relay; in
		// this unit test, no Pool is wired, so just take the first).
		select {
		case ev := <-ch:
			if ev.Kind != "inbox.message" {
				t.Errorf("subscriber %d kind %q want inbox.message", i, ev.Kind)
			}
			if ev.Message == nil || ev.Message.From != from {
				t.Errorf("subscriber %d missing message data: %+v", i, ev)
			}
		case <-time.After(500 * time.Millisecond):
			t.Errorf("subscriber %d did not receive event", i)
		}
	}
}

func TestDispatch_ChatFromContact_EmitsAck(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	from := "bob-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: from, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}

	var (
		ackTo  string
		ackRef string
		called int
	)
	d.testEmitAck = func(_ context.Context, to, ref string) {
		called++
		ackTo, ackRef = to, ref
	}

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "hi"}
	content, _ := envelope.Encode(env)
	rumor := makeRumor(from, content)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev"}, rumor)

	if called != 1 || ackTo != from || ackRef != rumor.ID {
		t.Errorf("ack call: count=%d to=%q ref=%q; want 1, %q, %q", called, ackTo, ackRef, from, rumor.ID)
	}
}

func TestDispatch_SelfCopy_NoAck(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()

	called := 0
	d.testEmitAck = func(context.Context, string, string) { called++ }

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "self-note"}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev-self"}, makeRumor(d.Key.PublicHex, content))

	if called != 0 {
		t.Errorf("ack emitted for self-copy: count=%d", called)
	}
}

func TestDispatch_BlockedSender_NoAck(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	from := "mallory-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: from, Tier: contacts.TierBlocked}); err != nil {
		t.Fatal(err)
	}

	called := 0
	d.testEmitAck = func(context.Context, string, string) { called++ }

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "rude"}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev-blk"}, makeRumor(from, content))

	if called != 0 {
		t.Errorf("ack emitted for blocked sender: count=%d", called)
	}
}

const ackTestRumorRef = "00000000000000000000000000000000000000000000000000000000aaaaaaaa"

func TestDispatch_InboundAck_MutatesOutbox(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	to := "bob-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: to, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	if err := d.Box.AppendOutbox(inbox.Sent{V: 1, EventID: "evW", InnerID: ackTestRumorRef, To: to, SentAt: 100}); err != nil {
		t.Fatal(err)
	}

	env := envelope.Envelope{V: 1, Type: envelope.TypeAck, Ref: ackTestRumorRef}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ackwrap1"}, makeRumor(to, content))

	rows, _ := d.Box.ListOutbox(nil, "", 0)
	if len(rows) != 1 || rows[0].AckedAt == 0 || rows[0].AckEventID != "ackwrap1" {
		t.Errorf("ack not recorded: %+v", rows)
	}
}

func TestDispatch_InboundAck_UnknownRef_Drops(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	to := "bob-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: to, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}

	env := envelope.Envelope{V: 1, Type: envelope.TypeAck, Ref: strings.Repeat("a", 64)}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ackwrap-orphan"}, makeRumor(to, content))

	rows, _ := d.Box.ListOutbox(nil, "", 0)
	if len(rows) != 0 {
		t.Errorf("orphan ack created rows: %+v", rows)
	}
}

func TestDispatch_InboundAck_WrongSender_Rejects(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	bob := "bob-pubkey-hex"
	carol := "carol-pubkey-hex"
	for _, p := range []string{bob, carol} {
		if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: p, Tier: contacts.TierFriend}); err != nil {
			t.Fatal(err)
		}
	}
	_ = d.Box.AppendOutbox(inbox.Sent{V: 1, EventID: "evW", InnerID: ackTestRumorRef, To: bob, SentAt: 100})

	env := envelope.Envelope{V: 1, Type: envelope.TypeAck, Ref: ackTestRumorRef}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ackwrap"}, makeRumor(carol, content))

	rows, _ := d.Box.ListOutbox(nil, "", 0)
	if len(rows) != 1 || rows[0].AckedAt != 0 {
		t.Errorf("ack from wrong sender was accepted: %+v", rows)
	}
}

func TestDispatch_InboundAck_FirstAckWins(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	to := "bob-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: to, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	_ = d.Box.AppendOutbox(inbox.Sent{V: 1, EventID: "evW", InnerID: ackTestRumorRef, To: to, SentAt: 100,
		AckedAt: 999, AckEventID: "ackwrap-original"})

	env := envelope.Envelope{V: 1, Type: envelope.TypeAck, Ref: ackTestRumorRef}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ackwrap-second"}, makeRumor(to, content))

	rows, _ := d.Box.ListOutbox(nil, "", 0)
	if rows[0].AckEventID != "ackwrap-original" {
		t.Errorf("first-ack-wins violated: got %s", rows[0].AckEventID)
	}
}

// TestDispatch_InboundAck_NoInboxNoWake guards against regressions of the
// "ack must not surface as an inbox row and must not trigger a wake" rule.
// Failure here would mean acks become user-visible chat or fire wake signals,
// either of which would defeat the daemon-control nature of tier-2.
func TestDispatch_InboundAck_NoInboxNoWake(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	to := "bob-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: to, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	_ = d.Box.AppendOutbox(inbox.Sent{V: 1, EventID: "evW", InnerID: ackTestRumorRef, To: to, SentAt: 100})

	wakeDir := t.TempDir()
	d.wakeDir = wakeDir

	env := envelope.Envelope{V: 1, Type: envelope.TypeAck, Ref: ackTestRumorRef}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ackwrap1"}, makeRumor(to, content))

	// No inbox row should appear for the ack envelope.
	msgs, _ := d.Box.ListInbox(nil, "", 0)
	for _, m := range msgs {
		if m.From == to {
			t.Errorf("ack created inbox row: %+v", m)
		}
	}

	// No wake-pending file should have been written.
	entries, _ := os.ReadDir(wakeDir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".discarded") {
			t.Errorf("ack triggered wake file: %s", e.Name())
		}
	}
}
