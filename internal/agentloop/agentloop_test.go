//go:build !windows

// internal/agentloop/agentloop_test.go
package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/sessionstate"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

func TestAgentLoop_EndToEnd_OneWakeOneTurn(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "self"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "self", "born_at"), []byte("1715600000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wakeStdinR, wakeStdinW := io.Pipe()

	opts := RunOpts{
		ClaudeBin:        stubClaudeBin,
		ExtraClaudeArgs:  []string{"--mode", "normal"},
		OntologyDir:      tmp,
		ClaudeDir:        filepath.Join(tmp, ".claude"),
		SessionStatePath: filepath.Join(tmp, "session.json"),
		DreamStatePath:   filepath.Join(tmp, "dream-state.json"),
		AgentStatePath:   filepath.Join(tmp, "agent-state.json"),
		TranscriptsDir:   filepath.Join(tmp, "transcripts"),
		AgentLockPath:    filepath.Join(tmp, "agent.lock"),
		ConfigPath:       filepath.Join(tmp, "config.toml"),
		WakeStdin:        wakeStdinR,
		IdleWait:         time.Second,
		CloseGrace:       500 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()

	// Wait for agent-loop to come up — agent-state.json should appear.
	waitFor(t, 3*time.Second, func() bool {
		_, err := os.Stat(opts.AgentStatePath)
		return err == nil
	})

	// Send a wake.
	sig := wake.Signal{V: 1, ID: "wake-1", Reason: wake.ReasonHeartBeat}
	body, _ := json.Marshal(sig)
	if _, err := wakeStdinW.Write(append(body, '\n')); err != nil {
		t.Fatal(err)
	}

	// Wait for ResultsSeen to reach 1 (one turn completed).
	waitFor(t, 5*time.Second, func() bool {
		body, err := os.ReadFile(opts.AgentStatePath)
		if err != nil {
			return false
		}
		return strings.Contains(string(body), `"results_seen":1`)
	})

	cancel()
	wakeStdinW.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("agent-loop did not exit within 3s after cancel")
	}

	st, err := sessionstate.Read(opts.SessionStatePath)
	if err != nil || st.SessionID == "" {
		t.Errorf("session.json: %+v, err=%v", st, err)
	}
}

func TestAgentLoop_DreamRotationProducesNewSession(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "self"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "self", "born_at"), []byte("1715600000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wakeStdinR, wakeStdinW := io.Pipe()

	opts := RunOpts{
		ClaudeBin:        stubClaudeBin,
		ExtraClaudeArgs:  []string{"--mode", "dream-then-exit"},
		OntologyDir:      tmp,
		ClaudeDir:        filepath.Join(tmp, ".claude"),
		SessionStatePath: filepath.Join(tmp, "session.json"),
		DreamStatePath:   filepath.Join(tmp, "dream-state.json"),
		AgentStatePath:   filepath.Join(tmp, "agent-state.json"),
		TranscriptsDir:   filepath.Join(tmp, "transcripts"),
		AgentLockPath:    filepath.Join(tmp, "agent.lock"),
		ConfigPath:       filepath.Join(tmp, "config.toml"),
		WakeStdin:        wakeStdinR,
		IdleWait:         time.Second,
		CloseGrace:       500 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()

	waitFor(t, 3*time.Second, func() bool {
		_, err := os.Stat(opts.AgentStatePath)
		return err == nil
	})

	st1, _ := sessionstate.Read(opts.SessionStatePath)
	if st1.SessionID == "" {
		t.Fatal("expected initial session UUID")
	}

	// Trigger dream end. The watcher fires rotation only when
	// LastDreamFinishedAt grows — so we must call Begin first (sets
	// CurrentlyDreaming=true) then End (clears it and increments
	// LastDreamFinishedAt).
	if err := dreamstate.Begin(opts.DreamStatePath, time.Now(), "test-begin"); err != nil {
		t.Fatal(err)
	}
	if err := dreamstate.End(opts.DreamStatePath, time.Now(), "test-end", ""); err != nil {
		t.Fatal(err)
	}

	// Wait for session UUID to change.
	waitFor(t, 8*time.Second, func() bool {
		st2, _ := sessionstate.Read(opts.SessionStatePath)
		return st2.SessionID != "" && st2.SessionID != st1.SessionID
	})

	cancel()
	wakeStdinW.Close()
	<-done
}

// TestAgentLoop_AuthRequiredHaltsAgentLoop documents the auth-required
// coverage gap: Run calls os.Exit(47) on ClaudeAuthRequired, which is not
// safely unit-testable (it would kill the test process). The stub's
// auth-required mode exits 47 with the correct stderr marker, and
// ClassifyClaudeExit is tested independently in internal/claudeexec.
// Full coverage of the agent-loop wiring requires the integration harness.
func TestAgentLoop_AuthRequiredHaltsAgentLoop(t *testing.T) {
	t.Skip("os.Exit on auth-required is not unit-testable; covered by integration scaffold")
}

func TestAgentLoop_StartGate_BlocksUntilBornAt(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "self"), 0o700); err != nil {
		t.Fatal(err)
	}
	wakeStdinR, wakeStdinW := io.Pipe()
	defer wakeStdinW.Close()

	opts := RunOpts{
		ClaudeBin:        stubClaudeBin,
		ExtraClaudeArgs:  []string{"--mode", "normal"},
		OntologyDir:      tmp,
		ClaudeDir:        filepath.Join(tmp, ".claude"),
		SessionStatePath: filepath.Join(tmp, "session.json"),
		DreamStatePath:   filepath.Join(tmp, "dream-state.json"),
		AgentStatePath:   filepath.Join(tmp, "agent-state.json"),
		TranscriptsDir:   filepath.Join(tmp, "transcripts"),
		AgentLockPath:    filepath.Join(tmp, "agent.lock"),
		ConfigPath:       filepath.Join(tmp, "config.toml"),
		WakeStdin:        wakeStdinR,
		IdleWait:         time.Second,
		CloseGrace:       500 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()

	// Verify agent-loop is NOT making progress (no agent-state.json yet).
	time.Sleep(750 * time.Millisecond)
	if _, err := os.Stat(opts.AgentStatePath); err == nil {
		t.Fatal("agent-state.json appeared before born_at was written; gate did not block")
	}

	// Write born_at; gate should unblock.
	if err := os.WriteFile(filepath.Join(tmp, "self", "born_at"), []byte("1715600000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 3*time.Second, func() bool {
		_, err := os.Stat(opts.AgentStatePath)
		return err == nil
	})

	cancel()
	wakeStdinW.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("agent-loop did not exit within 3s after cancel")
	}
}

func TestAgentLoop_StartGate_CtxCancelDuringWait(t *testing.T) {
	tmp := t.TempDir()
	// No self/ dir, no born_at — gate blocks forever absent cancel.
	wakeStdinR, wakeStdinW := io.Pipe()
	wakeStdinW.Close() // nothing to send; ensures forwarder doesn't block if gate unexpectedly clears

	opts := RunOpts{
		ClaudeBin:        stubClaudeBin,
		OntologyDir:      tmp,
		ClaudeDir:        filepath.Join(tmp, ".claude"),
		SessionStatePath: filepath.Join(tmp, "session.json"),
		DreamStatePath:   filepath.Join(tmp, "dream-state.json"),
		AgentStatePath:   filepath.Join(tmp, "agent-state.json"),
		TranscriptsDir:   filepath.Join(tmp, "transcripts"),
		AgentLockPath:    filepath.Join(tmp, "agent.lock"),
		ConfigPath:       filepath.Join(tmp, "config.toml"),
		WakeStdin:        wakeStdinR,
		IdleWait:         time.Second,
		CloseGrace:       500 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()

	time.Sleep(750 * time.Millisecond) // ensure gate is engaged
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected ctx.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent-loop did not exit within 3s of ctx cancel")
	}
}
