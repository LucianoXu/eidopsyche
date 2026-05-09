package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveStateDirPriority(t *testing.T) {
	t.Setenv("EIDOS_GATE_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	got, err := ResolveStateDir("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, ".eidos", "gate")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	t.Setenv("XDG_STATE_HOME", "/x")
	got, _ = ResolveStateDir("")
	if got != "/x/eidos/gate" {
		t.Fatalf("xdg: got %q", got)
	}

	t.Setenv("EIDOS_GATE_HOME", "/m")
	got, _ = ResolveStateDir("")
	if got != "/m" {
		t.Fatalf("env: got %q", got)
	}

	got, _ = ResolveStateDir("/flag")
	if got != "/flag" {
		t.Fatalf("flag: got %q", got)
	}
}

func TestDefaults_Dashboard(t *testing.T) {
	d := Defaults()
	if !d.Dashboard.Enabled {
		t.Error("dashboard enabled by default")
	}
	if d.Dashboard.Listen != "127.0.0.1:22893" {
		t.Errorf("dashboard listen default: got %q want 127.0.0.1:22893", d.Dashboard.Listen)
	}
}

// TestLoadIgnoresStrayRelaySection verifies that a gate config.toml containing
// a legacy [relay] section (from a pre-v0.5 install) is silently ignored rather
// than causing a decode error. BurntSushi/toml does not error on unknown keys by
// default; this test pins that behaviour so a regression is caught immediately.
func TestLoadIgnoresStrayRelaySection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
log_level = "info"

[daemon]
socket = "sock"

[relay]
mode = "paired"
listen = "0.0.0.0:7777"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Errorf("legacy [relay] section should be ignored, not error: %v", err)
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
