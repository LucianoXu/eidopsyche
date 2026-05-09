package relay

import (
	"os"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
)

func TestInitWritesConfigPaired(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOpts{
		dir:    dir,
		mode:   "paired",
		listen: "127.0.0.1:9001",
		owner:  "abc123deadbeef",
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := relaycfg.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Relay.Mode != "paired" || cfg.Relay.OwnerPubkey != "abc123deadbeef" {
		t.Errorf("unexpected cfg: %+v", cfg.Relay)
	}
	if _, err := os.Stat(relaycfg.EventStorePath(dir)); err != nil {
		t.Errorf("event store dir not created: %v", err)
	}
}

func TestInitRejectsPairedWithoutOwner(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOpts{dir: dir, mode: "paired", listen: ":7777"}); err == nil {
		t.Fatal("expected paired-without-owner to fail")
	}
}

func TestInitRejectsPublicWithOwner(t *testing.T) {
	dir := t.TempDir()
	err := runInit(initOpts{dir: dir, mode: "public", listen: ":7777", owner: "abc"})
	if err == nil {
		t.Fatal("expected public-with-owner to fail")
	}
}

func TestInitRejectsExistingConfig(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOpts{dir: dir, mode: "public", listen: ":7777"}); err != nil {
		t.Fatal(err)
	}
	if err := runInit(initOpts{dir: dir, mode: "public", listen: ":7777"}); err == nil {
		t.Fatal("expected re-init to fail without --force")
	}
	if err := runInit(initOpts{dir: dir, mode: "public", listen: ":8888", force: true}); err != nil {
		t.Fatalf("--force should overwrite: %v", err)
	}
	// Verify the listen address actually changed.
	cfg, err := relaycfg.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Relay.Listen != ":8888" {
		t.Errorf("force re-init did not update listen: %q", cfg.Relay.Listen)
	}
}
