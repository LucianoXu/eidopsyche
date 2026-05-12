package forge

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

type configFlags struct {
	model    string
	modelSet bool // distinguishes "--model not given" from "--model \"\""

	heartbeatInterval    string
	heartbeatIntervalSet bool

	dreamIdleWait    string
	dreamIdleWaitSet bool

	dreamCloseGrace    string
	dreamCloseGraceSet bool
}

func newConfigCmd() *cobra.Command {
	var f configFlags
	cmd := &cobra.Command{
		Use:   "config <name>",
		Short: "Update a mind-form's runtime configuration (in-container gate)",
		Long: `Update a runtime config key inside the mind-form's gate. Supported flags:

  --model <id>                Pin the claude model used by agent-runner.
                              Re-read at every wake — no restart needed.
  --heartbeat-interval <dur>  HeartBeat cadence (e.g. 2m, 30m, 2h).
                              Hot-reloaded by the daemon's apply hook —
                              no container restart needed.
  --dream-idle-wait <dur>     Max wait for claude to reach idle after a
                              dream-end signal (e.g. 5m). Hot-reloaded.
  --dream-close-grace <dur>   Max wait for claude to exit after stdin
                              close during rotation (e.g. 60s). Hot-reloaded.

Pass at most one flag per invocation. Internally dispatches to
` + "`eidos gate config set <key> <value>`" + ` inside the container so
the registry's validation + apply hook chain runs.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			f.modelSet = cmd.Flags().Changed("model")
			f.heartbeatIntervalSet = cmd.Flags().Changed("heartbeat-interval")
			f.dreamIdleWaitSet = cmd.Flags().Changed("dream-idle-wait")
			f.dreamCloseGraceSet = cmd.Flags().Changed("dream-close-grace")
			argv, err := buildConfigDockerArgv(name, f)
			if err != nil {
				return err
			}
			c := exec.Command("docker", argv...) //nolint:gosec // argv from validated inputs
			c.Stdin = os.Stdin
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			return c.Run()
		},
	}
	cmd.Flags().StringVar(&f.model, "model", "", `pin the claude model id (e.g. claude-sonnet-4-7); pass --model "" to unpin`)
	cmd.Flags().StringVar(&f.heartbeatInterval, "heartbeat-interval", "",
		`HeartBeat cadence (e.g. 2m, 30m, 2h). Supported: 1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m,1h,2h,3h,4h,6h,8h,12h,24h. Empty unsets the override and lets the supervisor use the 2h default. Hot-reloaded — no container restart.`)
	cmd.Flags().StringVar(&f.dreamIdleWait, "dream-idle-wait", "",
		`max wait for claude to reach idle after a dream-end signal (e.g. 5m, 30s). Hot-reloaded — no container restart.`)
	cmd.Flags().StringVar(&f.dreamCloseGrace, "dream-close-grace", "",
		`max wait for claude to exit after stdin close during rotation (e.g. 60s, 2m). Hot-reloaded — no container restart.`)
	return cmd
}

// buildConfigDockerArgv returns the `docker exec ...` argv for a single
// config update. Pre-validates on the host so a typo aborts before we
// shell out. Mutually exclusive flags: the helper rejects "more than one set"
// because each flag has different downstream behaviour and pairing them
// complicates the success/failure semantics for no gain.
//
// Empty --model is meaningful: --model "" clears a previously pinned
// model. Empty --heartbeat-interval is meaningful: --heartbeat-interval ""
// clears a previously set override and falls back to the default.
// The *Set booleans distinguish these from "operator did not pass the flag at all".
func buildConfigDockerArgv(name string, f configFlags) ([]string, error) {
	setCount := 0
	for _, set := range []bool{f.modelSet, f.heartbeatIntervalSet, f.dreamIdleWaitSet, f.dreamCloseGraceSet} {
		if set {
			setCount++
		}
	}
	if setCount == 0 {
		return nil, fmt.Errorf("forge config requires one of --model | --heartbeat-interval | --dream-idle-wait | --dream-close-grace")
	}
	if setCount > 1 {
		return nil, fmt.Errorf("forge config: pass at most one flag per invocation (each triggers different downstream behaviour)")
	}
	if f.modelSet {
		if err := config.ValidateModelID(f.model); err != nil {
			return nil, fmt.Errorf("invalid --model: %w", err)
		}
		return []string{
			"exec",
			forgectl.ContainerName(name),
			"eidos", "gate", "config", "set",
			"mindform.model", f.model,
		}, nil
	}
	if f.heartbeatIntervalSet {
		if err := config.ValidateHeartbeatInterval(f.heartbeatInterval); err != nil {
			return nil, fmt.Errorf("invalid --heartbeat-interval: %w", err)
		}
		return []string{
			"exec",
			forgectl.ContainerName(name),
			"eidos", "gate", "config", "set",
			"heartbeat.interval", f.heartbeatInterval,
		}, nil
	}
	if f.dreamIdleWaitSet {
		return []string{
			"exec",
			forgectl.ContainerName(name),
			"eidos", "gate", "config", "set",
			"mindform.dream_idle_wait", f.dreamIdleWait,
		}, nil
	}
	// dreamCloseGraceSet
	return []string{
		"exec",
		forgectl.ContainerName(name),
		"eidos", "gate", "config", "set",
		"mindform.dream_close_grace", f.dreamCloseGrace,
	}, nil
}
