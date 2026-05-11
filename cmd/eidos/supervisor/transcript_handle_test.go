package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

func TestTranscriptHandle_OpenWritesIndexOnFinalize(t *testing.T) {
	dir := t.TempDir()
	h, err := openTranscriptHandle(dir, "wake-123")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := h.Write([]byte(`{"type":"system"}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	entry := transcript.Entry{
		ID:        "wake-123",
		Reason:    "heartbeat",
		StartedAt: time.Now().Unix() - 1,
		EndedAt:   time.Now().Unix(),
		OK:        true,
	}
	if err := h.finalize(entry, 50, 10*1024); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	idx, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.Contains(string(idx), "wake-123") {
		t.Fatalf("index missing wake-123: %s", idx)
	}
}

func TestTranscriptHandle_CloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	h, err := openTranscriptHandle(dir, "wake-x")
	if err != nil {
		t.Fatal(err)
	}
	h.close()
	h.close() // must not panic / err
}

func TestTranscriptHandle_OpenRetriesOnCollision(t *testing.T) {
	dir := t.TempDir()
	first, err := openTranscriptHandle(dir, "wake-dup")
	if err != nil {
		t.Fatal(err)
	}
	defer first.close()
	second, err := openTranscriptHandle(dir, "wake-dup")
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	defer second.close()
	if second.wakeID == first.wakeID {
		t.Fatalf("expected suffixed wakeID, got same: %s", second.wakeID)
	}
	if !strings.HasPrefix(second.wakeID, "wake-dup-dup-") {
		t.Fatalf("unexpected retry wakeID: %s", second.wakeID)
	}
}
