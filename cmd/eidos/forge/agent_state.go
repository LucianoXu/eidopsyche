package forge

import (
	"encoding/json"

	"github.com/LucianoXu/eidopsyche/internal/agentloop"
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
