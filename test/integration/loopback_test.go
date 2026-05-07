//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/daemon"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/LucianoXu/eidopsyche/internal/relayd"
	"github.com/LucianoXu/eidopsyche/internal/store"
)

type instance struct {
	stateDir string
	relayURL string
	daemon   *daemon.Daemon
	relay    *relayd.Server
	cancel   context.CancelFunc
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// bringUp creates a state dir, generates an identity, writes meta + own_relays
// + config.toml, starts a relay on a random localhost port and a daemon
// against the resulting state dir.
//
// The optional modeOpt selects the relay mode (default: ModePaired, matching
// the typical single-user install). Tests that need to model a shared /
// community relay pass ModePublic so the relay accepts gift wraps for
// pubkeys other than its host's.
func bringUp(t *testing.T, name string, modeOpt ...relayd.Mode) *instance {
	t.Helper()
	mode := relayd.ModePaired
	if len(modeOpt) > 0 {
		mode = modeOpt[0]
	}
	dir := t.TempDir()

	k, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.SaveKey(filepath.Join(dir, "key"), k); err != nil {
		t.Fatal(err)
	}

	addr := freePort(t)
	relayURL := "ws://" + addr

	// Write initial state.db with meta + own_relays.
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
			relayURL, "home", time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}

	cfg := config.Defaults()
	// Each test instance gets its own dashboard port so two instances on
	// the same host don't conflict on the default 127.0.0.1:22893.
	// freePort returns "127.0.0.1:NNNNN" which is exactly what dashboard.Listen wants.
	cfg.Dashboard.Listen = freePort(t)
	if err := config.Save(filepath.Join(dir, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}

	// Start the paired relay (separate RO connection to the same SQLite file).
	dbRO, err := store.Open(filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	wl := relayd.NewWhitelistSource(dbRO, 100*time.Millisecond)
	rctx, rcancel := context.WithCancel(context.Background())
	go wl.Run(rctx)
	if err := wl.RefreshNow(rctx); err != nil {
		t.Fatal(err)
	}
	rsrv, err := relayd.New(relayd.Config{
		Mode:      mode,
		Listen:    addr,
		OwnerHex:  k.PublicHex,
		Whitelist: wl,
	})
	if err != nil {
		t.Fatal(err)
	}
	go rsrv.ListenAndServe()
	time.Sleep(200 * time.Millisecond)

	// Start the daemon. It opens its own RW connection to the same DB.
	d, err := daemon.Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	dctx, dcancel := context.WithCancel(context.Background())
	go d.Run(dctx)
	time.Sleep(200 * time.Millisecond)

	in := &instance{
		stateDir: dir,
		relayURL: relayURL,
		daemon:   d,
		relay:    rsrv,
		cancel: func() {
			dcancel()
			rcancel()
			rsrv.Shutdown(context.Background())
			dbRO.Close()
		},
	}
	t.Cleanup(in.cancel)
	return in
}

func dialIPC(t *testing.T, in *instance) *ipc.Client {
	t.Helper()
	cfg, _ := config.Load(filepath.Join(in.stateDir, "config.toml"))
	c, err := ipc.Dial(filepath.Join(in.stateDir, cfg.Daemon.Socket))
	if err != nil {
		t.Fatalf("dial ipc: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func addContact(t *testing.T, owner, peer *instance) {
	t.Helper()
	c := dialIPC(t, owner)
	var resp map[string]bool
	ipcErr, err := c.Call("contact.add", map[string]any{
		"npub":   peer.daemon.Key.Npub,
		"relays": []string{peer.relayURL},
		"label":  "peer",
		"tier":   string(contacts.TierFriend),
	}, &resp)
	if err != nil || ipcErr != nil {
		t.Fatalf("contact.add: %v %+v", err, ipcErr)
	}
}

func TestSendByLabel(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	addContact(t, alice, bob) // adds peer with label "peer"
	addContact(t, bob, alice)
	time.Sleep(800 * time.Millisecond)

	bobIPC := dialIPC(t, bob)
	var ack map[string]bool
	if e, err := bobIPC.Call("inbox.tail", nil, &ack); err != nil || e != nil {
		t.Fatalf("inbox.tail: %v %+v", err, e)
	}

	aliceIPC := dialIPC(t, alice)
	// Send using the label "peer" rather than Bob's npub.
	var sendResp map[string]any
	chatEnv := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "by label"}
	if e, err := aliceIPC.Call("send", map[string]any{
		"to":       "peer",
		"envelope": chatEnv,
	}, &sendResp); err != nil || e != nil {
		t.Fatalf("send by label: %v %+v", err, e)
	}

	deadline := time.After(8 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for inbox.message after send-by-label")
		case ev := <-bobIPC.Events():
			if ev.Event != "inbox.message" {
				continue
			}
			var msg map[string]any
			_ = json.Unmarshal(ev.Data, &msg)
			content, _ := msg["content"].(string)
			env, err := envelope.Decode(content)
			if err != nil {
				t.Fatalf("decode: %v (raw=%q)", err, content)
			}
			if env.Text != "by label" {
				t.Fatalf("text %v", env.Text)
			}
			return
		}
	}
}

func TestAliceBobLoopback(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")

	addContact(t, alice, bob)
	addContact(t, bob, alice)

	// Daemons need a moment for subscriptions to attach to peer relays.
	// Note: they subscribed at startup before contacts were added, so the
	// peer relay is not yet in their subscription set. We need to wait
	// long enough for the daemon to either pick up new contact relays
	// (if it polls) OR for our send path to publish to peer's relay (the
	// recipient relay is the one Bob will pull from anyway).
	time.Sleep(1500 * time.Millisecond)

	bobIPC := dialIPC(t, bob)
	var ack map[string]bool
	if e, err := bobIPC.Call("inbox.tail", nil, &ack); err != nil || e != nil {
		t.Fatalf("inbox.tail: %v %+v", err, e)
	}

	aliceIPC := dialIPC(t, alice)
	var sendResp map[string]any
	chatEnv := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "hello bob"}
	if e, err := aliceIPC.Call("send", map[string]any{
		"to":       bob.daemon.Key.Npub,
		"envelope": chatEnv,
	}, &sendResp); err != nil || e != nil {
		t.Fatalf("send: %v %+v", err, e)
	}
	t.Logf("send result: %+v", sendResp)

	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for inbox.message")
		case ev := <-bobIPC.Events():
			if ev.Event != "inbox.message" {
				continue
			}
			var msg map[string]any
			_ = json.Unmarshal(ev.Data, &msg)
			content, _ := msg["content"].(string)
			env, err := envelope.Decode(content)
			if err != nil {
				t.Fatalf("decode: %v (raw=%q)", err, content)
			}
			if env.Text != "hello bob" {
				t.Fatalf("unexpected text %v", env.Text)
			}
			if msg["from"] != alice.daemon.Key.PublicHex {
				t.Fatalf("from mismatch: got %v want %v", msg["from"], alice.daemon.Key.PublicHex)
			}
			return
		}
	}
}
