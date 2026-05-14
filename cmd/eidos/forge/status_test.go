package forge

import (
	"context"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// statusFake dispatches ContainerExec by command name (cmd[2]) so each
// test can stub the runtime-state, whoami, and status-detail surfaces
// independently. Embeds the orchestration fakeClient for the unrelated
// methods we don't care about here.
type statusFake struct {
	fakeClient
	state         string
	execResponses map[string]forgectl.ExecResult
	inspectErr    error
}

func (f *statusFake) ContainerInspectState(_ context.Context, _ string) (string, error) {
	return f.state, f.inspectErr
}
func (f *statusFake) ContainerInspectImage(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (f *statusFake) ContainerExec(_ context.Context, _ string, cmd []string) (forgectl.ExecResult, error) {
	if len(cmd) >= 3 {
		if r, ok := f.execResponses[cmd[2]]; ok {
			return r, nil
		}
	}
	// Default: command not registered → simulate older image (exit 1).
	return forgectl.ExecResult{ExitCode: 1, Stderr: []byte("unknown command")}, nil
}

func TestStatusOffline(t *testing.T) {
	f := &statusFake{state: "exited"}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "phase:   offline (exited)") {
		t.Errorf("missing phase-offline line: %q", out)
	}
}

func TestStatusThinking(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"thinking","auth_required":false,"container_started_at":1700000000}`)},
			"whoami":        {Stdout: []byte("npub: npub1mfx\n")},
			"status-detail": {Stdout: []byte("plans:   none\ndreams:  none yet\n")},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "phase:   thinking") {
		t.Errorf("phase line wrong: %q", out)
	}
	// The standalone `thinking: yes` line from the v1 output is gone —
	// phase already conveys it.
	if strings.Contains(out, "thinking: yes") {
		t.Errorf("redundant thinking line should be removed: %q", out)
	}
	if !strings.Contains(out, "npub1mfx") {
		t.Errorf("whoami missing: %q", out)
	}
	if !strings.Contains(out, "plans:   none") {
		t.Errorf("status-detail missing: %q", out)
	}
}

func TestStatusDreaming(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"dreaming","auth_required":false,"container_started_at":0}`)},
			"whoami":        {Stdout: []byte("npub: npub1mfx\n")},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "phase:   dreaming") {
		t.Errorf("phase line wrong: %q", out)
	}
}

func TestStatusIdle(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"idle","auth_required":false,"container_started_at":0}`)},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "phase:   idle") {
		t.Errorf("phase line wrong: %q", out)
	}
}

func TestStatusAuthRequired(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"auth-required","auth_required":true,"container_started_at":0}`)},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "phase:   auth-required") {
		t.Errorf("phase line wrong: %q", out)
	}
}

func TestStatusFallbackOldImage(t *testing.T) {
	// Older image: runtime-state returns exit 1. Status falls back to
	// the legacy `state: running` line so old images keep working.
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"whoami":        {Stdout: []byte("npub: npub1mfx\n")},
			"status-detail": {Stdout: []byte("plans:   none\n")},
			// runtime-state intentionally omitted → ContainerExec returns
			// the unknown-command default (exit 1).
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "state:   running") {
		t.Errorf("legacy state line missing: %q", out)
	}
	if strings.Contains(out, "phase:") {
		t.Errorf("phase line should be absent on fallback: %q", out)
	}
	if !strings.Contains(out, "npub1mfx") {
		t.Errorf("whoami still expected on fallback: %q", out)
	}
}

func TestStatusAbsent(t *testing.T) {
	f := &statusFake{state: "absent"}
	out, err := computeStatus(context.Background(), f, "alice")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("want not-found, got out=%q err=%v", out, err)
	}
}

func TestStatusThinking_WithSessionFields(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"thinking","auth_required":false,"container_started_at":1700000000,"session_id":"7d2f6f2e-1b2a-4c3d-9e8f-aabbccddeeff","session_started_at":1700000000,"turns":47}`)},
			"whoami":        {Stdout: []byte("npub: npub1mfx\n")},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "session: 7d2f6f2e") {
		t.Errorf("missing session line: %q", out)
	}
	if !strings.Contains(out, "47 turns") {
		t.Errorf("missing turn count: %q", out)
	}
}

func TestStatusStarting_NoSessionLineWhenAbsent(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"starting","auth_required":false,"container_started_at":0}`)},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "session:") {
		t.Errorf("session line should be omitted when absent: %q", out)
	}
	if strings.Contains(out, "last_active:") {
		t.Errorf("last_active line should be omitted when absent: %q", out)
	}
}

func TestStatusCrashed(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"crashed","auth_required":false,"container_started_at":0,"crashed_exit_code":137,"crashed_last_error":"context deadline exceeded"}`)},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "phase:   crashed") {
		t.Errorf("phase line wrong: %q", out)
	}
	if !strings.Contains(out, "exit_code=137") {
		t.Errorf("crash exit code missing: %q", out)
	}
	if !strings.Contains(out, `last_error="context deadline exceeded"`) {
		t.Errorf("crash last_error missing: %q", out)
	}
}

func TestStatusRejectsV1Schema(t *testing.T) {
	// A v1-shaped runtime-state response (older container image) must
	// not flow through the v2 struct — its phase strings ("sleeping",
	// "awake") would print verbatim and `wakes_in_session` would
	// silently zero out. fetchRuntimeState rejects on V mismatch and
	// the legacy `state: running` line takes over.
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":1,"phase":"sleeping","dreaming":false,"auth_required":false,"container_started_at":0,"wakes_in_session":47}`)},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "phase:") {
		t.Errorf("v1 response should not produce a phase line: %q", out)
	}
	if strings.Contains(out, "sleeping") {
		t.Errorf("v1 phase string leaked through: %q", out)
	}
	if !strings.Contains(out, "state:   running") {
		t.Errorf("fallback state line missing: %q", out)
	}
}

func TestStatusLastActive(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"idle","auth_required":false,"container_started_at":0,"last_event_at":1700000000}`)},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "last_active:") {
		t.Errorf("missing last_active line: %q", out)
	}
}
