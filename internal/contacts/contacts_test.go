package contacts

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/yingtexu/eidopsyche/internal/store"
)

func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

func TestAddGetList(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	c := Contact{Pubkey: "abc123", Label: "Bob", Relays: []string{"wss://r1", "wss://r2"}}
	if err := repo.Add(ctx, c); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := repo.Get(ctx, "abc123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Label != "Bob" {
		t.Fatalf("label %q", got.Label)
	}
	if len(got.Relays) != 2 || got.Relays[0] != "wss://r1" {
		t.Fatalf("relays %v", got.Relays)
	}
	if got.Tier != TierFriend {
		t.Fatalf("default tier %q", got.Tier)
	}
	all, err := repo.List(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("List: %v %d", err, len(all))
	}
}

func TestAddDuplicate(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	c := Contact{Pubkey: "x", Label: "X"}
	if err := repo.Add(ctx, c); err != nil {
		t.Fatalf("Add: %v", err)
	}
	err := repo.Add(ctx, c)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("got %v want ErrExists", err)
	}
}

func TestRemoveCascadesRelays(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	if err := repo.Add(ctx, Contact{Pubkey: "y", Label: "Y", Relays: []string{"wss://r"}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := repo.Remove(ctx, "y"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	rs, err := repo.AllRelaysUnion(ctx)
	if err != nil {
		t.Fatalf("AllRelaysUnion: %v", err)
	}
	if len(rs) != 0 {
		t.Fatalf("expected zero relays after cascade, got %v", rs)
	}
}
