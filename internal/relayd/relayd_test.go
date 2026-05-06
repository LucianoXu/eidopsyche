package relayd

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	gnostr "github.com/nbd-wtf/go-nostr"

	"github.com/yingtexu/eidopsyche/internal/store"
)

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestPairedModeRejection(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner := gnostr.GeneratePrivateKey()
	ownerPK, _ := gnostr.GetPublicKey(owner)
	if err := db.SetMeta(ctx, "owner_pubkey", ownerPK); err != nil {
		t.Fatal(err)
	}
	wl := NewWhitelistSource(db, 100*time.Millisecond)
	if err := wl.RefreshNow(ctx); err != nil {
		t.Fatal(err)
	}

	addr := freePort(t)
	srv, err := New(Config{
		Mode:      ModePaired,
		Listen:    addr,
		OwnerHex:  ownerPK,
		Whitelist: wl,
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.ListenAndServe()
	defer srv.Shutdown(context.Background())
	time.Sleep(200 * time.Millisecond)

	relay, err := gnostr.RelayConnect(ctx, "ws://"+addr)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	// A kind:1 event addressed to nobody should be rejected.
	stranger := gnostr.GeneratePrivateKey()
	strangerPK, _ := gnostr.GetPublicKey(stranger)
	ev := gnostr.Event{
		Kind:      1,
		PubKey:    strangerPK,
		CreatedAt: gnostr.Now(),
		Content:   "hi",
	}
	if err := ev.Sign(stranger); err != nil {
		t.Fatal(err)
	}
	err = relay.Publish(ctx, ev)
	if err == nil {
		t.Fatal("expected publish rejection for non-1059 event")
	}
}
