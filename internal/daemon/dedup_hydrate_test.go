package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/inbox"
)

// TestStartHydratesDedupAndSelfWrapIDs verifies the daemon constructor
// rebuilds the in-memory dedupe maps from on-disk inbox/outbox at
// startup. Without hydrate, every daemon restart loses the maps and
// the next relay re-delivery duplicates inbox rows / lets self-wraps
// echo into the operator's own inbox.
func TestStartHydratesDedupAndSelfWrapIDs(t *testing.T) {
	dir := t.TempDir()

	// Minimal state files Start() requires: config.toml + key.
	cfgPath := filepath.Join(dir, "config.toml")
	if err := writeFile(cfgPath, "log_level = \"error\"\n[daemon]\nsocket = \"sock\"\n"); err != nil {
		t.Fatal(err)
	}
	k, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.SaveKey(filepath.Join(dir, "key"), k); err != nil {
		t.Fatal(err)
	}

	// Pre-populate the inbox + outbox stores with known event ids.
	// Daemon.Start will hydrate from these.
	box := inbox.New(dir)
	now := time.Now().Unix()
	if err := box.AppendInbox(inbox.Message{EventID: "wrap-from-alice-1", From: "alice", ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := box.AppendInbox(inbox.Message{EventID: "wrap-from-bob-1", From: "bob", ReceivedAt: now + 1}); err != nil {
		t.Fatal(err)
	}
	if err := box.AppendOutbox(inbox.Sent{EventID: "wrap-mine-1", SelfEventID: "wrap-self-copy-1", To: "alice", SentAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := box.AppendOutbox(inbox.Sent{EventID: "wrap-mine-2", SelfEventID: "", To: "bob", SentAt: now + 1}); err != nil {
		t.Fatal(err)
	}

	d, err := Start(dir)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { d.DB.Close() })

	for _, want := range []string{"wrap-from-alice-1", "wrap-from-bob-1"} {
		if _, ok := d.dedupe[want]; !ok {
			t.Errorf("dedupe missing %q after hydrate; got %v", want, d.dedupe)
		}
	}
	if _, ok := d.selfWrapIDs["wrap-self-copy-1"]; !ok {
		t.Errorf("selfWrapIDs missing self-copy id after hydrate; got %v", d.selfWrapIDs)
	}
	for _, mustNot := range []string{"wrap-mine-1", "wrap-mine-2"} {
		if _, ok := d.selfWrapIDs[mustNot]; ok {
			t.Errorf("recipient-bound wrap %q must not be hydrated into selfWrapIDs (would drop self-addressed messages): %v", mustNot, d.selfWrapIDs)
		}
	}
}

// TestStartHydrateMissingFilesIsHarmless covers a fresh state dir: no
// inbox.jsonl, no outbox.jsonl. Start should still succeed and leave
// the maps empty (not blocked by ENOENT).
func TestStartHydrateMissingFilesIsHarmless(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := writeFile(cfgPath, "log_level = \"error\"\n[daemon]\nsocket = \"sock\"\n"); err != nil {
		t.Fatal(err)
	}
	k, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.SaveKey(filepath.Join(dir, "key"), k); err != nil {
		t.Fatal(err)
	}

	d, err := Start(dir)
	if err != nil {
		t.Fatalf("Start with no jsonl: %v", err)
	}
	t.Cleanup(func() { d.DB.Close() })

	if len(d.dedupe) != 0 {
		t.Errorf("expected empty dedupe with no inbox files, got %d", len(d.dedupe))
	}
	if len(d.selfWrapIDs) != 0 {
		t.Errorf("expected empty selfWrapIDs with no outbox files, got %d", len(d.selfWrapIDs))
	}
}

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}
