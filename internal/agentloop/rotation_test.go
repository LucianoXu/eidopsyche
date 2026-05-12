// internal/agentloop/rotation_test.go
package agentloop

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/sessionstate"
	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

func TestRotation_WaitsForIdleBeforeClosingStdin(t *testing.T) {
	tmp := t.TempDir()
	sessPath := filepath.Join(tmp, "session.json")
	if _, err := sessionstate.Mint(sessPath, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}

	sm := NewStateMachine(time.Unix(1000, 0))
	// State machine is busy initially.
	sm.Observe(parsedEvent(t, `{"type":"assistant"}`), time.Unix(1001, 0))

	stdinClosed := make(chan struct{})
	rc := RotationConfig{
		SessionStatePath: sessPath,
		StateMachine:     sm,
		IdleWait:         time.Second,
		CloseGrace:       100 * time.Millisecond,
		CloseStdin: func() error {
			close(stdinClosed)
			return nil
		},
		WaitForClaudeExit: func(time.Duration) bool { return true },
		Kill:              func() {},
		SpawnNewClaude:    func(uuid string) error { return nil },
	}
	r := NewRotation(rc)

	go func() {
		// Simulate claude turn ending 200ms later.
		time.Sleep(200 * time.Millisecond)
		sm.Observe(parsedEvent(t, `{"type":"result","subtype":"success"}`), time.Unix(1002, 0))
	}()

	if err := r.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	select {
	case <-stdinClosed:
	default:
		t.Fatal("stdin not closed")
	}
}

func TestRotation_IdleWaitTimeoutForceClose(t *testing.T) {
	tmp := t.TempDir()
	sessPath := filepath.Join(tmp, "session.json")
	if _, err := sessionstate.Mint(sessPath, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	sm := NewStateMachine(time.Unix(1000, 0))
	sm.Observe(parsedEvent(t, `{"type":"assistant"}`), time.Unix(1001, 0))
	// Never produce a `result` event → SM stays busy.

	var mu sync.Mutex
	stdinClosed := false
	rc := RotationConfig{
		SessionStatePath: sessPath,
		StateMachine:     sm,
		IdleWait:         100 * time.Millisecond,
		CloseGrace:       50 * time.Millisecond,
		CloseStdin: func() error {
			mu.Lock()
			defer mu.Unlock()
			stdinClosed = true
			return nil
		},
		WaitForClaudeExit: func(time.Duration) bool { return true },
		Kill:              func() {},
		SpawnNewClaude:    func(uuid string) error { return nil },
	}
	r := NewRotation(rc)
	if err := r.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !stdinClosed {
		t.Error("rotation should force-close stdin after idle-wait timeout")
	}
}

func TestRotation_SpawnsNewSessionAfterGraceTimeoutKillsClaude(t *testing.T) {
	tmp := t.TempDir()
	sessPath := filepath.Join(tmp, "session.json")
	if _, err := sessionstate.Mint(sessPath, time.Unix(1000, 0)); err != nil {
		t.Fatal(err)
	}
	sm := NewStateMachine(time.Unix(1000, 0))
	// already idle

	var killed, spawned bool
	var spawnedUUID string
	rc := RotationConfig{
		SessionStatePath:  sessPath,
		StateMachine:      sm,
		IdleWait:          100 * time.Millisecond,
		CloseGrace:        50 * time.Millisecond,
		CloseStdin:        func() error { return nil },
		WaitForClaudeExit: func(time.Duration) bool { return false },
		Kill:              func() { killed = true },
		SpawnNewClaude: func(uuid string) error {
			spawned = true
			spawnedUUID = uuid
			return nil
		},
	}
	r := NewRotation(rc)
	if err := r.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if !killed {
		t.Error("expected Kill() to be called when WaitForClaudeExit returns false")
	}
	if !spawned {
		t.Error("expected SpawnNewClaude to be called")
	}
	if spawnedUUID == "" {
		t.Error("expected SpawnNewClaude to receive a freshly minted UUID")
	}
	st, err := sessionstate.Read(sessPath)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	if st.SessionID != spawnedUUID {
		t.Errorf("session.json UUID %q != spawned UUID %q", st.SessionID, spawnedUUID)
	}
}

// parsedEvent panics on parse error so tests can stay terse.
func parsedEvent(t *testing.T, line string) transcript.Event {
	t.Helper()
	ev, err := transcript.ParseEvent([]byte(line))
	if err != nil {
		t.Fatalf("parse %q: %v", line, err)
	}
	return ev
}
