package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// execReplaceFlushDelay is the gap between returning the IPC response and
// firing syscall.Exec. The IPC connection FD is CLOEXEC, so exec closes it
// — without a small delay the response packet can sit in a buffer that the
// caller never gets a chance to drain. 200 ms is empirically generous on a
// loopback unix socket while still keeping the dashboard's "did the daemon
// come back?" reconnect feel snappy.
const execReplaceFlushDelay = 200 * time.Millisecond

// execReplaceMu guards execReplaceArmed so concurrent callers (a duplicated
// install-script run, a stale lifecycle retry, two dashboard tabs) don't
// each schedule their own goroutine to exec the same process.
var execReplaceMu sync.Mutex
var execReplaceArmed bool

// testExecReplaceFn, when non-nil, replaces the real syscall.Exec call so
// unit tests can drive the IPC handler and the goroutine without actually
// replacing the test binary's process image. Production leaves this nil
// and falls through to the real syscall.Exec branch.
var testExecReplaceFn func(self string, args []string, env []string) error

// resetExecReplaceArmedForTest is the test-only escape hatch for the
// "already scheduled" guard. After a test calls daemonExecReplace, the
// armed bit stays set forever (production never needs to clear it because
// the process is about to be replaced). Tests that drive the handler more
// than once must reset the flag between runs.
func resetExecReplaceArmedForTest() {
	execReplaceMu.Lock()
	defer execReplaceMu.Unlock()
	execReplaceArmed = false
}

// ExecReplaceResult is the response shape for the daemon.exec-replace IPC
// method. The caller (gate restart --if-running's IPC fallback) uses Pid +
// Binary to print a one-line confirmation; the dashboard discards the
// result and just waits for SSE to reconnect.
type ExecReplaceResult struct {
	Binary string `json:"binary"`
	Pid    int    `json:"pid"`
}

// daemonExecReplace handles the daemon.exec-replace IPC method. It schedules
// a syscall.Exec on a background goroutine after a short flush delay (so the
// response can land in the caller's read buffer) and returns immediately.
// PID is preserved across the exec, so:
//   - Managed daemons (systemd / launchd / SCM) keep the same MainPID and
//     therefore don't trigger an auto-restart.
//   - Unmanaged daemons (started via `eidos gate daemon &` from a shell)
//     keep the same job-control PID, so the user's background-job
//     references stay valid.
//
// Used by `gate restart --if-running` as a fallback when no managed service
// is found but a daemon is responding on the IPC socket — the typical
// "daemon started directly, not via gate start" deployment.
//
// Limitations: the IPC socket FD, the state-dir flock, the dashboard HTTP
// listener, and the SQLite DB handles are all CLOEXEC. They drop at exec
// and the new daemon reacquires them during normal startup. There is a
// small window (sub-second on a healthy host) where the IPC socket has no
// listener; clients dialed during that window get ECONNREFUSED and must
// retry.
func daemonExecReplace(_ context.Context, d *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
	self, err := os.Executable()
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInternal, Message: "locate self: " + err.Error()}
	}
	if fi, err := os.Stat(self); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInternal, Message: "stat self: " + err.Error()}
	} else if fi.Mode().Perm()&0o111 == 0 {
		return nil, &ipc.Error{Code: ipc.ErrInternal, Message: fmt.Sprintf("binary %s is not executable", self)}
	}

	execReplaceMu.Lock()
	if execReplaceArmed {
		execReplaceMu.Unlock()
		return nil, &ipc.Error{Code: ipc.ErrInternal, Message: "exec-replace already scheduled"}
	}
	execReplaceArmed = true
	execReplaceMu.Unlock()

	args := append([]string(nil), os.Args...)
	env := append([]string(nil), os.Environ()...)

	// Capture the test seam now so a t.Cleanup that nils
	// testExecReplaceFn between the sleep and the post-call check can't
	// race the goroutine into the real os.Exit(74) branch.
	testFn := testExecReplaceFn

	go func() {
		time.Sleep(execReplaceFlushDelay)
		if d.Log != nil {
			d.Log.Info("daemon exec-replace: replacing process image",
				"binary", self, "pid", os.Getpid())
		}
		if testFn != nil {
			// Test mode: drive the seam, never reach os.Exit so the test
			// binary keeps running. The seam's return value is ignored;
			// tests that need to assert on it record arguments inside
			// the seam itself.
			_ = testFn(self, args, env)
			return
		}
		// On success this call does not return. On failure we are a zombie
		// daemon that can't restart itself — log loudly and exit non-zero
		// so a service manager (if any) restarts us via ExecStart, which
		// is strictly better than silently running on an image the user
		// expected to have been replaced.
		err := syscall.Exec(self, args, env)
		if d.Log != nil {
			d.Log.Error("daemon exec-replace: syscall.Exec failed", "err", err)
		}
		fmt.Fprintf(os.Stderr, "daemon exec-replace: syscall.Exec failed: %v\n", err)
		os.Exit(74)
	}()

	return ExecReplaceResult{Binary: self, Pid: os.Getpid()}, nil
}
