package supervisor

import (
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/wake"
	"github.com/spf13/cobra"
)

const (
	wakeDir = "/eidos/run/wake"
	gateDir = "/eidos/gate"
	cronTab = "/etc/crontabs/root"
)

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run as PID 1: spawn crond + gate daemon, watch wake dir",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			children := processSpawner{}
			startChildren(ctx, children)
			return watchWakes(ctx)
		},
	}
}

// startChildren spawns long-running children (crond + gate daemon).
func startChildren(ctx context.Context, sp ChildSpawner) {
	_ = sp.Spawn(ctx, "crond", "-f", "-c", "/etc/crontabs")
	_ = sp.Spawn(ctx, "eidos", "gate", "daemon", "--state-dir", gateDir)
}

// watchWakes is the supervisor's main loop. Stub for now; Task 4.2 fills it
// in with inotify and agent-runner spawning.
func watchWakes(ctx context.Context) error {
	<-ctx.Done()
	return fmt.Errorf("watchWakes: not yet implemented (Task 4.2)")
}

// dummy export so wake import resolves at compile time.
var _ = wake.SchemaVersion
