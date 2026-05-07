//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/daemon"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/LucianoXu/eidopsyche/internal/relayd"
	"github.com/LucianoXu/eidopsyche/internal/store"
)

// bringUpDaemonOnly creates a state dir + identity + daemon that points
// at someone else's relay URL — the v0.5 daemon-only topology. No local
// relay process is started; cfg.Relay.Enabled stays false.
func bringUpDaemonOnly(t *testing.T, name, homeRelayURL string) *instance {
	t.Helper()
	dir := t.TempDir()

	k, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.SaveKey(filepath.Join(dir, "key"), k); err != nil {
		t.Fatal(err)
	}

	{
		db, err := store.Open(filepath.Join(dir, "state.db"), false)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.SetMeta(ctx, "owner_pubkey", k.PublicHex); err != nil {
			t.Fatal(err)
		}
		if err := db.SetMeta(ctx, "label", name); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO own_relays(relay_url,role,added_at) VALUES(?,?,?)`,
			homeRelayURL, "home", time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}

	cfg := config.Defaults()
	cfg.Relay.Enabled = false
	if err := config.Save(filepath.Join(dir, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}

	d, err := daemon.Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	dctx, dcancel := context.WithCancel(context.Background())
	go d.Run(dctx)
	time.Sleep(200 * time.Millisecond)

	in := &instance{
		stateDir: dir,
		relayURL: homeRelayURL,
		daemon:   d,
		relay:    nil, // no local relay
		cancel: func() {
			dcancel()
		},
	}
	t.Cleanup(in.cancel)
	return in
}

// awaitChatText drains the IPC event stream until an inbox.message
// envelope with the given text arrives, or fails the test on timeout.
func awaitChatText(t *testing.T, c *ipc.Client, want string, deadline time.Duration) {
	t.Helper()
	end := time.After(deadline)
	for {
		select {
		case <-end:
			t.Fatalf("timed out waiting for inbox.message text=%q", want)
		case ev := <-c.Events():
			if ev.Event != "inbox.message" {
				continue
			}
			var msg map[string]any
			_ = json.Unmarshal(ev.Data, &msg)
			content, _ := msg["content"].(string)
			env, err := envelope.Decode(content)
			if err != nil {
				continue // malformed / non-envelope soft-rejected; not what we await
			}
			if env.Text == want {
				return
			}
		}
	}
}

// TestDaemonOnly_SendsAndReceives verifies the v0.5 daemon-only topology:
// instance A runs a full daemon + a public-mode relay (modelling a shared
// or third-party relay that serves multiple pubkeys); instance B runs
// only a daemon and uses A's relayURL as its home. Both can send to and
// receive from each other through that single shared relay.
//
// We use ModePublic for A's relay because a paired-mode relay rejects
// kind:1059 events whose p-tag does not match its OwnerHex — which is
// exactly what blocks the multi-user shared-relay topology.
func TestDaemonOnly_SendsAndReceives(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice", relayd.ModePublic)
	bob := bringUpDaemonOnly(t, "bob", alice.relayURL)

	addContact(t, alice, bob)
	addContact(t, bob, alice)

	// Subscriptions need a moment to attach to all known relay URLs.
	time.Sleep(1500 * time.Millisecond)

	bobIPC := dialIPC(t, bob)
	var ack map[string]bool
	if e, err := bobIPC.Call("inbox.tail", nil, &ack); err != nil || e != nil {
		t.Fatalf("bob inbox.tail: %v %+v", err, e)
	}

	aliceIPC := dialIPC(t, alice)
	if e, err := aliceIPC.Call("inbox.tail", nil, &ack); err != nil || e != nil {
		t.Fatalf("alice inbox.tail: %v %+v", err, e)
	}

	// Direction 1: A → B (daemon-only B receives via its subscription on A's relay).
	{
		var sendResp map[string]any
		env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "alice to daemon-only bob"}
		if e, err := aliceIPC.Call("send", map[string]any{
			"to":       bob.daemon.Key.Npub,
			"envelope": env,
		}, &sendResp); err != nil || e != nil {
			t.Fatalf("alice send: %v %+v", err, e)
		}
		awaitChatText(t, bobIPC, "alice to daemon-only bob", 15*time.Second)
	}

	// Direction 2: B (daemon-only) → A. B's outbound publish targets
	// A's-relay (B's own home) ∪ A's contact relays (also A's-relay).
	{
		var sendResp map[string]any
		env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "daemon-only bob to alice"}
		if e, err := bobIPC.Call("send", map[string]any{
			"to":       alice.daemon.Key.Npub,
			"envelope": env,
		}, &sendResp); err != nil || e != nil {
			t.Fatalf("bob send: %v %+v", err, e)
		}
		awaitChatText(t, aliceIPC, "daemon-only bob to alice", 15*time.Second)
	}
}
