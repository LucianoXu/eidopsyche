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
