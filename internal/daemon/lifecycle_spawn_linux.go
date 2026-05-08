//go:build linux

package daemon

import (
	"context"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"
)

// loggedFallbackOnce makes the "systemd-run not on PATH" warning fire
// at most once per process — printing on every lifecycle action would
// drown the daemon log.
var loggedFallbackOnce sync.Once

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
	// cgroup; hosts without systemd-run are rare in practice
	// (Alpine/musl minimal containers, restricted seccomp profiles,
	// rootless podman). Log this exactly once per process so the
	// operator can correlate "purge took down the daemon mid-rmdir"
	// with the missing-systemd-run state.
	loggedFallbackOnce.Do(func() {
		slog.Default().Warn("lifecycle: systemd-run not found; falling back to setsid + Setpgid",
			"impact", "gate purge may stop the daemon before completing the state-dir wipe")
	})
	cmd := exec.CommandContext(ctx, self, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, nil
}
