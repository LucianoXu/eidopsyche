//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/nostr"
)

// waitForInboxFrom polls the daemon's inbox until at least one row matching
// the given sender pubkey arrives, returning the most recent matching row.
func waitForInboxFrom(t *testing.T, in *instance, fromPubkey string, timeout time.Duration) inbox.Message {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		msgs, err := in.daemon.Box.ListInbox(nil, fromPubkey, 1)
		if err == nil && len(msgs) > 0 {
			return msgs[0]
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for inbox row from %s", fromPubkey)
	return inbox.Message{}
}

// sendEnvelopeIPC encodes env and invokes the daemon's send IPC method.
func sendEnvelopeIPC(t *testing.T, in *instance, toPubkey string, env envelope.Envelope) {
	t.Helper()
	c := dialIPC(t, in)
	var resp struct {
		EventID    string   `json:"event_id"`
		AcceptedBy []string `json:"accepted_by"`
	}
	ipcErr, err := c.Call("send", map[string]any{"to": toPubkey, "envelope": env}, &resp)
	if err != nil || ipcErr != nil {
		t.Fatalf("send: %v %+v", err, ipcErr)
	}
}

// sendRawContent publishes a NIP-17 gift wrap from sender to recipient with
// the given raw content (used to simulate non-envelope or future-version
// peers, bypassing the IPC encode path).
func sendRawContent(t *testing.T, sender, recipient *instance, content string) {
	t.Helper()
	wrap, _, err := nostr.Wrap(sender.daemon.Key.PrivateHex, recipient.daemon.Key.PublicHex, content)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := sender.daemon.Pool.Publish(ctx, []string{recipient.relayURL}, wrap)
	for _, r := range res {
		if r.OK {
			return
		}
	}
	t.Fatalf("no relay accepted raw publish: %+v", res)
}

func TestEnvelope_ChatRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	addContact(t, alice, bob)
	addContact(t, bob, alice)

	env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "hello bob"}
	sendEnvelopeIPC(t, alice, bob.daemon.Key.PublicHex, env)

	row := waitForInboxFrom(t, bob, alice.daemon.Key.PublicHex, 5*time.Second)
	if row.Malformed {
		t.Fatalf("expected clean row, got malformed: %+v", row)
	}
	got, err := envelope.Decode(row.Content)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Text != "hello bob" {
		t.Fatalf("text mismatch: %q", got.Text)
	}
}

func TestEnvelope_LoopbackStatus(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")

	env := envelope.Envelope{V: 1, Type: envelope.TypeCommand,
		Command: &envelope.Command{Name: "status", Args: map[string]any{}}}
	sendEnvelopeIPC(t, alice, alice.daemon.Key.PublicHex, env)

	row := waitForInboxFrom(t, alice, alice.daemon.Key.PublicHex, 10*time.Second)
	if row.Malformed {
		t.Fatalf("status reply marked malformed: %+v", row)
	}
	got, err := envelope.Decode(row.Content)
	if err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	if !strings.Contains(got.Text, "uptime") {
		t.Fatalf("status reply missing uptime: %q", got.Text)
	}
}

func TestEnvelope_ForeignCommandSoftRejects(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	addContact(t, alice, bob)
	addContact(t, bob, alice)

	env := envelope.Envelope{V: 1, Type: envelope.TypeCommand,
		Command: &envelope.Command{Name: "status", Args: map[string]any{}}}
	sendEnvelopeIPC(t, bob, alice.daemon.Key.PublicHex, env)

	row := waitForInboxFrom(t, alice, bob.daemon.Key.PublicHex, 5*time.Second)
	if !row.Malformed || row.RejectReason != "unauthorized_command" {
		t.Fatalf("expected unauthorized_command soft-reject, got %+v", row)
	}
}

func TestEnvelope_PlainTextSoftRejects(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	addContact(t, alice, bob)
	addContact(t, bob, alice)

	sendRawContent(t, bob, alice, "not an envelope")

	row := waitForInboxFrom(t, alice, bob.daemon.Key.PublicHex, 5*time.Second)
	if !row.Malformed || row.RejectReason != "not_envelope" {
		t.Fatalf("expected not_envelope soft-reject, got %+v", row)
	}
}

func TestEnvelope_FutureVersionSoftRejects(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	addContact(t, alice, bob)
	addContact(t, bob, alice)

	sendRawContent(t, bob, alice, `{"v":2,"type":"chat","text":"hi"}`)

	row := waitForInboxFrom(t, alice, bob.daemon.Key.PublicHex, 5*time.Second)
	if !row.Malformed || row.RejectReason != "unsupported_version" {
		t.Fatalf("expected unsupported_version soft-reject, got %+v", row)
	}
}
