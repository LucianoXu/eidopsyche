package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// agentStateRuntimePath is the in-container path of agent-state.json.
// Var (not const) so tests can substitute a temp file.
var agentStateRuntimePath = "/eidos/run/agent-state.json"

// agentStateMethod is the daemon handler for `agent.state`. It reads
// /eidos/run/agent-state.json verbatim and returns its parsed contents
// as a map. A missing file returns a zero-value map (claude_busy=false)
// so observers don't have to special-case "agent-loop not yet running".
func agentStateMethod(_ context.Context, _ *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	body, err := os.ReadFile(agentStateRuntimePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{"v": 1, "claude_busy": false}, nil
		}
		return nil, &ipc.Error{Code: ipc.ErrInternal, Message: fmt.Sprintf("read agent-state: %v", err)}
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInternal, Message: fmt.Sprintf("unmarshal agent-state: %v", err)}
	}
	return out, nil
}
