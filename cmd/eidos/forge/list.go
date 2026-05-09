package forge

import (
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
				state, _ := c.ContainerInspectState(cmd.Context(), forgectl.ContainerName(name))
				fmt.Fprintf(cmd.OutOrStdout(), "%-32s %s\n", name, state)
			}
			return nil
		},
	}
}
