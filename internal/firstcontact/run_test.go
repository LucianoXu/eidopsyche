package firstcontact

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/identity"
)

// TestLoadOperatorIntoSummoning_RestoresLabelAndRelay pins the fix for
// the bug where subsequent-run mode reached phase 3 with empty
// OperatorLabel / HomeRelay, causing forge.Orchestrate to pass blank
// EIDOS_FORGE_LABEL / EIDOS_FORGE_RELAY to init-volume.
func TestLoadOperatorIntoSummoning_RestoresLabelAndRelay(t *testing.T) {
	dir := t.TempDir()
	npub, err := identity.Bootstrap(dir, "alice", "wss://relay.example/")
	if err != nil {
		t.Fatalf("seed Bootstrap: %v", err)
	}

	s := &Summoning{}
	if err := loadOperatorIntoSummoning(dir, s); err != nil {
		t.Fatalf("loadOperatorIntoSummoning: %v", err)
	}
	if s.OperatorNpub != npub {
		t.Errorf("OperatorNpub = %q, want %q", s.OperatorNpub, npub)
	}
	if s.OperatorLabel != "alice" {
		t.Errorf("OperatorLabel = %q, want %q", s.OperatorLabel, "alice")
	}
	if s.HomeRelay != "wss://relay.example/" {
		t.Errorf("HomeRelay = %q, want %q", s.HomeRelay, "wss://relay.example/")
	}
	if s.Lang != "zh" {
		t.Errorf("Lang = %q, want %q", s.Lang, "zh")
	}
}

func TestLoadOperatorIntoSummoning_MissingKeyErrors(t *testing.T) {
	dir := t.TempDir()
	s := &Summoning{}
	err := loadOperatorIntoSummoning(dir, s)
	if err == nil {
		t.Errorf("expected error when key file is absent")
	}
}

// TestIsSubsequentRun_PathDetection verifies the run-mode check uses
// the right path components. Avoids drift from the resolved state-dir.
func TestIsSubsequentRun_PathDetection(t *testing.T) {
	dir := t.TempDir()
	if v, _ := isSubsequentRun(dir); v {
		t.Errorf("empty dir should not be subsequent")
	}
	// Seed both files via Bootstrap.
	if _, err := identity.Bootstrap(dir, "alice", "wss://r/"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if v, err := isSubsequentRun(dir); err != nil || !v {
		t.Errorf("seeded dir should be subsequent: v=%v err=%v", v, err)
	}
	// Remove key but keep state.db — should still NOT be subsequent.
	if err := os.Remove(filepath.Join(dir, "key")); err != nil {
		t.Fatal(err)
	}
	if v, _ := isSubsequentRun(dir); v {
		t.Errorf("missing key file should not count as subsequent")
	}
}
