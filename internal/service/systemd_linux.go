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
func (s *systemd) systemctl(ctx context.Context, args ...string) ([]byte, error) {
	full := args
	if s.cfg.Scope == ScopeUser {
		full = append([]string{"--user"}, args...)
	}
	out, err := exec.CommandContext(ctx, "systemctl", full...).CombinedOutput()
	return out, err
}

func (s *systemd) Install(ctx context.Context) error {
	dir, err := s.unitDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create unit dir %s: %w", dir, err)
	}
	if err := writeUnitIfChanged(filepath.Join(dir, DaemonUnitName+".service"), s.daemonUnit()); err != nil {
		return err
	}
	if err := writeUnitIfChanged(filepath.Join(dir, RelayUnitName+".service"), s.relayUnit()); err != nil {
		return err
	}
	if out, err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *systemd) Start(ctx context.Context) error {
	if err := s.Install(ctx); err != nil {
		return err
	}
	out, err := s.systemctl(ctx, "enable", "--now", DaemonUnitName, RelayUnitName)
	if err != nil {
		return fmt.Errorf("systemctl enable --now: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *systemd) Stop(ctx context.Context) error {
	out, err := s.systemctl(ctx, "stop", DaemonUnitName, RelayUnitName)
	if err != nil {
		// Non-existent units exit 5; treat as already-stopped.
		if strings.Contains(string(out), "not loaded") || strings.Contains(string(out), "not found") {
			return nil
		}
		return fmt.Errorf("systemctl stop: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *systemd) Uninstall(ctx context.Context) error {
	// Best-effort stop + disable. A clean teardown should never fail because
	// units happen to be missing or already stopped.
	_, _ = s.systemctl(ctx, "stop", DaemonUnitName, RelayUnitName)
	_, _ = s.systemctl(ctx, "disable", DaemonUnitName, RelayUnitName)

	dir, err := s.unitDir()
	if err != nil {
		return err
	}
	for _, u := range []string{DaemonUnitName, RelayUnitName} {
		path := filepath.Join(dir, u+".service")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	if out, err := s.systemctl(ctx, "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	// reset-failed flushes failed-state for any runs that errored before this teardown.
	_, _ = s.systemctl(ctx, "reset-failed", DaemonUnitName, RelayUnitName)
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

// writeUnitIfChanged writes content to path only when it differs from the
// existing file. Avoids gratuitous mtime updates and unit reloads.
func writeUnitIfChanged(path, content string) error {
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == content {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o644)
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

func (s *systemd) relayUnit() string {
	envLine := ""
	if s.cfg.StateDir != "" {
		envLine = "Environment=EIDOS_GATE_HOME=" + s.cfg.StateDir + "\n"
	}
	return `[Unit]
Description=Eidopsyche gate relay
After=` + DaemonUnitName + `.service

[Service]
Type=simple
ExecStart=` + s.cfg.BinaryPath + ` gate relay
` + envLine + `Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`
}
