package cron

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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

// writeViaSudo performs an atomic write to a root-owned path. The
// strategy mirrors writeDirect's tmp+rename pattern but each step is
// escalated via sudo: write body to a sibling tmp file via `sudo tee`,
// chmod 0600 on the tmp, then `sudo mv` to swap into the final path.
// On any failure the tmp is removed best-effort so we don't litter
// the spool directory. busybox crond never observes a partially
// written crontab — either the old body remains or the new body is
// installed wholesale.
func writeViaSudo(ctx context.Context, sudoBin, path, body string) error {
	dir := filepath.Dir(path)
	if err := runSudo(ctx, sudoBin, "mkdir", "-p", dir); err != nil {
		return fmt.Errorf("sudo mkdir %s: %w", dir, err)
	}
	// Suffix the tmp with the pid + nanos so concurrent installs from
	// different processes don't clash; same-process serialization is the
	// caller's job. The leading dot keeps it hidden in directory listings.
	tmpPath := fmt.Sprintf("%s/.crontab.%d.%d", dir, os.Getpid(), time.Now().UnixNano())

	teeCmd := exec.CommandContext(ctx, sudoBin, "-n", "tee", tmpPath)
	teeCmd.Stdin = strings.NewReader(body)
	teeCmd.Stdout = nil
	teeCmd.Stderr = os.Stderr
	if err := teeCmd.Run(); err != nil {
		_ = runSudo(ctx, sudoBin, "rm", "-f", tmpPath)
		return fmt.Errorf("sudo tee %s: %w", tmpPath, err)
	}
	if err := runSudo(ctx, sudoBin, "chmod", "0600", tmpPath); err != nil {
		_ = runSudo(ctx, sudoBin, "rm", "-f", tmpPath)
		return fmt.Errorf("sudo chmod 0600 %s: %w", tmpPath, err)
	}
	if err := runSudo(ctx, sudoBin, "mv", tmpPath, path); err != nil {
		_ = runSudo(ctx, sudoBin, "rm", "-f", tmpPath)
		return fmt.Errorf("sudo mv %s %s: %w", tmpPath, path, err)
	}
	return nil
}

// runSudo runs `sudo -n <argv...>`, discarding stdout and forwarding
// stderr. Returns the command's run error.
func runSudo(ctx context.Context, sudoBin string, argv ...string) error {
	cmd := exec.CommandContext(ctx, sudoBin, append([]string{"-n"}, argv...)...)
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
