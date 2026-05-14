package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"
)

// TestForgeWorkspacesRoundTrip writes a config with workspaces, loads
// it, and confirms the structure round-trips.
func TestForgeWorkspacesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
state_dir = "/tmp/eidos"
log_level = "info"

[forge.alice]
workspaces = [
  { name = "proj-x", host_path = "/home/op/code/project-x", mode = "rw" },
  { name = "photos", host_path = "/home/op/Pictures",       mode = "ro" },
]

[forge.bob]
workspaces = [
  { name = "proj-x", host_path = "/home/op/code/project-x" },
]
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := map[string]ForgeMindForm{
		"alice": {Workspaces: []WorkspaceMount{
			{Name: "proj-x", HostPath: "/home/op/code/project-x", Mode: "rw"},
			{Name: "photos", HostPath: "/home/op/Pictures", Mode: "ro"},
		}},
		"bob": {Workspaces: []WorkspaceMount{
			{Name: "proj-x", HostPath: "/home/op/code/project-x", Mode: ""},
		}},
	}
	if !reflect.DeepEqual(cfg.Forge, want) {
		t.Fatalf("Forge mismatch\nwant: %#v\n got: %#v", want, cfg.Forge)
	}
}

// TestForgeWorkspacesEmptyConfig confirms a config with no [forge.*]
// blocks loads with a nil Forge map (no panics, no extra allocations).
func TestForgeWorkspacesEmptyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("log_level=\"info\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Forge) != 0 {
		t.Fatalf("expected empty Forge map, got %#v", cfg.Forge)
	}
}

// TestEffectiveMode covers WorkspaceMount.EffectiveMode fallback behavior.
func TestEffectiveMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want string
	}{
		{"empty mode defaults to rw", "", "rw"},
		{"explicit rw", "rw", "rw"},
		{"explicit ro", "ro", "ro"},
		{"unknown mode passes through", "unknown", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := WorkspaceMount{Mode: tt.mode}
			got := w.EffectiveMode()
			if got != tt.want {
				t.Errorf("EffectiveMode() = %q, want %q", got, tt.want)
			}
		})
	}
}
