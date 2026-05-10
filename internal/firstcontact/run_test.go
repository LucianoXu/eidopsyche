package firstcontact

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/identity"
)

// TestLoadLocalMasterDefault_RestoresLabelAndRelay pins the fix for
// the bug where subsequent-run mode reached phase 3 with empty
// MasterLabel / HomeRelay, causing forge.Orchestrate to pass blank
// EIDOS_FORGE_LABEL / EIDOS_FORGE_RELAY to init-volume.
func TestLoadLocalMasterDefault_RestoresLabelAndRelay(t *testing.T) {
	dir := t.TempDir()
	npub, err := identity.Bootstrap(dir, "alice", "wss://relay.example/")
	if err != nil {
		t.Fatalf("seed Bootstrap: %v", err)
	}

	s := &Summoning{}
	if err := loadLocalMasterDefault(dir, s); err != nil {
		t.Fatalf("loadLocalMasterDefault: %v", err)
	}
	if s.MasterNpub != npub {
		t.Errorf("MasterNpub = %q, want %q", s.MasterNpub, npub)
	}
	if s.MasterLabel != "alice" {
		t.Errorf("MasterLabel = %q, want %q", s.MasterLabel, "alice")
	}
	if s.HomeRelay != "wss://relay.example/" {
		t.Errorf("HomeRelay = %q, want %q", s.HomeRelay, "wss://relay.example/")
	}
	if !s.OperatorPresent {
		t.Errorf("OperatorPresent should be true after loading local default")
	}
	if s.Lang != "zh" {
		t.Errorf("Lang = %q, want %q", s.Lang, "zh")
	}
}

func TestLoadLocalMasterDefault_MissingKeyErrors(t *testing.T) {
	dir := t.TempDir()
	s := &Summoning{}
	err := loadLocalMasterDefault(dir, s)
	if err == nil {
		t.Errorf("expected error when key file is absent")
	}
}

// TestIsIdentityInitialized_PathDetection verifies the run-mode check
// uses the right path components. Avoids drift from the resolved
// state-dir.
func TestIsIdentityInitialized_PathDetection(t *testing.T) {
	dir := t.TempDir()
	if v, _ := isIdentityInitialized(dir); v {
		t.Errorf("empty dir should not be initialized")
	}
	// Seed both files via Bootstrap.
	if _, err := identity.Bootstrap(dir, "alice", "wss://r/"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if v, err := isIdentityInitialized(dir); err != nil || !v {
		t.Errorf("seeded dir should be initialized: v=%v err=%v", v, err)
	}
	// Remove key but keep state.db — should still NOT count as initialized.
	if err := os.Remove(filepath.Join(dir, "key")); err != nil {
		t.Fatal(err)
	}
	if v, _ := isIdentityInitialized(dir); v {
		t.Errorf("missing key file should not count as initialized")
	}
}
