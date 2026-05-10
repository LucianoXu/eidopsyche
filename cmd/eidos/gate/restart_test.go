package gate

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/service"
)

// fakeManager is a minimal in-memory service.Manager used by restart
// tests. It records each lifecycle method invocation so a test can
// assert the exact sequence runRestart drove.
type fakeManager struct {
	installed bool
	active    bool
	calls     []string

	startErr   error
	stopErr    error
	restartErr error
	statusErr  error
}

func (f *fakeManager) InstallDaemon(_ context.Context) error {
	f.calls = append(f.calls, "install-daemon")
	return nil
}
func (f *fakeManager) InstallRelay(_ context.Context, _ string) error { return nil }
func (f *fakeManager) UninstallDaemon(_ context.Context) error        { return nil }
func (f *fakeManager) UninstallRelay(_ context.Context) error         { return nil }
func (f *fakeManager) StartDaemon(_ context.Context) error {
	f.calls = append(f.calls, "start-daemon")
	if f.startErr != nil {
		return f.startErr
	}
	f.installed = true
	f.active = true
	return nil
}
func (f *fakeManager) StartRelay(_ context.Context, _ string) error { return nil }
func (f *fakeManager) StopDaemon(_ context.Context) error {
	f.calls = append(f.calls, "stop-daemon")
	if f.stopErr != nil {
		return f.stopErr
	}
	f.active = false
	return nil
}
func (f *fakeManager) StopRelay(_ context.Context) error { return nil }
func (f *fakeManager) RestartDaemon(_ context.Context) error {
	f.calls = append(f.calls, "restart-daemon")
	return f.restartErr
}
func (f *fakeManager) Status(_ context.Context) ([]service.Status, error) {
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	return []service.Status{
		{Name: service.DaemonUnitName, Installed: f.installed, Active: f.active},
		{Name: service.RelayUnitName},
	}, nil
}

// withFakeManager swaps the service-manager factory for the duration of
// a test so runRestart sees the fake instead of the real platform
// manager. Cleanup restores the previous factory. The same fake is
// returned for both user and system scope — single-scope tests don't
// care which slot it is plumbed into; dual-scope tests use
// withFakeManagersByScope below to differentiate.
//
// Also installs a no-op IPC fast path so the test does not accidentally
// dial the developer's real daemon socket at $HOME/.eidos/gate/sock and
// syscall.Exec the live daemon. Tests that want to exercise the IPC fast
// path should override tryIPCExecReplaceFn after this helper returns.
func withFakeManager(t *testing.T, m service.Manager, err error) {
	t.Helper()
	saved := serviceManagerFactory
	t.Cleanup(func() { serviceManagerFactory = saved })
	serviceManagerFactory = func(_ bool) (service.Manager, error) { return m, err }
	stubIPCExecReplace(t, false, nil)
}

// stubIPCExecReplace overrides the runRestart IPC fast path. Pass ok=true
// to simulate "running daemon accepted exec-replace" (writing the same
// success line the production path emits); ok=false + simulatedErr=nil to
// simulate "no daemon running" so the test falls through to the
// managed-service path; or any non-nil simulatedErr to drive the
// surface-failure branch.
func stubIPCExecReplace(t *testing.T, ok bool, simulatedErr error) {
	t.Helper()
	saved := tryIPCExecReplaceFn
	t.Cleanup(func() { tryIPCExecReplaceFn = saved })
	tryIPCExecReplaceFn = func(w io.Writer) (bool, error) {
		if ok {
			_, _ = w.Write([]byte("✓ gate daemon re-exec'd in place (pid=42, binary=/test/eidos)\n"))
			return true, nil
		}
		if simulatedErr == nil {
			return false, errNoDaemonRunning
		}
		return false, simulatedErr
	}
}

// withFakeManagersByScope installs distinct fakes per scope so a test
// can verify --if-running's "try user, then system" loop hits the right
// one. Either fake may be nil to simulate "no manager available in
// that scope".
func withFakeManagersByScope(t *testing.T, user, system service.Manager) {
	t.Helper()
	saved := serviceManagerFactory
	t.Cleanup(func() { serviceManagerFactory = saved })
	serviceManagerFactory = func(useSystem bool) (service.Manager, error) {
		if useSystem {
			if system == nil {
				return nil, service.ErrUnsupported
			}
			return system, nil
		}
		if user == nil {
			return nil, service.ErrUnsupported
		}
		return user, nil
	}
	stubIPCExecReplace(t, false, nil)
}

func TestRestart_NotInstalled_IfRunning_NoOp(t *testing.T) {
	fm := &fakeManager{}
	withFakeManager(t, fm, nil)

	var buf bytes.Buffer
	if err := runRestart(context.Background(), &buf, true); err != nil {
		t.Fatalf("runRestart: %v", err)
	}
	if !strings.Contains(buf.String(), "nothing to do") {
		t.Errorf("expected hint, got: %q", buf.String())
	}
	if len(fm.calls) != 0 {
		// Status() is also tracked indirectly via the *Daemon calls;
		// here we just want to confirm we didn't try to restart/start.
		for _, c := range fm.calls {
			if c == "restart-daemon" || c == "start-daemon" {
				t.Errorf("unexpected lifecycle call %q on a non-installed unit", c)
			}
		}
	}
}

func TestRestart_NotInstalled_NoFlag_Errors(t *testing.T) {
	fm := &fakeManager{}
	withFakeManager(t, fm, nil)

	err := runRestart(context.Background(), &bytes.Buffer{}, false)
	if err == nil {
		t.Fatal("expected error when daemon is not installed and --if-running not set")
	}
	if !strings.Contains(err.Error(), "eidos gate start") {
		t.Errorf("error should point user at `eidos gate start`, got: %v", err)
	}
}

func TestRestart_InstalledAndActive_Restarts(t *testing.T) {
	fm := &fakeManager{installed: true, active: true}
	withFakeManager(t, fm, nil)

	if err := runRestart(context.Background(), &bytes.Buffer{}, false); err != nil {
		t.Fatalf("runRestart: %v", err)
	}
	// First Status, then RestartDaemon, then Status again via printStatus.
	// We just assert that restart-daemon was invoked exactly once and that
	// no start-daemon happened (which would mean we took the "stopped"
	// branch incorrectly).
	if countCalls(fm.calls, "restart-daemon") != 1 {
		t.Errorf("expected exactly one restart-daemon call, got: %v", fm.calls)
	}
	if countCalls(fm.calls, "start-daemon") != 0 {
		t.Errorf("expected no start-daemon call on an active unit, got: %v", fm.calls)
	}
}

func TestRestart_InstalledButStopped_Starts(t *testing.T) {
	fm := &fakeManager{installed: true, active: false}
	withFakeManager(t, fm, nil)

	if err := runRestart(context.Background(), &bytes.Buffer{}, false); err != nil {
		t.Fatalf("runRestart: %v", err)
	}
	if countCalls(fm.calls, "start-daemon") != 1 {
		t.Errorf("expected exactly one start-daemon call, got: %v", fm.calls)
	}
	if countCalls(fm.calls, "restart-daemon") != 0 {
		t.Errorf("did not expect restart-daemon when starting from stopped, got: %v", fm.calls)
	}
}

func TestRestart_UnsupportedPlatform_IfRunning_NoOp(t *testing.T) {
	withFakeManager(t, nil, service.ErrUnsupported)

	var buf bytes.Buffer
	if err := runRestart(context.Background(), &buf, true); err != nil {
		t.Fatalf("runRestart: %v", err)
	}
	if !strings.Contains(buf.String(), "not supported on this platform") {
		t.Errorf("expected unsupported-platform hint, got: %q", buf.String())
	}
}

func TestRestart_UnsupportedPlatform_NoFlag_PropagatesError(t *testing.T) {
	withFakeManager(t, nil, service.ErrUnsupported)

	err := runRestart(context.Background(), &bytes.Buffer{}, false)
	if err == nil {
		t.Fatal("expected ErrUnsupported to propagate without --if-running")
	}
	if !errors.Is(err, service.ErrUnsupported) {
		t.Errorf("error chain missing ErrUnsupported: %v", err)
	}
}

// TestRestart_IfRunning_FindsSystemScopeWhenUserEmpty is the regression
// for the codex-flagged bug: a daemon installed via `eidos gate start
// --system` was being missed by install.sh's post-install
// `gate restart --if-running`, which only checked user scope. With the
// scope-walking fix, --if-running tries user first, finds nothing,
// falls through to system, and restarts the active system unit there.
func TestRestart_IfRunning_FindsSystemScopeWhenUserEmpty(t *testing.T) {
	userMgr := &fakeManager{}                                // not installed in user scope
	systemMgr := &fakeManager{installed: true, active: true} // installed in system scope
	withFakeManagersByScope(t, userMgr, systemMgr)

	// useSystemServices is left at its default (false) — install.sh
	// invokes gate restart without --system; --if-running widens the
	// search.
	saved := useSystemServices
	t.Cleanup(func() { useSystemServices = saved })
	useSystemServices = false

	if err := runRestart(context.Background(), &bytes.Buffer{}, true); err != nil {
		t.Fatalf("runRestart: %v", err)
	}
	if countCalls(systemMgr.calls, "restart-daemon") != 1 {
		t.Errorf("expected system-scope manager to receive exactly one restart-daemon, got: %v", systemMgr.calls)
	}
	if countCalls(userMgr.calls, "restart-daemon") != 0 {
		t.Errorf("user-scope manager unexpectedly restarted: %v", userMgr.calls)
	}
}

// TestRestart_IfRunning_ExplicitSystemFlagNarrowsToSystemOnly verifies
// that even with --if-running, an operator who passes --system narrows
// the search to system scope alone — we honour the explicit narrowing
// rather than silently widening to the user scope.
func TestRestart_IfRunning_ExplicitSystemFlagNarrowsToSystemOnly(t *testing.T) {
	userMgr := &fakeManager{installed: true, active: true} // would match if we searched
	systemMgr := &fakeManager{}                            // not installed in system scope
	withFakeManagersByScope(t, userMgr, systemMgr)

	saved := useSystemServices
	t.Cleanup(func() { useSystemServices = saved })
	useSystemServices = true // operator narrows to system scope

	var buf bytes.Buffer
	if err := runRestart(context.Background(), &buf, true); err != nil {
		t.Fatalf("runRestart: %v", err)
	}
	if countCalls(userMgr.calls, "restart-daemon") != 0 {
		t.Errorf("user manager unexpectedly invoked despite --system: %v", userMgr.calls)
	}
	if !strings.Contains(buf.String(), "nothing to do") {
		t.Errorf("expected no-op hint when system scope is empty; got: %q", buf.String())
	}
}

// TestRestart_IfRunning_IPCFastPath_Succeeds covers the unmanaged-daemon
// case the dashboard self-update flow lands in: no service installed,
// but a daemon is responding on the IPC socket. runRestart short-circuits
// to exec-replace and never even inspects the service manager. Without
// this fast path, install.sh's `gate restart --if-running` was a silent
// no-op for users who started the daemon directly with `eidos gate
// daemon &`.
func TestRestart_IfRunning_IPCFastPath_Succeeds(t *testing.T) {
	fm := &fakeManager{}
	withFakeManager(t, fm, nil)
	stubIPCExecReplace(t, true, nil)

	var buf bytes.Buffer
	if err := runRestart(context.Background(), &buf, true); err != nil {
		t.Fatalf("runRestart: %v", err)
	}
	if !strings.Contains(buf.String(), "re-exec'd in place") {
		t.Errorf("expected exec-replace confirmation, got: %q", buf.String())
	}
	for _, c := range fm.calls {
		if c == "restart-daemon" || c == "start-daemon" {
			t.Errorf("service manager should not be touched once IPC succeeds, got call %q", c)
		}
	}
}

// TestRestart_IfRunning_IPCNoDaemon_FallsThrough verifies the IPC fast
// path is silent when the daemon is not reachable: runRestart proceeds
// to the existing managed-service walk, so daemons that haven't been
// upgraded to the exec-replace method still see the prior --if-running
// behavior on freshly-installed managed services.
func TestRestart_IfRunning_IPCNoDaemon_FallsThrough(t *testing.T) {
	fm := &fakeManager{installed: true, active: true}
	withFakeManager(t, fm, nil)
	stubIPCExecReplace(t, false, nil) // simulate "daemon not running on IPC"

	var buf bytes.Buffer
	if err := runRestart(context.Background(), &buf, true); err != nil {
		t.Fatalf("runRestart: %v", err)
	}
	// --if-running walks both scopes; with a single fakeManager wired to
	// both, restart-daemon fires once per scope. We just assert it fired
	// at all — the point is the fallback happened, not the exact count.
	if countCalls(fm.calls, "restart-daemon") < 1 {
		t.Errorf("expected fallback to systemctl restart, got calls=%v", fm.calls)
	}
	if strings.Contains(buf.String(), "re-exec'd in place") {
		t.Errorf("should not print exec-replace line when IPC fast path was skipped")
	}
}

// TestRestart_IfRunning_IPCUnsupported_FallsThrough covers older daemons
// that don't have daemon.exec-replace registered. The IPC error code
// UNKNOWN_METHOD must map to the same silent fallback as "no daemon
// running" — surfacing it would spam the dashboard's lifecycle log on
// every self-update against a pre-fix daemon.
func TestRestart_IfRunning_IPCUnsupported_FallsThrough(t *testing.T) {
	fm := &fakeManager{installed: true, active: true}
	withFakeManager(t, fm, nil)
	stubIPCExecReplace(t, false, errExecReplaceUnsupported)

	var buf bytes.Buffer
	if err := runRestart(context.Background(), &buf, true); err != nil {
		t.Fatalf("runRestart: %v", err)
	}
	if countCalls(fm.calls, "restart-daemon") < 1 {
		t.Errorf("expected fallback to systemctl restart, got calls=%v", fm.calls)
	}
	if strings.Contains(buf.String(), "exec via IPC failed") {
		t.Errorf("UNKNOWN_METHOD should fall through silently, got: %q", buf.String())
	}
}

// TestRestart_IfRunning_IPCError_Surfaces verifies that real IPC errors
// (binary missing, permission denied, "exec-replace already scheduled"
// — anything that isn't "no daemon" or "unsupported method") get printed
// to the operator's log so they're not swallowed before the fall-through
// runs. The fall-through still tries the managed-service path so the
// surface message is supplementary, not fatal.
func TestRestart_IfRunning_IPCError_Surfaces(t *testing.T) {
	fm := &fakeManager{}
	withFakeManager(t, fm, nil)
	stubIPCExecReplace(t, false, errors.New("INTERNAL: stat self: permission denied"))

	var buf bytes.Buffer
	if err := runRestart(context.Background(), &buf, true); err != nil {
		t.Fatalf("runRestart: %v", err)
	}
	if !strings.Contains(buf.String(), "in-place exec via IPC failed") {
		t.Errorf("expected surfaced IPC failure, got: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "permission denied") {
		t.Errorf("expected error detail in output, got: %q", buf.String())
	}
}

// TestRestart_NoIfRunning_SkipsIPCFastPath: the IPC exec-replace fast
// path is gated to --if-running. An explicit `gate restart` (without
// the flag) is the operator saying "I want a managed-service restart"
// — using exec-replace here would silently swap an in-place exec for
// the systemctl-driven bounce the user asked for.
func TestRestart_NoIfRunning_SkipsIPCFastPath(t *testing.T) {
	fm := &fakeManager{installed: true, active: true}
	withFakeManager(t, fm, nil)
	// If the IPC path is consulted at all this returns success and
	// would short-circuit the systemctl path; the assertion below
	// proves we did NOT take that branch.
	stubIPCExecReplace(t, true, nil)

	var buf bytes.Buffer
	if err := runRestart(context.Background(), &buf, false); err != nil {
		t.Fatalf("runRestart: %v", err)
	}
	if countCalls(fm.calls, "restart-daemon") != 1 {
		t.Errorf("expected systemctl restart for explicit `gate restart`, got calls=%v", fm.calls)
	}
	if strings.Contains(buf.String(), "re-exec'd in place") {
		t.Errorf("explicit `gate restart` must not take the IPC fast path, got: %q", buf.String())
	}
}

// TestScopesToTry locks the dispatch table for the scope-walking
// matrix so a future refactor can't silently widen or narrow the
// search.
func TestScopesToTry(t *testing.T) {
	cases := []struct {
		ifRunning, explicitSystem bool
		want                      []bool
	}{
		{ifRunning: false, explicitSystem: false, want: []bool{false}},
		{ifRunning: false, explicitSystem: true, want: []bool{true}},
		{ifRunning: true, explicitSystem: false, want: []bool{false, true}},
		{ifRunning: true, explicitSystem: true, want: []bool{true}},
	}
	for _, tc := range cases {
		got := scopesToTry(tc.ifRunning, tc.explicitSystem)
		if len(got) != len(tc.want) {
			t.Errorf("scopesToTry(ifRunning=%v, explicit=%v): got %v, want %v",
				tc.ifRunning, tc.explicitSystem, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("scopesToTry(ifRunning=%v, explicit=%v): got %v, want %v",
					tc.ifRunning, tc.explicitSystem, got, tc.want)
				break
			}
		}
	}
}

func countCalls(calls []string, want string) int {
	n := 0
	for _, c := range calls {
		if c == want {
			n++
		}
	}
	return n
}
