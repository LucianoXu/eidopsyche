//go:build windows

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// platformNew returns the SCM-backed Manager. The actual SCM connection is
// made lazily inside each operation so a long-lived Manager doesn't hold
// open SCManager handles.
func platformNew(cfg Config) (Manager, error) {
	if cfg.BinaryPath == "" {
		return nil, fmt.Errorf("service.New: BinaryPath is required")
	}
	return &scm{cfg: cfg}, nil
}

// scm implements Manager via the Windows Service Control Manager.
//
// Services are created with SERVICE_AUTO_START and run as LocalSystem by
// default. The daemon picks up its state directory from `--state-dir` baked
// into the service's image path, so the same flag-driven contract that works
// from the CLI also works under SCM. ScopeUser (the default) and ScopeSystem
// behave identically here: both touch the host-wide SCM database (Windows has
// no per-user equivalent of `systemctl --user`). The Scope field is retained
// for API parity with the unix backends but is not branched on.
//
// Recovery actions: each managed service is configured with three
// SC_ACTION_RESTART entries (1s / 2s / 5s delays, reset window 60s) so a
// daemon crash is auto-respawned, matching systemd `Restart=on-failure` and
// launchd `KeepAlive=SuccessfulExit=false` semantics. The non-crash failure
// flag is enabled so a clean non-zero exit is also treated as a failure.
type scm struct {
	cfg Config
}

// recoveryActions are the per-service auto-restart steps applied at install
// time. The escalating delays (1s → 2s → 5s) tame a hot-loop crash while
// still bringing the daemon back quickly after a transient failure. The
// fourth state and beyond stays at SC_ACTION_NONE so a persistently broken
// daemon eventually gets the operator's attention rather than burning CPU
// in a crash loop.
var recoveryActions = []mgr.RecoveryAction{
	{Type: mgr.ServiceRestart, Delay: 1 * time.Second},
	{Type: mgr.ServiceRestart, Delay: 2 * time.Second},
	{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
}

// recoveryResetPeriod tells SCM how long the service must run cleanly before
// the failure counter resets. 60s is short enough that a service that
// recovers and runs steadily quickly clears the counter, and long enough to
// catch a quick-crashing daemon over the three-restart window above.
const recoveryResetPeriod uint32 = 60

// scmUnit defines one of the services this manager owns.
type scmUnit struct {
	name        string
	displayName string
	description string
	// args, when expanded by argsFor, is the command line we hand to SCM.
	// They become argv on service start and drive the cobra subcommand
	// lookup in `eidos service-run`.
	args func(stateDir string) []string
}

func (s *scm) units() []scmUnit {
	return []scmUnit{
		{
			name:        DaemonUnitName,
			displayName: "Eidopsyche Gate Daemon",
			description: "Eidopsyche MindGate daemon — manages NIP-17 message ingest, contacts, inbox.",
			args:        daemonArgs,
		},
		{
			name:        RelayUnitName,
			displayName: "Eidopsyche Gate Relay",
			description: "Eidopsyche MindGate embedded Nostr relay (paired or public).",
			args:        relayArgs,
		},
	}
}

func daemonArgs(stateDir string) []string {
	a := []string{"gate", "daemon"}
	if stateDir != "" {
		a = append(a, "--state-dir", stateDir)
	}
	return a
}

func relayArgs(stateDir string) []string {
	a := []string{"gate", "relay"}
	if stateDir != "" {
		a = append(a, "--state-dir", stateDir)
	}
	return a
}

// installUnits returns the units Install / Start should manage, filtered by
// cfg.WithRelay. Stop / Uninstall / Status iterate units() so they pick up
// residual relay services even when WithRelay is false.
func (s *scm) installUnits() []scmUnit {
	all := s.units()
	if s.cfg.WithRelay {
		return all
	}
	out := make([]scmUnit, 0, 1)
	for _, u := range all {
		if u.name == RelayUnitName {
			continue
		}
		out = append(out, u)
	}
	return out
}

func (s *scm) Install(ctx context.Context) error {
	m, err := mgr.Connect()
	if err != nil {
		return wrapSCMConnect(err)
	}
	defer m.Disconnect()

	for _, u := range s.installUnits() {
		if err := s.installOne(m, u); err != nil {
			return err
		}
	}
	return nil
}

func (s *scm) installOne(m *mgr.Mgr, u scmUnit) error {
	if existing, err := m.OpenService(u.name); err == nil {
		// Service exists — update its config to match the current binary
		// and state-dir so a self-update or `eidos gate init` rewrite is
		// reflected without requiring a manual reinstall.
		defer existing.Close()
		cfg, err := existing.Config()
		if err != nil {
			return fmt.Errorf("read service config %s: %w", u.name, err)
		}
		cfg.BinaryPathName = buildImagePath(s.cfg.BinaryPath, u.args(s.cfg.StateDir))
		cfg.DisplayName = u.displayName
		cfg.Description = u.description
		cfg.StartType = mgr.StartAutomatic
		if err := existing.UpdateConfig(cfg); err != nil {
			return fmt.Errorf("update service %s: %w", u.name, err)
		}
		if err := applyRecoveryActions(existing, u.name); err != nil {
			return err
		}
		return nil
	}

	cfg := mgr.Config{
		DisplayName: u.displayName,
		Description: u.description,
		StartType:   mgr.StartAutomatic,
		// ServiceStartName left empty → LocalSystem, the SCM default.
	}
	sv, err := m.CreateService(u.name, s.cfg.BinaryPath, cfg, u.args(s.cfg.StateDir)...)
	if err != nil {
		return fmt.Errorf("create service %s: %w", u.name, wrapSCMOp(err))
	}
	defer sv.Close()
	if err := applyRecoveryActions(sv, u.name); err != nil {
		return err
	}
	return nil
}

// applyRecoveryActions wires the auto-restart-on-failure policy onto a
// freshly-installed (or just-updated) service. We treat a failure to set
// recovery actions as a hard error: silently skipping it would leave the
// service without the cross-platform "comes back after a crash" contract
// that systemd Restart=on-failure and launchd KeepAlive provide.
func applyRecoveryActions(sv *mgr.Service, name string) error {
	if err := sv.SetRecoveryActions(recoveryActions, recoveryResetPeriod); err != nil {
		return fmt.Errorf("set recovery actions on %s: %w", name, wrapSCMOp(err))
	}
	// Also restart on a clean non-zero exit, not just on a crash. The
	// daemon's "fatal startup error" path is a normal exit, but from the
	// operator's point of view it's a failure that should be retried.
	if err := sv.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return fmt.Errorf("enable non-crash recovery on %s: %w", name, wrapSCMOp(err))
	}
	return nil
}

func (s *scm) Start(ctx context.Context) error {
	if err := s.Install(ctx); err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return wrapSCMConnect(err)
	}
	defer m.Disconnect()

	for _, u := range s.installUnits() {
		sv, err := m.OpenService(u.name)
		if err != nil {
			return fmt.Errorf("open service %s: %w", u.name, err)
		}
		// Skip if the service is already running so Start is idempotent.
		if st, qerr := sv.Query(); qerr == nil && st.State == svc.Running {
			sv.Close()
			continue
		}
		if err := sv.Start(); err != nil {
			sv.Close()
			// SCM returns ERROR_SERVICE_ALREADY_RUNNING when a parallel
			// caller raced us — treat that as success.
			if errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
				continue
			}
			return fmt.Errorf("start service %s: %w", u.name, wrapSCMOp(err))
		}
		sv.Close()
	}
	return s.waitFor(ctx, svc.Running, 10*time.Second, s.installUnits())
}

func (s *scm) Stop(ctx context.Context) error {
	m, err := mgr.Connect()
	if err != nil {
		return wrapSCMConnect(err)
	}
	defer m.Disconnect()

	for _, u := range s.units() {
		sv, err := m.OpenService(u.name)
		if err != nil {
			// Not installed → nothing to stop. Mirrors the systemd "not
			// loaded" exit-5 swallow.
			continue
		}
		_, ctlErr := sv.Control(svc.Stop)
		sv.Close()
		// "service has not been started" is benign: it means the service
		// was already stopped. Anything else gets surfaced.
		if ctlErr != nil && !errors.Is(ctlErr, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return fmt.Errorf("stop service %s: %w", u.name, wrapSCMOp(ctlErr))
		}
	}
	return s.waitFor(ctx, svc.Stopped, 10*time.Second, s.units())
}

func (s *scm) Uninstall(ctx context.Context) error {
	// Best-effort stop first. If the service is running when we ask SCM
	// to delete it, SCM marks it for deletion but doesn't actually free
	// the entry until every handle closes AND the service stops; that
	// half-state confuses re-installs.
	_ = s.Stop(ctx)

	m, err := mgr.Connect()
	if err != nil {
		return wrapSCMConnect(err)
	}
	defer m.Disconnect()

	for _, u := range s.units() {
		sv, err := m.OpenService(u.name)
		if err != nil {
			// Already gone.
			continue
		}
		if err := sv.Delete(); err != nil {
			sv.Close()
			if errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
				// SCM already accepted a prior delete request.
				continue
			}
			return fmt.Errorf("delete service %s: %w", u.name, wrapSCMOp(err))
		}
		sv.Close()
	}
	return nil
}

func (s *scm) Status(ctx context.Context) ([]Status, error) {
	// `eidos gate status` is a read-only operation, so we deliberately
	// avoid `mgr.Connect()` (which requests SC_MANAGER_ALL_ACCESS and
	// fails for a non-elevated shell) and instead open the SCM database
	// with SC_MANAGER_CONNECT, then each service with the minimal
	// SERVICE_QUERY_STATUS|CONFIG. That makes status work for any user.
	scHandle, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, fmt.Errorf("open SCM (query): %w", err)
	}
	defer windows.CloseServiceHandle(scHandle)

	out := make([]Status, 0, 2)
	for _, u := range s.units() {
		st := Status{Name: u.name}
		sv, err := openServiceForQuery(scHandle, u.name)
		if err != nil {
			// ERROR_SERVICE_DOES_NOT_EXIST → not installed.
			out = append(out, st)
			continue
		}
		st.Installed = true
		// SCM has no separate "enabled" toggle distinct from StartType.
		// We treat any non-Disabled StartType as enabled to match the
		// systemd "is-enabled" semantics callers expect.
		if cfg, err := sv.Config(); err == nil {
			st.Enabled = cfg.StartType != mgr.StartDisabled
		}
		if q, err := sv.Query(); err == nil {
			if q.State == svc.Running {
				st.Active = true
				st.PID = int(q.ProcessId)
			}
		}
		sv.Close()
		out = append(out, st)
	}
	return out, nil
}

// openServiceForQuery opens an *mgr.Service handle with read-only access
// rights so non-elevated users can run `eidos gate status`. The mgr package
// itself only exposes the all-access OpenService; we drop down to the
// underlying syscall to specify SERVICE_QUERY_STATUS|SERVICE_QUERY_CONFIG.
func openServiceForQuery(scmHandle windows.Handle, name string) (*mgr.Service, error) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.OpenService(scmHandle, namePtr, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return nil, err
	}
	return &mgr.Service{Name: name, Handle: h}, nil
}

// waitFor polls the given units until they all reach the target state or
// timeout elapses. On timeout it returns an error naming the unit and the
// last-observed state — `eidos gate start` should never claim success for
// a service that crashed back to Stopped immediately after sv.Start().
//
// The caller chooses the unit set: Start passes installUnits() so that a
// daemon-only install doesn't sit waiting for an absent relay; Stop passes
// units() so a residual relay (left from a prior --with-local-relay run)
// is also waited on.
func (s *scm) waitFor(ctx context.Context, target svc.State, timeout time.Duration, units []scmUnit) error {
	deadline := time.Now().Add(timeout)
	var last []namedState
	for time.Now().Before(deadline) {
		states, err := s.observeStates(units)
		if err == nil {
			last = states
			if statesAllAt(states, target) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return fmt.Errorf("services did not reach %s within %s: %s", stateName(target), timeout, formatStates(last))
}

// namedState pairs a unit with the state we last observed for it. Returned
// from observeStates so the timeout error can name which units are still
// not where we want them to be.
type namedState struct {
	name  string
	state svc.State
	// missing is true when the unit has no SCM entry; treated as
	// Stopped for the "all stopped?" check.
	missing bool
}

func (s *scm) observeStates(units []scmUnit) ([]namedState, error) {
	scHandle, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, err
	}
	defer windows.CloseServiceHandle(scHandle)

	out := make([]namedState, 0, len(units))
	for _, u := range units {
		sv, err := openServiceForQuery(scHandle, u.name)
		if err != nil {
			out = append(out, namedState{name: u.name, missing: true})
			continue
		}
		q, qerr := sv.Query()
		sv.Close()
		if qerr != nil {
			out = append(out, namedState{name: u.name, missing: true})
			continue
		}
		out = append(out, namedState{name: u.name, state: q.State})
	}
	return out, nil
}

// statesAllAt reports whether every observed unit is at the target state.
// A missing unit counts as Stopped (used for the post-Stop poll).
func statesAllAt(states []namedState, target svc.State) bool {
	for _, st := range states {
		if st.missing {
			if target == svc.Stopped {
				continue
			}
			return false
		}
		if st.state != target {
			return false
		}
	}
	return true
}

// formatStates renders the observed-state list for a timeout error message.
func formatStates(states []namedState) string {
	if len(states) == 0 {
		return "no services observed"
	}
	parts := make([]string, 0, len(states))
	for _, st := range states {
		if st.missing {
			parts = append(parts, st.name+"=missing")
			continue
		}
		parts = append(parts, st.name+"="+stateName(st.state))
	}
	return strings.Join(parts, ", ")
}

// stateName turns a svc.State into the lowercase token used in error
// messages. Unknown values fall through to a numeric fallback so we never
// hide a state from the user.
func stateName(s svc.State) string {
	switch s {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start-pending"
	case svc.StopPending:
		return "stop-pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue-pending"
	case svc.PausePending:
		return "pause-pending"
	case svc.Paused:
		return "paused"
	default:
		return fmt.Sprintf("state(%d)", s)
	}
}

// buildImagePath assembles the SCM ImagePathName from a binary plus args,
// quoting each segment so embedded spaces survive Windows command-line
// parsing. SCM stores this string verbatim in the registry and Windows
// reparses it on service start.
func buildImagePath(bin string, args []string) string {
	out := syscall.EscapeArg(bin)
	for _, a := range args {
		out += " " + syscall.EscapeArg(a)
	}
	return out
}

// wrapSCMConnect classifies the most common Connect failure (lack of admin
// privileges) for a clearer CLI error.
func wrapSCMConnect(err error) error {
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return fmt.Errorf(
			"open SCM: %w\nhint: SCM operations require an elevated shell on Windows; rerun from an Administrator PowerShell",
			err,
		)
	}
	return fmt.Errorf("open SCM: %w", err)
}

// wrapSCMOp adds the same "needs admin" hint to per-service operations that
// SCM rejects with ERROR_ACCESS_DENIED. Other errors pass through unchanged.
func wrapSCMOp(err error) error {
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return fmt.Errorf("%w (rerun from an elevated/Administrator shell)", err)
	}
	return err
}

// init keeps the same sanity check the launchd implementation runs on the
// shared unit-name constants — SCM service names must not contain forward
// slashes or backslashes.
func init() {
	for _, name := range []string{DaemonUnitName, RelayUnitName} {
		if strings.ContainsAny(name, `/\`) {
			panic(errors.New("service unit name contains a character SCM service names reject: " + name))
		}
	}
}
