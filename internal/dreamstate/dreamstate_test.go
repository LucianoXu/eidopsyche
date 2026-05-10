package dreamstate

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestReadMissingReturnsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	st, err := Read(path)
	if err != nil {
		t.Fatalf("Read missing: %v", err)
	}
	if st.DreamCount != 0 || st.LastDreamFinishedAt != 0 {
		t.Errorf("zero state expected, got %+v", st)
	}
}

func TestReadCorruptReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Error("expected error on corrupt JSON")
	}
}

func TestBeginThenEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	t0 := time.Date(2026, 5, 9, 3, 0, 0, 0, time.UTC)
	t1 := t0.Add(11 * time.Minute)
	if err := Begin(path, t0, "consolidating bob"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	st, _ := Read(path)
	if !st.CurrentlyDreaming {
		t.Error("CurrentlyDreaming should be true after Begin")
	}
	if st.LastDreamStartedAt != t0.Unix() {
		t.Errorf("LastDreamStartedAt = %d", st.LastDreamStartedAt)
	}
	if err := End(path, t1, "done", "memory/episodic/2026/05/dream-001.md"); err != nil {
		t.Fatalf("End: %v", err)
	}
	st, _ = Read(path)
	if st.CurrentlyDreaming {
		t.Error("CurrentlyDreaming should be false after End")
	}
	if st.DreamCount != 1 {
		t.Errorf("DreamCount = %d", st.DreamCount)
	}
	if st.LastDreamFinishedAt != t1.Unix() {
		t.Errorf("LastDreamFinishedAt = %d", st.LastDreamFinishedAt)
	}
	if st.LastDreamNote != "done" {
		t.Errorf("LastDreamNote = %q", st.LastDreamNote)
	}
	if st.LastDreamProse != "memory/episodic/2026/05/dream-001.md" {
		t.Errorf("LastDreamProse = %q", st.LastDreamProse)
	}
}

func TestEndWithoutBegin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	t0 := time.Date(2026, 5, 9, 3, 0, 0, 0, time.UTC)
	if err := End(path, t0, "recovered", ""); err != nil {
		t.Fatalf("End without Begin: %v", err)
	}
	st, _ := Read(path)
	if st.DreamCount != 1 {
		t.Errorf("DreamCount = %d", st.DreamCount)
	}
}

func TestEndRequiresNote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	t0 := time.Now()
	if err := End(path, t0, "", ""); err == nil {
		t.Error("End should require a note")
	}
}

func TestEndProsePathMustBeUnderEpisodic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	t0 := time.Now()
	if err := End(path, t0, "ok", "secret/leaks.md"); err == nil {
		t.Error("End should reject prose path outside memory/episodic/")
	}
}

func TestEndProsePathRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	t0 := time.Now()
	if err := End(path, t0, "ok", "memory/episodic/../../../etc/passwd"); err == nil {
		t.Error("End should reject path-traversal even when prefix matches")
	}
}

func TestBeginConcurrentSerialises(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	t0 := time.Date(2026, 5, 9, 3, 0, 0, 0, time.UTC)
	const N = 16
	var wg sync.WaitGroup
	errCh := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errCh <- Begin(path, t0.Add(time.Duration(i)*time.Second), "intent")
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Errorf("Begin: %v", err)
		}
	}
	st, _ := Read(path)
	if !st.CurrentlyDreaming {
		t.Error("expected CurrentlyDreaming=true after concurrent Begin")
	}
}

func TestBeginThenEndPreservesNote(t *testing.T) {
	// End must overwrite Begin's optional note with its own required note.
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	t0 := time.Now()
	if err := Begin(path, t0, "begin-note"); err != nil {
		t.Fatal(err)
	}
	if err := End(path, t0.Add(time.Minute), "end-note", ""); err != nil {
		t.Fatal(err)
	}
	st, _ := Read(path)
	if st.LastDreamNote != "end-note" {
		t.Errorf("LastDreamNote = %q, want end-note", st.LastDreamNote)
	}
}
