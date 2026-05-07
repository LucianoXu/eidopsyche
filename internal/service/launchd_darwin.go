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
func (l *launchd) launchctl(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "launchctl", args...).CombinedOutput()
}

// Install writes / refreshes plist files. Idempotent. Note: unlike systemd
// which has `daemon-reload` to flush the manager's view, launchd needs an
// explicit bootout/bootstrap to pick up an updated plist; that is handled
// inside Start so a stale Install does not surprise users.
func (l *launchd) Install(ctx context.Context) error {
	dir, err := launchdPlistDir(l.cfg.Scope, l.cfg.UnitDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create plist dir %s: %w", dir, err)
	}
	if err := os.MkdirAll(launchdLogDir(l.cfg.StateDir), 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	for _, u := range l.units() {
		path, err := l.plistPath(u.label)
		if err != nil {
			return err
		}
		content := launchdPlist(u.label, l.cfg.BinaryPath, l.cfg.StateDir, u.args)
		if err := writeUnitIfChanged(path, content); err != nil {
			return fmt.Errorf("write plist %s: %w", path, err)
		}
	}
	return nil
}

// Start writes plists, boots out any prior instance (so changes to an
// already-loaded plist take effect), then bootstraps. RunAtLoad=true in the
// plist makes bootstrap also start the process.
func (l *launchd) Start(ctx context.Context) error {
	if err := l.Install(ctx); err != nil {
		return err
	}
	domain := l.domainTarget()
	for _, u := range l.units() {
		// Best-effort bootout — service may not be loaded yet.
		_, _ = l.launchctl(ctx, "bootout", l.serviceTarget(u.label))

		path, err := l.plistPath(u.label)
		if err != nil {
			return err
		}
		out, err := l.launchctl(ctx, "bootstrap", domain, path)
		if err != nil {
			return fmt.Errorf("launchctl bootstrap %s: %w (output: %s)",
				u.label, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Stop sends SIGTERM via `launchctl stop`. Because the plist sets KeepAlive
// = {SuccessfulExit=false}, the gate daemon's clean SIGTERM handler exits 0
// and launchd does not respawn it. The agent stays loaded; bring it back
// with `eidos gate start` (or remove with purge).
func (l *launchd) Stop(ctx context.Context) error {
	for _, u := range l.units() {
		out, err := l.launchctl(ctx, "stop", l.serviceTarget(u.label))
		if err != nil {
			lower := strings.ToLower(string(out))
			if strings.Contains(lower, "could not find") ||
				strings.Contains(lower, "no such") ||
				strings.Contains(lower, "not loaded") {
				continue
			}
			return fmt.Errorf("launchctl stop %s: %w (output: %s)",
				u.label, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Uninstall boots out the agents and removes the plist files. Best-effort
// for partially-installed setups.
func (l *launchd) Uninstall(ctx context.Context) error {
	for _, u := range l.units() {
		_, _ = l.launchctl(ctx, "bootout", l.serviceTarget(u.label))

		path, err := l.plistPath(u.label)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	return nil
}

func (l *launchd) Status(ctx context.Context) ([]Status, error) {
	out := make([]Status, 0, 2)
	for _, u := range l.units() {
		st := Status{Name: u.label}

		path, err := l.plistPath(u.label)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(path); err == nil {
			st.Installed = true
		}

		if b, err := l.launchctl(ctx, "print", l.serviceTarget(u.label)); err == nil {
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

// units returns the ordered set of services this manager owns. Order is
// stable so Status output is consistent across calls.
type unitDef struct {
	label string
	args  []string
}

func (l *launchd) units() []unitDef {
	return []unitDef{
		{label: DaemonUnitName, args: []string{"gate", "daemon"}},
		{label: RelayUnitName, args: []string{"gate", "relay"}},
	}
}
