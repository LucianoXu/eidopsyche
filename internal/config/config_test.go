package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveStateDirPriority(t *testing.T) {
	t.Setenv("MINDGATE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	got, err := ResolveStateDir("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, ".mindgate")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	t.Setenv("XDG_STATE_HOME", "/x")
	got, _ = ResolveStateDir("")
	if got != "/x/mindgate" {
		t.Fatalf("xdg: got %q", got)
	}

	t.Setenv("MINDGATE_HOME", "/m")
	got, _ = ResolveStateDir("")
	if got != "/m" {
		t.Fatalf("env: got %q", got)
	}

	got, _ = ResolveStateDir("/flag")
	if got != "/flag" {
		t.Fatalf("flag: got %q", got)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	cfg := Defaults()
	cfg.LogLevel = "debug"
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LogLevel != "debug" {
		t.Fatalf("got %q", loaded.LogLevel)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}
