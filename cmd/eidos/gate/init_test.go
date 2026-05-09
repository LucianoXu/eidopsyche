package gate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// callInit invokes runInit with controlled flag values and resolves
// state into dir. The package-level flag vars are saved/restored so
// tests don't leak into each other.
func callInit(t *testing.T, dir, label, home string) error {
	t.Helper()
	savedLabel, savedHome, savedStateDir := initLabel, initHome, globalStateDir
	t.Cleanup(func() {
		initLabel, initHome, globalStateDir = savedLabel, savedHome, savedStateDir
	})
	initLabel = label
	initHome = home
	globalStateDir = dir
	return runInit(initCmd, nil)
}

func TestInit_HomeMissing_Errors(t *testing.T) {
	dir := t.TempDir()
	err := callInit(t, dir, "alice", "")
	if err == nil {
		t.Fatal("expected error when --home is missing")
	}
	if !strings.Contains(err.Error(), "--home is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInit_HomeBadScheme_Errors(t *testing.T) {
	dir := t.TempDir()
	err := callInit(t, dir, "alice", "http://relay.example.com")
	if err == nil {
		t.Fatal("expected error for non-ws scheme")
	}
	if !strings.Contains(err.Error(), "ws://") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInit_HomeNoHost_Errors(t *testing.T) {
	dir := t.TempDir()
	err := callInit(t, dir, "alice", "ws://")
	if err == nil {
		t.Fatal("expected error for ws:// with no host")
	}
}

func TestInit_WritesConfigAndHomeRow(t *testing.T) {
	dir := t.TempDir()
	if err := callInit(t, dir, "alice", "wss://relay.example.com"); err != nil {
		t.Fatalf("init: %v", err)
	}

	if _, err := config.Load(filepath.Join(dir, "config.toml")); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var url, role string
	if err := db.QueryRowContext(context.Background(),
		`SELECT relay_url, role FROM own_relays`).Scan(&url, &role); err != nil {
		t.Fatal(err)
	}
	if url != "wss://relay.example.com" || role != "home" {
		t.Errorf("own_relays row = (%q, %q), want (wss://relay.example.com, home)", url, role)
	}
}

func TestInit_RefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := callInit(t, dir, "alice", "wss://r.com"); err != nil {
		t.Fatalf("first init: %v", err)
	}
	err := callInit(t, dir, "alice", "wss://r.com")
	if err == nil {
		t.Fatal("expected refusal on second init")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "key")); err != nil {
		t.Fatal(err)
	}
}
