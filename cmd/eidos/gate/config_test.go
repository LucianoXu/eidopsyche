package gate

import (
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// TestConfigKeys exercises the shared config.Keys registry directly,
// without spawning a CLI process. Keys live in internal/config so the
// dashboard's Settings → Config tab and this CLI use one source of truth.
func TestConfigKeys(t *testing.T) {
	t.Run("unknown key absent", func(t *testing.T) {
		if _, ok := config.KeyByPath("nonexistent.key"); ok {
			t.Fatal("expected key to be absent")
		}
	})

	t.Run("set log_level=debug then get returns debug", func(t *testing.T) {
		cfg := config.Defaults()
		k, ok := config.KeyByPath("log_level")
		if !ok {
			t.Fatal("log_level should be registered")
		}
		if err := k.Set(&cfg, "debug"); err != nil {
			t.Fatal(err)
		}
		if got := k.Get(&cfg); got != "debug" {
			t.Fatalf("got %q, want %q", got, "debug")
		}
	})

	t.Run("set daemon.shutdown_grace_seconds=12 (int parse), get returns 12", func(t *testing.T) {
		cfg := config.Defaults()
		k, _ := config.KeyByPath("daemon.shutdown_grace_seconds")
		if err := k.Set(&cfg, "12"); err != nil {
			t.Fatal(err)
		}
		if got := k.Get(&cfg); got != "12" {
			t.Fatalf("got %q, want %q", got, "12")
		}
	})

	t.Run("set daemon.shutdown_grace_seconds with non-integer returns error", func(t *testing.T) {
		cfg := config.Defaults()
		k, _ := config.KeyByPath("daemon.shutdown_grace_seconds")
		if err := k.Set(&cfg, "notanint"); err == nil {
			t.Fatal("expected error for non-integer value")
		}
	})

	t.Run("relay.* keys removed from gate config", func(t *testing.T) {
		for _, key := range []string{
			"relay.enabled",
			"relay.mode",
			"relay.listen",
			"relay.data_dir",
		} {
			if _, ok := config.KeyByPath(key); ok {
				t.Errorf("gate config key %s should have been removed; use `eidos relay config` instead", key)
			}
		}
	})

	t.Run("all expected keys are present", func(t *testing.T) {
		expected := []string{
			"log_level",
			"daemon.socket",
			"daemon.shutdown_grace_seconds",
			"dashboard.enabled",
			"dashboard.listen",
		}
		for _, p := range expected {
			if _, ok := config.KeyByPath(p); !ok {
				t.Errorf("missing expected config key: %s", p)
			}
		}
	})

	t.Run("get on all registered keys does not panic on defaults", func(t *testing.T) {
		cfg := config.Defaults()
		for _, k := range config.KeyList() {
			_ = k.Get(&cfg)
		}
	})
}
