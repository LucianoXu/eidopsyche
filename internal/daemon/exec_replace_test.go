package daemon

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// TestDaemonExecReplace_Registered guards the wiring in methods.go's init()
// — without it the IPC method silently falls back to UNKNOWN_METHOD and
// gate restart --if-running's exec-replace fast path is dead code.
func TestDaemonExecReplace_Registered(t *testing.T) {
	if _, ok := methodTable["daemon.exec-replace"]; !ok {
		t.Fatal("daemon.exec-replace is not registered in methodTable")
	}
}

// TestDaemonExecReplace_HappyPath verifies the handler returns a populated
// ExecReplaceResult and that the deferred exec call fires with the
// daemon's own argv and env. Uses testExecReplaceFn so the test binary
// is not actually replaced.
func TestDaemonExecReplace_HappyPath(t *testing.T) {
	resetExecReplaceArmedForTest()
	t.Cleanup(func() {
		testExecReplaceFn = nil
		resetExecReplaceArmedForTest()
	})

	var (
		mu       sync.Mutex
		gotSelf  string
		gotArgs  []string
		gotEnv   []string
		called   atomic.Bool
		callDone = make(chan struct{})
	)
	testExecReplaceFn = func(self string, args, env []string) error {
		mu.Lock()
		gotSelf = self
		gotArgs = append([]string(nil), args...)
		gotEnv = append([]string(nil), env...)
		mu.Unlock()
		called.Store(true)
		close(callDone)
		return nil
	}

	res, ipcErr := daemonExecReplace(context.Background(), &Daemon{}, nil, json.RawMessage(`{}`))
	if ipcErr != nil {
		t.Fatalf("unexpected IPC error: %s: %s", ipcErr.Code, ipcErr.Message)
	}
	out, ok := res.(ExecReplaceResult)
	if !ok {
		t.Fatalf("result type = %T, want ExecReplaceResult", res)
	}
	if out.Pid != os.Getpid() {
		t.Errorf("Pid = %d, want %d", out.Pid, os.Getpid())
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if out.Binary != self {
		t.Errorf("Binary = %q, want %q", out.Binary, self)
	}

	// The exec is fired from a goroutine after a short flush delay; wait
	// for it to land. The flush delay is 200 ms in production; allow a
	// generous margin for slow CI hosts.
	select {
	case <-callDone:
	case <-time.After(2 * time.Second):
		t.Fatal("testExecReplaceFn was not called within 2s")
	}

	mu.Lock()
	defer mu.Unlock()
	if gotSelf != self {
		t.Errorf("exec self = %q, want %q", gotSelf, self)
	}
	if len(gotArgs) == 0 || gotArgs[0] != os.Args[0] {
		t.Errorf("exec args[0] = %v, want %q", gotArgs, os.Args[0])
	}
	if len(gotEnv) == 0 {
		t.Errorf("exec env should not be empty")
	}
	if !called.Load() {
		t.Errorf("called flag was not set")
	}
}

// TestDaemonExecReplace_AlreadyScheduled covers the concurrency guard:
// once a process has armed an exec-replace, subsequent calls return
// INTERNAL with a recognizable message instead of scheduling a second
// exec. Without this the second click of a dashboard self-update could
// race two goroutines into syscall.Exec.
func TestDaemonExecReplace_AlreadyScheduled(t *testing.T) {
	resetExecReplaceArmedForTest()
	t.Cleanup(func() {
		testExecReplaceFn = nil
		resetExecReplaceArmedForTest()
	})

	// First call arms the flag; the test fn never returns to syscall.Exec
	// so the goroutine just blocks until the test ends.
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	testExecReplaceFn = func(string, []string, []string) error {
		<-block
		return nil
	}

	if _, err := daemonExecReplace(context.Background(), &Daemon{}, nil, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("first call should succeed, got: %s", err.Message)
	}

	// Second call must be rejected.
	_, err := daemonExecReplace(context.Background(), &Daemon{}, nil, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("second call should have returned an IPC error")
	}
	if err.Code != ipc.ErrInternal {
		t.Errorf("err.Code = %q, want %q", err.Code, ipc.ErrInternal)
	}
}
