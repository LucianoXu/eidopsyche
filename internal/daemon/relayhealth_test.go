package daemon

import (
	"testing"
)

func TestRelayHealth_RoleAndStateTransitions(t *testing.T) {
	s := newRelayHealthStore()
	s.setRole("ws://a", "home")
	s.setRole("ws://b", "fallback")

	snap := s.snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(snap))
	}
	for _, h := range snap {
		if h.State != "pending" {
			t.Errorf("%s initial state = %q, want pending", h.URL, h.State)
		}
	}

	s.setState("ws://a", "connecting", "")
	s.setState("ws://a", "connected", "")
	s.setState("ws://b", "error", "boom")
	s.markEvent("ws://a")

	for _, h := range s.snapshot() {
		switch h.URL {
		case "ws://a":
			if h.State != "connected" {
				t.Errorf("ws://a state = %q, want connected", h.State)
			}
			if h.Role != "home" {
				t.Errorf("ws://a role = %q, want home", h.Role)
			}
			if h.LastError != "" {
				t.Errorf("ws://a should not have LastError, got %q", h.LastError)
			}
			if h.LastEventAt == 0 {
				t.Errorf("ws://a markEvent didn't set LastEventAt")
			}
		case "ws://b":
			if h.State != "error" {
				t.Errorf("ws://b state = %q, want error", h.State)
			}
			if h.LastError != "boom" {
				t.Errorf("ws://b LastError = %q, want boom", h.LastError)
			}
		}
	}
}

func TestRelayHealth_ResetPrunesAbsent(t *testing.T) {
	s := newRelayHealthStore()
	s.setRole("ws://a", "home")
	s.setRole("ws://b", "fallback")
	s.setRole("ws://c", "extra")

	keep := map[string]struct{}{"ws://a": {}, "ws://c": {}}
	s.reset(keep)

	snap := s.snapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(snap))
	}
	urls := map[string]bool{}
	for _, h := range snap {
		urls[h.URL] = true
	}
	if !urls["ws://a"] || !urls["ws://c"] || urls["ws://b"] {
		t.Errorf("reset kept the wrong URLs: %v", urls)
	}
}
