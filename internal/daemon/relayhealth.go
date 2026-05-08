package daemon

import (
	"sync"
	"time"
)

// RelayHealth captures per-URL subscription state. Daemon owns the source
// of truth; consumers read snapshots via IPC (relays.health) or the
// dashboard SSE event hub.
type RelayHealth struct {
	URL         string `json:"url"`
	Role        string `json:"role"`             // "home" | "fallback" | "contact" | "extra"
	State       string `json:"state"`            // "pending" | "connecting" | "connected" | "error" | "auth-failed"
	LastError   string `json:"last_error,omitempty"`
	LastEventAt int64  `json:"last_event_at,omitempty"` // unix seconds; 0 when never
	UpdatedAt   int64  `json:"updated_at"`
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
