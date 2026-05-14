package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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

// TestDispatch_InboundAck_ConcurrentDuplicates_AppendsOneDelta asserts the
// CAS guard inside handleInboundAck: two concurrent inbound ack envelopes
// for the same Sent must produce exactly one ack-delta row on disk, not
// one per goroutine. ListOutbox would still render first-ack-wins, but
// duplicate disk rows would slowly bloat the outbox with control-plane
// noise.
func TestDispatch_InboundAck_ConcurrentDuplicates_AppendsOneDelta(t *testing.T) {
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

	const concurrency = 8
	start := make(chan struct{})
	done := make(chan struct{}, concurrency)
	for i := 0; i < concurrency; i++ {
		i := i
		go func() {
			<-start
			d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ackwrap-" + strconv.Itoa(i)}, makeRumor(to, content))
			done <- struct{}{}
		}()
	}
	close(start)
	for i := 0; i < concurrency; i++ {
		<-done
	}

	// Count raw ack-delta rows directly on disk: any line whose AckedAt > 0
	// is an ack delta. We expect exactly one despite the N concurrent dispatch
	// calls.
	deltaCount := 0
	files, _ := filepath.Glob(filepath.Join(d.StateDir, "outbox", "*", "*", "*.jsonl"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line == "" {
				continue
			}
			var s inbox.Sent
			if err := json.Unmarshal([]byte(line), &s); err != nil {
				t.Fatal(err)
			}
			if s.AckedAt != 0 {
				deltaCount++
			}
		}
	}
	if deltaCount != 1 {
		t.Errorf("concurrent acks produced %d delta rows on disk, want 1", deltaCount)
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

// TestDispatch_UnknownSender_PersistsNoWakeNoAck asserts the new policy:
// chats from a pubkey with no contact row are persisted to the inbox so
// the operator can see them in `eidos gate inbox --sender unknown`, but
// they do NOT fire a wake (no cold-summoning of a mindform by a stranger)
// and they do NOT receive an ack envelope (no presence leak to strangers).
func TestDispatch_UnknownSender_PersistsNoWakeNoAck(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	from := "stranger-pubkey-hex"

	ackCalled := 0
	d.testEmitAck = func(context.Context, string, string) { ackCalled++ }

	wakeDir := t.TempDir()
	d.wakeDir = wakeDir

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "first contact"}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev-stranger"}, makeRumor(from, content))

	// Persisted.
	got, _ := d.Box.ListInbox(nil, "", 10)
	if len(got) != 1 || got[0].From != from || got[0].Malformed {
		t.Fatalf("expected one clean inbox row from unknown sender; got %+v", got)
	}
	// No ack.
	if ackCalled != 0 {
		t.Errorf("ack emitted for unknown sender: count=%d", ackCalled)
	}
	// No wake file.
	entries, _ := os.ReadDir(wakeDir)
	if len(entries) != 0 {
		t.Errorf("wake fired for unknown sender: dir entries=%v", entries)
	}
}

// TestDispatch_BlockedSender_HardDrop asserts that TierBlocked is still
// the hardest possible filter: no persistence, no wake, no ack, no
// broadcast. The new persist-unknowns policy must NOT downgrade blocked
// into pending.
func TestDispatch_BlockedSender_HardDrop(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	from := "mallory-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: from, Tier: contacts.TierBlocked}); err != nil {
		t.Fatal(err)
	}

	ackCalled := 0
	d.testEmitAck = func(context.Context, string, string) { ackCalled++ }

	wakeDir := t.TempDir()
	d.wakeDir = wakeDir

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "rude"}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev-blocked"}, makeRumor(from, content))

	got, _ := d.Box.ListInbox(nil, "", 10)
	if len(got) != 0 {
		t.Errorf("blocked sender persisted: %+v", got)
	}
	if ackCalled != 0 {
		t.Errorf("blocked sender acked: %d", ackCalled)
	}
	entries, _ := os.ReadDir(wakeDir)
	if len(entries) != 0 {
		t.Errorf("blocked sender woke mindform: %v", entries)
	}
}

// TestDispatch_KnownContact_WakeFires guards against accidental tightening
// of the wake gate. A normal known contact's chat must still fire a wake
// when wakeDir is configured.
func TestDispatch_KnownContact_WakeFires(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	from := "bob-pubkey-hex"
	if err := d.Repo.Add(ctx, contacts.Contact{Pubkey: from, Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}
	wakeDir := t.TempDir()
	d.wakeDir = wakeDir

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "hi"}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev-known"}, makeRumor(from, content))

	entries, _ := os.ReadDir(wakeDir)
	if len(entries) == 0 {
		t.Errorf("known contact did NOT fire a wake; entries=%v", entries)
	}
}

// TestDispatch_SelfCopy_WakeFires asserts that self-chats still wake
// (today's behavior). The wake-from-self path is used by the container
// daemon for self-reflection bumps; do not break it.
func TestDispatch_SelfCopy_WakeFires(t *testing.T) {
	d := newTestDaemon(t)
	ctx := context.Background()
	wakeDir := t.TempDir()
	d.wakeDir = wakeDir

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "self-note"}
	content, _ := envelope.Encode(env)
	d.dispatchEnvelope(ctx, &gnostr.Event{ID: "ev-self-wake"}, makeRumor(d.Key.PublicHex, content))

	entries, _ := os.ReadDir(wakeDir)
	if len(entries) == 0 {
		t.Errorf("self-chat did NOT fire a wake; entries=%v", entries)
	}
}

// TestSenderFilter_Allows exercises the per-subscriber inbox.tail filter
// matrix so a regression in broadcastInbox's allows() check is caught
// before a stranger's message can leak into a known-only tail.
func TestSenderFilter_Allows(t *testing.T) {
	pending := inbox.Message{Pending: true}
	known := inbox.Message{Pending: false}
	cases := []struct {
		name   string
		f      senderFilter
		row    inbox.Message
		allows bool
	}{
		{"known filter + pending row → drop", senderKnown, pending, false},
		{"known filter + known row → push", senderKnown, known, true},
		{"unknown filter + pending row → push", senderUnknown, pending, true},
		{"unknown filter + known row → drop", senderUnknown, known, false},
		{"all filter + pending row → push", senderAll, pending, true},
		{"all filter + known row → push", senderAll, known, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.f.allows(tc.row); got != tc.allows {
				t.Errorf("allows: got %v want %v", got, tc.allows)
			}
		})
	}
}
