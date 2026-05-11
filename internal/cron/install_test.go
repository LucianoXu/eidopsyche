package cron

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstall_WritesAtomically(t *testing.T) {
	dir := t.TempDir()
	spool := filepath.Join(dir, "crontabs", "eidos")
	inst := &Installer{SpoolPath: spool} // SudoCommand="" → direct write
	body := "*/1 * * * * /bin/true\n"
	if err := inst.Install(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(spool)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("body mismatch: got %q want %q", got, body)
	}
	info, err := os.Stat(spool)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm: got %o want 0600", info.Mode().Perm())
	}
}

func TestInstall_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	spool := filepath.Join(dir, "eidos")
	inst := &Installer{SpoolPath: spool}
	if err := inst.Install(context.Background(), "old\n"); err != nil {
		t.Fatal(err)
	}
	if err := inst.Install(context.Background(), "new\n"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(spool)
	if string(got) != "new\n" {
		t.Errorf("got %q", got)
	}
}

func TestInstall_EmptySpoolPath(t *testing.T) {
	inst := &Installer{}
	if err := inst.Install(context.Background(), "x\n"); err == nil {
		t.Error("expected error for unset SpoolPath")
	}
}
