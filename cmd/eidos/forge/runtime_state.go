package forge

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/agentloop"
	"github.com/LucianoXu/eidopsyche/internal/authstate"
	"github.com/spf13/cobra"
)

// In-container paths consulted by `forge runtime-state`. Vars (not
// consts) so tests can substitute temp paths; mirrors dreamStatePath
// pattern in dream.go. agentStateRuntimePath is declared in
// agent_state.go (shared with the agent-state subcommand).
var (
	authStatePath    = authstate.Path
	procStatPath     = "/proc/1/stat"
	procBootTimePath = "/proc/stat"
)

// RuntimeState is the JSON shape emitted by `eidos forge runtime-state`.
// Schema v2 — see
// docs/superpowers/specs/2026-05-12-forge-status-always-on-alignment-design.md
// for the field semantics. The host's `forge status` and `forge list`
// parse this verbatim.
type RuntimeState struct {
	V                  int    `json:"v"`
	Phase              string `json:"phase"`
	AuthRequired       bool   `json:"auth_required"`
	ContainerStartedAt int64  `json:"container_started_at"`

	// Session fields, omitted when agent-state.json is absent.
	// Turns is the live counter from agent-state.json (every result-event
	// is one turn; signal-driven and mailbox-driven turns both count).
	SessionID        string `json:"session_id,omitempty"`
	SessionStartedAt int64  `json:"session_started_at,omitempty"`
	Turns            int    `json:"turns,omitempty"`
	LastEventAt      int64  `json:"last_event_at,omitempty"`
}

// runtimeStateSchemaVersion is bumped when RuntimeState's JSON contract
// changes. Pre-production: host parser is updated in lockstep, no v1
// fallback path.
const runtimeStateSchemaVersion = 2

// newRuntimeStateCmd is an in-container hidden subcommand that prints the
// current phase as JSON. The host's `forge status` and `forge list` parse
// it via docker-exec.
//
// Hidden because operators are expected to use `forge status` / `forge
// list`, not invoke this directly. Kept JSON-only (no text format) so the
// host parser has one shape to handle.
func newRuntimeStateCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "runtime-state",
		Short:  "Internal: print runtime phase JSON for forge status",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rs := computeRuntimeState(time.Now())
			body, err := json.MarshalIndent(rs, "", "  ")
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

// computeRuntimeState derives the phase from the on-disk authstate and
// agent-state.json signals. Pure-ish (`now` injected for testability;
// file paths are package-level vars overridable from tests).
//
// Phase derivation (single source of truth: agent-state.json under the
// always-on agent-loop, with authstate as a short-circuit):
//
//	authstate present                 → "auth-required"
//	agent-state.json missing / empty  → "starting"
//	agentState.Dreaming               → "dreaming"
//	agentState.ClaudeBusy             → "thinking"
//	otherwise                         → "idle"
//
// "offline" is host-derived (the container itself isn't running) and
// never emitted by this command.
//
// The `now` parameter is currently unused but retained so callers and
// tests don't need to be reworked when future phases need wall-clock
// comparisons (e.g. stale-state detection).
func computeRuntimeState(_ time.Time) RuntimeState {
	rs := RuntimeState{V: runtimeStateSchemaVersion}
	rs.ContainerStartedAt = readContainerStartTime()

	if state, _ := authstate.ReadAt(authStatePath); state != nil {
		rs.AuthRequired = true
		rs.Phase = "auth-required"
		return rs
	}

	as, err := agentloop.ReadAgentState(agentStateRuntimePath)
	if err != nil || as.V == 0 {
		rs.Phase = "starting"
		return rs
	}

	switch {
	case as.Dreaming:
		rs.Phase = "dreaming"
	case as.ClaudeBusy:
		rs.Phase = "thinking"
	default:
		rs.Phase = "idle"
	}

	rs.SessionID = as.SessionID
	rs.SessionStartedAt = as.SessionStartedAt
	rs.Turns = as.WakesInSession
	rs.LastEventAt = as.LastEventAt
	return rs
}

// readContainerStartTime parses /proc/1/stat field 22 (start_time, in clock
// ticks since boot) and combines it with /proc/stat's btime to yield a
// unix epoch second. Best-effort: returns 0 on any error.
//
// This is the documented Linux way to get PID 1's start time. With
// CLOCK_TICK = 100 (sysconf(_SC_CLK_TCK), virtually universal on Linux)
// we get second-level resolution, which is sufficient.
func readContainerStartTime() int64 {
	statBody, err := os.ReadFile(procStatPath)
	if err != nil {
		return 0
	}
	btimeBody, err := os.ReadFile(procBootTimePath)
	if err != nil {
		return 0
	}

	var btime int64
	for _, line := range strings.Split(string(btimeBody), "\n") {
		if strings.HasPrefix(line, "btime ") {
			if _, err := fmt.Sscanf(line, "btime %d", &btime); err != nil {
				return 0
			}
			break
		}
	}
	if btime == 0 {
		return 0
	}

	// /proc/<pid>/stat: pid (comm) state ... — comm can contain whitespace
	// and parens, so split on the LAST ')' to be safe.
	s := string(statBody)
	rparen := strings.LastIndex(s, ")")
	if rparen < 0 || rparen+1 >= len(s) {
		return 0
	}
	fields := strings.Fields(s[rparen+1:])
	// After ')' the fields are state(0) ppid(1) ... start_time is field 22
	// of the full record, so index 19 (22 - 3 for pid/comm/state).
	const startTimeIdx = 19
	if len(fields) <= startTimeIdx {
		return 0
	}
	var ticks int64
	if _, err := fmt.Sscanf(fields[startTimeIdx], "%d", &ticks); err != nil {
		return 0
	}
	const clkTck = 100
	return btime + ticks/clkTck
}
