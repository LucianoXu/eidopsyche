package inbox

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLabelNotPersisted asserts that the Label field is a transit-only
// display annotation: AppendInbox / AppendOutbox never write it to the
// JSONL, even when callers accidentally hand it over.
//
// Rationale: labels can change over time. Persisting them in the inbox
// or outbox snapshots would create stale duplicates of the canonical
// value in the contacts store.
func TestLabelNotPersisted(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	now := time.Now().Unix()

	m := Message{EventID: "e1", From: "abc", Content: "hi", Kind: 14, RumorAt: now, ReceivedAt: now, Label: "alice"}
	if err := s.AppendInbox(m); err != nil {
		t.Fatal(err)
	}
	o := Sent{EventID: "x", To: "abc", Content: "hi", SentAt: now, Label: "alice"}
	if err := s.AppendOutbox(o); err != nil {
		t.Fatal(err)
	}

	assertNoLabelOnDisk(t, filepath.Join(dir, "inbox"))
	assertNoLabelOnDisk(t, filepath.Join(dir, "outbox"))

	// And the in-memory round-trip from List* should not surface a
	// label that was never persisted; daemon-side join is the only
	// place Label is allowed to appear.
	got, err := s.ListInbox(nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Label != "" {
		t.Fatalf("expected ListInbox to drop the in-memory Label, got %+v", got)
	}
	gotOut, err := s.ListOutbox(nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotOut) != 1 || gotOut[0].Label != "" {
		t.Fatalf("expected ListOutbox to drop the in-memory Label, got %+v", gotOut)
	}
}

func assertNoLabelOnDisk(t *testing.T, root string) {
	t.Helper()
	walked := 0
	err := filepathWalkJSONL(root, func(path string, raw []byte) error {
		walked++
		if bytes.Contains(raw, []byte(`"label"`)) {
			t.Errorf("expected no \"label\" field in %s, got: %s", path, string(raw))
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		if _, ok := obj["label"]; ok {
			t.Errorf("unexpected label key in %s: %v", path, obj)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if walked == 0 {
		t.Fatalf("walked no JSONL files under %s", root)
	}
}

func filepathWalkJSONL(root string, fn func(path string, line []byte) error) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(root, e.Name())
		if e.IsDir() {
			if err := filepathWalkJSONL(full, fn); err != nil {
				return err
			}
			continue
		}
		if filepath.Ext(full) != ".jsonl" {
			continue
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		for _, line := range bytes.Split(bytes.TrimRight(raw, "\n"), []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			if err := fn(full, line); err != nil {
				return err
			}
		}
	}
	return nil
}
