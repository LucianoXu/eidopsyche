package forge

import (
	"fmt"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/wake"
	"github.com/spf13/cobra"
)

func newWakeInContainerCmd() *cobra.Command {
	var reason, hint string
	cmd := &cobra.Command{
		Use:   "wake",
		Short: "Submit a wake signal (manual or heartbeat)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			r := wake.Reason(reason)
			if r != wake.ReasonHeartBeat && r != wake.ReasonManual {
				return fmt.Errorf("--reason must be heartbeat|manual (mindgate is gate-only)")
			}
			now := time.Now().Unix()
			sig := wake.Signal{
				ID:          fmt.Sprintf("%d-%s", now, r),
				Reason:      r,
				TriggeredAt: now,
				Hint:        hint,
			}
			return wake.Submit("/eidos/run/wake", sig)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "manual", "wake reason")
	cmd.Flags().StringVar(&hint, "hint", "", "human-readable hint")
	return cmd
}
