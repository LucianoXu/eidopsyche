package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// addTestContact inserts a friend-tier contact via the daemon's Repo so
// the contact.* tests have a real row to operate on. Returns the hex
// pubkey of the inserted contact.
func addTestContact(t *testing.T, d *Daemon, label string) string {
	t.Helper()
	k, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Repo.Add(context.Background(), contacts.Contact{
		Pubkey: k.PublicHex,
		Label:  label,
		Tier:   contacts.TierFriend,
	}); err != nil {
		t.Fatal(err)
	}
	return k.PublicHex
}

func TestContactGet_ByHex(t *testing.T) {
	d := newTestDaemon(t)
	pk := addTestContact(t, d, "alice")
	var c contacts.Contact
	if err := d.Call(context.Background(), "contact.get",
		map[string]string{"target": pk}, &c); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if c.Pubkey != pk {
		t.Errorf("Pubkey=%q, want %q", c.Pubkey, pk)
	}
	if c.Label != "alice" {
		t.Errorf("Label=%q, want alice", c.Label)
	}
}

func TestContactGet_ByLabel(t *testing.T) {
	d := newTestDaemon(t)
	pk := addTestContact(t, d, "bob")
	var c contacts.Contact
	if err := d.Call(context.Background(), "contact.get",
		map[string]string{"target": "bob"}, &c); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if c.Pubkey != pk {
		t.Errorf("Pubkey=%q, want %q", c.Pubkey, pk)
	}
}

func TestContactGet_NotFound(t *testing.T) {
	d := newTestDaemon(t)
	err := d.Call(context.Background(), "contact.get",
		map[string]string{"target": "0000000000000000000000000000000000000000000000000000000000000000"}, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) || ipcErr.Code != ipc.ErrContactNotFound {
		t.Fatalf("err=%v, want CONTACT_NOT_FOUND", err)
	}
}

func TestContactSetTier_Persists(t *testing.T) {
	d := newTestDaemon(t)
	pk := addTestContact(t, d, "alice")
	if err := d.Call(context.Background(), "contact.set-tier",
		ContactSetTierParams{Target: "alice", Tier: "master"}, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
	got, err := d.Repo.Get(context.Background(), pk)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tier != contacts.TierMaster {
		t.Errorf("Tier=%q, want master", got.Tier)
	}
}

func TestContactSetTier_RejectsBadTier(t *testing.T) {
	d := newTestDaemon(t)
	addTestContact(t, d, "alice")
	err := d.Call(context.Background(), "contact.set-tier",
		ContactSetTierParams{Target: "alice", Tier: "stranger"}, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) || ipcErr.Code != ipc.ErrInvalidParams {
		t.Fatalf("err=%v, want INVALID_PARAMS", err)
	}
}

func TestContactSetTier_NotFound(t *testing.T) {
	d := newTestDaemon(t)
	err := d.Call(context.Background(), "contact.set-tier",
		ContactSetTierParams{Target: "0000000000000000000000000000000000000000000000000000000000000001", Tier: "friend"}, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) || ipcErr.Code != ipc.ErrContactNotFound {
		t.Fatalf("err=%v, want CONTACT_NOT_FOUND", err)
	}
}
