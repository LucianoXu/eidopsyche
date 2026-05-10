package sessionstate

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRead_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	st, err := Read(path)
	if err != nil {
		t.Fatalf("Read of missing file: want nil err, got %v", err)
	}
	if st.SessionID != "" || st.SessionStartedAt != 0 || st.WakesInSession != 0 {
		t.Fatalf("Read of missing file: want zero State, got %+v", st)
	}
}

func TestRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	body := []byte(`{"v":1,"session_id":"abc-123","session_started_at":1700000000,"wakes_in_session":7}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Read(path)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if st.SessionID != "abc-123" || st.SessionStartedAt != 1700000000 || st.WakesInSession != 7 {
		t.Fatalf("got %+v", st)
	}
}

func TestRead_Corrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := os.WriteFile(path, []byte("{this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatalf("Read of corrupt file: want non-nil err")
	}
}

func TestMint_WritesValidStateAndReturnsIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	now := time.Unix(1700000000, 0)

	st, err := Mint(path, now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if st.SessionID == "" {
		t.Fatalf("Mint: returned empty SessionID")
	}
	if st.SessionStartedAt != now.Unix() {
		t.Fatalf("Mint: SessionStartedAt = %d, want %d", st.SessionStartedAt, now.Unix())
	}
	if st.WakesInSession != 0 {
		t.Fatalf("Mint: WakesInSession = %d, want 0", st.WakesInSession)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read after Mint: %v", err)
	}
	if got != st {
		t.Fatalf("Read != Mint return value: %+v vs %+v", got, st)
	}
}

func TestMint_GeneratesUniqueIDs(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1700000000, 0)
	a, err := Mint(filepath.Join(dir, "a.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Mint(filepath.Join(dir, "b.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	if a.SessionID == b.SessionID {
		t.Fatalf("Mint: collision %q == %q", a.SessionID, b.SessionID)
	}
}

func TestIncrementWake_BumpsCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if _, err := Mint(path, time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 3; i++ {
		if err := IncrementWake(path); err != nil {
			t.Fatalf("IncrementWake #%d: %v", i, err)
		}
		st, err := Read(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.WakesInSession != i {
			t.Fatalf("WakesInSession after %d increments: got %d, want %d", i, st.WakesInSession, i)
		}
	}
}

func TestIncrementWake_NoOpWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := IncrementWake(path); err != nil {
		t.Fatalf("IncrementWake on missing file: want nil, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("IncrementWake should not create the file; stat err=%v", err)
	}
}

func TestClear_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if _, err := Mint(path, time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}
	if err := Clear(path); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Clear left file behind; stat err=%v", err)
	}
}

func TestClear_IdempotentOnMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := Clear(path); err != nil {
		t.Fatalf("Clear of missing file: want nil, got %v", err)
	}
	if err := Clear(path); err != nil {
		t.Fatalf("second Clear: want nil, got %v", err)
	}
}

func TestIncrementWake_ParallelSerialised(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if _, err := Mint(path, time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if err := IncrementWake(path); err != nil {
				t.Errorf("IncrementWake: %v", err)
			}
		}()
	}
	wg.Wait()

	st, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.WakesInSession != n {
		t.Fatalf("after %d parallel IncrementWake: got %d, want %d", n, st.WakesInSession, n)
	}
}
