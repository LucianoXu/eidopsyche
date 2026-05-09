package daemon

import (
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

func TestSubmitWakeWritesPending(t *testing.T) {
	dir := t.TempDir()
	msg := inbox.Message{
		From:       "npub1alicetest",
		Content:    "hello",
		EventID:    "abcdef0123456789",
		ReceivedAt: 1000,
	}
	if err := submitWake(dir, msg); err != nil {
		t.Fatal(err)
	}
	got, err := wake.ReadPending(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("ReadPending returned nil, want a signal")
	}
	if got.Reason != wake.ReasonMindGate {
		t.Errorf("Reason = %q, want %q", got.Reason, wake.ReasonMindGate)
	}
	if !strings.Contains(got.Hint, "npub1alic") {
		t.Errorf("hint missing sender prefix: %q", got.Hint)
	}
	if got.Context.InboxUnread != 1 {
		t.Errorf("InboxUnread = %d, want 1", got.Context.InboxUnread)
	}
	if got.TriggeredAt != 1000 {
		t.Errorf("TriggeredAt = %d, want 1000", got.TriggeredAt)
	}
	if !strings.Contains(got.ID, "mindgate") {
		t.Errorf("ID missing 'mindgate': %q", got.ID)
	}
}

func TestSubmitWakeEnvelopeTextFallback(t *testing.T) {
	dir := t.TempDir()
	// Content is a v1 envelope JSON; envelopeText should extract the "text" field.
	msg := inbox.Message{
		From:       "npub1bob",
		Content:    `{"v":1,"type":"chat","text":"envelope message","client":{"name":"eidos"}}`,
		EventID:    "00112233",
		ReceivedAt: 2000,
	}
	if err := submitWake(dir, msg); err != nil {
		t.Fatal(err)
	}
	got, err := wake.ReadPending(dir)
	if err != nil || got == nil {
		t.Fatalf("ReadPending: %v, got=%v", err, got)
	}
	if !strings.Contains(got.Hint, "envelope message") {
		t.Errorf("hint should contain extracted text: %q", got.Hint)
	}
}

func TestHelpers(t *testing.T) {
	t.Run("truncate short", func(t *testing.T) {
		if got := truncate("hi", 10); got != "hi" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("truncate long", func(t *testing.T) {
		got := truncate("abcdefghij", 5)
		if !strings.HasSuffix(got, "…") {
			t.Errorf("missing ellipsis: %q", got)
		}
		if len([]rune(got)) != 6 { // 5 chars + ellipsis rune
			t.Errorf("unexpected length: %q", got)
		}
	})
	t.Run("shortNpub long", func(t *testing.T) {
		got := shortNpub("npub1alicetest")
		if !strings.HasPrefix(got, "npub1alice") || !strings.HasSuffix(got, "…") {
			t.Errorf("got %q", got)
		}
	})
	t.Run("shortNpub short", func(t *testing.T) {
		if got := shortNpub("npub1"); got != "npub1" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("shortID", func(t *testing.T) {
		if got := shortID("abcdef0123456789"); got != "abcdef01" {
			t.Errorf("got %q", got)
		}
		if got := shortID("short"); got != "short" {
			t.Errorf("got %q", got)
		}
	})
}
