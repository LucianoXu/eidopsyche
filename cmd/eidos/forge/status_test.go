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

func TestStatusAwake(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":1,"phase":"awake","wake_reason":"mindgate","active_wake_id":"abc","dreaming":false,"auth_required":false,"container_started_at":1700000000}`)},
			"whoami":        {Stdout: []byte("npub: npub1mfx\n")},
			"status-detail": {Stdout: []byte("plans:   none\ndreams:  none yet\n")},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "phase:   awake (mindgate)") {
		t.Errorf("phase line wrong: %q", out)
	}
	if !strings.Contains(out, "npub1mfx") {
		t.Errorf("whoami missing: %q", out)
	}
	if !strings.Contains(out, "plans:   none") {
		t.Errorf("status-detail missing: %q", out)
	}
}

func TestStatusAwakeDreaming(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":1,"phase":"awake+dreaming","wake_reason":"heartbeat","active_wake_id":"x","dreaming":true,"auth_required":false,"container_started_at":0}`)},
			"whoami":        {Stdout: []byte("npub: npub1mfx\n")},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "phase:   awake+dreaming (heartbeat)") {
		t.Errorf("phase line wrong: %q", out)
	}
}

func TestStatusSleeping(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":1,"phase":"sleeping","dreaming":false,"auth_required":false,"container_started_at":0}`)},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "phase:   sleeping") {
		t.Errorf("phase line wrong: %q", out)
	}
	// Sleeping phase has no `(reason)` suffix.
	if strings.Contains(out, "sleeping (") {
		t.Errorf("sleeping should not carry a wake_reason: %q", out)
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
