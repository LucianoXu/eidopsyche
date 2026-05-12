package agentloop

import (
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

func TestStateMachine_IdleByDefault(t *testing.T) {
	now := time.Unix(1715500000, 0)
	sm := NewStateMachine(now)

	snap := sm.Snapshot()
	if snap.ClaudeBusy {
		t.Errorf("fresh state machine should be idle, got busy")
	}
}

func TestStateMachine_AssistantEventGoesBusy(t *testing.T) {
	now := time.Unix(1715500000, 0)
	sm := NewStateMachine(now)

	later := now.Add(2 * time.Second)
	sm.Observe(transcript.Event{Type: transcript.TypeAssistant}, later)

	snap := sm.Snapshot()
	if !snap.ClaudeBusy {
		t.Errorf("after assistant event state should be busy, got idle")
	}
	if snap.SinceUnix != later.Unix() {
		t.Errorf("since unix: got %d, want %d", snap.SinceUnix, later.Unix())
	}
}

func TestStateMachine_ResultEventGoesIdle(t *testing.T) {
	now := time.Unix(1715500000, 0)
	sm := NewStateMachine(now)

	sm.Observe(transcript.Event{Type: transcript.TypeAssistant}, now.Add(1*time.Second))
	resultAt := now.Add(5 * time.Second)
	sm.Observe(transcript.Event{Type: transcript.TypeResult}, resultAt)

	snap := sm.Snapshot()
	if snap.ClaudeBusy {
		t.Errorf("after result event state should be idle, got busy")
	}
	if snap.SinceUnix != resultAt.Unix() {
		t.Errorf("since unix: got %d, want %d", snap.SinceUnix, resultAt.Unix())
	}
}

func TestStateMachine_SystemInitDoesNotTransition(t *testing.T) {
	now := time.Unix(1715500000, 0)
	sm := NewStateMachine(now)

	sm.Observe(transcript.Event{Type: transcript.TypeSystem, Subtype: "init"}, now.Add(1*time.Second))

	snap := sm.Snapshot()
	if snap.ClaudeBusy {
		t.Errorf("system init should not transition to busy")
	}
}

func TestStateMachine_LastEventTrackedAlways(t *testing.T) {
	now := time.Unix(1715500000, 0)
	sm := NewStateMachine(now)

	sm.Observe(transcript.Event{Type: transcript.TypeAssistant}, now.Add(1*time.Second))
	sm.Observe(transcript.Event{Type: transcript.TypeResult}, now.Add(2*time.Second))
	sm.Observe(transcript.Event{Type: transcript.TypeSystem, Subtype: "other"}, now.Add(3*time.Second))

	snap := sm.Snapshot()
	if snap.LastEventAt != now.Add(3*time.Second).Unix() {
		t.Errorf("last_event_at not updated on every event: got %d", snap.LastEventAt)
	}
	if snap.LastEventType != "system" {
		t.Errorf("last_event_type: got %q, want %q", snap.LastEventType, "system")
	}
}
