package agentloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentState_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-state.json")

	st := AgentState{
		V:                    1,
		ClaudeBusy:           true,
		SinceUnix:            1715500000,
		LastEventType:        "assistant",
		LastEventAt:          1715500005,
		SessionID:            "abc-123",
		SessionStartedAt:     1715499500,
		WakesInSession:       7,
		OutstandingWakesSent: 8,
		ResultsSeen:          7,
		Dreaming:             false,
	}

	if err := WriteAgentState(path, st); err != nil {
		t.Fatalf("WriteAgentState: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	var got AgentState
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != st {
		t.Errorf("round-trip mismatch:\n got: %+v\nwant: %+v", got, st)
	}
}

func TestAgentState_AtomicWrite_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-state.json")

	if err := WriteAgentState(path, AgentState{V: 1, ClaudeBusy: false}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteAgentState(path, AgentState{V: 1, ClaudeBusy: true}); err != nil {
		t.Fatalf("second write: %v", err)
	}

	body, _ := os.ReadFile(path)
	var got AgentState
	_ = json.Unmarshal(body, &got)
	if !got.ClaudeBusy {
		t.Errorf("second write did not overwrite first")
	}
}

func TestAgentState_NoStrayTmpFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-state.json")

	if err := WriteAgentState(path, AgentState{V: 1}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected exactly one entry, got %d: %v", len(entries), entries)
	}
}
