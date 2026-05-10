package forge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/authstate"
	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/sessionstate"
	"github.com/LucianoXu/eidopsyche/internal/wake"
	"github.com/spf13/cobra"
)

// In-container paths consulted by `forge runtime-state`. Vars (not
// consts) so tests can substitute temp paths; mirrors plansDir /
// dreamStatePath pattern.
var (
	wakeDir                 = "/eidos/run/wake"
	authStatePath           = authstate.Path
	procStatPath            = "/proc/1/stat"
	procBootTimePath        = "/proc/stat"
	sessionStateRuntimePath = "/eidos/run/session.json"
)

// RuntimeState is the JSON shape emitted by `eidos forge runtime-state`.
// See docs/superpowers/specs/2026-05-10-mindform-status-and-observer-design.md
// §3.2 for the field semantics.
type RuntimeState struct {
	V                       int    `json:"v"`
	Phase                   string `json:"phase"`
	WakeReason              string `json:"wake_reason,omitempty"`
	ActiveWakeID            string `json:"active_wake_id,omitempty"`
	Dreaming                bool   `json:"dreaming"`
	AuthRequired            bool   `json:"auth_required"`
	SincePhaseChangeSeconds *int64 `json:"since_phase_change_seconds,omitempty"`
	ContainerStartedAt      int64  `json:"container_started_at"`

	// Session fields, omitted when session.json is absent. Wake counter
	// is the in-progress count (incremented after each wake completes).
	// See docs/superpowers/specs/2026-05-10-persistent-wake-context-design.md.
	SessionID        string `json:"session_id,omitempty"`
	SessionStartedAt int64  `json:"session_started_at,omitempty"`
	WakesInSession   int    `json:"wakes_in_session,omitempty"`
}

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

// computeRuntimeState derives the phase from the on-disk wake / dream-state
// / authstate signals. Pure-ish (`now` injected for testability; file paths
// are package-level vars overridable from tests).
//
// Phase rules (from spec §3.1):
//   - active.json present + CurrentlyDreaming → "awake+dreaming"
//   - active.json present                      → "awake"
//   - otherwise                                → "sleeping"
//
// "offline" is host-derived (the container itself isn't running) and never
// emitted by this command.
func computeRuntimeState(now time.Time) RuntimeState {
	rs := RuntimeState{V: 1}

	ds, _ := dreamstate.Read(dreamStatePath)
	rs.Dreaming = ds.CurrentlyDreaming

	active, _ := wake.ReadActive(wakeDir)
	if active != nil {
		rs.Phase = "awake"
		rs.WakeReason = string(active.Reason)
		rs.ActiveWakeID = active.ID
		if rs.Dreaming {
			rs.Phase = "awake+dreaming"
		}
		if info, err := os.Stat(filepath.Join(wakeDir, "active.json")); err == nil {
			since := int64(now.Sub(info.ModTime()).Seconds())
			if since < 0 {
				since = 0
			}
			rs.SincePhaseChangeSeconds = &since
		}
	} else {
		rs.Phase = "sleeping"
	}

	if state, _ := authstate.ReadAt(authStatePath); state != nil {
		rs.AuthRequired = true
	}

	rs.ContainerStartedAt = readContainerStartTime()

	// Surface session info when session.json is present. A corrupt file
	// is silently skipped (status command falls back gracefully).
	if sess, err := sessionstate.Read(sessionStateRuntimePath); err == nil && sess.SessionID != "" {
		rs.SessionID = sess.SessionID
		rs.SessionStartedAt = sess.SessionStartedAt
		rs.WakesInSession = sess.WakesInSession
	}

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
