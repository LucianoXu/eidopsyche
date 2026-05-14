package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
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
		fmt.Fprintf(&sb, "phase:   %s\n", rs.Phase)
		if rs.Phase == "crashed" {
			fmt.Fprintf(&sb, "crashed: exit_code=%d", rs.CrashedExitCode)
			if rs.CrashedLastError != "" {
				fmt.Fprintf(&sb, " last_error=%q", rs.CrashedLastError)
			}
			sb.WriteString("\n")
		}
		if rs.SessionID != "" {
			short := rs.SessionID
			if len(short) > 8 {
				short = short[:8]
			}
			age := time.Since(time.Unix(rs.SessionStartedAt, 0)).Truncate(time.Second)
			fmt.Fprintf(&sb, "session: %s (age %s, %d turns)\n", short, age, rs.Turns)
		}
		if rs.LastEventAt > 0 {
			age := time.Since(time.Unix(rs.LastEventAt, 0)).Round(time.Second)
			fmt.Fprintf(&sb, "last_active: %s ago\n", age)
		}
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

	// Workspace state: desired (from config.toml) vs actual (from docker
	// inspect). Only shown when at least one side is non-empty.
	desired, actual, pendingRestart, werr := workspaceStatusDiff(ctx, c, name)
	if werr == nil && (len(desired) > 0 || len(actual) > 0) {
		fmt.Fprintf(&sb, "workspaces (desired): %s\n", formatWorkspaceEntries(desired))
		fmt.Fprintf(&sb, "workspaces (actual):  %s\n", formatWorkspaceEntries(actual))
		if pendingRestart {
			fmt.Fprintf(&sb, "⚠ pending restart: workspaces changed — run 'eidos forge restart %s' to apply\n", name)
		}
	}

	return sb.String(), nil
}

type workspaceStatusEntry struct {
	Name     string
	HostPath string
	Mode     string
}

// workspaceStatusDiff returns (desired, actual, pendingRestart). It is a
// local mirror of forge.workspace.list's logic but talks docker directly
// because forge status is currently a CLI-direct command. If the host
// gate config can't be read (no config.toml yet), desired is empty
// rather than an error — that's a fresh-install state.
func workspaceStatusDiff(ctx context.Context, c forgectl.Client, name string) (desired, actual []workspaceStatusEntry, pendingRestart bool, err error) {
	stateDir, derr := config.ResolveStateDir("")
	if derr == nil {
		if cfg, lerr := config.Load(filepath.Join(stateDir, "config.toml")); lerr == nil {
			for _, w := range cfg.Forge[name].Workspaces {
				desired = append(desired, workspaceStatusEntry{
					Name: w.Name, HostPath: w.HostPath, Mode: w.EffectiveMode(),
				})
			}
		}
	}

	mounts, merr := c.ContainerInspectMounts(ctx, forgectl.ContainerName(name))
	if merr == nil {
		const prefix = "/workspace/"
		for _, m := range mounts {
			if m.Type != forgectl.MountBind {
				continue
			}
			if !strings.HasPrefix(m.Target, prefix) {
				continue
			}
			n := strings.TrimPrefix(m.Target, prefix)
			if n == "" || strings.Contains(n, "/") {
				continue
			}
			mode := "rw"
			if m.ReadOnly {
				mode = "ro"
			}
			actual = append(actual, workspaceStatusEntry{Name: n, HostPath: m.Source, Mode: mode})
		}
	}

	pendingRestart = !workspaceEntriesEqual(desired, actual)
	return desired, actual, pendingRestart, nil
}

func workspaceEntriesEqual(a, b []workspaceStatusEntry) bool {
	if len(a) != len(b) {
		return false
	}
	idx := map[string]workspaceStatusEntry{}
	for _, e := range b {
		idx[e.Name] = e
	}
	for _, e := range a {
		other, ok := idx[e.Name]
		if !ok || other != e {
			return false
		}
	}
	return true
}

func formatWorkspaceEntries(entries []workspaceStatusEntry) string {
	if len(entries) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("%s (%s)", e.Name, e.Mode))
	}
	return strings.Join(parts, ", ")
}

// fetchRuntimeState execs `eidos forge runtime-state` in the container and
// parses its JSON. Returns (zero, false) on any exec / parse failure
// or schema-version mismatch so the caller falls back to the legacy
// `state:` line for old containers — printing nothing is safer than
// flowing v1 phase strings ("sleeping"/"awake") through the v2 struct
// (which would silently zero out v1 fields like wakes_in_session that
// have moved to "turns" in v2).
func fetchRuntimeState(ctx context.Context, c forgectl.Client, cont string) (RuntimeState, bool) {
	res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "runtime-state"})
	if err != nil || res.ExitCode != 0 {
		return RuntimeState{}, false
	}
	var rs RuntimeState
	if err := json.Unmarshal(res.Stdout, &rs); err != nil {
		return RuntimeState{}, false
	}
	if rs.V != runtimeStateSchemaVersion {
		return RuntimeState{}, false
	}
	return rs, true
}
