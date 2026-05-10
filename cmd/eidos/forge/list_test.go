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

func TestListPhase_Sleeping(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":1,"phase":"sleeping","dreaming":false,"auth_required":false,"container_started_at":0}`)},
		},
	}
	if got := listPhase(context.Background(), f, "alice"); got != "sleeping" {
		t.Errorf("got %q, want sleeping", got)
	}
}

func TestListPhase_Awake(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":1,"phase":"awake","wake_reason":"mindgate","dreaming":false,"auth_required":false,"container_started_at":0}`)},
		},
	}
	// list omits the wake reason — column stays narrow.
	if got := listPhase(context.Background(), f, "alice"); got != "awake" {
		t.Errorf("got %q, want awake", got)
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
