package nostr

import (
	"context"
	"errors"
	"fmt"
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

// Pool wraps a set of relay connections and provides concurrent publish /
// subscribe helpers.
type Pool struct {
	mu      sync.Mutex
	relays  map[string]*gnostr.Relay
	dialer  func(ctx context.Context, url string) (*gnostr.Relay, error)
	timeout time.Duration
}

// NewPool returns an empty Pool with sane defaults.
func NewPool() *Pool {
	return &Pool{
		relays:  make(map[string]*gnostr.Relay),
		dialer:  defaultDial,
		timeout: 5 * time.Second,
	}
}

func defaultDial(ctx context.Context, url string) (*gnostr.Relay, error) {
	return gnostr.RelayConnect(ctx, url)
}

// Connect returns an existing relay connection or dials a new one. Connections
// are cached by URL; concurrent callers for the same URL may each dial once
// but the winner's connection is stored.
func (p *Pool) Connect(ctx context.Context, url string) (*gnostr.Relay, error) {
	p.mu.Lock()
	if r, ok := p.relays[url]; ok {
		p.mu.Unlock()
		return r, nil
	}
	p.mu.Unlock()

	dialCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	r, err := p.dialer(dialCtx, url)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}

	p.mu.Lock()
	p.relays[url] = r
	p.mu.Unlock()

	return r, nil
}

// Close disconnects all cached relays and clears the pool.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.relays {
		_ = r.Close()
	}
	p.relays = make(map[string]*gnostr.Relay)
}

// Publish sends ev to each URL concurrently and returns one PublishResult per
// URL. A per-relay timeout of p.timeout is applied.
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
			if err = r.Publish(pubCtx, *ev); err != nil {
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
func (p *Pool) Subscribe(ctx context.Context, urls []string, filter gnostr.Filter) (<-chan *gnostr.Event, error) {
	out := make(chan *gnostr.Event, 256)
	var wg sync.WaitGroup
	var anyOK bool
	for _, u := range urls {
		r, err := p.Connect(ctx, u)
		if err != nil {
			continue
		}
		sub, err := r.Subscribe(ctx, gnostr.Filters{filter})
		if err != nil {
			continue
		}
		anyOK = true
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case ev, ok := <-sub.Events:
					if !ok {
						return
					}
					select {
					case out <- ev:
					case <-ctx.Done():
						return
					}
				}
			}
		}(u)
	}
	if !anyOK {
		close(out)
		return out, errors.New("no relays subscribed")
	}
	go func() { wg.Wait(); close(out) }()
	return out, nil
}
