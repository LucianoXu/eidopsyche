//go:build linux

package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise the file-content + filesystem logic of the systemd
// manager. They deliberately avoid running `systemctl` itself: they steer
// every code path that doesn't shell out, plus they verify Install writes
// the expected files. The systemctl-driven branches (Start, Stop, Status's
// is-active probe) are exercised by the end-to-end smoke test on the host.

func newTestManager(t *testing.T) (*systemd, string) {
	t.Helper()
	tmp := t.TempDir()
	return &systemd{cfg: Config{
		BinaryPath: "/usr/local/bin/eidos",
		StateDir:   "/tmp/test-state",
		Scope:      ScopeUser,
		UnitDir:    tmp,
	}}, tmp
}

func TestUnitContentEmbedsBinaryAndStateDir(t *testing.T) {
	mgr, _ := newTestManager(t)

	daemon := mgr.daemonUnit()
	relay := mgr.relayUnit()

	for _, want := range []string{
		"ExecStart=/usr/local/bin/eidos gate daemon",
		"Environment=EIDOS_GATE_HOME=/tmp/test-state",
		"After=network-online.target",
		"Restart=on-failure",
	} {
		if !strings.Contains(daemon, want) {
			t.Errorf("daemon unit missing %q\nfull:\n%s", want, daemon)
		}
	}
	for _, want := range []string{
		"ExecStart=/usr/local/bin/eidos gate relay",
		"Environment=EIDOS_GATE_HOME=/tmp/test-state",
		"After=eidos-gate-daemon.service",
	} {
		if !strings.Contains(relay, want) {
			t.Errorf("relay unit missing %q\nfull:\n%s", want, relay)
		}
	}
}

func TestUnitContentOmitsEmptyStateDir(t *testing.T) {
	mgr := &systemd{cfg: Config{BinaryPath: "/eidos", Scope: ScopeUser}}
	for name, content := range map[string]string{"daemon": mgr.daemonUnit(), "relay": mgr.relayUnit()} {
		if strings.Contains(content, "Environment=EIDOS_GATE_HOME=") {
			t.Errorf("%s: empty StateDir should not produce an Environment= line\n%s", name, content)
		}
	}
}

func TestWriteUnitIfChangedSkipsIdenticalContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "u.service")
	content := "alpha\n"
	if err := writeUnitIfChanged(path, content); err != nil {
		t.Fatal(err)
	}
	info1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sleep so any rewrite would change mtime in a detectable way.
	if err := os.Chtimes(path, info1.ModTime().Add(-1), info1.ModTime().Add(-1)); err != nil {
		t.Fatal(err)
	}
	info1, _ = os.Stat(path)

	if err := writeUnitIfChanged(path, content); err != nil {
		t.Fatal(err)
	}
	info2, _ := os.Stat(path)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Errorf("mtime changed despite identical content: %v → %v", info1.ModTime(), info2.ModTime())
	}

	if err := writeUnitIfChanged(path, "beta\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "beta\n" {
		t.Errorf("changed content not written: %q", got)
	}
}

func TestUnitDirHonoursOverride(t *testing.T) {
	tmp := t.TempDir()
	mgr := &systemd{cfg: Config{BinaryPath: "/eidos", Scope: ScopeUser, UnitDir: tmp}}
	d, err := mgr.unitDir()
	if err != nil {
		t.Fatal(err)
	}
	if d != tmp {
		t.Errorf("unitDir: got %q, want %q", d, tmp)
	}
}

func TestUnitDirRespectsXDGAndScope(t *testing.T) {
	t.Setenv("HOME", "/home/test")

	t.Setenv("XDG_CONFIG_HOME", "/x")
	mgr := &systemd{cfg: Config{BinaryPath: "/eidos", Scope: ScopeUser}}
	got, err := mgr.unitDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/x/systemd/user" {
		t.Errorf("XDG user: got %q", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	got, err = mgr.unitDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/home/test/.config/systemd/user" {
		t.Errorf("HOME user: got %q", got)
	}

	mgr.cfg.Scope = ScopeSystem
	got, err = mgr.unitDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/etc/systemd/system" {
		t.Errorf("system: got %q", got)
	}
}

func TestStatusReportsInstalledFlag(t *testing.T) {
	// Pure-filesystem status: unit file presence drives Installed=true.
	// The systemctl-driven flags (Enabled, Active, PID) are validated by the
	// end-to-end smoke test, not here.
	mgr, dir := newTestManager(t)

	st, err := mgr.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		if s.Installed {
			t.Errorf("%s reported installed before any unit was written", s.Name)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, DaemonUnitName+".service"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = mgr.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		want := s.Name == DaemonUnitName
		if s.Installed != want {
			t.Errorf("%s: Installed=%v, want %v", s.Name, s.Installed, want)
		}
	}
}

func TestScopeString(t *testing.T) {
	if ScopeUser.String() != "user" {
		t.Errorf("ScopeUser: %q", ScopeUser.String())
	}
	if ScopeSystem.String() != "system" {
		t.Errorf("ScopeSystem: %q", ScopeSystem.String())
	}
}
