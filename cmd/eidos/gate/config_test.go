package gate

import (
	"strings"
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

	t.Run("set relay.mode=public, get returns public", func(t *testing.T) {
		cfg := config.Defaults()
		k, _ := config.KeyByPath("relay.mode")
		if err := k.Set(&cfg, "public"); err != nil {
			t.Fatal(err)
		}
		if got := k.Get(&cfg); got != "public" {
			t.Fatalf("got %q, want %q", got, "public")
		}
	})

	t.Run("set relay.mode=bogus returns error", func(t *testing.T) {
		cfg := config.Defaults()
		k, _ := config.KeyByPath("relay.mode")
		if err := k.Set(&cfg, "bogus"); err == nil {
			t.Fatal("expected error for invalid relay.mode")
		}
	})

	t.Run("set relay.listen=127.0.0.1:8080, get returns 127.0.0.1:8080", func(t *testing.T) {
		cfg := config.Defaults()
		k, _ := config.KeyByPath("relay.listen")
		if err := k.Set(&cfg, "127.0.0.1:8080"); err != nil {
			t.Fatal(err)
		}
		if got := k.Get(&cfg); got != "127.0.0.1:8080" {
			t.Fatalf("got %q, want %q", got, "127.0.0.1:8080")
		}
	})

	t.Run("set relay.listen=invalid returns error", func(t *testing.T) {
		cfg := config.Defaults()
		k, _ := config.KeyByPath("relay.listen")
		if err := k.Set(&cfg, "invalid"); err == nil {
			t.Fatal("expected error for invalid relay.listen")
		}
	})

	t.Run("all expected keys are present", func(t *testing.T) {
		expected := []string{
			"log_level",
			"daemon.socket",
			"daemon.shutdown_grace_seconds",
			"relay.enabled",
			"relay.mode",
			"relay.listen",
			"relay.data_dir",
		}
		for _, p := range expected {
			if _, ok := config.KeyByPath(p); !ok {
				t.Errorf("missing expected config key: %s", p)
			}
		}
	})

	t.Run("relay.public_url removed", func(t *testing.T) {
		if _, ok := config.KeyByPath("relay.public_url"); ok {
			t.Fatal("relay.public_url should have been removed")
		}
	})

	t.Run("set relay.enabled toggles bool", func(t *testing.T) {
		cfg := config.Defaults()
		k, _ := config.KeyByPath("relay.enabled")
		if err := k.Set(&cfg, "true"); err != nil {
			t.Fatalf("set true: %v", err)
		}
		if got := k.Get(&cfg); got != "true" {
			t.Fatalf("get = %q after set true", got)
		}
		if err := k.Set(&cfg, "false"); err != nil {
			t.Fatalf("set false: %v", err)
		}
		if got := k.Get(&cfg); got != "false" {
			t.Fatalf("get = %q after set false", got)
		}
	})

	t.Run("set relay.enabled rejects garbage", func(t *testing.T) {
		cfg := config.Defaults()
		k, _ := config.KeyByPath("relay.enabled")
		if err := k.Set(&cfg, "maybe"); err == nil {
			t.Fatal("expected error for non-bool value")
		}
	})

	t.Run("set relay.listen='' rejected with helpful message", func(t *testing.T) {
		cfg := config.Defaults()
		k, _ := config.KeyByPath("relay.listen")
		err := k.Set(&cfg, "")
		if err == nil {
			t.Fatal("expected error for empty relay.listen, got nil")
		}
		if !strings.Contains(err.Error(), "relay.enabled = false") {
			t.Fatalf("error should redirect to relay.enabled, got: %v", err)
		}
	})

	t.Run("get on all registered keys does not panic on defaults", func(t *testing.T) {
		cfg := config.Defaults()
		for _, k := range config.KeyList() {
			_ = k.Get(&cfg)
		}
	})

	t.Run("relay.listen error message contains host:port guidance", func(t *testing.T) {
		cfg := config.Defaults()
		k, _ := config.KeyByPath("relay.listen")
		err := k.Set(&cfg, "badaddr")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "host:port") {
			t.Fatalf("error message should mention host:port, got: %s", err.Error())
		}
	})
}
