//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/daemon"
	"github.com/LucianoXu/eidopsyche/internal/invitedb"
)

func TestInviteOneTimeFlow(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")

	aliceIPC := dialIPC(t, alice)
	bobIPC := dialIPC(t, bob)

	// Alice subscribes to inbox.tail to listen for contact.added.
	var ack map[string]bool
	if e, err := aliceIPC.Call("inbox.tail", nil, &ack); err != nil || e != nil {
		t.Fatalf("alice inbox.tail: %v %+v", err, e)
	}

	// 1. Alice creates a single-use invite.
	var inv daemon.InviteCreateMethodResult
	if e, err := aliceIPC.Call("invite.create", map[string]any{
		"single_use":     true,
		"issuer_label":   "Alice",
		"redeemer_label": "Bob",
	}, &inv); err != nil || e != nil {
		t.Fatalf("invite.create: %v %+v", err, e)
	}
	t.Logf("invite uri len=%d id=%s", len(inv.URI), inv.Invite.ID[:12])
	if inv.Invite.MaxUses != 1 {
		t.Fatalf("expected max_uses=1, got %d", inv.Invite.MaxUses)
	}

	// 2. Bob redeems.
	var rd map[string]any
	if e, err := bobIPC.Call("invite.redeem", map[string]string{"token": inv.URI}, &rd); err != nil || e != nil {
		t.Fatalf("invite.redeem: %v %+v", err, e)
	}
	t.Logf("redeem result: %+v", rd)

	// 3. Wait for Alice's daemon to receive the redemption and add Bob.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for contact.added on Alice")
		case ev := <-aliceIPC.Events():
			if ev.Event != "contact.added" {
				continue
			}
			var data map[string]any
			_ = json.Unmarshal(ev.Data, &data)
			t.Logf("contact.added: %+v", data)
			if data["pubkey"] != bob.daemon.Key.PublicHex {
				t.Fatalf("contact.added pubkey mismatch: got %v want %v", data["pubkey"], bob.daemon.Key.PublicHex)
			}
			if data["source"] != "invite" {
				t.Fatalf("contact.added source mismatch: got %v want invite", data["source"])
			}
			// Verify Alice's contacts now include Bob.
			ctx := context.Background()
			list, err := alice.daemon.Repo.List(ctx)
			if err != nil {
				t.Fatalf("Repo.List: %v", err)
			}
			found := false
			for _, c := range list {
				if c.Pubkey == bob.daemon.Key.PublicHex {
					found = true
					if c.Label != "Bob" {
						t.Fatalf("Bob's label should be 'Bob', got %q", c.Label)
					}
				}
			}
			if !found {
				t.Fatal("Alice's contacts list did not include Bob after invite")
			}
			// Verify Bob has Alice as contact.
			bobList, _ := bob.daemon.Repo.List(ctx)
			aliceFound := false
			for _, c := range bobList {
				if c.Pubkey == alice.daemon.Key.PublicHex {
					aliceFound = true
				}
			}
			if !aliceFound {
				t.Fatal("Bob's contacts list did not include Alice after redeem")
			}
			return
		}
	}
}

func TestInviteIdempotentReRedeem(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")

	aliceIPC := dialIPC(t, alice)
	bobIPC := dialIPC(t, bob)

	// Subscribe alice to inbox.tail.
	var ack map[string]bool
	aliceIPC.Call("inbox.tail", nil, &ack) //nolint:errcheck

	// Alice creates a multi-use invite so exhaustion won't interfere.
	var inv daemon.InviteCreateMethodResult
	if e, err := aliceIPC.Call("invite.create", map[string]any{
		"max_uses":     5,
		"issuer_label": "Alice",
	}, &inv); err != nil || e != nil {
		t.Fatalf("invite.create: %v %+v", err, e)
	}

	// Bob redeems twice.
	var rd1 map[string]any
	if e, err := bobIPC.Call("invite.redeem", map[string]string{"token": inv.URI}, &rd1); err != nil || e != nil {
		t.Fatalf("first invite.redeem: %v %+v", err, e)
	}
	// Wait for Alice to receive the first redemption.
	deadline := time.After(10 * time.Second)
outer:
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for first contact.added")
		case ev := <-aliceIPC.Events():
			if ev.Event == "contact.added" {
				break outer
			}
		}
	}

	// Second redeem — should not error (idempotent publish + graceful drop on issuer side).
	var rd2 map[string]any
	if e, err := bobIPC.Call("invite.redeem", map[string]string{"token": inv.URI}, &rd2); err != nil || e != nil {
		t.Fatalf("second invite.redeem: %v %+v", err, e)
	}

	// Wait a bit then check uses=1.
	time.Sleep(3 * time.Second)

	var invList []*invitedb.Invite
	if e, err := aliceIPC.Call("invite.list", map[string]string{"status": ""}, &invList); err != nil || e != nil {
		t.Fatalf("invite.list: %v %+v", err, e)
	}
	for _, il := range invList {
		if il.ID == inv.Invite.ID && il.Uses != 1 {
			t.Fatalf("expected uses=1 after idempotent re-redeem, got %d", il.Uses)
		}
	}
}

func TestInviteExhaustion(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")
	carol := bringUp(t, "carol")

	aliceIPC := dialIPC(t, alice)
	bobIPC := dialIPC(t, bob)
	carolIPC := dialIPC(t, carol)

	// Subscribe alice to inbox.tail.
	var ack map[string]bool
	aliceIPC.Call("inbox.tail", nil, &ack) //nolint:errcheck

	// Alice creates single-use invite.
	var inv daemon.InviteCreateMethodResult
	if e, err := aliceIPC.Call("invite.create", map[string]any{
		"single_use":   true,
		"issuer_label": "Alice",
	}, &inv); err != nil || e != nil {
		t.Fatalf("invite.create: %v %+v", err, e)
	}

	// Bob redeems first (succeeds).
	if e, err := bobIPC.Call("invite.redeem", map[string]string{"token": inv.URI}, nil); err != nil || e != nil {
		t.Fatalf("bob invite.redeem: %v %+v", err, e)
	}
	// Wait for Alice to receive bob's redemption.
	deadline := time.After(10 * time.Second)
outer:
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for first contact.added")
		case ev := <-aliceIPC.Events():
			if ev.Event == "contact.added" {
				break outer
			}
		}
	}

	// Carol redeems (exhausted on issuer side; Carol still gets Alice as contact locally).
	if e, err := carolIPC.Call("invite.redeem", map[string]string{"token": inv.URI}, nil); err != nil || e != nil {
		t.Fatalf("carol invite.redeem: %v %+v", err, e)
	}

	// Wait a bit; alice should NOT have carol as contact.
	time.Sleep(3 * time.Second)

	ctx := context.Background()
	list, _ := alice.daemon.Repo.List(ctx)
	for _, c := range list {
		if c.Pubkey == carol.daemon.Key.PublicHex {
			t.Fatal("Alice should not have Carol after exhausted invite redemption")
		}
	}

	// Carol should have Alice as contact (redeemer adds issuer locally).
	carolList, _ := carol.daemon.Repo.List(ctx)
	aliceFound := false
	for _, c := range carolList {
		if c.Pubkey == alice.daemon.Key.PublicHex {
			aliceFound = true
		}
	}
	if !aliceFound {
		t.Fatal("Carol should have Alice as contact after redeem")
	}
}

func TestInviteRevoke(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")

	aliceIPC := dialIPC(t, alice)
	bobIPC := dialIPC(t, bob)

	// Subscribe alice to inbox.tail.
	var ack map[string]bool
	aliceIPC.Call("inbox.tail", nil, &ack) //nolint:errcheck

	// Alice creates invite.
	var inv daemon.InviteCreateMethodResult
	if e, err := aliceIPC.Call("invite.create", map[string]any{
		"issuer_label": "Alice",
	}, &inv); err != nil || e != nil {
		t.Fatalf("invite.create: %v %+v", err, e)
	}

	// Alice revokes.
	if e, err := aliceIPC.Call("invite.revoke", map[string]string{"id_prefix": inv.Invite.ID[:12]}, nil); err != nil || e != nil {
		t.Fatalf("invite.revoke: %v %+v", err, e)
	}

	// Bob redeems — publish goes through (relay can't block it), Alice drops it.
	if e, err := bobIPC.Call("invite.redeem", map[string]string{"token": inv.URI}, nil); err != nil || e != nil {
		t.Fatalf("bob invite.redeem: %v %+v", err, e)
	}

	// Wait; Alice should NOT add Bob.
	time.Sleep(3 * time.Second)

	ctx := context.Background()
	list, _ := alice.daemon.Repo.List(ctx)
	for _, c := range list {
		if c.Pubkey == bob.daemon.Key.PublicHex {
			t.Fatal("Alice should not have Bob after revoked invite redemption")
		}
	}

	// Bob should have Alice as contact (redeemer adds issuer locally).
	bobList, _ := bob.daemon.Repo.List(ctx)
	aliceFound := false
	for _, c := range bobList {
		if c.Pubkey == alice.daemon.Key.PublicHex {
			aliceFound = true
		}
	}
	if !aliceFound {
		t.Fatal("Bob should have Alice as contact after redeem (even revoked)")
	}
}

func TestInviteExpiry(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	alice := bringUp(t, "alice")
	bob := bringUp(t, "bob")

	aliceIPC := dialIPC(t, alice)
	bobIPC := dialIPC(t, bob)

	// Alice creates invite that expires in 1 second.
	var inv daemon.InviteCreateMethodResult
	if e, err := aliceIPC.Call("invite.create", map[string]any{
		"expires_seconds": int64(1),
		"issuer_label":    "Alice",
	}, &inv); err != nil || e != nil {
		t.Fatalf("invite.create: %v %+v", err, e)
	}

	// Sleep 2s so invite expires.
	time.Sleep(2 * time.Second)

	// Bob tries to redeem — should get INVITE_EXPIRED.
	var ipcErr interface{ Error() string }
	var rd map[string]any
	e, err := bobIPC.Call("invite.redeem", map[string]string{"token": inv.URI}, &rd)
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	_ = ipcErr
	if e == nil {
		t.Fatalf("expected IPC error for expired invite, got success: %v", rd)
	}
	if e.Code != "INVITE_EXPIRED" {
		t.Fatalf("expected INVITE_EXPIRED, got %s: %s", e.Code, e.Message)
	}
}
