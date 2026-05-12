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

// TestRelayHealth_SetPublishDoesNotMaskFailureFromSecondCall: codex review
// on the PR flagged a real risk — sendMessage publishes twice to the same
// URL (recipient wrap, then best-effort self-copy). If both calls fed
// setPublish, a self-copy success would overwrite a recipient failure on
// the same relay and operators/mind-forms would see `publish ok` while
// the actual user-visible send had failed. The structural fix lives in
// internal/daemon.(*Daemon).recordPublishHealth (only the primary publish
// feeds it); this store-level test pins the secondary lemma: setPublish
// itself does NOT have "remember the worst outcome" magic — last writer
// wins. The test exists to make the trade-off explicit so a future
// refactor can't accidentally regress the recordPublishHealth call sites
// without breaking a test.
// TestRelayHealth_SetPublishOnFreshURLInitializesState: a publish to a URL
// the subscription pump has never touched (e.g. a configured fallback or
// invite-issuer relay) creates a new RelayHealth entry. The State field
// must default to "pending" — the dashboard's `state-pill is-{{.State}}`
// rule would otherwise render the invalid class `is-` for these
// publish-only entries.
func TestRelayHealth_SetPublishOnFreshURLInitializesState(t *testing.T) {
	s := newRelayHealthStore()
	s.setPublish("ws://fallback", false, "blocked")

	for _, h := range s.snapshot() {
		if h.URL != "ws://fallback" {
			continue
		}
		if h.State != "pending" {
			t.Errorf("fresh publish-only entry State = %q, want %q", h.State, "pending")
		}
	}
}

func TestRelayHealth_SetPublishDoesNotMaskFailureFromSecondCall(t *testing.T) {
	s := newRelayHealthStore()
	s.setPublish("ws://a", false, "blocked: spam")
	s.setPublish("ws://a", true, "")

	for _, h := range s.snapshot() {
		if h.URL != "ws://a" {
			continue
		}
		if !h.LastPublishOk || h.LastPublishErr != "" {
			t.Fatalf("setPublish should be last-writer-wins; got Ok=%v Err=%q",
				h.LastPublishOk, h.LastPublishErr)
		}
	}
}

func TestRelayHealth_SetPublishTracksOkErrAt(t *testing.T) {
	s := newRelayHealthStore()
	s.setRole("ws://a", "home")
	s.setRole("ws://b", "home")

	s.setPublish("ws://a", true, "")
	s.setPublish("ws://b", false, "msg: blocked: spam")

	got := map[string]RelayHealth{}
	for _, h := range s.snapshot() {
		got[h.URL] = h
	}
	a, b := got["ws://a"], got["ws://b"]

	if !a.LastPublishOk || a.LastPublishErr != "" || a.LastPublishAt == 0 {
		t.Errorf("ws://a publish: ok=%v err=%q at=%d — want ok=true err=\"\" at>0",
			a.LastPublishOk, a.LastPublishErr, a.LastPublishAt)
	}
	if b.LastPublishOk || b.LastPublishErr == "" || b.LastPublishAt == 0 {
		t.Errorf("ws://b publish: ok=%v err=%q at=%d — want ok=false err!=\"\" at>0",
			b.LastPublishOk, b.LastPublishErr, b.LastPublishAt)
	}

	// Subscribe-state must be untouched by setPublish — these are
	// independent signals; that orthogonality is the whole point of
	// the fix.
	if a.State != "pending" || b.State != "pending" {
		t.Errorf("setPublish leaked into State: a=%q b=%q", a.State, b.State)
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
