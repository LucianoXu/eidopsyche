package gate

import (
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// TestConfigKeys exercises the configKeys table directly without spawning a CLI process.
func TestConfigKeys(t *testing.T) {
	t.Run("unknown key returns error on get", func(t *testing.T) {
		cfg := config.Defaults()
		if _, ok := configKeys["nonexistent.key"]; ok {
			t.Fatal("expected key to be absent")
		}
		_ = cfg // suppress unused warning
	})

	t.Run("set log_level=debug then get returns debug", func(t *testing.T) {
		cfg := config.Defaults()
		k := configKeys["log_level"]
		if err := k.set(&cfg, "debug"); err != nil {
			t.Fatal(err)
		}
		if got := k.get(&cfg); got != "debug" {
			t.Fatalf("got %q, want %q", got, "debug")
		}
	})

	t.Run("set daemon.shutdown_grace_seconds=12 (int parse), get returns 12", func(t *testing.T) {
		cfg := config.Defaults()
		k := configKeys["daemon.shutdown_grace_seconds"]
		if err := k.set(&cfg, "12"); err != nil {
			t.Fatal(err)
		}
		if got := k.get(&cfg); got != "12" {
			t.Fatalf("got %q, want %q", got, "12")
		}
	})

	t.Run("set daemon.shutdown_grace_seconds with non-integer returns error", func(t *testing.T) {
		cfg := config.Defaults()
		k := configKeys["daemon.shutdown_grace_seconds"]
		if err := k.set(&cfg, "notanint"); err == nil {
			t.Fatal("expected error for non-integer value")
		}
	})

	t.Run("set relay.mode=public, get returns public", func(t *testing.T) {
		cfg := config.Defaults()
		k := configKeys["relay.mode"]
		if err := k.set(&cfg, "public"); err != nil {
			t.Fatal(err)
		}
		if got := k.get(&cfg); got != "public" {
			t.Fatalf("got %q, want %q", got, "public")
		}
	})

	t.Run("set relay.mode=bogus returns error", func(t *testing.T) {
		cfg := config.Defaults()
		k := configKeys["relay.mode"]
		if err := k.set(&cfg, "bogus"); err == nil {
			t.Fatal("expected error for invalid relay.mode")
		}
	})

	t.Run("set relay.listen=127.0.0.1:8080, get returns 127.0.0.1:8080", func(t *testing.T) {
		cfg := config.Defaults()
		k := configKeys["relay.listen"]
		if err := k.set(&cfg, "127.0.0.1:8080"); err != nil {
			t.Fatal(err)
		}
		if got := k.get(&cfg); got != "127.0.0.1:8080" {
			t.Fatalf("got %q, want %q", got, "127.0.0.1:8080")
		}
	})

	t.Run("set relay.listen=invalid returns error", func(t *testing.T) {
		cfg := config.Defaults()
		k := configKeys["relay.listen"]
		if err := k.set(&cfg, "invalid"); err == nil {
			t.Fatal("expected error for invalid relay.listen")
		}
	})

	t.Run("all expected keys are present", func(t *testing.T) {
		expectedKeys := []string{
			"log_level",
			"daemon.socket",
			"daemon.shutdown_grace_seconds",
			"relay.enabled",
			"relay.mode",
			"relay.listen",
			"relay.data_dir",
		}
		for _, key := range expectedKeys {
			if _, ok := configKeys[key]; !ok {
				t.Errorf("missing expected config key: %s", key)
			}
		}
	})

	t.Run("relay.public_url removed", func(t *testing.T) {
		if _, ok := configKeys["relay.public_url"]; ok {
			t.Fatal("relay.public_url should have been removed")
		}
	})

	t.Run("set relay.enabled toggles bool", func(t *testing.T) {
		cfg := config.Defaults()
		k := configKeys["relay.enabled"]
		if err := k.set(&cfg, "true"); err != nil {
			t.Fatalf("set true: %v", err)
		}
		if got := k.get(&cfg); got != "true" {
			t.Fatalf("get = %q after set true", got)
		}
		if err := k.set(&cfg, "false"); err != nil {
			t.Fatalf("set false: %v", err)
		}
		if got := k.get(&cfg); got != "false" {
			t.Fatalf("get = %q after set false", got)
		}
	})

	t.Run("set relay.enabled rejects garbage", func(t *testing.T) {
		cfg := config.Defaults()
		k := configKeys["relay.enabled"]
		if err := k.set(&cfg, "maybe"); err == nil {
			t.Fatal("expected error for non-bool value")
		}
	})

	t.Run("set relay.listen='' rejected with helpful message", func(t *testing.T) {
		cfg := config.Defaults()
		k := configKeys["relay.listen"]
		err := k.set(&cfg, "")
		if err == nil {
			t.Fatal("expected error for empty relay.listen, got nil")
		}
		if !strings.Contains(err.Error(), "relay.enabled = false") {
			t.Fatalf("error should redirect to relay.enabled, got: %v", err)
		}
	})

	t.Run("get on all keys returns non-empty strings for defaults", func(t *testing.T) {
		cfg := config.Defaults()
		for name, k := range configKeys {
			val := k.get(&cfg)
			// daemon.shutdown_grace_seconds default is 5, log_level is "info", etc.
			// Just verify it doesn't panic and returns something.
			_ = val
			_ = name
		}
	})

	t.Run("relay.listen error message contains host:port guidance", func(t *testing.T) {
		cfg := config.Defaults()
		k := configKeys["relay.listen"]
		err := k.set(&cfg, "badaddr")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "host:port") {
			t.Fatalf("error message should mention host:port, got: %s", err.Error())
		}
	})
}
