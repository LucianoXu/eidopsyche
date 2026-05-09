package daemon

import (
	"context"
	"os"
	"os/exec"
)

// lifecycleSpawn returns an *exec.Cmd that, when started, runs
// `os.Args[0] <args...>` outside the daemon's own process group/cgroup
// so a child invoking `eidos gate stop` (which terminates the daemon's
// systemd unit) doesn't take the child down with it.
//
// On Linux: prefer `systemd-run --user --scope --collect …` to put the
// child into a transient unit OUTSIDE the daemon's cgroup. The child
// survives `systemctl --user stop eidos-gate-daemon.service`, which is
// what `eidos gate purge` does — without this, purge would stop the
// daemon but never get to the rm-rf of state-dir.
// Fallback (no systemd-run on PATH): `setsid` + `Setpgid` so the child
// is in its own session and process group. Less airtight than a
// separate cgroup but better than inheriting.
//
// On Darwin: launchd doesn't group children with the parent by
// default, so a plain exec.Cmd is fine.
//
// On Windows: the SCM service is independent of the spawning process
// group, and `eidos gate stop` goes through SCM rather than killing the
// daemon's own process tree, so no extra isolation is required.
//
// shortLifecycleHex (used to suffix transient systemd-run unit names)
// lives next to its only caller in lifecycle_spawn_linux.go so it
// doesn't show up as unused on non-linux builds.
func lifecycleSpawn(ctx context.Context, args []string) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return platformLifecycleSpawn(ctx, self, args)
}
