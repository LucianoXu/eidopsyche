package sessionstate

import (
	"os"
	"path/filepath"
	"testing"
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
