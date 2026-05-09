package relaycfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.LogLevel != "info" {
		t.Errorf("log_level = %q, want %q", d.LogLevel, "info")
	}
	if d.Relay.Mode != "paired" {
		t.Errorf("mode = %q, want %q", d.Relay.Mode, "paired")
	}
	if d.Relay.Listen != "0.0.0.0:7777" {
		t.Errorf("listen = %q", d.Relay.Listen)
	}
	if !d.Relay.Auth.Required {
		t.Errorf("auth.required = false, want true")
	}
	if d.Relay.OwnerPubkey != "" {
		t.Errorf("owner_pubkey = %q, want empty (no default)", d.Relay.OwnerPubkey)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := Defaults()
	cfg.Relay.Mode = "public"
	cfg.Relay.Listen = "127.0.0.1:9001"
	cfg.Relay.TLS.CertFile = "/etc/eidos/cert.pem"
	cfg.Relay.TLS.KeyFile = "/etc/eidos/key.pem"
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Relay.Mode != "public" || got.Relay.Listen != "127.0.0.1:9001" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.Relay.TLS.CertFile != "/etc/eidos/cert.pem" || got.Relay.TLS.KeyFile != "/etc/eidos/key.pem" {
		t.Errorf("tls round-trip mismatch: %+v", got.Relay.TLS)
	}
}

func TestLoadAuthDefaultsTrueWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	body := `
[relay]
mode = "public"
listen = "0.0.0.0:7777"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Relay.Auth.Required {
		t.Errorf("auth.required defaulted to false; want true when section absent")
	}
}

func TestLoadRejectsPairedWithoutOwner(t *testing.T) {
	dir := t.TempDir()
	body := `
[relay]
mode = "paired"
listen = "0.0.0.0:7777"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected paired-without-owner to fail validation")
	}
}

func TestLoadRejectsPublicWithOwner(t *testing.T) {
	dir := t.TempDir()
	body := `
[relay]
mode = "public"
listen = "0.0.0.0:7777"
owner_pubkey = "deadbeef"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected public-with-owner to fail validation")
	}
}

func TestSaveLoadRespectsFalseRequired(t *testing.T) {
	dir := t.TempDir()
	cfg := Defaults()
	cfg.Relay.Mode = "public"
	cfg.Relay.Auth.Required = false
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Relay.Auth.Required {
		t.Errorf("auth.required defaulted back to true after explicit false")
	}
}
