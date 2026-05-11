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
}

func newConfigCmd() *cobra.Command {
	var f configFlags
	cmd := &cobra.Command{
		Use:   "config <name>",
		Short: "Update a mind-form's runtime configuration (in-container gate)",
		Long: `Update a runtime config key inside the mind-form's gate. Supported flags:

  --model <id>                Pin the claude model used by agent-runner.
                              Re-read at every wake — no restart needed.
  --heartbeat-interval <dur>  HeartBeat cadence (e.g. 2m, 30m, 2h). The
                              supervisor only reads [heartbeat] interval
                              at PID-1 startup, so this surface
                              auto-restarts the container after writing.

Pass at most one flag per invocation: each has its own downstream
behaviour and we keep them separate so 'write + restart' semantics
stay simple.

Internally dispatches to ` + "`eidos gate config set <key> <value>`" + `
inside the container so the registry's validation runs once.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			f.modelSet = cmd.Flags().Changed("model")
			f.heartbeatIntervalSet = cmd.Flags().Changed("heartbeat-interval")
			argv, err := buildConfigDockerArgv(name, f)
			if err != nil {
				return err
			}
			c := exec.Command("docker", argv...) //nolint:gosec // argv from validated inputs
			c.Stdin = os.Stdin
			c.Stdout = cmd.OutOrStdout()
			c.Stderr = cmd.ErrOrStderr()
			if err := c.Run(); err != nil {
				return err
			}
			if f.heartbeatIntervalSet {
				return restartContainerForHeartbeat(cmd, name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&f.model, "model", "", `pin the claude model id (e.g. claude-sonnet-4-7); pass --model "" to unpin`)
	cmd.Flags().StringVar(&f.heartbeatInterval, "heartbeat-interval", "",
		`HeartBeat cadence (e.g. 2m, 30m, 2h). Supported: 1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m,1h,2h,3h,4h,6h,8h,12h,24h. Empty unsets the override and lets the supervisor use the 2h default. Container auto-restarts so the change takes effect.`)
	return cmd
}

// buildConfigDockerArgv returns the `docker exec ...` argv for a single
// config update. Pre-validates on the host so a typo aborts before we
// shell out. Mutually exclusive flags: the helper rejects "both set"
// because each flag has different downstream behaviour (heartbeat
// needs a container restart; model does not) and pairing them
// complicates the success/failure semantics for no gain.
//
// Empty --model is meaningful: --model "" clears a previously pinned
// model. Empty --heartbeat-interval is meaningful: --heartbeat-interval ""
// clears a previously set override and falls back to the default.
// f.modelSet / f.heartbeatIntervalSet distinguish these from "operator
// did not pass the flag at all".
func buildConfigDockerArgv(name string, f configFlags) ([]string, error) {
	if !f.modelSet && !f.heartbeatIntervalSet {
		return nil, fmt.Errorf("forge config requires one of --model | --heartbeat-interval")
	}
	if f.modelSet && f.heartbeatIntervalSet {
		return nil, fmt.Errorf("forge config: pass at most one of --model | --heartbeat-interval per invocation (each triggers different downstream behaviour)")
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
	// heartbeatIntervalSet
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

// restartContainerForHeartbeat issues `docker restart <container>` so
// the supervisor re-reads /eidos/gate/config.toml and re-renders the
// busybox-cron crontab from the new [heartbeat] interval. Most config
// keys (model, log_level, ...) are re-read on every wake, but the
// crontab is only written at PID-1 startup, so heartbeat is the one
// key that requires a process bounce.
func restartContainerForHeartbeat(cmd *cobra.Command, name string) error {
	fmt.Fprintf(cmd.ErrOrStderr(), "restarting %s so the new heartbeat interval takes effect...\n", forgectl.ContainerName(name))
	rc := exec.Command("docker", "restart", forgectl.ContainerName(name)) //nolint:gosec
	rc.Stdout = cmd.OutOrStdout()
	rc.Stderr = cmd.ErrOrStderr()
	if err := rc.Run(); err != nil {
		return fmt.Errorf("docker restart %s: %w (the config change was written but did not take effect)", forgectl.ContainerName(name), err)
	}
	return nil
}
