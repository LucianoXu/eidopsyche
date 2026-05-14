package contacts

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/store"
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

func TestGetByLabelUniqueAndAmbiguous(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	if err := repo.Add(ctx, Contact{Pubkey: "aa11", Label: "Bob"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Add(ctx, Contact{Pubkey: "bb22", Label: "Carol"}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByLabel(ctx, "Bob")
	if err != nil || got.Pubkey != "aa11" {
		t.Fatalf("unique: got=%+v err=%v", got, err)
	}
	if _, err := repo.GetByLabel(ctx, "Dave"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: got %v want ErrNotFound", err)
	}
	if err := repo.Add(ctx, Contact{Pubkey: "cc33", Label: "Bob"}); err != nil {
		t.Fatal(err)
	}
	_, err = repo.GetByLabel(ctx, "Bob")
	var amb *AmbiguousLabelError
	if !errors.As(err, &amb) {
		t.Fatalf("ambiguous: got %v want AmbiguousLabelError", err)
	}
	if len(amb.Pubkeys) != 2 {
		t.Fatalf("expected 2 pubkeys in error, got %v", amb.Pubkeys)
	}
}

func TestSetLabel(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	if err := repo.Add(ctx, Contact{Pubkey: "p1", Label: "old"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := repo.SetLabel(ctx, "p1", "new"); err != nil {
		t.Fatalf("SetLabel: %v", err)
	}
	got, err := repo.Get(ctx, "p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Label != "new" {
		t.Errorf("label: got %q, want %q", got.Label, "new")
	}

	if err := repo.SetLabel(ctx, "missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: got %v, want ErrNotFound", err)
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

func TestIsPending(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	const self = "selfhex"
	// add a friend
	if err := repo.Add(ctx, Contact{Pubkey: "friendhex", Label: "F", Tier: TierFriend}); err != nil {
		t.Fatalf("Add friend: %v", err)
	}
	// add a blocked contact
	if err := repo.Add(ctx, Contact{Pubkey: "blockedhex", Label: "B", Tier: TierBlocked}); err != nil {
		t.Fatalf("Add blocked: %v", err)
	}
	cases := []struct {
		name    string
		pubkey  string
		pending bool
	}{
		{"self is never pending", self, false},
		{"friend is not pending", "friendhex", false},
		{"blocked is pending", "blockedhex", true},
		{"unknown is pending", "strangerhex", true},
		{"empty is pending", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsPending(ctx, repo, tc.pubkey, self)
			if got != tc.pending {
				t.Errorf("IsPending(%q): got %v want %v", tc.pubkey, got, tc.pending)
			}
		})
	}
}
