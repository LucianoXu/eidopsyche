//go:build !linux

package daemon

import (
	"context"
	"os/exec"
)

// platformLifecycleSpawn for darwin (and any non-linux GOOS): plain
// exec.Cmd. launchd doesn't group children with the parent by default,
// so the child survives the parent's stop without extra isolation.
func platformLifecycleSpawn(ctx context.Context, self string, args []string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, self, args...), nil
}
