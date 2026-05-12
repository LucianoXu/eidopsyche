package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// promptDumpFake embeds fakeClient (defined in create_test.go) for the
// boilerplate methods and overrides the two methods this wrapper uses.
type promptDumpFake struct {
	fakeClient
	state    string
	stateErr error
	execArgs []string
	execOut  []byte
	execExit int
	execErr  error
}

func (f *promptDumpFake) ContainerInspectState(_ context.Context, _ string) (string, error) {
	return f.state, f.stateErr
}
func (f *promptDumpFake) ContainerExec(_ context.Context, _ string, cmd []string) (forgectl.ExecResult, error) {
	f.execArgs = cmd
	return forgectl.ExecResult{Stdout: f.execOut, ExitCode: f.execExit}, f.execErr
}

func TestPromptDumpHost_ContainerNotRunning(t *testing.T) {
	c := &promptDumpFake{state: "exited"}
	var out, errOut bytes.Buffer
	err := runPromptDumpHost(context.Background(), promptDumpHostInput{
		Client: c, Name: "alice", Stdout: &out, Stderr: &errOut,
	})
	if err == nil {
		t.Fatal("expected error when container not running")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error=%v", err)
	}
}

func TestPromptDumpHost_AbsentContainerErrors(t *testing.T) {
	c := &promptDumpFake{state: "absent"}
	err := runPromptDumpHost(context.Background(), promptDumpHostInput{
		Client: c, Name: "alice", Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestPromptDumpHost_ExecArgsIncludeMindformName(t *testing.T) {
	c := &promptDumpFake{
		state:   "running",
		execOut: []byte(`{"captured_at":"x","captured_from":{"mindform":"alice"}}` + "\n"),
	}
	var out bytes.Buffer
	err := runPromptDumpHost(context.Background(), promptDumpHostInput{
		Client: c, Name: "alice", Prompt: "ping", Stdout: &out, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []string{"eidos", "forge", "prompt-dump", "--mindform-name", "alice", "--prompt", "ping"}
	for _, w := range want {
		found := false
		for _, a := range c.execArgs {
			if a == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("execArgs missing %q: %v", w, c.execArgs)
		}
	}
	if !strings.Contains(out.String(), `"mindform":"alice"`) {
		t.Errorf("stdout did not contain envelope: %s", out.String())
	}
}

func TestPromptDumpHost_BarePassedThrough(t *testing.T) {
	c := &promptDumpFake{
		state:   "running",
		execOut: []byte(`{}` + "\n"),
	}
	err := runPromptDumpHost(context.Background(), promptDumpHostInput{
		Client: c, Name: "alice", Bare: true, Stdout: &bytes.Buffer{}, Stderr: os.Stderr,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	found := false
	for _, a := range c.execArgs {
		if a == "--bare" {
			found = true
		}
	}
	if !found {
		t.Errorf("--bare not in execArgs: %v", c.execArgs)
	}
}

func TestPromptDumpHost_OutPathDualWrite(t *testing.T) {
	envJSON, _ := json.Marshal(map[string]any{
		"captured_at": "x",
		"request":     map[string]any{"system": []any{}},
	})
	c := &promptDumpFake{
		state:   "running",
		execOut: append(envJSON, '\n'),
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "snap")
	err := runPromptDumpHost(context.Background(), promptDumpHostInput{
		Client: c, Name: "alice", OutPath: base,
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(base + ".json"); err != nil {
		t.Errorf("json missing: %v", err)
	}
	if _, err := os.Stat(base + ".md"); err != nil {
		t.Errorf("md missing: %v", err)
	}
}

func TestPromptDumpHost_ExecNonZeroExit(t *testing.T) {
	c := &promptDumpFake{
		state:    "running",
		execOut:  []byte(""),
		execExit: 2,
	}
	err := runPromptDumpHost(context.Background(), promptDumpHostInput{
		Client: c, Name: "alice", Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected non-zero exit error")
	}
}
