package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/contacts"
	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
	"github.com/LucianoXu/eidopsyche/internal/invitedb"
	"github.com/LucianoXu/eidopsyche/internal/nostr"
	"github.com/LucianoXu/eidopsyche/internal/store"
)

// newTestDaemon creates a Daemon backed by a temporary state dir with a fresh
// keypair and migrated DB. Used by command and dispatch tests.
func newTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	dir := t.TempDir()
	k, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Daemon{
		StateDir:    dir,
		Key:         k,
		DB:          db,
		Repo:        contacts.New(db),
		Invites:     invitedb.New(db),
		Box:         inbox.New(dir),
		Pool:        nostr.NewPool(),
		Log:         slog.New(slog.NewJSONHandler(testWriter{t}, nil)),
		startedAt:   time.Now(),
		kick:        make(chan struct{}, 1),
		dedupe:      map[string]struct{}{},
		selfWrapIDs: map[string]struct{}{},
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Log(string(p)); return len(p), nil }
