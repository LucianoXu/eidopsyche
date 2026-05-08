package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
// On any other GOOS we fall through to the plain command — Windows
// is not on the supported-host list for the daemon today.
func lifecycleSpawn(ctx context.Context, args []string) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return platformLifecycleSpawn(ctx, self, args)
}

// shortLifecycleHex returns 8 hex chars used as a transient
// systemd-run unit suffix. Avoiding `rand.Read` failures here means
// our transient unit name might collide with another, but
// `--collect` cleans the unit on exit so the worst case is a
// systemd warning — preferred over the spawner failing outright.
func shortLifecycleHex() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback so the spawn doesn't fail on a randomness error.
		return "00000000"
	}
	return hex.EncodeToString(b[:])
}
