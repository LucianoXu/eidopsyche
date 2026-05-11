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

// TestStateGet_ContactByHex pins state.get contacts.<pk> as the
// replacement for the legacy contact.get IPC method. The state.get
// path is hex-only — label resolution is a CLI-level concern handled
// by iterating state.get contacts (the dashboard, the only typed
// consumer, already passes hex pubkeys).
func TestStateGet_ContactByHex(t *testing.T) {
	d := newTestDaemon(t)
	d.registerCoreStateContributors()
	pk := addTestContact(t, d, "alice")
	resp := map[string]any{}
	if err := d.Call(context.Background(), "state.get",
		map[string]string{"path": "contacts." + pk}, &resp); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if got, _ := resp["pubkey"].(string); got != pk {
		t.Errorf("pubkey=%q, want %q", got, pk)
	}
	if got, _ := resp["label"].(string); got != "alice" {
		t.Errorf("label=%q, want alice", got)
	}
}

func TestStateGet_ContactNotFound(t *testing.T) {
	d := newTestDaemon(t)
	d.registerCoreStateContributors()
	var resp map[string]any
	err := d.Call(context.Background(), "state.get",
		map[string]string{"path": "contacts.0000000000000000000000000000000000000000000000000000000000000000"}, &resp)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) || ipcErr.Code != ipc.ErrPathNotFound {
		t.Fatalf("err=%v, want PATH_NOT_FOUND", err)
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
