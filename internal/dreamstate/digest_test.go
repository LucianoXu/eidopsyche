package dreamstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteDigest_CreatesDailyAndIndex(t *testing.T) {
	dir := t.TempDir()

	if err := WriteDigest(dir, time.Date(2026, 5, 13, 3, 0, 0, 0, time.UTC), "Reflected on Bob's tide-pool image."); err != nil {
		t.Fatalf("WriteDigest: %v", err)
	}

	dailyPath := filepath.Join(dir, "dreams", "2026-05-13.md")
	dailyBody, err := os.ReadFile(dailyPath)
	if err != nil {
		t.Fatalf("read daily digest: %v", err)
	}
	if !strings.Contains(string(dailyBody), "tide-pool") {
		t.Errorf("daily digest missing summary; got:\n%s", dailyBody)
	}

	indexBody, err := os.ReadFile(filepath.Join(dir, "dreams", "DREAMS.md"))
	if err != nil {
		t.Fatalf("read DREAMS.md: %v", err)
	}
	if !strings.Contains(string(indexBody), "2026-05-13") {
		t.Errorf("DREAMS.md index missing 2026-05-13 entry; got:\n%s", indexBody)
	}
	if !strings.Contains(string(indexBody), "tide-pool") {
		t.Errorf("DREAMS.md index missing summary hook; got:\n%s", indexBody)
	}
}

func TestWriteDigest_AppendsToExistingIndex(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dreams"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed := "# DREAMS\n\n- [2026-05-12](2026-05-12.md) — prior entry\n"
	if err := os.WriteFile(filepath.Join(dir, "dreams", "DREAMS.md"), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed DREAMS.md: %v", err)
	}

	if err := WriteDigest(dir, time.Date(2026, 5, 13, 3, 0, 0, 0, time.UTC), "new entry"); err != nil {
		t.Fatalf("WriteDigest: %v", err)
	}

	body, _ := os.ReadFile(filepath.Join(dir, "dreams", "DREAMS.md"))
	if !strings.Contains(string(body), "prior entry") {
		t.Errorf("existing index entry was overwritten; got:\n%s", body)
	}
	if !strings.Contains(string(body), "new entry") {
		t.Errorf("new entry not appended; got:\n%s", body)
	}
}

// TestWriteDigest_PreservesMultipleSameDayCycles pins the regression
// codex flagged: when a mind-form ends more than one dream on the
// same UTC date, the per-day file must accumulate one section per
// cycle (not overwrite), and DREAMS.md must hold an entry per cycle
// that disambiguates by time.
func TestWriteDigest_PreservesMultipleSameDayCycles(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 13, 3, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 5, 13, 18, 30, 0, 0, time.UTC)
	if err := WriteDigest(dir, t0, "morning reflection"); err != nil {
		t.Fatalf("WriteDigest #1: %v", err)
	}
	if err := WriteDigest(dir, t1, "evening reflection"); err != nil {
		t.Fatalf("WriteDigest #2: %v", err)
	}

	daily, err := os.ReadFile(filepath.Join(dir, "dreams", "2026-05-13.md"))
	if err != nil {
		t.Fatalf("read daily digest: %v", err)
	}
	for _, want := range []string{"morning reflection", "evening reflection", "03:00:00", "18:30:00"} {
		if !strings.Contains(string(daily), want) {
			t.Errorf("daily digest missing %q after two cycles; got:\n%s", want, daily)
		}
	}

	idx, err := os.ReadFile(filepath.Join(dir, "dreams", "DREAMS.md"))
	if err != nil {
		t.Fatalf("read DREAMS.md: %v", err)
	}
	if !strings.Contains(string(idx), "03:00:00") || !strings.Contains(string(idx), "18:30:00") {
		t.Errorf("DREAMS.md must disambiguate same-day cycles by time; got:\n%s", idx)
	}
	if !strings.Contains(string(idx), "morning reflection") || !strings.Contains(string(idx), "evening reflection") {
		t.Errorf("DREAMS.md missing one or both same-day cycle hooks; got:\n%s", idx)
	}
}

func TestWriteDigest_LongSummaryTruncatedInIndex(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 200)
	if err := WriteDigest(dir, time.Now(), long); err != nil {
		t.Fatalf("WriteDigest: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "dreams", "DREAMS.md"))
	if !strings.Contains(string(body), "...") {
		t.Errorf("expected ellipsis truncation in DREAMS.md index; got:\n%s", body)
	}
}
