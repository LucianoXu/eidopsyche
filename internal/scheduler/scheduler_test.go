package scheduler

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewIDFormat(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 30, 0, 0, time.UTC)
	id := newIDAt(now)
	if !strings.HasPrefix(id, "20260509T123000Z-plan-") {
		t.Errorf("id prefix wrong: %q", id)
	}
	// 8 hex chars after "-plan-"
	if len(id) != len("20260509T123000Z-plan-")+8 {
		t.Errorf("id length wrong: %d (got %q)", len(id), id)
	}
}

func TestNewIDUnique(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 30, 0, 0, time.UTC)
	seen := map[string]bool{}
	for i := 0; i < 256; i++ {
		id := newIDAt(now)
		if seen[id] {
			t.Fatalf("duplicate id at i=%d: %q", i, id)
		}
		seen[id] = true
	}
}

func TestAddRejectsTooSoon(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	_, err := Add(dir, now, "too soon", now.Add(30*time.Second))
	if err == nil || !strings.Contains(err.Error(), "60s") {
		t.Errorf("expected 60s-bound error, got %v", err)
	}
}

func TestAddRejectsTooFar(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	_, err := Add(dir, now, "too far", now.Add(31*24*time.Hour))
	if err == nil || !strings.Contains(err.Error(), "30d") {
		t.Errorf("expected 30d-bound error, got %v", err)
	}
}

func TestAddRejectsEmptyHint(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	_, err := Add(dir, now, "", now.Add(2*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "hint") {
		t.Errorf("expected hint-required error, got %v", err)
	}
}

func TestAddRejectsHintWithNewline(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	_, err := Add(dir, now, "first\nsecond", now.Add(2*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "single-line") {
		t.Errorf("expected single-line error, got %v", err)
	}
}

func TestAddRejectsHintTooLong(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	long := strings.Repeat("x", 257)
	_, err := Add(dir, now, long, now.Add(2*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "256") {
		t.Errorf("expected length error, got %v", err)
	}
}

func TestAddWritesFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	plan, err := Add(dir, now, "follow up on bob", now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if plan.ID == "" {
		t.Fatal("missing ID")
	}
	if plan.At != now.Add(2*time.Hour).Unix() {
		t.Errorf("At = %d, want %d", plan.At, now.Add(2*time.Hour).Unix())
	}
	if plan.Hint != "follow up on bob" {
		t.Errorf("Hint = %q", plan.Hint)
	}
	if plan.CreatedAt != now.Unix() {
		t.Errorf("CreatedAt = %d", plan.CreatedAt)
	}
	path := filepath.Join(dir, plan.ID+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("plan file not on disk: %v", err)
	}
}

func TestListEmpty(t *testing.T) {
	dir := t.TempDir()
	plans, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 0 {
		t.Errorf("len = %d", len(plans))
	}
}

func TestListSortedByID(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		_, err := Add(dir, now.Add(time.Duration(i)*time.Second), "x", now.Add(time.Hour))
		if err != nil {
			t.Fatalf("Add[%d]: %v", i, err)
		}
	}
	plans, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 3 {
		t.Fatalf("len = %d", len(plans))
	}
	if !(plans[0].ID < plans[1].ID && plans[1].ID < plans[2].ID) {
		t.Errorf("not sorted: %v", []string{plans[0].ID, plans[1].ID, plans[2].ID})
	}
}

func TestListExcludesFired(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	plan, err := Add(dir, now, "live", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	firedDir := filepath.Join(dir, "fired")
	if err := os.MkdirAll(firedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(
		filepath.Join(dir, plan.ID+".json"),
		filepath.Join(firedDir, plan.ID+".json"),
	); err != nil {
		t.Fatal(err)
	}
	plans, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 0 {
		t.Errorf("expected 0 active, got %d", len(plans))
	}
}

func TestCancel(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	plan, err := Add(dir, now, "x", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := Cancel(dir, plan.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	plans, _ := List(dir)
	if len(plans) != 0 {
		t.Errorf("expected 0 after cancel, got %d", len(plans))
	}
}

func TestCancelMissingErrors(t *testing.T) {
	dir := t.TempDir()
	err := Cancel(dir, "20260509T123000Z-plan-deadbeef")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found, got %v", err)
	}
}

func TestCancelRejectsBadID(t *testing.T) {
	dir := t.TempDir()
	if err := Cancel(dir, "../etc/passwd"); err == nil {
		t.Error("Cancel should reject path traversal")
	}
}

func TestClear(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i := 0; i < 4; i++ {
		_, _ = Add(dir, now.Add(time.Duration(i)*time.Second), "x", now.Add(time.Hour))
	}
	n, err := Clear(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("Clear returned %d, want 4", n)
	}
	plans, _ := List(dir)
	if len(plans) != 0 {
		t.Errorf("plans still present after Clear: %d", len(plans))
	}
}

func TestScanDueOnlyDue(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	pastPlan := Plan{
		V:         SchemaVersion,
		ID:        "20260509T095900Z-plan-aaaaaaaa",
		At:        now.Add(-1 * time.Minute).Unix(),
		Hint:      "past",
		CreatedAt: now.Add(-2 * time.Minute).Unix(),
	}
	if err := writePlan(dir, pastPlan); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(dir, now, "future", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}

	due, err := ScanDue(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("len(due) = %d, want 1; got %v", len(due), due)
	}
	if due[0].Hint != "past" {
		t.Errorf("due Hint = %q", due[0].Hint)
	}
}

func TestMarkFired(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	plan, _ := Add(dir, now, "x", now.Add(2*time.Minute))
	if err := MarkFired(dir, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, plan.ID+".json")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("active file still present")
	}
	if _, err := os.Stat(filepath.Join(dir, "fired", plan.ID+".json")); err != nil {
		t.Errorf("fired file missing: %v", err)
	}
}

func TestMarkFiredIdempotent(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	plan, _ := Add(dir, now, "x", now.Add(2*time.Minute))
	if err := MarkFired(dir, plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := MarkFired(dir, plan.ID); err != nil {
		t.Errorf("second MarkFired returned %v, want nil", err)
	}
}

func TestAddConcurrentDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	const N = 32
	var wg sync.WaitGroup
	errCh := make(chan error, N)
	idCh := make(chan string, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := Add(dir, now, "x", now.Add(2*time.Minute))
			if err != nil {
				errCh <- err
				return
			}
			errCh <- nil
			idCh <- p.ID
		}()
	}
	wg.Wait()
	close(errCh)
	close(idCh)
	for err := range errCh {
		if err != nil {
			t.Errorf("Add: %v", err)
		}
	}
	seen := map[string]bool{}
	for id := range idCh {
		if seen[id] {
			t.Errorf("duplicate ID: %s", id)
		}
		seen[id] = true
	}
	plans, _ := List(dir)
	if len(plans) != N {
		t.Errorf("expected %d plans on disk, got %d", N, len(plans))
	}
}
