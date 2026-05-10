package nostr

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

// TestConnect_ConcurrentDialDiscardsLoser: N concurrent Connect calls for the
// same URL all enter the dialer (since each finds the cache empty under lock,
// releases, and dials in parallel). Only one of the dialed relays ends up in
// the pool; all callers must observe the same winner, and every loser must
// be closed exactly once so the underlying websocket isn't leaked.
//
// Regression test for the "last-writer-wins on p.relays[url]" leak flagged
// by codex review on 2026-05-10.
func TestConnect_ConcurrentDialDiscardsLoser(t *testing.T) {
	p := NewPool()
	const N = 8

	var dialed int32
	started := make(chan struct{}, N)
	release := make(chan struct{})

	p.dialer = func(ctx context.Context, url string) (*gnostr.Relay, error) {
		atomic.AddInt32(&dialed, 1)
		started <- struct{}{}
		<-release
		return &gnostr.Relay{URL: url}, nil
	}
	p.alive = func(r *gnostr.Relay) bool { return true }

	var closedMu sync.Mutex
	closedRelays := make(map[*gnostr.Relay]int)
	p.closer = func(r *gnostr.Relay) error {
		closedMu.Lock()
		closedRelays[r]++
		closedMu.Unlock()
		return nil
	}

	results := make([]*gnostr.Relay, N)
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			r, err := p.Connect(context.Background(), "ws://race")
			if err != nil {
				t.Errorf("connect %d: %v", i, err)
				return
			}
			results[i] = r
		}()
	}

	// Wait for all N goroutines to enter the dialer, then release them
	// together so they all race on the post-dial store.
	for i := 0; i < N; i++ {
		<-started
	}
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&dialed); got != N {
		t.Fatalf("expected %d concurrent dials, got %d (test barrier failed)", N, got)
	}

	winner := results[0]
	if winner == nil {
		t.Fatal("winner is nil")
	}
	for i, r := range results {
		if r != winner {
			t.Errorf("caller %d got %p, want winner %p", i, r, winner)
		}
	}

	p.mu.Lock()
	n := len(p.relays)
	stored := p.relays["ws://race"]
	p.mu.Unlock()
	if n != 1 {
		t.Errorf("pool has %d entries after race, want 1", n)
	}
	if stored != winner {
		t.Errorf("stored relay %p does not match winner %p", stored, winner)
	}

	closedMu.Lock()
	defer closedMu.Unlock()
	if got := len(closedRelays); got != N-1 {
		t.Errorf("closer called on %d distinct relays, want %d (one per loser)", got, N-1)
	}
	for r, count := range closedRelays {
		if r == winner {
			t.Errorf("winner %p was closed; closer must not be called on it", r)
		}
		if count != 1 {
			t.Errorf("loser %p closed %d times, want 1", r, count)
		}
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

// fakeSigner is a deterministic test signer that records calls without
// performing real Schnorr signing. AUTH-event-shape tests don't need real
// signatures; integration tests cover the on-the-wire path.
type fakeSigner struct {
	pub    string
	signed []*gnostr.Event
}

func (s *fakeSigner) Sign(ev *gnostr.Event) error {
	ev.PubKey = s.pub
	ev.ID = "fake-" + ev.PubKey
	ev.Sig = "fake-sig"
	s.signed = append(s.signed, ev)
	return nil
}
func (s *fakeSigner) PublicHex() string { return s.pub }

func TestPool_SignFunc_RefusesURLMismatch(t *testing.T) {
	signer := &fakeSigner{pub: "abcd"}
	p := NewPoolWithSigner(signer)
	r := &gnostr.Relay{URL: "wss://us.example"}
	signFn := p.signFuncFor(r)

	ev := &gnostr.Event{
		Kind: 22242,
		Tags: gnostr.Tags{
			gnostr.Tag{"relay", "wss://attacker.example"},
			gnostr.Tag{"challenge", "x"},
		},
	}
	err := signFn(ev)
	if err == nil {
		t.Fatal("expected URL-mismatch refusal")
	}
	if len(signer.signed) != 0 {
		t.Errorf("signer was called despite URL mismatch")
	}
}

func TestPool_SignFunc_AcceptsMatchingURL(t *testing.T) {
	signer := &fakeSigner{pub: "abcd"}
	p := NewPoolWithSigner(signer)
	r := &gnostr.Relay{URL: "wss://us.example"}
	signFn := p.signFuncFor(r)

	ev := &gnostr.Event{
		Kind: 22242,
		Tags: gnostr.Tags{
			gnostr.Tag{"relay", "wss://us.example"},
			gnostr.Tag{"challenge", "x"},
		},
	}
	if err := signFn(ev); err != nil {
		t.Fatalf("sign failed: %v", err)
	}
	if len(signer.signed) != 1 {
		t.Errorf("signer called %d times, want 1", len(signer.signed))
	}
	if ev.PubKey != "abcd" {
		t.Errorf("PubKey = %q, want abcd", ev.PubKey)
	}
}

func TestPool_SignFunc_RefusesMissingRelayTag(t *testing.T) {
	signer := &fakeSigner{pub: "abcd"}
	p := NewPoolWithSigner(signer)
	r := &gnostr.Relay{URL: "wss://us.example"}
	signFn := p.signFuncFor(r)

	// Event has a challenge tag but no relay tag — refuse.
	ev := &gnostr.Event{
		Kind: 22242,
		Tags: gnostr.Tags{gnostr.Tag{"challenge", "x"}},
	}
	if err := signFn(ev); err == nil {
		t.Fatal("expected refusal when relay tag is absent")
	}
	if len(signer.signed) != 0 {
		t.Errorf("signer was called despite missing relay tag")
	}
}

func TestPool_SignFunc_NoSignerErrors(t *testing.T) {
	p := NewPool() // no signer
	r := &gnostr.Relay{URL: "wss://us"}
	signFn := p.signFuncFor(r)
	err := signFn(&gnostr.Event{Tags: gnostr.Tags{gnostr.Tag{"relay", "wss://us"}, gnostr.Tag{"challenge", "x"}}})
	if err == nil {
		t.Fatal("expected error when Pool has no signer")
	}
}
