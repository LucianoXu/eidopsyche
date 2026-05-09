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
// guidance to run `eidos gate daemon` / `eidos relay start` directly.
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
	// StateDir is exposed to the running daemon service via $EIDOS_GATE_HOME so
	// alternate state directories survive systemd's clean environment.
	StateDir string
	// Scope picks user vs system mode.
	Scope Scope
	// UnitDir overrides the platform-derived unit directory; intended for
	// tests. Empty means "use the platform default for Scope".
	UnitDir string
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
// on this host. Install/Start/Stop/Uninstall are split by unit role so the
// gate CLI can manage only the daemon, while cmd/eidos/relay manages only
// the relay unit, independently.
type Manager interface {
	// InstallDaemon writes / refreshes the gate daemon unit file. Idempotent.
	InstallDaemon(ctx context.Context) error
	// InstallRelay writes / refreshes the relay unit file with its working
	// directory set to relayDir. Idempotent.
	InstallRelay(ctx context.Context, relayDir string) error
	// UninstallDaemon stops, disables, and removes the daemon unit. Idempotent.
	UninstallDaemon(ctx context.Context) error
	// UninstallRelay stops, disables, and removes the relay unit. Idempotent.
	UninstallRelay(ctx context.Context) error
	// StartDaemon installs (if needed), enables, and starts the daemon unit.
	StartDaemon(ctx context.Context) error
	// StartRelay installs (if needed), enables, and starts the relay unit.
	StartRelay(ctx context.Context, relayDir string) error
	// StopDaemon stops the daemon unit without uninstalling it.
	StopDaemon(ctx context.Context) error
	// StopRelay stops the relay unit without uninstalling it.
	StopRelay(ctx context.Context) error
	// RestartDaemon restarts the daemon unit so a freshly-installed binary
	// (e.g. just dropped in by `eidos self-update`) takes effect. It is
	// idempotent: if the unit is currently stopped it is started; if running
	// it is bounced. Callers that want a no-op when the unit is not even
	// installed should consult Status() first — RestartDaemon itself returns
	// the underlying manager error in that case.
	RestartDaemon(ctx context.Context) error
	// Status returns one entry per managed unit, in stable order.
	// It always checks both daemon and relay so residuals stay discoverable.
	Status(ctx context.Context) ([]Status, error)
}

// Unit names. Exported so callers (CLI, tests) can refer to them by name.
const (
	DaemonUnitName = "eidos-gate-daemon"
	RelayUnitName  = "eidos-relay"
)

// New returns the platform-appropriate Manager, or ErrUnsupported (possibly
// wrapped with platform-specific detail).
func New(cfg Config) (Manager, error) {
	return platformNew(cfg)
}
