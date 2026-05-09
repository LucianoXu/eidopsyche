package forge

import (
	"fmt"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/LucianoXu/eidopsyche/internal/scheduler"
	"github.com/spf13/cobra"
)

// newStatusDetailCmd is an in-container subcommand that prints the
// plan + dream summary lines appended to `forge status` on the host.
//
// Hidden because it's not meant for direct operator use; the host-side
// `forge status` execs it. Kept as a separate command (rather than
// folded into `whoami`) so its output is composable.
func newStatusDetailCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "status-detail",
		Short:  "Internal: print plans + dreams summary lines for forge status",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tz := loadMindFormTZ()
			plans, _ := scheduler.List(plansDir)
			ds, _ := dreamstate.Read(dreamStatePath)
			now := time.Now()

			// Plans line
			if len(plans) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "plans:   none")
			} else {
				next := plans[0]
				for _, p := range plans {
					if p.At < next.At {
						next = p
					}
				}
				at := time.Unix(next.At, 0).In(tz)
				fmt.Fprintf(cmd.OutOrStdout(), "plans:   %d active   (next: %s — %s)\n",
					len(plans), at.Format(time.RFC3339), next.Hint)
			}

			// Dreams line
			if ds.DreamCount == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "dreams:  none yet")
			} else {
				gap := now.Sub(time.Unix(ds.LastDreamFinishedAt, 0)).Truncate(time.Second)
				note := ds.LastDreamNote
				if note == "" {
					note = "(no note)"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "dreams:  %d total    (last: %s ago, %q)\n",
					ds.DreamCount, formatDuration(gap), note)
			}
			if ds.CurrentlyDreaming {
				fmt.Fprintln(cmd.OutOrStdout(), "dream:   currently dreaming")
			}
			return nil
		},
	}
}
