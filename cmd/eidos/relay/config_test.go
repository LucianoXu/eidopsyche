package relay

import (
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
)

func pairedCfg() relaycfg.Config {
	return relaycfg.Config{
		LogLevel: "info",
		Relay: relaycfg.RelaySection{
			Mode:        "paired",
			Listen:      "0.0.0.0:7777",
			OwnerPubkey: "deadbeef",
			Auth:        relaycfg.AuthSection{Required: true},
		},
	}
}

func TestGetKeyRoundTrip(t *testing.T) {
	cfg := pairedCfg()
	cases := []struct {
		key  string
		want string
	}{
		{"log_level", "info"},
		{"relay.mode", "paired"},
		{"relay.listen", "0.0.0.0:7777"},
		{"relay.owner_pubkey", "deadbeef"},
		{"relay.tls.cert_file", ""},
		{"relay.tls.key_file", ""},
		{"relay.auth.required", "true"},
		{"relay.auth.service_url", ""},
	}
	for _, c := range cases {
		got, err := getKey(cfg, c.key)
		if err != nil {
			t.Errorf("getKey(%q): unexpected error: %v", c.key, err)
			continue
		}
		if got != c.want {
			t.Errorf("getKey(%q) = %q; want %q", c.key, got, c.want)
		}
	}
}

func TestGetKeyUnknown(t *testing.T) {
	cfg := pairedCfg()
	if _, err := getKey(cfg, "does.not.exist"); err == nil {
		t.Error("expected error for unknown key, got nil")
	}
}

func TestSetKeyRoundTrip(t *testing.T) {
	cfg := pairedCfg()
	if err := setKey(&cfg, "relay.listen", ":9000"); err != nil {
		t.Fatalf("setKey listen: %v", err)
	}
	if cfg.Relay.Listen != ":9000" {
		t.Errorf("listen not updated: %q", cfg.Relay.Listen)
	}

	if err := setKey(&cfg, "relay.auth.required", "false"); err != nil {
		t.Fatalf("setKey auth.required: %v", err)
	}
	if cfg.Relay.Auth.Required {
		t.Error("auth.required should be false")
	}
}

func TestSetKeyBadBool(t *testing.T) {
	cfg := pairedCfg()
	if err := setKey(&cfg, "relay.auth.required", "maybe"); err == nil {
		t.Error("expected parse error for non-bool value")
	}
}

func TestSetKeyUnknown(t *testing.T) {
	cfg := pairedCfg()
	if err := setKey(&cfg, "no.such.key", "val"); err == nil {
		t.Error("expected error for unknown key")
	}
}
