package gate

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/card"
	"github.com/LucianoXu/eidopsyche/internal/identity"
)

func TestCardExportToFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, err := identity.Bootstrap(dir, "alice", "wss://relay.example/"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	out := filepath.Join(dir, "alice.eidos-card.toml")
	if err := exportCardToFile(dir, out, "", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("exportCardToFile: %v", err)
	}
	c, err := card.Read(out)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if c.Label != "alice" {
		t.Errorf("label = %q, want alice", c.Label)
	}
	if c.Relay != "wss://relay.example/" {
		t.Errorf("home_relay = %q", c.Relay)
	}
	if c.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", c.SchemaVersion)
	}
}

func TestCardExportToFile_LabelOverride(t *testing.T) {
	dir := t.TempDir()
	if _, err := identity.Bootstrap(dir, "alice", "wss://relay.example/"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	out := filepath.Join(dir, "x.eidos-card.toml")
	if err := exportCardToFile(dir, out, "Alice (display)", time.Now().UTC()); err != nil {
		t.Fatalf("exportCardToFile: %v", err)
	}
	c, err := card.Read(out)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if c.Label != "Alice (display)" {
		t.Errorf("label = %q, want override applied", c.Label)
	}
}
