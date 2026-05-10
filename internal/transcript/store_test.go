package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOrphan writes an ndjson file directly under s.Dir, simulating
// agent-runner crashing mid-wake (file exists but no index entry).
func writeOrphan(t *testing.T, s *Store, id, content string) {
	t.Helper()
	if err := os.WriteFile(s.WakePath(id), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNewStore_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "transcripts")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Dir); err != nil {
		t.Errorf("dir not created: %v", err)
	}
}

func TestStore_OpenWritesAndSwapsCurrent(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	f, err := s.Open("abc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"system"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	target, err := os.Readlink(s.CurrentPath())
	if err != nil {
		t.Fatalf("current symlink missing: %v", err)
	}
	if target != "wake-abc.ndjson" {
		t.Errorf("symlink target=%q", target)
	}
	if _, err := os.Stat(s.WakePath("abc")); err != nil {
		t.Errorf("wake file missing: %v", err)
	}
}

func TestStore_OpenRejectsDuplicate(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	f, err := s.Open("abc")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := s.Open("abc"); err == nil {
		t.Errorf("expected EEXIST on duplicate Open(abc)")
	}
}

func TestStore_FinalizeAddsToIndexAndClearsCurrent(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	f, _ := s.Open("abc")
	f.WriteString(`{"type":"result","total_cost_usd":0.01,"is_error":false}` + "\n")
	f.Close()

	cost := 0.01
	entry := Entry{
		ID:           "abc",
		Reason:       "mindgate",
		StartedAt:    1000,
		EndedAt:      1042,
		OK:           true,
		ExitCode:     0,
		CostUSD:      &cost,
		ToolUseCount: 3,
	}
	if err := s.Finalize(entry, DefaultMaxCount, DefaultMaxBytes); err != nil {
		t.Fatal(err)
	}

	idx, _ := s.ReadIndex()
	if len(idx.Wakes) != 1 || idx.Wakes[0].ID != "abc" {
		t.Errorf("index entry missing: %+v", idx)
	}
	if idx.Wakes[0].SizeBytes == 0 {
		t.Errorf("size_bytes not populated")
	}
	if _, err := os.Lstat(s.CurrentPath()); err == nil {
		t.Errorf("current symlink should be cleared after finalize")
	}
}

func TestStore_FinalizeIdempotent(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	f, _ := s.Open("abc")
	f.Close()

	entry := Entry{ID: "abc", Reason: "heartbeat", StartedAt: 1000, EndedAt: 1010, OK: true}
	if err := s.Finalize(entry, 0, 0); err != nil {
		t.Fatal(err)
	}
	// Re-finalize: should replace, not duplicate.
	entry.EndedAt = 1020
	if err := s.Finalize(entry, 0, 0); err != nil {
		t.Fatal(err)
	}
	idx, _ := s.ReadIndex()
	if len(idx.Wakes) != 1 {
		t.Errorf("expected single entry after re-finalize, got %d", len(idx.Wakes))
	}
	if idx.Wakes[0].EndedAt != 1020 {
		t.Errorf("entry not updated: %+v", idx.Wakes[0])
	}
}

func TestStore_PruneByCount(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	for i := 0; i < 5; i++ {
		f, _ := s.Open(intID(i))
		f.WriteString(`{"type":"result"}` + "\n")
		f.Close()
		if err := s.Finalize(Entry{ID: intID(i), StartedAt: int64(i * 10), OK: true}, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	// Cap at 2 — 3 oldest must be dropped.
	if err := s.PruneToLimits(2, 0); err != nil {
		t.Fatal(err)
	}
	idx, _ := s.ReadIndex()
	if len(idx.Wakes) != 2 {
		t.Errorf("expected 2 after prune, got %d", len(idx.Wakes))
	}
	// The two newest (StartedAt 40 and 30) must remain.
	if idx.Wakes[0].StartedAt != 40 || idx.Wakes[1].StartedAt != 30 {
		t.Errorf("wrong wakes survived: %+v", idx.Wakes)
	}
	for _, dropped := range []int{0, 1, 2} {
		if _, err := os.Stat(s.WakePath(intID(dropped))); err == nil {
			t.Errorf("file for dropped wake %d still exists", dropped)
		}
	}
}

func TestStore_PruneByBytes(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	// 3 entries, 10 bytes each declared — cap at 25 bytes drops the oldest.
	for i := 0; i < 3; i++ {
		f, _ := s.Open(intID(i))
		f.WriteString("0123456789") // 10 bytes
		f.Close()
		if err := s.Finalize(Entry{ID: intID(i), StartedAt: int64(i)}, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PruneToLimits(0, 25); err != nil {
		t.Fatal(err)
	}
	idx, _ := s.ReadIndex()
	if len(idx.Wakes) != 2 {
		t.Errorf("expected 2 surviving entries (30 → 20 bytes), got %d", len(idx.Wakes))
	}
}

func TestStore_RecoverOrphan(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	// Pre-populate one finalized wake to confirm Recover doesn't disturb it.
	f, _ := s.Open("ok")
	f.Close()
	if err := s.Finalize(Entry{ID: "ok", StartedAt: 100, OK: true}, 0, 0); err != nil {
		t.Fatal(err)
	}
	// Drop an orphan ndjson with one tool_use and a result event.
	writeOrphan(t, s, "crash",
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"},{"type":"thinking","thinking":"x"}]}}`+"\n"+
			`{"type":"result","total_cost_usd":0.001,"is_error":true}`+"\n")

	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	idx, _ := s.ReadIndex()
	if len(idx.Wakes) != 2 {
		t.Fatalf("expected 2 entries after recover, got %d: %+v", len(idx.Wakes), idx.Wakes)
	}
	var crash *Entry
	for i, w := range idx.Wakes {
		if w.ID == "crash" {
			crash = &idx.Wakes[i]
			break
		}
	}
	if crash == nil {
		t.Fatal("crash entry not synthesized")
	}
	if crash.ExitCode != -1 || crash.OK {
		t.Errorf("crash entry wrong: %+v", crash)
	}
	if crash.ToolUseCount != 1 || crash.ThinkingBlocks != 1 {
		t.Errorf("counters from orphan not populated: %+v", crash)
	}
}

func TestStore_RecoverIdempotent(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	writeOrphan(t, s, "x", `{"type":"result"}`+"\n")
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	idx, _ := s.ReadIndex()
	if len(idx.Wakes) != 1 {
		t.Errorf("Recover should be idempotent, got %d entries", len(idx.Wakes))
	}
}

func TestStore_RecoverClearsDanglingCurrent(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	if err := os.Symlink("wake-missing.ndjson", s.CurrentPath()); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(s.CurrentPath()); err == nil {
		t.Errorf("dangling current symlink not cleared")
	}
}

func TestStore_RecoverHandlesLargeLine(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	// 200KB tool_result content — exercises the unbounded line reader.
	bigContent := strings.Repeat("x", 200*1024)
	writeOrphan(t, s, "big",
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"`+bigContent+`"}]}}`+"\n")
	if err := s.Recover(); err != nil {
		t.Fatal(err)
	}
	idx, _ := s.ReadIndex()
	if len(idx.Wakes) != 1 {
		t.Errorf("expected 1 entry, got %d", len(idx.Wakes))
	}
}

func intID(i int) string {
	return string(rune('a' + i))
}
