package invitedb

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/store"
)

func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"), false)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testInvite(id string) Invite {
	return Invite{
		ID:            id,
		CreatedAt:     time.Now(),
		ExpiresAt:     time.Now().Add(7 * 24 * time.Hour),
		MaxUses:       1,
		IssuerLabel:   "issuer",
		RedeemerLabel: "redeemer",
	}
}

func TestInsertAndGet(t *testing.T) {
	db := openDB(t)
	repo := New(db)
	ctx := context.Background()

	inv := testInvite("aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff6666aaaa1111bbbb2222")
	if err := repo.Insert(ctx, inv); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, inv.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != inv.ID {
		t.Fatalf("ID mismatch: want %s got %s", inv.ID, got.ID)
	}
	if got.Status != StatusActive {
		t.Fatalf("status want active got %s", got.Status)
	}
	if got.Uses != 0 {
		t.Fatalf("uses want 0 got %d", got.Uses)
	}
}

func TestGetNotFound(t *testing.T) {
	db := openDB(t)
	repo := New(db)
	_, err := repo.Get(context.Background(), "nonexistent")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFindByPrefix(t *testing.T) {
	db := openDB(t)
	repo := New(db)
	ctx := context.Background()

	id := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	inv := testInvite(id)
	_ = repo.Insert(ctx, inv)

	got, err := repo.FindByPrefix(ctx, "abcdef12")
	if err != nil {
		t.Fatalf("FindByPrefix: %v", err)
	}
	if got.ID != id {
		t.Fatalf("ID mismatch: %s", got.ID)
	}
}

func TestFindByPrefixAmbiguous(t *testing.T) {
	db := openDB(t)
	repo := New(db)
	ctx := context.Background()

	id1 := "aabb1111cccc2222dddd3333eeee4444ffff5555aaaa6666bbbb7777cccc8888"
	id2 := "aabb2222cccc3333dddd4444eeee5555ffff6666aaaa7777bbbb8888cccc9999"
	_ = repo.Insert(ctx, testInvite(id1))
	_ = repo.Insert(ctx, testInvite(id2))

	_, err := repo.FindByPrefix(ctx, "aabb")
	if err != ErrPrefixAmbiguous {
		t.Fatalf("expected ErrPrefixAmbiguous, got %v", err)
	}
}

func TestFindByPrefixNotFound(t *testing.T) {
	db := openDB(t)
	repo := New(db)
	_, err := repo.FindByPrefix(context.Background(), "zzzzz")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestList(t *testing.T) {
	db := openDB(t)
	repo := New(db)
	ctx := context.Background()

	id1 := "1111aaaa2222bbbb3333cccc4444dddd5555eeee6666ffff7777aaaa8888bbbb"
	id2 := "2222aaaa3333bbbb4444cccc5555dddd6666eeee7777ffff8888aaaa9999bbbb"
	_ = repo.Insert(ctx, testInvite(id1))
	_ = repo.Insert(ctx, testInvite(id2))
	_ = repo.MarkRevoked(ctx, id2)

	all, err := repo.List(ctx, "")
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}

	active, err := repo.List(ctx, "active")
	if err != nil {
		t.Fatalf("List active: %v", err)
	}
	if len(active) != 1 || active[0].ID != id1 {
		t.Fatalf("expected only id1 active, got %+v", active)
	}
}

func TestMarkExpiredAndRevoked(t *testing.T) {
	db := openDB(t)
	repo := New(db)
	ctx := context.Background()

	id1 := "eeee1111ffff2222aaaa3333bbbb4444cccc5555dddd6666eeee7777ffff8888"
	id2 := "ffff1111aaaa2222bbbb3333cccc4444dddd5555eeee6666ffff7777aaaa8888"
	_ = repo.Insert(ctx, testInvite(id1))
	_ = repo.Insert(ctx, testInvite(id2))

	if err := repo.MarkExpired(ctx, id1); err != nil {
		t.Fatalf("MarkExpired: %v", err)
	}
	if err := repo.MarkRevoked(ctx, id2); err != nil {
		t.Fatalf("MarkRevoked: %v", err)
	}

	got1, _ := repo.Get(ctx, id1)
	if got1.Status != StatusExpired {
		t.Fatalf("expected expired, got %s", got1.Status)
	}
	got2, _ := repo.Get(ctx, id2)
	if got2.Status != StatusRevoked {
		t.Fatalf("expected revoked, got %s", got2.Status)
	}
}

func TestRecordRedemption(t *testing.T) {
	db := openDB(t)
	repo := New(db)
	ctx := context.Background()

	id := "cccc1111dddd2222eeee3333ffff4444aaaa5555bbbb6666cccc7777dddd8888"
	_ = repo.Insert(ctx, testInvite(id))

	redeemer := "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	inv, err := repo.RecordRedemption(ctx, id, redeemer)
	if err != nil {
		t.Fatalf("RecordRedemption: %v", err)
	}
	if inv.Uses != 1 {
		t.Fatalf("uses want 1 got %d", inv.Uses)
	}
}

func TestRecordRedemptionIdempotent(t *testing.T) {
	db := openDB(t)
	repo := New(db)
	ctx := context.Background()

	id := "dddd1111eeee2222ffff3333aaaa4444bbbb5555cccc6666dddd7777eeee8888"
	inv := testInvite(id)
	inv.MaxUses = 5
	_ = repo.Insert(ctx, inv)

	redeemer := "0202030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
	_, _ = repo.RecordRedemption(ctx, id, redeemer)

	// Second call with same redeemer PK should return ErrAlreadyRedeemed
	_, err := repo.RecordRedemption(ctx, id, redeemer)
	if err != ErrAlreadyRedeemed {
		t.Fatalf("expected ErrAlreadyRedeemed, got %v", err)
	}

	// Uses must still be 1 (not 2)
	got, _ := repo.Get(ctx, id)
	if got.Uses != 1 {
		t.Fatalf("uses must be 1, got %d", got.Uses)
	}
}

func TestNoExpiry(t *testing.T) {
	db := openDB(t)
	repo := New(db)
	ctx := context.Background()

	id := "bbbb1111cccc2222dddd3333eeee4444ffff5555aaaa6666bbbb7777cccc8888"
	inv := Invite{
		ID:          id,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Time{}, // zero = no expiry
		MaxUses:     0,
		IssuerLabel: "me",
	}
	if err := repo.Insert(ctx, inv); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, _ := repo.Get(ctx, id)
	if !got.ExpiresAt.IsZero() {
		t.Fatalf("expected zero ExpiresAt, got %v", got.ExpiresAt)
	}
}
