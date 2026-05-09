//go:build darwin

package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func platformNew(cfg Config) (Manager, error) {
	if cfg.BinaryPath == "" {
		return nil, fmt.Errorf("service.New: BinaryPath is required")
	}
	if _, err := exec.LookPath("launchctl"); err != nil {
		return nil, fmt.Errorf("launchctl not found: %w", ErrUnsupported)
	}
	return &launchd{cfg: cfg}, nil
}

type launchd struct {
	cfg Config
	// run is the function that actually invokes the launchctl binary.
	// Tests substitute it; production callers leave it nil and fall
	// through to defaultLaunchctl via the launchctl method below.
	run func(ctx context.Context, args ...string) ([]byte, error)
}

// domainTarget returns the launchctl domain identifier for the configured
// scope: gui/<uid> for ScopeUser (the standard for LaunchAgents launched
// from the GUI session) or system for ScopeSystem (LaunchDaemons).
func (l *launchd) domainTarget() string {
	if l.cfg.Scope == ScopeSystem {
		return "system"
	}
	return fmt.Sprintf("gui/%d", os.Getuid())
}

// serviceTarget returns the full <domain>/<label> identifier for one unit.
func (l *launchd) serviceTarget(label string) string {
	return l.domainTarget() + "/" + label
}

// plistPath returns the on-disk plist location for a unit.
func (l *launchd) plistPath(label string) (string, error) {
	dir, err := launchdPlistDir(l.cfg.Scope, l.cfg.UnitDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, label+".plist"), nil
}

// launchctl runs `launchctl <args...>` and returns combined output. As with
// the systemd manager, query-shaped subcommands signal state via exit code,
// so we do not treat non-zero as a hard error.
//
// The actual invocation is routed through l.run, which tests can override.
func (l *launchd) launchctl(ctx context.Context, args ...string) ([]byte, error) {
	if l.run != nil {
		return l.run(ctx, args...)
	}
	return exec.CommandContext(ctx, "launchctl", args...).CombinedOutput()
}

// installOnePlist writes the plist for one unit and ensures the log directory
// exists. Unlike systemd, launchd needs an explicit bootout/bootstrap to pick
// up an updated plist; that is handled inside startOne.
func (l *launchd) installOnePlist(label, binaryPath, stateDir string, programArgs []string) error {
	dir, err := launchdPlistDir(l.cfg.Scope, l.cfg.UnitDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create plist dir %s: %w", dir, err)
	}
	if err := os.MkdirAll(launchdLogDir(stateDir), 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	path, err := l.plistPath(label)
	if err != nil {
		return err
	}
	content := launchdPlist(label, binaryPath, stateDir, programArgs)
	if err := writeUnitIfChanged(path, content); err != nil {
		return fmt.Errorf("write plist %s: %w", path, err)
	}
	return nil
}

// startOne boots out any prior instance then bootstraps the given plist.
// RunAtLoad=true in the plist makes bootstrap also start the process.
func (l *launchd) startOne(ctx context.Context, label string) error {
	// Best-effort bootout — service may not be loaded yet.
	_, _ = l.launchctl(ctx, "bootout", l.serviceTarget(label))

	path, err := l.plistPath(label)
	if err != nil {
		return err
	}
	out, err := l.launchctl(ctx, "bootstrap", l.domainTarget(), path)
	if err != nil {
		return fmt.Errorf("launchctl bootstrap %s: %w (output: %s)",
			label, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// stopOne sends SIGTERM via `launchctl stop`. Best-effort: any error from
// launchctl is swallowed because launchd's exit codes are not well-documented
// enough to distinguish "not running" from transient conditions. Use Status
// to determine actual stopped state.
func (l *launchd) stopOne(ctx context.Context, label string) {
	_, _ = l.launchctl(ctx, "stop", l.serviceTarget(label))
}

// uninstallOne boots out and removes the plist for one unit.
func (l *launchd) uninstallOne(ctx context.Context, label string) error {
	_, _ = l.launchctl(ctx, "bootout", l.serviceTarget(label))

	path, err := l.plistPath(label)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func (l *launchd) InstallDaemon(ctx context.Context) error {
	return l.installOnePlist(DaemonUnitName, l.cfg.BinaryPath, l.cfg.StateDir,
		[]string{"gate", "daemon"})
}

func (l *launchd) InstallRelay(ctx context.Context, relayDir string) error {
	args := []string{"relay", "start"}
	if relayDir != "" {
		args = append(args, "--dir", relayDir)
	}
	// For the relay we use relayDir as its stateDir so log files land alongside
	// relay data. Fall back to cfg.StateDir if relayDir is empty.
	stateDir := relayDir
	if stateDir == "" {
		stateDir = l.cfg.StateDir
	}
	return l.installOnePlist(RelayUnitName, l.cfg.BinaryPath, stateDir, args)
}

func (l *launchd) UninstallDaemon(ctx context.Context) error {
	return l.uninstallOne(ctx, DaemonUnitName)
}

func (l *launchd) UninstallRelay(ctx context.Context) error {
	return l.uninstallOne(ctx, RelayUnitName)
}

// StartDaemon writes the plist, boots out any prior instance, and bootstraps.
func (l *launchd) StartDaemon(ctx context.Context) error {
	if err := l.InstallDaemon(ctx); err != nil {
		return err
	}
	return l.startOne(ctx, DaemonUnitName)
}

// StartRelay writes the plist, boots out any prior instance, and bootstraps.
func (l *launchd) StartRelay(ctx context.Context, relayDir string) error {
	if err := l.InstallRelay(ctx, relayDir); err != nil {
		return err
	}
	return l.startOne(ctx, RelayUnitName)
}

// StopDaemon sends SIGTERM to the daemon. See stopOne for best-effort
// semantics.
func (l *launchd) StopDaemon(ctx context.Context) error {
	l.stopOne(ctx, DaemonUnitName)
	return nil
}

// StopRelay sends SIGTERM to the relay. See stopOne for best-effort
// semantics.
func (l *launchd) StopRelay(ctx context.Context) error {
	l.stopOne(ctx, RelayUnitName)
	return nil
}

func (l *launchd) Status(ctx context.Context) ([]Status, error) {
	out := make([]Status, 0, 2)
	for _, label := range []string{DaemonUnitName, RelayUnitName} {
		st := Status{Name: label}

		path, err := l.plistPath(label)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(path); err == nil {
			st.Installed = true
		}

		if b, err := l.launchctl(ctx, "print", l.serviceTarget(label)); err == nil {
			st.Enabled = true
			if pid := parseLaunchctlPrintPID(string(b)); pid > 0 {
				st.PID = pid
				// Presence of a PID in `launchctl print` is launchd's own
				// answer to "does this service have a live process". We
				// previously gated Active on parsing `state = running`,
				// which misreported services in the brief
				// `state = spawn scheduled` window after bootstrap as
				// stopped (despite having a PID). Trust the PID instead.
				st.Active = true
			}
		}

		out = append(out, st)
	}
	return out, nil
}
