package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentStateMethod_ReadsFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "agent-state.json")
	body := `{"v":1,"claude_busy":true,"session_id":"abc"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	oldPath := agentStateRuntimePath
	agentStateRuntimePath = path
	defer func() { agentStateRuntimePath = oldPath }()

	out, ipcErr := agentStateMethod(context.Background(), nil, nil, json.RawMessage(`{}`))
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", out)
	}
	if m["claude_busy"] != true {
		t.Errorf("claude_busy: got %v, want true", m["claude_busy"])
	}
	if m["session_id"] != "abc" {
		t.Errorf("session_id: got %v, want abc", m["session_id"])
	}
}

func TestAgentStateMethod_MissingFileReturnsZero(t *testing.T) {
	oldPath := agentStateRuntimePath
	agentStateRuntimePath = "/tmp/does-not-exist-eidos-test-agentstate-stage17"
	defer func() { agentStateRuntimePath = oldPath }()

	out, ipcErr := agentStateMethod(context.Background(), nil, nil, json.RawMessage(`{}`))
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	m := out.(map[string]any)
	if m["claude_busy"] != false {
		t.Errorf("missing file should yield claude_busy=false, got %v", m["claude_busy"])
	}
	if m["v"] != float64(1) && m["v"] != 1 {
		// JSON unmarshalling yields float64; raw map literal yields int.
		// Accept either.
		t.Errorf("v: got %v, want 1", m["v"])
	}
}

func TestAgentStateMethod_CorruptFileReturnsError(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "agent-state.json")
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldPath := agentStateRuntimePath
	agentStateRuntimePath = path
	defer func() { agentStateRuntimePath = oldPath }()

	_, ipcErr := agentStateMethod(context.Background(), nil, nil, json.RawMessage(`{}`))
	if ipcErr == nil {
		t.Errorf("expected error on corrupt agent-state.json")
	}
}
