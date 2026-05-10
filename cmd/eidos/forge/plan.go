package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/scheduler"
	"github.com/spf13/cobra"
)

// plansDir is the in-container dir where plan files live. var (not const)
// so tests can redirect it to a temp dir.
var plansDir = "/eidos/run/plans"

// gateConfigPathInContainer is the canonical config.toml path inside the
// mind-form container. Used by the in-container forge commands to render
// times in the mind-form's configured timezone.
const gateConfigPathInContainer = "/eidos/gate/config.toml"

// planAdd is the CLI-agnostic implementation of `forge plan add`. The
// cobra command in newPlanAddCmd is a thin wrapper.
//
// Exactly one of inDuration / atSpec must be non-empty. atSpec is parsed
// first as RFC3339, then as a unix-seconds integer.
func planAdd(dir string, now time.Time, hint, inDuration, atSpec string) (scheduler.Plan, string, error) {
	hasIn := inDuration != ""
	hasAt := atSpec != ""
	if hasIn == hasAt {
		return scheduler.Plan{}, "", errors.New("exactly one of --in or --at is required")
	}
	var at time.Time
	if hasIn {
		d, err := time.ParseDuration(inDuration)
		if err != nil {
			return scheduler.Plan{}, "", fmt.Errorf("invalid --in duration %q: %w", inDuration, err)
		}
		at = now.Add(d)
	} else {
		var err error
		at, err = parseAtSpec(atSpec)
		if err != nil {
			return scheduler.Plan{}, "", err
		}
	}
	plan, err := scheduler.Add(dir, now, hint, at)
	if err != nil {
		return scheduler.Plan{}, "", err
	}
	delta := at.Sub(now).Truncate(time.Second)
	msg := fmt.Sprintf("plan %s set for %s (in %s)\n",
		plan.ID, at.Format(time.RFC3339), delta)
	return plan, msg, nil
}

func parseAtSpec(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(n, 0), nil
	}
	return time.Time{}, fmt.Errorf("--at must be RFC3339 (e.g. 2026-05-09T14:00:00+08:00) or unix seconds, got %q", s)
}

// planList renders the active plan table in tz, matching the spec format.
func planList(dir string, now time.Time, tz *time.Location) (string, error) {
	plans, err := scheduler.List(dir)
	if err != nil {
		return "", err
	}
	if len(plans) == 0 {
		return "no plans\n", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-30s %-26s %-8s %s\n", "ID", "AT", "IN", "HINT")
	for _, p := range plans {
		at := time.Unix(p.At, 0).In(tz)
		delta := at.Sub(now).Truncate(time.Second)
		if delta < 0 {
			delta = 0
		}
		fmt.Fprintf(&b, "%-30s %-26s %-8s %s\n",
			p.ID, at.Format(time.RFC3339), formatDuration(delta), p.Hint)
	}
	return b.String(), nil
}

// planCancel cancels a plan by ID. Returns the scheduler error verbatim.
func planCancel(dir, id string) error {
	return scheduler.Cancel(dir, id)
}

// planClear removes every active plan and returns a one-line summary.
func planClear(dir string) (string, error) {
	n, err := scheduler.Clear(dir)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "no plans to clear\n", nil
	}
	return fmt.Sprintf("cleared %d plans\n", n), nil
}

// formatDuration is a compact "1h57m" / "1d8h" form for the IN column.
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "now"
	}
	d = d.Truncate(time.Second)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int(d / time.Minute)
	d -= time.Duration(mins) * time.Minute
	secs := int(d / time.Second)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm%ds", mins, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

// loadMindFormTZ returns the configured timezone or time.Local on error.
// Used only for human-friendly output formatting.
func loadMindFormTZ() *time.Location {
	cfg, err := config.Load(gateConfigPathInContainer)
	if err != nil || cfg.MindForm.TZ == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(cfg.MindForm.TZ)
	if err != nil {
		return time.Local
	}
	return loc
}

// newPlanInContainerCmd registers `eidos forge plan {add,list,cancel,clear}`
// when running inside the mind-form container (EIDOS_IN_CONTAINER=1).
func newPlanInContainerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Set, list, or cancel future wake signals",
	}
	cmd.AddCommand(newPlanAddCmd())
	cmd.AddCommand(newPlanListCmd())
	cmd.AddCommand(newPlanCancelCmd())
	cmd.AddCommand(newPlanClearCmd())
	return cmd
}

func newPlanAddCmd() *cobra.Command {
	var inDur, atSpec, hint string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Schedule a future wake. Use --in DURATION or --at TIMESTAMP.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, msg, err := planAdd(plansDir, time.Now(), hint, inDur, atSpec)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), msg)
			return nil
		},
	}
	cmd.Flags().StringVar(&inDur, "in", "", "fire in this much time (e.g. 2h, 30m)")
	cmd.Flags().StringVar(&atSpec, "at", "", "fire at RFC3339 timestamp or unix seconds")
	cmd.Flags().StringVar(&hint, "hint", "", "single-line agent-authored note (required)")
	_ = cmd.MarkFlagRequired("hint")
	return cmd
}

func newPlanListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List active plans (excludes already-fired plans)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tz := loadMindFormTZ()
			out, err := planList(plansDir, time.Now(), tz)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
}

func newPlanCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel an active plan by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return planCancel(plansDir, args[0])
		},
	}
}

func newPlanClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Cancel every active plan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			msg, err := planClear(plansDir)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), msg)
			return nil
		},
	}
}

// newPlanHostCmd registers the operator's host-side `eidos forge plan
// {list,cancel}` subcommands. Each execs into the named mind-form's
// container and runs the same in-container code path.
func newPlanHostCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Inspect or cancel a mind-form's scheduled wakes",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list <name>",
		Short: "List active plans in <name>'s container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return execInForge(cmd.Context(), args[0],
				[]string{"eidos", "forge", "plan", "list"})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "cancel <name> <id>",
		Short: "Cancel a plan in <name>'s container",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return execInForge(cmd.Context(), args[0],
				[]string{"eidos", "forge", "plan", "cancel", args[1]})
		},
	})
	return cmd
}

// execInForge runs argv inside the named mind-form's container via the
// existing forgectl docker client. Output flows through to the operator's
// terminal verbatim.
func execInForge(ctx context.Context, name string, argv []string) error {
	if err := forgectl.ValidateName(name); err != nil {
		return err
	}
	c, err := forgectl.New()
	if err != nil {
		return err
	}
	res, err := c.ContainerExec(ctx, forgectl.ContainerName(name), argv)
	if err != nil {
		return err
	}
	if len(res.Stdout) > 0 {
		fmt.Print(string(res.Stdout))
	}
	if len(res.Stderr) > 0 {
		fmt.Fprint(os.Stderr, string(res.Stderr))
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("forge plan exited %d", res.ExitCode)
	}
	return nil
}
