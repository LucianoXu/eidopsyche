// Package agentloop — rotation.go
//
// Rotation handles dream-end transitions: wait for the state machine to
// reach idle, close claude's stdin, wait for the process to exit (with a
// grace timeout and SIGTERM/SIGKILL fallback), clear session.json, mint a
// fresh one, reset the state machine, and spawn a new claude process.
//
// All I/O sites are supplied as callbacks (CloseStdin, WaitForClaudeExit,
// Kill, SpawnNewClaude) so the orchestration logic itself is exercisable in
// pure unit tests without spawning real processes.
package agentloop

import (
	"fmt"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/sessionstate"
)

// RotationConfig wires a Rotation. All I/O sites are callbacks so the
// rotation logic itself is pure and testable.
type RotationConfig struct {
	SessionStatePath string
	StateMachine     *StateMachine

	// IdleWait is the maximum time to wait for the state machine to reach
	// idle before force-closing stdin anyway.
	IdleWait time.Duration

	// CloseGrace is the maximum time to wait for claude to exit after stdin
	// is closed before escalating to Kill.
	CloseGrace time.Duration

	// CloseStdin closes the write end of claude's stdin pipe.
	CloseStdin func() error

	// WaitForClaudeExit blocks until claude's process exits or the timeout
	// elapses. Returns true if the process exited, false on timeout.
	WaitForClaudeExit func(timeout time.Duration) bool

	// Kill forcefully terminates the claude process group (SIGTERM → SIGKILL).
	Kill func()

	// SpawnNewClaude launches a fresh claude process using the supplied
	// session UUID. Called after the new session.json has been minted.
	SpawnNewClaude func(newUUID string) error

	// OnRotationDone is optional. Called synchronously after the new claude
	// process has been successfully spawned.
	OnRotationDone func()
}

// Rotation runs one dream rotation. Construct with NewRotation; call
// Rotate once (synchronously).
type Rotation struct {
	cfg RotationConfig
}

// NewRotation constructs a Rotation. Call Rotate to execute it.
func NewRotation(cfg RotationConfig) *Rotation { return &Rotation{cfg: cfg} }

// Rotate executes one full rotation:
//  1. Quiescence gate — wait for the state machine to reach idle (up to
//     IdleWait). After the timeout, proceed anyway and log a warning.
//  2. Close claude's stdin.
//  3. Wait for claude to exit (CloseGrace); escalate via Kill on timeout.
//  4. Clear and re-mint session.json.
//  5. Reset the state machine.
//  6. Spawn the new claude with the fresh session UUID.
//
// Returns an error only for hard failures (session minting, spawn).
func (r *Rotation) Rotate() error {
	// 1. Quiescence gate.
	deadline := time.Now().Add(r.cfg.IdleWait)
	for r.cfg.StateMachine.Snapshot().ClaudeBusy {
		if time.Now().After(deadline) {
			fmt.Fprintf(stderr(),
				"agent-loop: rotation: idle-wait timeout (%s); forcing stdin close\n",
				r.cfg.IdleWait)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 2. Close claude stdin.
	if err := r.cfg.CloseStdin(); err != nil {
		fmt.Fprintf(stderr(), "agent-loop: rotation: close stdin: %v\n", err)
	}

	// 3. Wait for claude to exit; kill if it takes too long.
	if !r.cfg.WaitForClaudeExit(r.cfg.CloseGrace) {
		fmt.Fprintf(stderr(),
			"agent-loop: rotation: close grace (%s) exceeded; sending SIGTERM/SIGKILL\n",
			r.cfg.CloseGrace)
		r.cfg.Kill()
	}

	// 4. Clear and mint a fresh session.
	if err := sessionstate.Clear(r.cfg.SessionStatePath); err != nil {
		return fmt.Errorf("rotation: clear session: %w", err)
	}
	fresh, err := sessionstate.Mint(r.cfg.SessionStatePath, time.Now())
	if err != nil {
		return fmt.Errorf("rotation: mint session: %w", err)
	}

	// 5. Reset state machine.
	r.cfg.StateMachine.Reset(time.Now())

	// 6. Spawn new claude.
	if err := r.cfg.SpawnNewClaude(fresh.SessionID); err != nil {
		return fmt.Errorf("rotation: spawn new claude: %w", err)
	}

	if r.cfg.OnRotationDone != nil {
		r.cfg.OnRotationDone()
	}
	return nil
}
