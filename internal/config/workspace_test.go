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

func TestValidateWorkspaceName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"proj-x", true},
		{"a", true},
		{"abc123", true},
		{"a-b-c", true},
		{"", false},
		{"-foo", false},    // leading hyphen
		{"Foo", false},     // uppercase
		{"foo_bar", false}, // underscore
		{"foo/bar", false}, // slash
		{"..", false},
		{"foo-", false}, // trailing hyphen
		{"a-", false},   // trailing hyphen on single-letter prefix
	}
	for _, c := range cases {
		got := ValidateWorkspaceName(c.in) == nil
		if got != c.want {
			t.Errorf("ValidateWorkspaceName(%q) ok=%v want %v", c.in, got, c.want)
		}
	}
}

func TestValidateWorkspaceMode(t *testing.T) {
	if err := ValidateWorkspaceMode(""); err != nil {
		t.Errorf("empty mode should be valid (defaults to rw), got %v", err)
	}
	if err := ValidateWorkspaceMode("rw"); err != nil {
		t.Error(err)
	}
	if err := ValidateWorkspaceMode("ro"); err != nil {
		t.Error(err)
	}
	if err := ValidateWorkspaceMode("RW"); err == nil {
		t.Error("RW should be rejected (case-sensitive)")
	}
	if err := ValidateWorkspaceMode("write"); err == nil {
		t.Error("write should be rejected")
	}
}

func TestValidateWorkspaceHostPath(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "regular")
	if err := os.Mkdir(regular, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := ValidateWorkspaceHostPath(regular); err != nil {
		t.Errorf("regular dir: %v", err)
	}
	if err := ValidateWorkspaceHostPath("relative/path"); err == nil {
		t.Error("relative path should be rejected")
	}
	if err := ValidateWorkspaceHostPath("/tmp/../etc"); err == nil {
		t.Error("path containing .. should be rejected")
	}
	if err := ValidateWorkspaceHostPath("/no/such/path/" + t.Name()); err == nil {
		t.Error("nonexistent path should be rejected")
	}
	if err := ValidateWorkspaceHostPath(file); err == nil {
		t.Error("file (not directory) should be rejected")
	}
}

func TestDangerousHostPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool // true = should produce a warning
	}{
		{"/", true},
		{"/proc", true},
		{"/proc/cpuinfo", true},
		{"/sys", true},
		{"/dev", true},
		{"/etc", true},
		{"/var/run/docker.sock", true},
		{"/tmp/docker.sock", true},
		{"/home/op/.ssh", true},
		{"/home/op/.ssh/keys", true},
		{"/home/op/.config/eidos", true},
		{"/home/op/.config/eidos/config.toml", true},
		{"/home/op/code/project-x", false},
		{"/home/op/Pictures", false},
		{"/var/log", false},
	}
	for _, c := range cases {
		got := DangerousHostPath(c.in) != ""
		if got != c.want {
			t.Errorf("DangerousHostPath(%q) warned=%v want %v", c.in, got, c.want)
		}
	}
}
