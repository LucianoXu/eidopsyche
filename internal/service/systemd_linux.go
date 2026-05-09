//go:build linux

package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func platformNew(cfg Config) (Manager, error) {
	if cfg.BinaryPath == "" {
		return nil, fmt.Errorf("service.New: BinaryPath is required")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil, fmt.Errorf("systemd not available: %w", ErrUnsupported)
	}
	return &systemd{cfg: cfg}, nil
}

type systemd struct {
	cfg Config
	// run is the function that actually invokes systemctl. Tests substitute
	// it; production callers leave it nil and fall through to exec.Command
	// via the systemctl method below. Mirrors the launchd manager's seam.
	run func(ctx context.Context, args ...string) ([]byte, error)
}

func (s *systemd) unitDir() (string, error) {
	if s.cfg.UnitDir != "" {
		return s.cfg.UnitDir, nil
	}
	if s.cfg.Scope == ScopeSystem {
		return "/etc/systemd/system", nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

// systemctl runs `systemctl [--user] <args...>` and returns combined output.
// It does NOT propagate non-zero exit as a hard error for query-shaped calls
// (is-enabled, is-active) because systemd uses exit code to signal state.
//
// The actual invocation is routed through s.run, which tests can override.
func (s *systemd) systemctl(ctx context.Context, args ...string) ([]byte, error) {
	full := args
	if s.cfg.Scope == ScopeUser {
		full = append([]string{"--user"}, args...)
	}
	if s.run != nil {
		return s.run(ctx, full...)
	}
	out, err := exec.CommandContext(ctx, "systemctl", full...).CombinedOutput()
	return out, err
}

// installOne writes a single unit file and triggers daemon-reload.
func (s *systemd) installOne(ctx context.Context, unitName, unitContent string) error {
	dir, err := s.unitDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create unit dir %s: %w", dir, err)
	}
	if err := writeUnitIfChanged(filepath.Join(dir, unitName+".service"), unitContent); err != nil {
		return err
	}
	if out, err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// uninstallOne stops, disables, removes the unit file, reloads, and
// resets any failed state. Best-effort: already-absent units are not errors.
func (s *systemd) uninstallOne(ctx context.Context, unitName string) error {
	_, _ = s.systemctl(ctx, "stop", unitName)
	_, _ = s.systemctl(ctx, "disable", unitName)

	dir, err := s.unitDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, unitName+".service")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	if out, err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	_, _ = s.systemctl(ctx, "reset-failed", unitName)
	return nil
}

// enableNow enables and starts a single unit (install must have been called
// first so the unit file is on disk).
func (s *systemd) enableNow(ctx context.Context, unitName string) error {
	out, err := s.systemctl(ctx, "enable", "--now", unitName)
	if err != nil {
		return fmt.Errorf("systemctl enable --now %s: %w (output: %s)",
			unitName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// stopOne stops a single unit. Non-existent units are treated as already-stopped.
func (s *systemd) stopOne(ctx context.Context, unitName string) error {
	out, err := s.systemctl(ctx, "stop", unitName)
	if err != nil {
		if strings.Contains(string(out), "not loaded") || strings.Contains(string(out), "not found") {
			return nil
		}
		return fmt.Errorf("systemctl stop %s: %w (output: %s)",
			unitName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *systemd) InstallDaemon(ctx context.Context) error {
	return s.installOne(ctx, DaemonUnitName, s.daemonUnit())
}

func (s *systemd) InstallRelay(ctx context.Context, relayDir string) error {
	return s.installOne(ctx, RelayUnitName, s.relayUnit(relayDir))
}

func (s *systemd) UninstallDaemon(ctx context.Context) error {
	return s.uninstallOne(ctx, DaemonUnitName)
}

func (s *systemd) UninstallRelay(ctx context.Context) error {
	return s.uninstallOne(ctx, RelayUnitName)
}

func (s *systemd) StartDaemon(ctx context.Context) error {
	if err := s.InstallDaemon(ctx); err != nil {
		return err
	}
	return s.enableNow(ctx, DaemonUnitName)
}

func (s *systemd) StartRelay(ctx context.Context, relayDir string) error {
	if err := s.InstallRelay(ctx, relayDir); err != nil {
		return err
	}
	return s.enableNow(ctx, RelayUnitName)
}

func (s *systemd) StopDaemon(ctx context.Context) error {
	return s.stopOne(ctx, DaemonUnitName)
}

func (s *systemd) StopRelay(ctx context.Context) error {
	return s.stopOne(ctx, RelayUnitName)
}

// RestartDaemon issues `systemctl [--user] restart <daemon>`. systemd's
// `restart` is atomic — the unit's main process is replaced under one
// transaction — so we prefer it over a stop+start sequence which would
// briefly leave the daemon down even when the unit was already running.
//
// The unit must be installed; restart on a non-installed unit returns
// systemd's "Unit not loaded" error verbatim. Callers that want the
// "no-op when nothing installed" semantics should gate on Status() first.
func (s *systemd) RestartDaemon(ctx context.Context) error {
	out, err := s.systemctl(ctx, "restart", DaemonUnitName)
	if err != nil {
		return fmt.Errorf("systemctl restart %s: %w (output: %s)",
			DaemonUnitName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *systemd) Status(ctx context.Context) ([]Status, error) {
	dir, err := s.unitDir()
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, 2)
	for _, name := range []string{DaemonUnitName, RelayUnitName} {
		st := Status{Name: name}
		if _, err := os.Stat(filepath.Join(dir, name+".service")); err == nil {
			st.Installed = true
		}
		if b, _ := s.systemctl(ctx, "is-enabled", name); strings.TrimSpace(string(b)) == "enabled" {
			st.Enabled = true
		}
		if b, _ := s.systemctl(ctx, "is-active", name); strings.TrimSpace(string(b)) == "active" {
			st.Active = true
		}
		if b, err := s.systemctl(ctx, "show", "-p", "MainPID", "--value", name); err == nil {
			if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid > 0 {
				st.PID = pid
			}
		}
		out = append(out, st)
	}
	return out, nil
}

func (s *systemd) daemonUnit() string {
	envLine := ""
	if s.cfg.StateDir != "" {
		envLine = "Environment=EIDOS_GATE_HOME=" + s.cfg.StateDir + "\n"
	}
	return `[Unit]
Description=Eidopsyche gate daemon
After=network-online.target

[Service]
Type=simple
ExecStart=` + s.cfg.BinaryPath + ` gate daemon
` + envLine + `Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`
}

// relayUnit generates the systemd unit content for the relay service.
// relayDir is the relay's working directory and is also passed as --dir.
func (s *systemd) relayUnit(relayDir string) string {
	wdLine := ""
	dirFlag := ""
	if relayDir != "" {
		wdLine = "WorkingDirectory=" + relayDir + "\n"
		dirFlag = " --dir " + relayDir
	}
	return `[Unit]
Description=Eidopsyche relay
After=network-online.target

[Service]
Type=simple
` + wdLine + `ExecStart=` + s.cfg.BinaryPath + ` relay start` + dirFlag + `
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`
}
