package forge

import (
	"context"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

func TestListPhase_Offline(t *testing.T) {
	f := &statusFake{state: "exited"}
	if got := listPhase(context.Background(), f, "alice"); got != "offline" {
		t.Errorf("got %q, want offline", got)
	}
}

func TestListPhase_Starting(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"starting","auth_required":false,"container_started_at":0}`)},
		},
	}
	if got := listPhase(context.Background(), f, "alice"); got != "starting" {
		t.Errorf("got %q, want starting", got)
	}
}

func TestListPhase_Idle(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"idle","auth_required":false,"container_started_at":0}`)},
		},
	}
	if got := listPhase(context.Background(), f, "alice"); got != "idle" {
		t.Errorf("got %q, want idle", got)
	}
}

func TestListPhase_Thinking(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"thinking","auth_required":false,"container_started_at":0}`)},
		},
	}
	if got := listPhase(context.Background(), f, "alice"); got != "thinking" {
		t.Errorf("got %q, want thinking", got)
	}
}

func TestListPhase_Dreaming(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"dreaming","auth_required":false,"container_started_at":0}`)},
		},
	}
	if got := listPhase(context.Background(), f, "alice"); got != "dreaming" {
		t.Errorf("got %q, want dreaming", got)
	}
}

func TestListPhase_AuthRequired(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":2,"phase":"auth-required","auth_required":true,"container_started_at":0}`)},
		},
	}
	if got := listPhase(context.Background(), f, "alice"); got != "auth-required" {
		t.Errorf("got %q, want auth-required", got)
	}
}

func TestListPhase_FallbackOldImage(t *testing.T) {
	// runtime-state not registered → simulates older image. Falls back to
	// raw docker state.
	f := &statusFake{
		state:         "running",
		execResponses: map[string]forgectl.ExecResult{},
	}
	if got := listPhase(context.Background(), f, "alice"); got != "running" {
		t.Errorf("got %q, want running (fallback)", got)
	}
}
