package agentloop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SchemaVersion is bumped when the on-disk agent-state.json format
// changes.
const SchemaVersion = 1

// AgentState is the on-disk snapshot at /eidos/run/agent-state.json.
// gate daemon's `agent.state` IPC handler reads this file verbatim.
type AgentState struct {
	V                    int    `json:"v"`
	ClaudeBusy           bool   `json:"claude_busy"`
	SinceUnix            int64  `json:"since_unix"`
	LastEventType        string `json:"last_event_type,omitempty"`
	LastEventAt          int64  `json:"last_event_at,omitempty"`
	SessionID            string `json:"session_id,omitempty"`
	SessionStartedAt     int64  `json:"session_started_at,omitempty"`
	WakesInSession       int    `json:"wakes_in_session"`
	OutstandingWakesSent int    `json:"outstanding_wakes_sent"`
	ResultsSeen          int    `json:"results_seen"`
	Dreaming             bool   `json:"dreaming,omitempty"`
}

// WriteAgentState atomically replaces the file at path with the
// serialized state. Uses tmp + rename + fsync-dir (same pattern as
// internal/sessionstate). Caller must ensure path's parent dir exists.
func WriteAgentState(path string, st AgentState) error {
	if st.V == 0 {
		st.V = SchemaVersion
	}
	body, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("marshal agent-state: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "agent-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	cleanup = false
	return nil
}

// ReadAgentState reads the file at path. Missing file returns zero
// value + nil error (caller treats as "agent-loop not yet running").
func ReadAgentState(path string) (AgentState, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AgentState{}, nil
		}
		return AgentState{}, fmt.Errorf("read agent-state: %w", err)
	}
	var st AgentState
	if err := json.Unmarshal(body, &st); err != nil {
		return AgentState{}, fmt.Errorf("unmarshal agent-state: %w", err)
	}
	return st, nil
}
