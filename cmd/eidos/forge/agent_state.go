package forge

import (
	"context"
	"encoding/json"

	"github.com/LucianoXu/eidopsyche/internal/agentloop"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

// agentStateRuntimePath is the in-container path of agent-state.json.
// Var (not const) so tests can substitute a temp path.
var agentStateRuntimePath = "/eidos/run/agent-state.json"

// newAgentStateCmd is an in-container hidden subcommand that prints the
// current agent-state.json as JSON. The host's `forge status` parses it
// via docker-exec to surface the thinking + last_active lines.
//
// Hidden because operators are expected to use `forge status`, not this
// command directly.
func newAgentStateCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "agent-state",
		Short:  "Internal: print agent-state JSON for forge status",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := agentloop.ReadAgentState(agentStateRuntimePath)
			if err != nil {
				return err
			}
			body, err := json.Marshal(st)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if _, err := out.Write(body); err != nil {
				return err
			}
			_, err = out.Write([]byte("\n"))
			return err
		},
	}
}

// fetchAgentState execs `eidos forge agent-state` in the container and
// parses its JSON. Returns (zero, false) on any exec / parse failure so
// the caller can silently omit derived fields on older images or when
// the agent-loop has not started yet. Used by `forge watch` to poll the
// busy / idle transitions for `--wake current` follow mode.
func fetchAgentState(ctx context.Context, c forgectl.Client, cont string) (agentloop.AgentState, bool) {
	res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "agent-state"})
	if err != nil || res.ExitCode != 0 {
		return agentloop.AgentState{}, false
	}
	var as agentloop.AgentState
	if err := json.Unmarshal(res.Stdout, &as); err != nil {
		return agentloop.AgentState{}, false
	}
	return as, true
}
