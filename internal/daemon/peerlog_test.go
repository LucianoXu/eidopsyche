package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
)

// TestPersistSoftReject_LogsPeerLabel asserts that the soft-reject log
// entry includes the human-readable peer string ("alice (abc1234…)")
// derived from the contacts store. Operators should not have to memo
// pubkey hexes when triaging a noisy log line.
func TestPersistSoftReject_LogsPeerLabel(t *testing.T) {
	d := newTestDaemon(t)
	var buf bytes.Buffer
	d.Log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	const senderHex = "abc1234567890abcdef1234567890abcdef1234567890abcdef1234567890abc"
	if err := d.Repo.Add(context.Background(), contacts.Contact{Pubkey: senderHex, Label: "alice", Tier: contacts.TierFriend}); err != nil {
		t.Fatal(err)
	}

	d.persistSoftReject(context.Background(), &gnostr.Event{ID: "ev"}, &gnostr.Event{PubKey: senderHex}, "not_envelope")

	if got := buf.String(); !strings.Contains(got, `peer="alice (abc1234…)"`) {
		t.Errorf("expected peer label in log, got: %s", got)
	}
}
