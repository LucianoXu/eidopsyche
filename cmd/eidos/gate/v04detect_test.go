package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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
	// up. We exercise the real cobra dispatch path so this test catches
	// regressions in how rootCmd's PersistentPreRunE gates by cmd.Name(),
	// not just the hook function in isolation.
	dir := writeConfig(t, `
log_level = "info"
[relay]
  mode = "paired"
  listen = "127.0.0.1:22895"
  data_dir = "relay"
`)
	// Stub purge's RunE so we don't actually delete anything; we only
	// care that the v0.4 PersistentPreRunE check did not block dispatch.
	savedRunE := purgeCmd.RunE
	purgeCmd.RunE = func(cmd *cobra.Command, args []string) error { return nil }
	t.Cleanup(func() { purgeCmd.RunE = savedRunE })

	rootCmd.SetArgs([]string{"--state-dir", dir, "purge", "--yes"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("purge through cobra failed v0.4 detection: %v", err)
	}
}

func TestDetectV04_NonPurgeBlockedByRootHook(t *testing.T) {
	// Companion to the above: a non-purge subcommand against a v0.4 state
	// dir must fail at the root's PersistentPreRunE before reaching its
	// own RunE. We use `status` because it's a read-only command we don't
	// have to stub.
	dir := writeConfig(t, `
log_level = "info"
[relay]
  mode = "paired"
  listen = "127.0.0.1:22895"
  data_dir = "relay"
`)
	// status's RunE talks to the OS service manager; stub it so test
	// isolation doesn't depend on systemctl/launchctl being present.
	savedRunE := statusCmd.RunE
	statusCmd.RunE = func(cmd *cobra.Command, args []string) error {
		t.Fatal("status RunE should not have been reached on a v0.4 state dir")
		return nil
	}
	t.Cleanup(func() { statusCmd.RunE = savedRunE })

	rootCmd.SetArgs([]string{"--state-dir", dir, "status"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected v0.4 detection to block status, got nil")
	}
	if !strings.Contains(err.Error(), "older eidos version") {
		t.Fatalf("expected v0.4 detection error, got: %v", err)
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
