package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

func TestContactAddFromCard_FreshContact(t *testing.T) {
	d := newTestDaemon(t)
	uri, pk := makeTestCard(t, "alice", "wss://relay.example/")

	var c contacts.Contact
	if err := d.Call(context.Background(), "contact.add-from-card",
		ContactAddFromCardParams{URI: uri}, &c); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if c.Pubkey != pk {
		t.Errorf("Pubkey=%q, want %q", c.Pubkey, pk)
	}
	if c.Label != "alice" {
		t.Errorf("Label=%q, want alice", c.Label)
	}
	if c.Tier != contacts.TierFriend {
		t.Errorf("Tier=%q, want friend", c.Tier)
	}
	if len(c.Relays) != 1 || c.Relays[0] != "wss://relay.example/" {
		t.Errorf("Relays=%v, want [wss://relay.example/]", c.Relays)
	}
}

func TestContactAddFromCard_LabelOverride(t *testing.T) {
	d := newTestDaemon(t)
	uri, _ := makeTestCard(t, "alice", "wss://relay.example/")

	var c contacts.Contact
	if err := d.Call(context.Background(), "contact.add-from-card",
		ContactAddFromCardParams{URI: uri, LabelOverride: "alyce"}, &c); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if c.Label != "alyce" {
		t.Errorf("Label=%q, want override alyce", c.Label)
	}
}

func TestContactAddFromCard_UpsertPreservesTierAddsRelay(t *testing.T) {
	d := newTestDaemon(t)
	uri, pk := makeTestCard(t, "alice", "wss://relay.example/")

	// First insert directly at a non-default tier so we can verify
	// upsert leaves the tier alone.
	if err := d.Repo.Add(context.Background(), contacts.Contact{
		Pubkey: pk,
		Label:  "alice-original",
		Tier:   contacts.TierMaster,
		Relays: []string{"wss://other.example/"},
	}); err != nil {
		t.Fatal(err)
	}

	var refreshed contacts.Contact
	if err := d.Call(context.Background(), "contact.add-from-card",
		ContactAddFromCardParams{URI: uri, LabelOverride: "alice-renamed"}, &refreshed); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if refreshed.Label != "alice-renamed" {
		t.Errorf("Label=%q, want alice-renamed", refreshed.Label)
	}
	if refreshed.Tier != contacts.TierMaster {
		t.Errorf("Tier=%q, want master (preserved on upsert)", refreshed.Tier)
	}
	hasNew, hasOld := false, false
	for _, r := range refreshed.Relays {
		if r == "wss://relay.example/" {
			hasNew = true
		}
		if r == "wss://other.example/" {
			hasOld = true
		}
	}
	if !hasNew || !hasOld {
		t.Errorf("Relays=%v, want both old and new", refreshed.Relays)
	}
}

func TestContactAddFromCard_GarbageURI_RejectedAsCardInvalid(t *testing.T) {
	d := newTestDaemon(t)
	err := d.Call(context.Background(), "contact.add-from-card",
		ContactAddFromCardParams{URI: "not-a-card"}, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) || ipcErr.Code != ipc.ErrCardInvalid {
		t.Fatalf("err=%v, want CARD_INVALID", err)
	}
}

func TestContactAddFromCard_NoLabelAnywhereRejected(t *testing.T) {
	d := newTestDaemon(t)
	uri, _ := makeTestCard(t, "", "wss://relay.example/")
	err := d.Call(context.Background(), "contact.add-from-card",
		ContactAddFromCardParams{URI: uri}, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) || ipcErr.Code != ipc.ErrInvalidParams {
		t.Fatalf("err=%v, want INVALID_PARAMS", err)
	}
}
