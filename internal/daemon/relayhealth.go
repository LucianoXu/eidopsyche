package daemon

import (
	"sync"
	"time"
)

// RelayHealth captures per-URL relay state. Daemon owns the source of
// truth; consumers read snapshots via state.get relays or the dashboard
// SSE event hub.
//
// Two independent signals share one record:
//   - Subscription health — State, LastError, LastEventAt — driven by the
//     Subscribe pump.
//   - Publish health — LastPublishOk, LastPublishErr, LastPublishAt —
//     driven by Publish results.
//
// They are independent because a relay can hold a sub connection alive
// while rejecting kind-14 wraps (spam filter, content rules, per-pubkey
// rate limits — NIP-20 OK false). "Subscribe is happy" does not imply
// "Publish will be accepted"; only the latter answers "did my message
// actually leave the host".
type RelayHealth struct {
	URL         string `json:"url"`
	Role        string `json:"role"`  // "home" | "fallback" | "contact" | "extra"
	State       string `json:"state"` // "pending" | "connecting" | "connected" | "error" | "auth-failed"
	LastError   string `json:"last_error,omitempty"`
	LastEventAt int64  `json:"last_event_at,omitempty"` // unix seconds; 0 when never

	// LastPublish* track the most recent Publish outcome for this URL,
	// orthogonal to State. LastPublishAt == 0 means "no publish attempted
	// yet"; consumers must check that before drawing inferences.
	LastPublishOk  bool   `json:"last_publish_ok,omitempty"`
	LastPublishErr string `json:"last_publish_err,omitempty"`
	LastPublishAt  int64  `json:"last_publish_at,omitempty"`

	UpdatedAt int64 `json:"updated_at"`
}

// relayHealthStore is the in-memory map of URL → state. Concurrent-safe.
type relayHealthStore struct {
	mu sync.RWMutex
	m  map[string]*RelayHealth
}

func newRelayHealthStore() *relayHealthStore {
	return &relayHealthStore{m: map[string]*RelayHealth{}}
}

// setRole records the role we believe a URL is currently filling. Called
// by runSubscriber when it computes the union of own_relays / contact /
// extra. Doesn't transition state; that's setState's job.
func (s *relayHealthStore) setRole(url, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.m[url]
	if !ok {
		h = &RelayHealth{URL: url, State: "pending"}
		s.m[url] = h
	}
	h.Role = role
	h.UpdatedAt = time.Now().Unix()
}

// setState transitions a URL to a new state with an optional last-error
// string. Returns the resulting RelayHealth so the caller can fan it out
// (e.g., to the dashboard SSE hub) without re-reading.
func (s *relayHealthStore) setState(url, state, lastErr string) RelayHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.m[url]
	if !ok {
		h = &RelayHealth{URL: url}
		s.m[url] = h
	}
	h.State = state
	h.LastError = lastErr
	h.UpdatedAt = time.Now().Unix()
	return *h
}

// markEvent records that an event was received from this URL. Used to
// drive the LastEventAt field consumers display as "active 2s ago".
func (s *relayHealthStore) markEvent(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if h, ok := s.m[url]; ok {
		h.LastEventAt = time.Now().Unix()
	}
}

// setPublish records the outcome of the most recent Publish for url. ok
// mirrors PublishResult.OK; reason carries the relay's error string when
// ok=false. Updates only the LastPublish* fields — subscription state
// (State / LastError / LastEventAt) is untouched. Returns the resulting
// RelayHealth so callers can fan it out (mirrors setState's contract).
func (s *relayHealthStore) setPublish(url string, ok bool, reason string) RelayHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, found := s.m[url]
	if !found {
		// Match setRole's initial state — "pending" is the
		// schema-defined neutral value the dashboard's
		// `state-pill is-{{.State}}` rule has CSS for. Leaving
		// State empty would render as the invalid class `is-`
		// when state.get relays exposes this publish-only entry.
		h = &RelayHealth{URL: url, State: "pending"}
		s.m[url] = h
	}
	h.LastPublishOk = ok
	if ok {
		h.LastPublishErr = ""
	} else {
		h.LastPublishErr = reason
	}
	h.LastPublishAt = time.Now().Unix()
	h.UpdatedAt = h.LastPublishAt
	return *h
}

// snapshot returns a copy of all current entries. Order is unspecified.
func (s *relayHealthStore) snapshot() []RelayHealth {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RelayHealth, 0, len(s.m))
	for _, h := range s.m {
		out = append(out, *h)
	}
	return out
}

// reset removes URLs not in the given keep set. Called by runSubscriber
// when the subscription set changes (e.g., a contact was removed) so that
// stale entries don't linger in the snapshot.
func (s *relayHealthStore) reset(keep map[string]struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for url := range s.m {
		if _, ok := keep[url]; !ok {
			delete(s.m, url)
		}
	}
}
