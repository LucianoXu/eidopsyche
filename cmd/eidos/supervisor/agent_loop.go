//go:build !windows

package supervisor

import (
	"os"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/agentloop"
	"github.com/spf13/cobra"
)

// In-container paths owned by agent-loop.
const (
	agentLoopGateConfigPath   = "/eidos/gate/config.toml"
	agentLoopOntologyDir      = "/eidos/ontology"
	agentLoopSessionStatePath = "/eidos/run/session.json"
	agentLoopDreamStatePath   = "/eidos/run/dream-state.json"
	agentLoopAgentStatePath   = "/eidos/run/agent-state.json"
	agentLoopTranscriptsDir   = "/eidos/run/transcripts"
	agentLoopAgentLockPath    = "/eidos/run/agent.lock"
	agentLoopIdentityPath     = "/eidos/ontology/self/identity.md"
	agentLoopClaudeDir        = "/eidos/ontology/.claude"
)

// newAgentLoopCmd returns the internal `eidos supervisor agent-loop`
// subcommand. It is spawned by the supervisor as a long-lived child
// (one per mind-form) and holds a single persistent claude subprocess
// open across many wakes. Reads wake.Signal JSONL from stdin (piped
// by supervisor's Forward callback) and emits stream-json events on
// stdout/stderr.
func newAgentLoopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "agent-loop",
		Short:  "Internal: long-lived per-mindform claude harness (PID-1 child)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return agentloop.Run(cmd.Context(), agentloop.RunOpts{
				ClaudeBin:        "claude",
				OntologyDir:      agentLoopOntologyDir,
				ClaudeDir:        agentLoopClaudeDir,
				IdentityPath:     agentLoopIdentityPath,
				SessionStatePath: agentLoopSessionStatePath,
				DreamStatePath:   agentLoopDreamStatePath,
				AgentStatePath:   agentLoopAgentStatePath,
				TranscriptsDir:   agentLoopTranscriptsDir,
				AgentLockPath:    agentLoopAgentLockPath,
				ConfigPath:       agentLoopGateConfigPath,
				WakeStdin:        os.Stdin,
				// Stage 16.1 will wire these from mindform.dream_idle_wait /
				// mindform.dream_close_grace config keys; for now use the
				// plan's defaults.
				IdleWait:   5 * time.Minute,
				CloseGrace: 60 * time.Second,
			})
		},
	}
	return cmd
}
