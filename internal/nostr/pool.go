package nostr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	gnostr "github.com/nbd-wtf/go-nostr"
)

// PublishResult reports the outcome of a single relay publish attempt.
type PublishResult struct {
	Relay  string
	OK     bool
	Reason string
}

// Signer signs Nostr events with a long-term identity key. Used by Pool to
// respond to NIP-42 AUTH challenges. The implementation must populate
// ev.PubKey, ev.ID, and ev.Sig.
type Signer interface {
	Sign(ev *gnostr.Event) error
	PublicHex() string
}

// Pool wraps a set of relay connections and provides concurrent publish /
// subscribe helpers.
type Pool struct {
	mu          sync.Mutex
	relays      map[string]*gnostr.Relay
	dialer      func(ctx context.Context, url string) (*gnostr.Relay, error)
	alive       func(*gnostr.Relay) bool
	closer      func(*gnostr.Relay) error                                // test seam; defaults to (*gnostr.Relay).Close
	publisher   func(*gnostr.Relay, context.Context, gnostr.Event) error // test seam; defaults to (*gnostr.Relay).Publish
	timeout     time.Duration
	signer      Signer                           // optional; when set, Subscribe handles NIP-42 AUTH transparently
	stateHookMu sync.RWMutex                     // protects stateHook against concurrent SetStateHook calls
	stateHook   func(url, state, lastErr string) // optional; emits per-URL state transitions from Subscribe pumps
	eventHook   func(url string)                 // optional; emits per-event hit from Subscribe pumps for LastEventAt tracking
}

// NewPool returns an empty Pool with sane defaults and no signer (AUTH
// challenges will surface as auth-required CLOSED reasons that the caller
// must handle).
func NewPool() *Pool {
	return &Pool{
		relays:    make(map[string]*gnostr.Relay),
		dialer:    defaultDial,
		alive:     defaultAlive,
		closer:    func(r *gnostr.Relay) error { return r.Close() },
		publisher: func(r *gnostr.Relay, ctx context.Context, ev gnostr.Event) error { return r.Publish(ctx, ev) },
		timeout:   5 * time.Second,
	}
}

// NewPoolWithSigner returns a Pool that responds to NIP-42 AUTH challenges
// transparently using the given signer. Subscribe will detect
// "auth-required" CLOSED reasons, AUTH on the same connection, and
// re-subscribe once.
func NewPoolWithSigner(signer Signer) *Pool {
	p := NewPool()
	p.signer = signer
	return p
}

// SetStateHook registers a callback invoked from inside Subscribe's per-URL
// pumps on state transitions. State is one of: "connecting", "connected",
// "error", "auth-failed". Empty lastErr unless state ∈ {"error", "auth-failed"}.
// The hook may be called from multiple goroutines; the callback must be safe
// for concurrent use. Pass nil to remove.
func (p *Pool) SetStateHook(h func(url, state, lastErr string)) {
	p.stateHookMu.Lock()
	p.stateHook = h
	p.stateHookMu.Unlock()
}

// SetEventHook registers a callback invoked once per event delivered through
// Subscribe, parameterised by the source URL. Used by callers that want to
// track LastEventAt per relay. Same concurrency contract as SetStateHook.
func (p *Pool) SetEventHook(h func(url string)) {
	p.stateHookMu.Lock()
	p.eventHook = h
	p.stateHookMu.Unlock()
}

func (p *Pool) notifyState(url, state, lastErr string) {
	p.stateHookMu.RLock()
	h := p.stateHook
	p.stateHookMu.RUnlock()
	if h != nil {
		h(url, state, lastErr)
	}
}

func (p *Pool) notifyEvent(url string) {
	p.stateHookMu.RLock()
	h := p.eventHook
	p.stateHookMu.RUnlock()
	if h != nil {
		h(url)
	}
}

func defaultDial(ctx context.Context, url string) (*gnostr.Relay, error) {
	return gnostr.RelayConnect(ctx, url)
}

func defaultAlive(r *gnostr.Relay) bool { return r != nil && r.IsConnected() }

// Connect returns an existing relay connection or dials a new one. Connections
// are cached by URL; concurrent callers for the same URL may each dial once
// but only one winner is stored — losers are closed before return.
//
// A cached entry whose underlying websocket has gone away (e.g., the peer
// relay restarted) is evicted and re-dialed — without this, every consumer
// would silently fail subscribe / publish until they restarted the daemon.
func (p *Pool) Connect(ctx context.Context, url string) (*gnostr.Relay, error) {
	p.mu.Lock()
	if r, ok := p.relays[url]; ok {
		if p.alive(r) {
			p.mu.Unlock()
			return r, nil
		}
		delete(p.relays, url)
		p.mu.Unlock()
		_ = p.closer(r)
	} else {
		p.mu.Unlock()
	}

	dialCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	r, err := p.dialer(dialCtx, url)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}

	p.mu.Lock()
	if existing, ok := p.relays[url]; ok {
		if p.alive(existing) {
			// Another concurrent caller won the race and already stored a
			// live relay for this URL while we were dialing. Drop our
			// redundant connection and return the canonical entry.
			p.mu.Unlock()
			_ = p.closer(r)
			return existing, nil
		}
		// The racing winner's connection died between store and re-check.
		// Returning it would hand the caller a dead relay and force the
		// next Connect to re-dial anyway; instead replace it with our
		// fresh dial here, mirroring the stale-cache eviction at the top
		// of Connect.
		p.relays[url] = r
		p.mu.Unlock()
		_ = p.closer(existing)
		return r, nil
	}
	p.relays[url] = r
	p.mu.Unlock()

	return r, nil
}

// Close disconnects all cached relays and clears the pool.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.relays {
		_ = p.closer(r)
	}
	p.relays = make(map[string]*gnostr.Relay)
}

// Publish sends ev to each URL concurrently and returns one PublishResult per
// URL. A per-relay timeout of p.timeout is applied.
//
// Publish does NOT side-channel publish-health into any hook: callers
// already hold the results and know whether this is a user-visible
// publish or a best-effort internal one (self-copy, ack rebroadcast),
// and only the former should feed RelayHealth — otherwise a self-copy
// succeeding on a relay that rejected the recipient publish would mask
// the real failure. See internal/daemon.(*Daemon).recordPublishHealth.
func (p *Pool) Publish(ctx context.Context, urls []string, ev *gnostr.Event) []PublishResult {
	out := make([]PublishResult, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		i, u := i, u
		wg.Add(1)
		go func() {
			defer wg.Done()
			pubCtx, cancel := context.WithTimeout(ctx, p.timeout)
			defer cancel()

			r, err := p.Connect(pubCtx, u)
			if err != nil {
				out[i] = PublishResult{Relay: u, OK: false, Reason: err.Error()}
				return
			}
			if err = p.publisher(r, pubCtx, *ev); err != nil {
				out[i] = PublishResult{Relay: u, OK: false, Reason: err.Error()}
				return
			}
			out[i] = PublishResult{Relay: u, OK: true}
		}()
	}
	wg.Wait()
	return out
}

// Subscribe opens a subscription on each URL concurrently and fans all
// matching events into a single output channel. The channel is buffered;
// slow consumers may drop events if the buffer fills. Returns an error if no
// relay could be connected. The channel is closed when ctx is canceled or
// all per-URL pump goroutines exit.
//
// When the Pool has a Signer (NewPoolWithSigner) and a relay closes a
// subscription with an "auth-required" reason, the pump performs NIP-42
// AUTH on that connection and re-subscribes once. Failures or repeated
// AUTH-required closes terminate the per-URL pump.
func (p *Pool) Subscribe(ctx context.Context, urls []string, filter gnostr.Filter) (<-chan *gnostr.Event, error) {
	out := make(chan *gnostr.Event, 256)
	var wg sync.WaitGroup
	var anyOK bool
	for _, u := range urls {
		p.notifyState(u, "connecting", "")
		r, err := p.Connect(ctx, u)
		if err != nil {
			p.notifyState(u, "error", err.Error())
			continue
		}
		sub, err := r.Subscribe(ctx, gnostr.Filters{filter})
		if err != nil {
			p.notifyState(u, "error", err.Error())
			continue
		}
		p.notifyState(u, "connected", "")
		anyOK = true
		wg.Add(1)
		go p.pumpSubscription(ctx, &wg, u, r, sub, filter, out)
	}
	if !anyOK {
		close(out)
		return out, errors.New("no relays subscribed")
	}
	go func() { wg.Wait(); close(out) }()
	return out, nil
}

// pumpSubscription forwards events from sub to out, handling NIP-42 AUTH
// challenges transparently when p.signer is set. On "auth-required" close,
// it performs r.Auth(...) and re-subscribes once; subsequent close (for
// any reason) terminates the goroutine.
func (p *Pool) pumpSubscription(
	ctx context.Context,
	wg *sync.WaitGroup,
	url string,
	r *gnostr.Relay,
	sub *gnostr.Subscription,
	filter gnostr.Filter,
	out chan<- *gnostr.Event,
) {
	defer wg.Done()
	authed := false
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub.Events:
			if !ok {
				return
			}
			p.notifyEvent(url)
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		case reason, ok := <-sub.ClosedReason:
			if !ok {
				return
			}
			if !authed && p.signer != nil && strings.HasPrefix(reason, "auth-required") {
				authCtx, cancel := context.WithTimeout(ctx, p.timeout)
				err := r.Auth(authCtx, p.signFuncFor(r))
				cancel()
				if err != nil {
					p.notifyState(url, "auth-failed", err.Error())
					return
				}
				authed = true
				newSub, err := r.Subscribe(ctx, gnostr.Filters{filter})
				if err != nil {
					p.notifyState(url, "error", err.Error())
					return
				}
				p.notifyState(url, "connected", "")
				sub = newSub
				continue
			}
			if strings.HasPrefix(reason, "auth-") {
				p.notifyState(url, "auth-failed", reason)
			} else {
				p.notifyState(url, "error", reason)
			}
			return
		}
	}
}

// signFuncFor returns a sign callback bound to a specific relay connection.
// The callback validates that the AUTH event includes a ["relay", url] tag
// matching the connection URL — defends against a hypothetical malicious
// go-nostr build that might construct the event with the wrong URL or
// without the relay tag — then signs with the Pool's signer.
//
// NIP-42 mandates the relay tag; missing tag is treated as a defect that
// could just as plausibly be a deliberate evasion as a bug, so we refuse
// to sign rather than fall back to "URL must just be the connection one".
func (p *Pool) signFuncFor(r *gnostr.Relay) func(ev *gnostr.Event) error {
	return func(ev *gnostr.Event) error {
		if p.signer == nil {
			return errors.New("AUTH challenge received but Pool has no signer")
		}
		seenRelayTag := false
		for _, t := range ev.Tags {
			if len(t) >= 2 && t[0] == "relay" {
				seenRelayTag = true
				if t[1] != r.URL {
					return fmt.Errorf("AUTH event relay tag %q does not match connection URL %q", t[1], r.URL)
				}
				break
			}
		}
		if !seenRelayTag {
			return fmt.Errorf("AUTH event missing required [\"relay\", %q] tag", r.URL)
		}
		ev.PubKey = p.signer.PublicHex()
		return p.signer.Sign(ev)
	}
}
