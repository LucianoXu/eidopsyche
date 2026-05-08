//go:build linux

package daemon

import (
	"context"
	"os/exec"
	"syscall"
)

func platformLifecycleSpawn(ctx context.Context, self string, args []string) (*exec.Cmd, error) {
	// Prefer systemd-run --user --scope --collect to escape the
	// daemon's cgroup. The transient unit is auto-cleaned on exit.
	if path, err := exec.LookPath("systemd-run"); err == nil {
		full := append([]string{
			"--user", "--scope", "--collect",
			"--unit", "eidos-lifecycle-" + shortLifecycleHex(),
			"--",
			self,
		}, args...)
		return exec.CommandContext(ctx, path, full...), nil
	}
	// Fallback: own session + process group so the child survives
	// the parent's group teardown. Not as airtight as a separate
	// cgroup; hosts without systemd-run are rare in practice.
	cmd := exec.CommandContext(ctx, self, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, nil
}
