package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDetectV04_NoConfig_ReturnsNil(t *testing.T) {
	dir := t.TempDir() // no config.toml inside
	if err := detectV04State(dir); err != nil {
		t.Fatalf("expected nil for missing config, got %v", err)
	}
}

func TestDetectV04_LegacyConfig_ReturnsError(t *testing.T) {
	dir := writeConfig(t, `
log_level = "info"
[relay]
  mode = "paired"
  listen = "127.0.0.1:22895"
  public_url = "ws://127.0.0.1:22895"
  data_dir = "relay"
`)
	err := detectV04State(dir)
	if err == nil {
		t.Fatal("expected error for v0.4 config, got nil")
	}
	if !strings.Contains(err.Error(), "older eidos version") {
		t.Fatalf("unexpected error message: %v", err)
	}
	if !strings.Contains(err.Error(), "docs/INSTALL.md#migrating-from-v04") {
		t.Fatalf("error should point users at the migration recipe: %v", err)
	}
}

func TestDetectV04_V05Config_ReturnsNil(t *testing.T) {
	dir := writeConfig(t, `
log_level = "info"
[relay]
  enabled = false
  mode = "paired"
  listen = "0.0.0.0:22895"
  data_dir = "relay"
`)
	if err := detectV04State(dir); err != nil {
		t.Fatalf("expected nil for v0.5 config, got %v", err)
	}
}

func TestDetectV04_PurgeSkipsDetection(t *testing.T) {
	// purge must keep working on v0.4 state dirs — that's how users clean
	// up. The opt-out is a PersistentPreRunE on purgeCmd that returns nil
	// (overriding the root command's hook).
	if purgeCmd.PersistentPreRunE == nil {
		t.Fatal("purgeCmd.PersistentPreRunE is nil — v0.4 users cannot purge")
	}
	if err := purgeCmd.PersistentPreRunE(purgeCmd, nil); err != nil {
		t.Fatalf("purgeCmd opt-out returned error: %v", err)
	}
}

func TestDetectV04_V05ConfigEnabledTrue_ReturnsNil(t *testing.T) {
	dir := writeConfig(t, `
log_level = "info"
[relay]
  enabled = true
  mode = "paired"
  listen = "127.0.0.1:22895"
  data_dir = "relay"
`)
	if err := detectV04State(dir); err != nil {
		t.Fatalf("expected nil for v0.5 config with enabled=true, got %v", err)
	}
}
