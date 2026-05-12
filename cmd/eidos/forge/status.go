package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/agentloop"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <name>",
		Short: "Show mind-form runtime status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			c, err := forgectl.New()
			if err != nil {
				return err
			}
			out, err := computeStatus(cmd.Context(), c, name)
			if err != nil {
				return err
			}
			cmd.Print(out)
			return nil
		},
	}
}

func computeStatus(ctx context.Context, c forgectl.Client, name string) (string, error) {
	cont := forgectl.ContainerName(name)
	state, err := c.ContainerInspectState(ctx, cont)
	if err != nil {
		return "", err
	}
	if state == "absent" {
		return "", fmt.Errorf("mind-form %q not found", name)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "name:    %s\n", name)

	if state != "running" {
		// Container down → phase is offline; nothing else to surface.
		fmt.Fprintf(&sb, "phase:   offline (%s)\n", state)
		return sb.String(), nil
	}

	// Try to fetch structured runtime-state. On any failure (older image
	// without the subcommand, exec error, malformed JSON), fall back to
	// the legacy `state: running` line so old images stay supported.
	if rs, ok := fetchRuntimeState(ctx, c, cont); ok {
		fmt.Fprintf(&sb, "phase:   %s\n", formatPhase(rs))
		if rs.SessionID != "" {
			short := rs.SessionID
			if len(short) > 8 {
				short = short[:8]
			}
			age := time.Since(time.Unix(rs.SessionStartedAt, 0)).Truncate(time.Second)
			fmt.Fprintf(&sb, "session: %s (age %s, %d wakes)\n", short, age, rs.WakesInSession)
		}
	} else {
		fmt.Fprintf(&sb, "state:   %s\n", state)
	}

	// Best-effort agent-state: thinking + last_active. Omitted when the
	// agent-loop has not started yet or the exec fails (older images).
	if as, ok := fetchAgentState(ctx, c, cont); ok {
		if as.ClaudeBusy {
			fmt.Fprintf(&sb, "thinking: yes\n")
		} else {
			fmt.Fprintf(&sb, "thinking: no\n")
		}
		if as.LastEventAt > 0 {
			age := time.Since(time.Unix(as.LastEventAt, 0)).Round(time.Second)
			fmt.Fprintf(&sb, "last_active: %s ago\n", age)
		}
	}

	// Best-effort whoami via in-container reflection.
	res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "whoami"})
	if err != nil || res.ExitCode != 0 {
		fmt.Fprintf(&sb, "whoami:  unavailable\n")
	} else {
		sb.WriteString(string(res.Stdout))
	}
	// Plans + dreams summary. Best-effort; older mind-form images
	// without status-detail return non-zero — silently omit.
	dres, derr := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "status-detail"})
	if derr == nil && dres.ExitCode == 0 {
		sb.Write(dres.Stdout)
	}
	return sb.String(), nil
}

// fetchRuntimeState execs `eidos forge runtime-state` in the container and
// parses its JSON. Returns (zero, false) on any exec / parse failure so the
// caller can fall back to the legacy `state:` line on older images.
func fetchRuntimeState(ctx context.Context, c forgectl.Client, cont string) (RuntimeState, bool) {
	res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "runtime-state"})
	if err != nil || res.ExitCode != 0 {
		return RuntimeState{}, false
	}
	var rs RuntimeState
	if err := json.Unmarshal(res.Stdout, &rs); err != nil {
		return RuntimeState{}, false
	}
	return rs, true
}

// fetchAgentState execs `eidos forge agent-state` in the container and
// parses its JSON. Returns (zero, false) on any exec / parse failure so the
// caller can silently omit the thinking + last_active lines on older images
// or when the agent-loop has not started yet.
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

// formatPhase renders the phase line: `awake (mindgate)` /
// `awake+dreaming (heartbeat)` / `sleeping`. The wake reason is appended
// only when the agent is awake AND the reason is known.
func formatPhase(rs RuntimeState) string {
	if (rs.Phase == "awake" || rs.Phase == "awake+dreaming") && rs.WakeReason != "" {
		return fmt.Sprintf("%s (%s)", rs.Phase, rs.WakeReason)
	}
	return rs.Phase
}
