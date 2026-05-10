package forge

import (
	"context"
	"fmt"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all mind-forms on this host",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := forgectl.New()
			if err != nil {
				return err
			}
			vols, err := c.VolumeList(cmd.Context(), forgectl.VolumePrefix)
			if err != nil {
				return err
			}
			if len(vols) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no mind-forms found (create one with `eidos forge create <name>`)")
				return nil
			}
			for _, v := range vols {
				name := strings.TrimPrefix(v, forgectl.VolumePrefix)
				fmt.Fprintf(cmd.OutOrStdout(), "%-32s %s\n", name, listPhase(cmd.Context(), c, name))
			}
			return nil
		},
	}
}

// listPhase returns the second column of `forge list`: a phase derived
// from runtime-state JSON when the container is up and the in-container
// subcommand exists; falls back to the raw docker state on older images.
//
// Wake reason is intentionally omitted to keep the column narrow — run
// `forge status <name>` for the full breakdown.
func listPhase(ctx context.Context, c forgectl.Client, name string) string {
	cont := forgectl.ContainerName(name)
	state, _ := c.ContainerInspectState(ctx, cont)
	if state != "running" {
		return "offline"
	}
	if rs, ok := fetchRuntimeState(ctx, c, cont); ok {
		return rs.Phase
	}
	return state
}
