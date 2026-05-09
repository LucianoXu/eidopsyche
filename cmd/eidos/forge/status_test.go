package forge

import (
	"context"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

type statusFake struct {
	fakeClient
	state     string
	whoamiOut string
	whoamiErr error
}

func (f *statusFake) ContainerInspectState(_ context.Context, _ string) (string, error) {
	return f.state, nil
}
func (f *statusFake) ContainerExec(_ context.Context, _ string, cmd []string) (forgectl.ExecResult, error) {
	if f.whoamiErr != nil {
		return forgectl.ExecResult{ExitCode: 1, Stderr: []byte(f.whoamiErr.Error())}, f.whoamiErr
	}
	return forgectl.ExecResult{ExitCode: 0, Stdout: []byte(f.whoamiOut)}, nil
}

func TestStatusRunning(t *testing.T) {
	f := &statusFake{state: "running", whoamiOut: "npub: npub1mfx\nrelay: wss://r\nmaster: npub1own\n"}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "running") {
		t.Errorf("status missing 'running': %s", out)
	}
	if !strings.Contains(out, "npub1mfx") {
		t.Errorf("status missing npub: %s", out)
	}
}

func TestStatusAbsent(t *testing.T) {
	f := &statusFake{state: "absent"}
	out, err := computeStatus(context.Background(), f, "alice")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("want not-found, got out=%q err=%v", out, err)
	}
}
