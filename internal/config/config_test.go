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

func TestDefaults_RelayAuthAndTLS(t *testing.T) {
	d := Defaults()
	if !d.Relay.Auth.Required {
		t.Error("Defaults().Relay.Auth.Required must be true (NIP-17 §Recommendations default)")
	}
	if d.Relay.Auth.ServiceURL != "" {
		t.Errorf("Defaults().Relay.Auth.ServiceURL = %q, want empty", d.Relay.Auth.ServiceURL)
	}
	if d.Relay.TLS.CertFile != "" || d.Relay.TLS.KeyFile != "" {
		t.Errorf("Defaults().Relay.TLS must be empty: %+v", d.Relay.TLS)
	}
}

func TestLoad_LegacyConfigKeepsAuthRequiredDefault(t *testing.T) {
	// A v0.5 config has no [relay.auth] block; loading it must apply the
	// spec-default Required=true rather than Go's zero-value false.
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	body := `
log_level = "info"
[relay]
  enabled = false
  mode = "paired"
  listen = "0.0.0.0:22895"
  data_dir = "relay"
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Relay.Auth.Required {
		t.Error("legacy config: Relay.Auth.Required should default to true")
	}
}

func TestLoad_ExplicitAuthRequiredFalse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	body := `
[relay]
  enabled = true
[relay.auth]
  required = false
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Relay.Auth.Required {
		t.Error("explicit required=false should override default")
	}
}

func TestSaveLoadRoundtrip_RelayTLS(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	cfg := Defaults()
	cfg.Relay.TLS.CertFile = "/etc/letsencrypt/live/example.com/fullchain.pem"
	cfg.Relay.TLS.KeyFile = "/etc/letsencrypt/live/example.com/privkey.pem"
	cfg.Relay.Auth.ServiceURL = "wss://example.com"
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Relay.TLS.CertFile != cfg.Relay.TLS.CertFile {
		t.Errorf("CertFile roundtrip: got %q", loaded.Relay.TLS.CertFile)
	}
	if loaded.Relay.TLS.KeyFile != cfg.Relay.TLS.KeyFile {
		t.Errorf("KeyFile roundtrip: got %q", loaded.Relay.TLS.KeyFile)
	}
	if loaded.Relay.Auth.ServiceURL != "wss://example.com" {
		t.Errorf("Auth.ServiceURL roundtrip: got %q", loaded.Relay.Auth.ServiceURL)
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
