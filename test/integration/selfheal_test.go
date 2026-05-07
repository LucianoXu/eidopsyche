//go:build integration

package integration

import (
	"context"
	"encoding/json"
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

// TestSelfHealDaemonBeforeRelay starts both daemons before any relay is
// listening, then brings up the relays. With the retry loop, daemons should
// reattach without any manual reconnect, and a normal Alice->Bob send should
// be received within ~10s. No `subscribe.refresh` call.
func TestSelfHealDaemonBeforeRelay(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	dirA := t.TempDir()
	dirB := t.TempDir()
	addrA := freePort(t)
	addrB := freePort(t)

	kA := seedState(t, dirA, "alice", "ws://"+addrA)
	kB := seedState(t, dirB, "bob", "ws://"+addrB)

	// Start daemons FIRST. Their initial Subscribe will fail since no relays
	// are listening yet.
	dA, err := daemon.Start(dirA)
	if err != nil {
		t.Fatal(err)
	}
	dB, err := daemon.Start(dirB)
	if err != nil {
		t.Fatal(err)
	}
	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelA()
	defer cancelB()
	go dA.Run(ctxA)
	go dB.Run(ctxB)

	// Give them time to fail and enter retry backoff.
	time.Sleep(500 * time.Millisecond)

	// Now start the relays.
	rA := startRelayDir(t, dirA, addrA, kA.PublicHex)
	rB := startRelayDir(t, dirB, addrB, kB.PublicHex)
	t.Cleanup(func() { rA.Shutdown(context.Background()); rB.Shutdown(context.Background()) })

	// Add contacts (this also kicks the subscriber via auto-refresh).
	addContactDirect(t, dirA, kB.Npub, "ws://"+addrB)
	addContactDirect(t, dirB, kA.Npub, "ws://"+addrA)

	// Wait for self-heal: backoff is 1s->2s->4s, so after the first failure at
	// t=0 the retry loop will try again at t=1s and succeed once the relays
	// are up. Plus add-contact issues an explicit kick, so this should be
	// fast -- but allow generous slack.
	time.Sleep(3 * time.Second)

	bobIPC := dialIPCDir(t, dirB)
	var ack map[string]bool
	if e, err := bobIPC.Call("inbox.tail", nil, &ack); err != nil || e != nil {
		t.Fatalf("inbox.tail: %v %+v", err, e)
	}

	aliceIPC := dialIPCDir(t, dirA)
	var sendResp map[string]any
	chatEnv := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "self-heal works"}
	if e, err := aliceIPC.Call("send", map[string]any{
		"to":       kB.Npub,
		"envelope": chatEnv,
	}, &sendResp); err != nil || e != nil {
		t.Fatalf("send: %v %+v", err, e)
	}
	t.Logf("send result: %+v", sendResp)

	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for inbox.message after self-heal")
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
			if env.Text != "self-heal works" {
				t.Fatalf("content mismatch: %v", env.Text)
			}
			return
		}
	}
}

// --- helpers used only by selfheal test ---

func seedState(t *testing.T, dir, name, relayURL string) *identity.Keypair {
	t.Helper()
	k, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.SaveKey(filepath.Join(dir, "key"), k); err != nil {
		t.Fatal(err)
	}
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
	if err := config.Save(filepath.Join(dir, "config.toml"), config.Defaults()); err != nil {
		t.Fatal(err)
	}
	return k
}

func startRelayDir(t *testing.T, dir, addr, ownerHex string) *relayd.Server {
	t.Helper()
	db, err := store.Open(filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	wl := relayd.NewWhitelistSource(db, 100*time.Millisecond)
	ctx := context.Background()
	if err := wl.RefreshNow(ctx); err != nil {
		t.Fatal(err)
	}
	go wl.Run(ctx)
	rsrv, err := relayd.New(relayd.Config{
		Mode:      relayd.ModePaired,
		Listen:    addr,
		OwnerHex:  ownerHex,
		Whitelist: wl,
	})
	if err != nil {
		t.Fatal(err)
	}
	go rsrv.ListenAndServe()
	time.Sleep(150 * time.Millisecond)
	return rsrv
}

func dialIPCDir(t *testing.T, dir string) *ipc.Client {
	t.Helper()
	cfg, _ := config.Load(filepath.Join(dir, "config.toml"))
	c, err := ipc.Dial(filepath.Join(dir, cfg.Daemon.Socket))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func addContactDirect(t *testing.T, dir, npub, relayURL string) {
	t.Helper()
	c := dialIPCDir(t, dir)
	var resp map[string]bool
	e, err := c.Call("contact.add", map[string]any{
		"npub":   npub,
		"relays": []string{relayURL},
		"label":  "peer",
		"tier":   string(contacts.TierFriend),
	}, &resp)
	if err != nil || e != nil {
		t.Fatalf("contact.add: %v %+v", err, e)
	}
}
