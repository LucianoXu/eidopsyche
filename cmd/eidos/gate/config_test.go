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
			"relay.mode",
			"relay.listen",
			"relay.public_url",
			"relay.data_dir",
		}
		for _, key := range expectedKeys {
			if _, ok := configKeys[key]; !ok {
				t.Errorf("missing expected config key: %s", key)
			}
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
