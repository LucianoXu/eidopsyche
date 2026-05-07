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

func TestDefaultsRelayDisabled(t *testing.T) {
	cfg := Defaults()
	if cfg.Relay.Enabled {
		t.Errorf("Defaults().Relay.Enabled = true, want false")
	}
	if cfg.Relay.Listen != "0.0.0.0:22895" {
		t.Errorf("Defaults().Relay.Listen = %q, want 0.0.0.0:22895", cfg.Relay.Listen)
	}
	if cfg.Relay.Mode != "paired" {
		t.Errorf("Defaults().Relay.Mode = %q, want paired", cfg.Relay.Mode)
	}
}

func TestRelayEnabledHelper(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		want    bool
	}{
		{"explicit true", true, true},
		{"explicit false", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{Relay: RelayConfig{Enabled: tc.enabled}}
			if c.RelayEnabled() != tc.want {
				t.Errorf("RelayEnabled() = %v, want %v", c.RelayEnabled(), tc.want)
			}
		})
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

func TestSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	cfg := Defaults()
	cfg.LogLevel = "debug"
	cfg.Relay.Enabled = true
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
	if !loaded.Relay.Enabled {
		t.Fatalf("Relay.Enabled = false after roundtrip")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}
