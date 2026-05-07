// Package service manages the gate daemon and relay as native OS services.
//
// On Linux with systemd it installs systemd user units (or system units when
// the caller requests Scope == ScopeSystem) and drives them via systemctl.
// On other platforms it returns ErrUnsupported so the CLI can print a clear
// "manage processes manually" message rather than silently misbehaving.
package service

import (
	"context"
	"errors"
)

// ErrUnsupported is returned by New when the host platform has no first-class
// service manager wired up. Callers should surface this to the user with
// guidance to run `eidos gate daemon` / `eidos gate relay` directly.
var ErrUnsupported = errors.New("system service management is not supported on this platform")

// Scope picks where unit files live and which systemctl context is used.
type Scope int

const (
	// ScopeUser writes to ~/.config/systemd/user/ and uses `systemctl --user`.
	// Survives across logins only when `loginctl enable-linger <user>` has been
	// run on the host.
	ScopeUser Scope = iota
	// ScopeSystem writes to /etc/systemd/system/ and uses `systemctl`.
	// Requires root; survives reboot.
	ScopeSystem
)

// String returns "user" / "system".
func (s Scope) String() string {
	if s == ScopeSystem {
		return "system"
	}
	return "user"
}

// Config carries everything a Manager needs to install and operate units.
type Config struct {
	// BinaryPath is the absolute path to the eidos binary that the unit's
	// ExecStart will invoke. Resolve via os.Executable() at the call site.
	BinaryPath string
	// StateDir is exposed to the running services via $EIDOS_GATE_HOME so
	// alternate state directories survive systemd's clean environment.
	StateDir string
	// Scope picks user vs system mode.
	Scope Scope
	// UnitDir overrides the platform-derived unit directory; intended for
	// tests. Empty means "use the platform default for Scope".
	UnitDir string
	// WithRelay controls whether Install / Start manage the relay unit.
	// false (default): only the daemon unit is managed; any residual relay
	// unit on disk is left untouched. true: both units are managed. Stop /
	// Uninstall / Status iterate both names regardless so residuals are
	// always reachable for cleanup.
	WithRelay bool
}

// Status describes one managed unit.
type Status struct {
	Name      string
	Installed bool
	Enabled   bool
	Active    bool
	PID       int
}

// Manager is the abstract interface to whatever service manager is in charge
// on this host.
type Manager interface {
	// Install writes / refreshes unit files. Idempotent.
	Install(ctx context.Context) error
	// Uninstall stops, disables, and removes unit files. Idempotent: works
	// from any prior state, including "never installed".
	Uninstall(ctx context.Context) error
	// Start installs (if needed), enables, and starts both units.
	// Idempotent — already-running units are left alone.
	Start(ctx context.Context) error
	// Stop stops both units without uninstalling them.
	Stop(ctx context.Context) error
	// Status returns one entry per managed unit, in stable order.
	Status(ctx context.Context) ([]Status, error)
}

// Unit names. Exported so callers (CLI, tests) can refer to them by name.
const (
	DaemonUnitName = "eidos-gate-daemon"
	RelayUnitName  = "eidos-gate-relay"
)

// New returns the platform-appropriate Manager, or ErrUnsupported (possibly
// wrapped with platform-specific detail).
func New(cfg Config) (Manager, error) {
	return platformNew(cfg)
}
