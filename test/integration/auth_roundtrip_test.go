//go:build integration

package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/daemon"
	"github.com/LucianoXu/eidopsyche/internal/envelope"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/relayd"
	"github.com/LucianoXu/eidopsyche/internal/store"
)

// bringUpAuthRequired is a bringUp variant that runs the relay with
// Auth.Required = true. Used to model a NIP-17-compliant relay that
// gates kind:1059 reads behind NIP-42 AUTH.
func bringUpAuthRequired(t *testing.T, name string, mode relayd.Mode) *instance {
	t.Helper()
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
	cfg.Dashboard.Listen = freePort(t)
	if err := config.Save(filepath.Join(dir, "config.toml"), cfg); err != nil {
		t.Fatal(err)
	}

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
		Auth:      relayd.AuthConfig{Required: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	go rsrv.ListenAndServe()
	time.Sleep(200 * time.Millisecond)

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

// TestAuth_RoundTrip_TwoUsersOnSharedRelay verifies that:
//  1. A's relay is in public mode + AUTH-required.
//  2. A and B both connect; their daemons authenticate transparently.
//  3. A and B can mutually message via the same relay.
//  4. relays.health on each side reports 'connected' (not 'auth-failed').
func TestAuth_RoundTrip_TwoUsersOnSharedRelay(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUpAuthRequired(t, "alice", relayd.ModePublic)
	bob := bringUpDaemonOnly(t, "bob", alice.relayURL)

	addContact(t, alice, bob)
	addContact(t, bob, alice)

	// Subscriptions need a moment to attach + AUTH.
	time.Sleep(2 * time.Second)

	// Sanity: relays.health on both sides shows their home as connected
	// (no auth-failed); the AUTH dance happened transparently.
	for _, in := range []*instance{alice, bob} {
		c := dialIPC(t, in)
		var rows []daemon.RelayHealth
		if e, err := c.Call("relays.health", nil, &rows); err != nil || e != nil {
			t.Fatalf("%s relays.health: %v %+v", in.stateDir, err, e)
		}
		var found *daemon.RelayHealth
		for i := range rows {
			if rows[i].URL == alice.relayURL {
				found = &rows[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("%s did not surface %s in relays.health: rows=%+v", in.stateDir, alice.relayURL, rows)
		}
		if found.State == "auth-failed" {
			t.Fatalf("%s: AUTH dance should have succeeded but state=auth-failed err=%q", in.stateDir, found.LastError)
		}
	}

	// Bidirectional message exchange. With AUTH gating reads, neither
	// side would see the other's events without successful AUTH.
	bobIPC := dialIPC(t, bob)
	if e, err := bobIPC.Call("inbox.tail", nil, &map[string]bool{}); err != nil || e != nil {
		t.Fatalf("bob inbox.tail: %v %+v", err, e)
	}
	aliceIPC := dialIPC(t, alice)
	if e, err := aliceIPC.Call("inbox.tail", nil, &map[string]bool{}); err != nil || e != nil {
		t.Fatalf("alice inbox.tail: %v %+v", err, e)
	}

	{
		var ack map[string]any
		env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "alice→bob via AUTH'd relay"}
		if e, err := aliceIPC.Call("send", map[string]any{
			"to":       bob.daemon.Key.Npub,
			"envelope": env,
		}, &ack); err != nil || e != nil {
			t.Fatalf("alice send: %v %+v", err, e)
		}
		awaitChatText(t, bobIPC, "alice→bob via AUTH'd relay", 15*time.Second)
	}

	{
		var ack map[string]any
		env := envelope.Envelope{V: 1, Type: envelope.TypeChat, Text: "bob→alice via AUTH'd relay"}
		if e, err := bobIPC.Call("send", map[string]any{
			"to":       alice.daemon.Key.Npub,
			"envelope": env,
		}, &ack); err != nil || e != nil {
			t.Fatalf("bob send: %v %+v", err, e)
		}
		awaitChatText(t, aliceIPC, "bob→alice via AUTH'd relay", 15*time.Second)
	}

	_ = contacts.TierFriend // keep contacts import alive for future expansion
}
