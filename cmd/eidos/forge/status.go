package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	} else {
		fmt.Fprintf(&sb, "state:   %s\n", state)
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

// formatPhase renders the phase line: `awake (mindgate)` /
// `awake+dreaming (heartbeat)` / `sleeping`. The wake reason is appended
// only when the agent is awake AND the reason is known.
func formatPhase(rs RuntimeState) string {
	if (rs.Phase == "awake" || rs.Phase == "awake+dreaming") && rs.WakeReason != "" {
		return fmt.Sprintf("%s (%s)", rs.Phase, rs.WakeReason)
	}
	return rs.Phase
}
