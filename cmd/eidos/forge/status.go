package forge

import (
	"context"
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
	fmt.Fprintf(&sb, "name:    %s\nstate:   %s\n", name, state)
	if state == "running" {
		// Best-effort whoami via in-container reflection.
		res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "whoami"})
		if err != nil || res.ExitCode != 0 {
			fmt.Fprintf(&sb, "whoami:  unavailable\n")
		} else {
			sb.WriteString(string(res.Stdout))
		}
	}
	return sb.String(), nil
}
