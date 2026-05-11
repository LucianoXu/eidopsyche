package cron

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Installer writes crontab bodies to the busybox crond spool. Both the
// supervisor's initial render (PID-1 startup) and the heartbeat.interval
// apply hook flow through this. busybox crond requires the spool entry
// to be root-owned 0600 (it silently ignores user-owned files), so
// production uses sudo; tests use a direct write into a tempdir.
type Installer struct {
	// SpoolPath is the per-user crontab file (e.g. /var/spool/cron/crontabs/eidos).
	SpoolPath string
	// SudoCommand is the binary used to escalate. Production: "sudo".
	// Tests leave empty to write directly and skip the chmod-via-sudo step.
	SudoCommand string
}

// DefaultInstaller returns the production Installer pointing at the
// container's busybox spool and escalating via `sudo -n`.
func DefaultInstaller() *Installer {
	return &Installer{SpoolPath: CrontabPath, SudoCommand: "sudo"}
}

// Install writes body to SpoolPath. Atomic: writes to a tmp file in the
// same directory and renames. Sets 0600 (chmod via sudo when SudoCommand
// is set, direct chmod when empty).
func (i *Installer) Install(ctx context.Context, body string) error {
	if i == nil || i.SpoolPath == "" {
		return fmt.Errorf("cron.Install: SpoolPath unset")
	}
	if i.SudoCommand == "" {
		return writeDirect(i.SpoolPath, body)
	}
	return writeViaSudo(ctx, i.SudoCommand, i.SpoolPath, body)
}

func writeDirect(path, body string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".crontab-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		cleanup()
		return err
	}
	return os.Rename(tmpPath, path)
}

func writeViaSudo(ctx context.Context, sudoBin, path, body string) error {
	cmd := exec.CommandContext(ctx, sudoBin, "-n", "tee", path)
	cmd.Stdin = strings.NewReader(body)
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo tee %s: %w", path, err)
	}
	chmod := exec.CommandContext(ctx, sudoBin, "-n", "chmod", "0600", path)
	chmod.Stdout = nil
	chmod.Stderr = os.Stderr
	if err := chmod.Run(); err != nil {
		return fmt.Errorf("sudo chmod 0600 %s: %w", path, err)
	}
	return nil
}
