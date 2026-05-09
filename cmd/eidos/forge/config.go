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
}

func newConfigCmd() *cobra.Command {
	var f configFlags
	cmd := &cobra.Command{
		Use:   "config <name>",
		Short: "Update a mind-form's runtime configuration (in-container gate)",
		Long: `Update a runtime config key inside the mind-form's gate. The change
takes effect at the next wake — agent-runner re-reads config.toml each
time it spawns claude. Currently supported flags:

  --model <id>   Pin the claude model used by agent-runner.

Internally this dispatches to ` + "`eidos gate config set <key> <value>`" + `
inside the container, so the registry's validation runs once.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			f.modelSet = cmd.Flags().Changed("model")
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
	return cmd
}

// buildConfigDockerArgv returns the `docker exec ...` argv for a single
// config update. Today only --model is supported; if more keys arrive
// the cobra command grows a flag for each. Pre-validates on the host so
// a typo aborts before we shell out.
//
// Empty --model is meaningful: pass --model "" to clear a previously
// pinned model and let claude pick its subscription default. f.modelSet
// distinguishes this from "the operator did not pass --model" (which is
// an error — forge config without any flag has nothing to do).
func buildConfigDockerArgv(name string, f configFlags) ([]string, error) {
	if !f.modelSet {
		return nil, fmt.Errorf("forge config requires --model <id> (pass --model \"\" to unpin)")
	}
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
