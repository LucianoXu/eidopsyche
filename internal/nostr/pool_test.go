package nostr

import (
	"context"
	"errors"
	"testing"

	gnostr "github.com/nbd-wtf/go-nostr"
)

func TestPoolConstruct(t *testing.T) {
	p := NewPool()
	if p == nil {
		t.Fatal("nil pool")
	}
	p.Close()
}

// TestConnect_AliveCachedReusesEntry: a cached connection that the alive
// check still considers alive must be returned without re-dialing.
func TestConnect_AliveCachedReusesEntry(t *testing.T) {
	p := NewPool()
	dials := 0
	p.dialer = func(ctx context.Context, url string) (*gnostr.Relay, error) {
		dials++
		return &gnostr.Relay{URL: url}, nil
	}
	p.alive = func(r *gnostr.Relay) bool { return true }

	first, err := p.Connect(context.Background(), "ws://test/a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Connect(context.Background(), "ws://test/a")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("alive cached entry not reused: %p vs %p", first, second)
	}
	if dials != 1 {
		t.Errorf("dialer called %d times, want 1 (cache hit)", dials)
	}
}

// TestConnect_DeadCachedEvictsAndRedials: this is the regression test for
// the silent "no relays subscribed" loop a peer fell into when its cached
// websocket was killed by a relay restart. The pool must detect the dead
// entry, evict it, and re-dial, instead of returning the stale handle.
func TestConnect_DeadCachedEvictsAndRedials(t *testing.T) {
	p := NewPool()
	dials := 0
	dialed := []*gnostr.Relay{}
	p.dialer = func(ctx context.Context, url string) (*gnostr.Relay, error) {
		dials++
		r := &gnostr.Relay{URL: url}
		dialed = append(dialed, r)
		return r, nil
	}
	// First Connect: cache miss. Second Connect: cached but reported dead.
	calls := 0
	p.alive = func(r *gnostr.Relay) bool {
		calls++
		return false
	}

	first, err := p.Connect(context.Background(), "ws://test/dead")
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Connect(context.Background(), "ws://test/dead")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Errorf("dead cached entry was not evicted: same pointer returned twice")
	}
	if dials != 2 {
		t.Errorf("dialer called %d times, want 2 (eviction + redial)", dials)
	}
	if calls < 1 {
		t.Errorf("alive check never invoked on second Connect")
	}
}

// TestConnect_DialerErrorPropagates: when the dialer fails on a fresh URL
// the error is returned and the cache stays empty so retries reach the
// dialer again.
func TestConnect_DialerErrorPropagates(t *testing.T) {
	p := NewPool()
	p.dialer = func(ctx context.Context, url string) (*gnostr.Relay, error) {
		return nil, errors.New("boom")
	}
	p.alive = func(r *gnostr.Relay) bool { return true }

	if _, err := p.Connect(context.Background(), "ws://test/fail"); err == nil {
		t.Fatal("expected dial error to surface")
	}
	p.mu.Lock()
	_, cached := p.relays["ws://test/fail"]
	p.mu.Unlock()
	if cached {
		t.Error("failed dial must not populate the cache")
	}
}
