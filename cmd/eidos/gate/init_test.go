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
func callInit(t *testing.T, dir, label, home string, withRelay bool, listen string) error {
	t.Helper()
	savedLabel, savedHome, savedWithRelay, savedListen, savedStateDir := initLabel, initHome, initWithLocalRelay, initListen, globalStateDir
	t.Cleanup(func() {
		initLabel, initHome, initWithLocalRelay, initListen, globalStateDir = savedLabel, savedHome, savedWithRelay, savedListen, savedStateDir
	})
	initLabel = label
	initHome = home
	initWithLocalRelay = withRelay
	initListen = listen
	globalStateDir = dir
	return runInit(initCmd, nil)
}

func TestInit_HomeMissing_Errors(t *testing.T) {
	dir := t.TempDir()
	err := callInit(t, dir, "alice", "", false, "")
	if err == nil {
		t.Fatal("expected error when --home is missing")
	}
	if !strings.Contains(err.Error(), "--home is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInit_HomeBadScheme_Errors(t *testing.T) {
	dir := t.TempDir()
	err := callInit(t, dir, "alice", "http://relay.example.com", false, "")
	if err == nil {
		t.Fatal("expected error for non-ws scheme")
	}
	if !strings.Contains(err.Error(), "ws://") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInit_HomeNoHost_Errors(t *testing.T) {
	dir := t.TempDir()
	err := callInit(t, dir, "alice", "ws://", false, "")
	if err == nil {
		t.Fatal("expected error for ws:// with no host")
	}
}

func TestInit_ListenWithoutWithLocalRelay_Errors(t *testing.T) {
	dir := t.TempDir()
	err := callInit(t, dir, "alice", "wss://relay.example.com", false, "0.0.0.0:9999")
	if err == nil {
		t.Fatal("expected error for --listen without --with-local-relay")
	}
	if !strings.Contains(err.Error(), "--listen requires --with-local-relay") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInit_DaemonOnly_WritesEnabledFalseAndHomeRow(t *testing.T) {
	dir := t.TempDir()
	if err := callInit(t, dir, "alice", "wss://relay.example.com", false, ""); err != nil {
		t.Fatalf("init: %v", err)
	}

	cfg, err := config.Load(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Relay.Enabled {
		t.Errorf("Relay.Enabled = true, want false")
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

func TestInit_WithLocalRelay_DefaultsListen_WritesEnabledTrue(t *testing.T) {
	dir := t.TempDir()
	if err := callInit(t, dir, "alice", "ws://127.0.0.1:22895", true, ""); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Relay.Enabled {
		t.Errorf("Relay.Enabled = false, want true")
	}
	if cfg.Relay.Listen != "0.0.0.0:22895" {
		t.Errorf("Relay.Listen = %q, want 0.0.0.0:22895 (default)", cfg.Relay.Listen)
	}
}

func TestInit_WithLocalRelay_ExplicitListen(t *testing.T) {
	dir := t.TempDir()
	if err := callInit(t, dir, "alice", "wss://my.host", true, "127.0.0.1:9999"); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Relay.Listen != "127.0.0.1:9999" {
		t.Errorf("Relay.Listen = %q, want 127.0.0.1:9999", cfg.Relay.Listen)
	}
	// own_relays row must equal --home, NOT --listen.
	db, _ := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	defer db.Close()
	var url string
	if err := db.QueryRowContext(context.Background(),
		`SELECT relay_url FROM own_relays WHERE role='home'`).Scan(&url); err != nil {
		t.Fatal(err)
	}
	if url != "wss://my.host" {
		t.Errorf("home row url = %q, want wss://my.host", url)
	}
}

func TestInit_WithLocalRelay_BadListen_Errors(t *testing.T) {
	dir := t.TempDir()
	err := callInit(t, dir, "alice", "wss://my.host", true, "garbage")
	if err == nil {
		t.Fatal("expected error for invalid --listen value")
	}
	if !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("error should mention host:port, got: %v", err)
	}
}

func TestInit_RefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := callInit(t, dir, "alice", "wss://r.com", false, ""); err != nil {
		t.Fatalf("first init: %v", err)
	}
	err := callInit(t, dir, "alice", "wss://r.com", false, "")
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
