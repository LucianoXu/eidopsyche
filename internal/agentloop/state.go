//go:build !windows

package agentloop

import (
	"sync"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

// Snapshot captures the state machine's current view at one moment.
// Returned by Snapshot(); safe to pass across goroutines.
type Snapshot struct {
	ClaudeBusy    bool
	SinceUnix     int64
	LastEventType string
	LastEventAt   int64
}

// StateMachine tracks whether claude is mid-turn (busy) or waiting on
// stdin (idle), driven by stream-json events.
//
//   - system / subtype=="init": no transition (session start);
//     LastEventType / LastEventAt still updated.
//   - assistant or user: transition to busy if currently idle.
//   - result: transition to idle.
//
// All other events update LastEventType / LastEventAt without
// transitioning busy/idle.
type StateMachine struct {
	mu            sync.Mutex
	busy          bool
	since         time.Time
	lastEventType string
	lastEventAt   time.Time
}

// NewStateMachine returns a state machine in the idle state with
// `since` set to now.
func NewStateMachine(now time.Time) *StateMachine {
	return &StateMachine{since: now}
}

// Observe folds one event into the state machine at the given clock
// time. The clock parameter (vs time.Now()) keeps the machine
// deterministic in tests.
func (s *StateMachine) Observe(ev transcript.Event, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastEventType = string(ev.Type)
	s.lastEventAt = now

	switch ev.Type {
	case transcript.TypeSystem:
		// no busy/idle transition; session init / etc.
	case transcript.TypeAssistant, transcript.TypeUser:
		if !s.busy {
			s.busy = true
			s.since = now
		}
	case transcript.TypeResult:
		s.busy = false
		s.since = now
	}
}

// Snapshot returns a copy of the current state.
func (s *StateMachine) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		ClaudeBusy:    s.busy,
		SinceUnix:     s.since.Unix(),
		LastEventType: s.lastEventType,
		LastEventAt:   s.lastEventAt.Unix(),
	}
}

// Reset returns the machine to a fresh-session state. Called by the
// rotation goroutine after dream-end before respawning claude.
func (s *StateMachine) Reset(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
	s.since = now
	s.lastEventType = ""
	s.lastEventAt = time.Time{}
}
