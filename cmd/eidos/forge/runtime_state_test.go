package forge

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/authstate"
	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/sessionstate"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

// runtimeStateFixture redirects the package-level paths used by
// computeRuntimeState to test-temp locations and restores them on
// cleanup. Mirrors statusDetailFixture.
func runtimeStateFixture(t *testing.T) (wakeT, dreamT, authT, procStatT, procBootT string) {
	t.Helper()
	prevWake := wakeDir
	prevDream := dreamStatePath
	prevAuth := authStatePath
	prevProcStat := procStatPath
	prevProcBoot := procBootTimePath

	wakeT = t.TempDir()
	dreamT = filepath.Join(t.TempDir(), "dream-state.json")
	authT = filepath.Join(t.TempDir(), "auth_required.json")
	procStatT = filepath.Join(t.TempDir(), "proc1stat")
	procBootT = filepath.Join(t.TempDir(), "procstat")

	wakeDir = wakeT
	dreamStatePath = dreamT
	authStatePath = authT
	procStatPath = procStatT
	procBootTimePath = procBootT

	t.Cleanup(func() {
		wakeDir = prevWake
		dreamStatePath = prevDream
		authStatePath = prevAuth
		procStatPath = prevProcStat
		procBootTimePath = prevProcBoot
	})
	return
}

func TestRuntimeState_Sleeping(t *testing.T) {
	runtimeStateFixture(t)
	rs := computeRuntimeState(time.Now())
	if rs.V != 1 {
		t.Errorf("v=%d, want 1", rs.V)
	}
	if rs.Phase != "sleeping" {
		t.Errorf("phase=%q, want sleeping", rs.Phase)
	}
	if rs.WakeReason != "" {
		t.Errorf("wake_reason should be empty when sleeping: %q", rs.WakeReason)
	}
	if rs.ActiveWakeID != "" {
		t.Errorf("active_wake_id should be empty when sleeping: %q", rs.ActiveWakeID)
	}
	if rs.SincePhaseChangeSeconds != nil {
		t.Errorf("since_phase_change_seconds should be nil when sleeping")
	}
	if rs.AuthRequired {
		t.Errorf("auth_required should be false")
	}
	if rs.Dreaming {
		t.Errorf("dreaming should be false")
	}
}

func TestRuntimeState_Awake(t *testing.T) {
	wakeT, _, _, _, _ := runtimeStateFixture(t)
	sig := wake.Signal{V: 1, ID: "1715000000-mindgate", Reason: wake.ReasonMindGate, TriggeredAt: 1715000000}
	if err := wake.WritePending(wakeT, sig); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.PromoteToActive(wakeT); err != nil {
		t.Fatal(err)
	}

	rs := computeRuntimeState(time.Now())
	if rs.Phase != "awake" {
		t.Errorf("phase=%q, want awake", rs.Phase)
	}
	if rs.WakeReason != "mindgate" {
		t.Errorf("wake_reason=%q", rs.WakeReason)
	}
	if rs.ActiveWakeID != "1715000000-mindgate" {
		t.Errorf("active_wake_id=%q", rs.ActiveWakeID)
	}
	if rs.SincePhaseChangeSeconds == nil {
		t.Errorf("since_phase_change_seconds should be set when awake")
	}
}

func TestRuntimeState_AwakeDreaming(t *testing.T) {
	wakeT, dreamT, _, _, _ := runtimeStateFixture(t)
	sig := wake.Signal{V: 1, ID: "abc", Reason: wake.ReasonHeartBeat, TriggeredAt: 1}
	if err := wake.WritePending(wakeT, sig); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.PromoteToActive(wakeT); err != nil {
		t.Fatal(err)
	}
	if err := dreamstate.Begin(dreamT, time.Now(), "consolidating"); err != nil {
		t.Fatal(err)
	}

	rs := computeRuntimeState(time.Now())
	if rs.Phase != "awake+dreaming" {
		t.Errorf("phase=%q, want awake+dreaming", rs.Phase)
	}
	if !rs.Dreaming {
		t.Errorf("dreaming should be true")
	}
	if rs.WakeReason != "heartbeat" {
		t.Errorf("wake_reason=%q", rs.WakeReason)
	}
}

func TestRuntimeState_DreamingWithoutActiveWake(t *testing.T) {
	// Defensive: if dreamstate says dreaming but no active wake (race or
	// inconsistency), phase should stay "sleeping" — but Dreaming bool
	// surfaces the inconsistency to whoever inspects the JSON.
	_, dreamT, _, _, _ := runtimeStateFixture(t)
	if err := dreamstate.Begin(dreamT, time.Now(), "ghost dream"); err != nil {
		t.Fatal(err)
	}

	rs := computeRuntimeState(time.Now())
	if rs.Phase != "sleeping" {
		t.Errorf("phase=%q, want sleeping (no active wake)", rs.Phase)
	}
	if !rs.Dreaming {
		t.Errorf("dreaming should still be true (orthogonal field)")
	}
}

func TestRuntimeState_AuthRequired(t *testing.T) {
	_, _, authT, _, _ := runtimeStateFixture(t)
	if err := authstate.WriteAt(authT, time.Now()); err != nil {
		t.Fatal(err)
	}

	rs := computeRuntimeState(time.Now())
	if !rs.AuthRequired {
		t.Errorf("auth_required should be true")
	}
}

func TestRuntimeState_ContainerStartedAt(t *testing.T) {
	// Synthesise minimal /proc/1/stat and /proc/stat to exercise the
	// best-effort start-time parser.
	_, _, _, procStatT, procBootT := runtimeStateFixture(t)
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
	if rs.Phase != "sleeping" {
		t.Errorf("phase=%q", rs.Phase)
	}
}

func TestComputeRuntimeState_SessionFieldsPresent(t *testing.T) {
	dir := t.TempDir()
	prev := sessionStateRuntimePath
	sessionStateRuntimePath = filepath.Join(dir, "session.json")
	t.Cleanup(func() { sessionStateRuntimePath = prev })

	st, err := sessionstate.Mint(sessionStateRuntimePath, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionstate.IncrementWake(sessionStateRuntimePath); err != nil {
		t.Fatal(err)
	}

	rs := computeRuntimeState(time.Unix(1700001000, 0))
	if rs.SessionID != st.SessionID {
		t.Fatalf("SessionID = %q, want %q", rs.SessionID, st.SessionID)
	}
	if rs.SessionStartedAt != 1700000000 {
		t.Fatalf("SessionStartedAt = %d", rs.SessionStartedAt)
	}
	if rs.WakesInSession != 1 {
		t.Fatalf("WakesInSession = %d", rs.WakesInSession)
	}
}

func TestComputeRuntimeState_SessionFieldsOmittedWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	prev := sessionStateRuntimePath
	sessionStateRuntimePath = filepath.Join(dir, "session.json")
	t.Cleanup(func() { sessionStateRuntimePath = prev })

	rs := computeRuntimeState(time.Unix(1700000000, 0))
	if rs.SessionID != "" || rs.SessionStartedAt != 0 || rs.WakesInSession != 0 {
		t.Fatalf("expected zero session fields when session.json absent; got %+v", rs)
	}
}
