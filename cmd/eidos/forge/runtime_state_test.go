package forge

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/agentloop"
	"github.com/LucianoXu/eidopsyche/internal/authstate"
)

// runtimeStateFixture redirects the package-level paths used by
// computeRuntimeState to test-temp locations and restores them on
// cleanup. Mirrors statusDetailFixture.
func runtimeStateFixture(t *testing.T) (authT, procStatT, procBootT, agentStateT string) {
	t.Helper()
	prevAuth := authStatePath
	prevProcStat := procStatPath
	prevProcBoot := procBootTimePath
	prevAgent := agentStateRuntimePath

	authT = filepath.Join(t.TempDir(), "auth_required.json")
	procStatT = filepath.Join(t.TempDir(), "proc1stat")
	procBootT = filepath.Join(t.TempDir(), "procstat")
	agentStateT = filepath.Join(t.TempDir(), "agent-state.json")

	authStatePath = authT
	procStatPath = procStatT
	procBootTimePath = procBootT
	agentStateRuntimePath = agentStateT

	t.Cleanup(func() {
		authStatePath = prevAuth
		procStatPath = prevProcStat
		procBootTimePath = prevProcBoot
		agentStateRuntimePath = prevAgent
	})
	return
}

func TestRuntimeState_Starting(t *testing.T) {
	// No agent-state.json yet: agent-loop hasn't written its first state.
	runtimeStateFixture(t)
	rs := computeRuntimeState(time.Now())
	if rs.V != runtimeStateSchemaVersion {
		t.Errorf("v=%d, want %d", rs.V, runtimeStateSchemaVersion)
	}
	if rs.Phase != "starting" {
		t.Errorf("phase=%q, want starting", rs.Phase)
	}
	if rs.AuthRequired {
		t.Errorf("auth_required should be false")
	}
	if rs.SessionID != "" || rs.Turns != 0 {
		t.Errorf("session fields should be zero when agent-state missing: %+v", rs)
	}
}

func TestRuntimeState_Idle(t *testing.T) {
	_, _, _, agentT := runtimeStateFixture(t)
	if err := agentloop.WriteAgentState(agentT, agentloop.AgentState{
		ClaudeBusy:       false,
		Dreaming:         false,
		SessionID:        "session-1",
		SessionStartedAt: 1700000000,
		WakesInSession:   3,
		LastEventAt:      1700000500,
	}); err != nil {
		t.Fatal(err)
	}

	rs := computeRuntimeState(time.Now())
	if rs.Phase != "idle" {
		t.Errorf("phase=%q, want idle", rs.Phase)
	}
	if rs.SessionID != "session-1" {
		t.Errorf("session_id=%q", rs.SessionID)
	}
	if rs.SessionStartedAt != 1700000000 {
		t.Errorf("session_started_at=%d", rs.SessionStartedAt)
	}
	if rs.Turns != 3 {
		t.Errorf("turns=%d, want 3", rs.Turns)
	}
	if rs.LastEventAt != 1700000500 {
		t.Errorf("last_event_at=%d", rs.LastEventAt)
	}
}

func TestRuntimeState_Thinking(t *testing.T) {
	_, _, _, agentT := runtimeStateFixture(t)
	if err := agentloop.WriteAgentState(agentT, agentloop.AgentState{
		ClaudeBusy:       true,
		Dreaming:         false,
		SessionID:        "session-1",
		SessionStartedAt: 1700000000,
		WakesInSession:   7,
	}); err != nil {
		t.Fatal(err)
	}

	rs := computeRuntimeState(time.Now())
	if rs.Phase != "thinking" {
		t.Errorf("phase=%q, want thinking", rs.Phase)
	}
	if rs.Turns != 7 {
		t.Errorf("turns=%d, want 7", rs.Turns)
	}
}

func TestRuntimeState_Dreaming(t *testing.T) {
	_, _, _, agentT := runtimeStateFixture(t)
	// Dreaming overrides busy: the consolidation pass is the meaningful
	// state to surface even if claude is mid-token at the moment.
	if err := agentloop.WriteAgentState(agentT, agentloop.AgentState{
		ClaudeBusy: true,
		Dreaming:   true,
	}); err != nil {
		t.Fatal(err)
	}

	rs := computeRuntimeState(time.Now())
	if rs.Phase != "dreaming" {
		t.Errorf("phase=%q, want dreaming (overrides thinking)", rs.Phase)
	}
}

func TestRuntimeState_AuthRequired(t *testing.T) {
	authT, _, _, agentT := runtimeStateFixture(t)
	if err := authstate.WriteAt(authT, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Even with claude_busy=true on disk, auth-required short-circuits
	// because the agent-loop is intentionally gated; surfacing
	// "thinking" would be misleading.
	if err := agentloop.WriteAgentState(agentT, agentloop.AgentState{
		ClaudeBusy: true,
	}); err != nil {
		t.Fatal(err)
	}

	rs := computeRuntimeState(time.Now())
	if !rs.AuthRequired {
		t.Errorf("auth_required should be true")
	}
	if rs.Phase != "auth-required" {
		t.Errorf("phase=%q, want auth-required", rs.Phase)
	}
}

func TestRuntimeState_ContainerStartedAt(t *testing.T) {
	// Synthesise minimal /proc/1/stat and /proc/stat to exercise the
	// best-effort start-time parser.
	_, procStatT, procBootT, _ := runtimeStateFixture(t)
	// /proc/1/stat: pid (comm) state ppid pgrp session tty_nr tpgid flags
	// minflt cminflt majflt cmajflt utime stime cutime cstime priority nice
	// num_threads itrealvalue starttime ...
	// 22 fields up to and including starttime (which is field 22).
	// We only need fields after the closing ')': 19 fields then start_time.
	stat := "1 (init) S 0 1 1 0 -1 4194304 0 0 0 0 0 0 0 0 20 0 1 0 12345 0 0\n"
	if err := os.WriteFile(procStatT, []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	// /proc/stat: btime line is the boot unix time
	procStat := "cpu  1 2 3 4\nbtime 1700000000\n"
	if err := os.WriteFile(procBootT, []byte(procStat), 0o644); err != nil {
		t.Fatal(err)
	}

	rs := computeRuntimeState(time.Now())
	// 1700000000 + 12345/100 = 1700000123 (integer division)
	if rs.ContainerStartedAt != 1700000123 {
		t.Errorf("container_started_at=%d, want 1700000123", rs.ContainerStartedAt)
	}
}

func TestRuntimeState_ContainerStartedAtFallback(t *testing.T) {
	runtimeStateFixture(t)
	rs := computeRuntimeState(time.Now())
	if rs.ContainerStartedAt != 0 {
		t.Errorf("container_started_at should be 0 when /proc files missing, got %d", rs.ContainerStartedAt)
	}
}

func TestRuntimeStateCmd_JSONOutput(t *testing.T) {
	runtimeStateFixture(t)
	cmd := newRuntimeStateCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	var rs RuntimeState
	if err := json.Unmarshal(buf.Bytes(), &rs); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if rs.Phase != "starting" {
		t.Errorf("phase=%q, want starting (no agent-state.json present)", rs.Phase)
	}
	if rs.V != runtimeStateSchemaVersion {
		t.Errorf("v=%d, want %d", rs.V, runtimeStateSchemaVersion)
	}
}
