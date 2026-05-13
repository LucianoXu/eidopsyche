package wake_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/wake"
)

func TestBirthSignal_RoundTrip(t *testing.T) {
	in := wake.BirthSignal{
		V:                 wake.BirthSchemaVersion,
		OperatorNpub:      "npub1example",
		SummoningBookPath: "/eidos/ontology/chest/summoning-book.md",
		CallingWordsPath:  "/eidos/ontology/self/calling-words.md",
		ResponsePath:      "/eidos/ontology/chest/first-message.md",
		TriggeredAt:       1700000000,
	}
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out wake.BirthSignal
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch:\n got: %+v\nwant: %+v", out, in)
	}
}

func TestReasonBirth_StringForm(t *testing.T) {
	if string(wake.ReasonBirth) != "birth" {
		t.Errorf("ReasonBirth = %q, want %q", wake.ReasonBirth, "birth")
	}
}

func TestBirthSchemaVersion(t *testing.T) {
	if wake.BirthSchemaVersion != 1 {
		t.Errorf("BirthSchemaVersion = %d, want 1", wake.BirthSchemaVersion)
	}
}

func TestWriteBirthReadBirth_RoundTrips(t *testing.T) {
	dir := t.TempDir()
	in := wake.BirthSignal{
		V: 1, OperatorNpub: "npub1aaa", TriggeredAt: 42,
		SummoningBookPath: "/x/book.md", CallingWordsPath: "/x/words.md", ResponsePath: "/x/resp.md",
	}
	if err := wake.WriteBirth(dir, in); err != nil {
		t.Fatalf("WriteBirth: %v", err)
	}
	got, err := wake.ReadBirth(dir)
	if err != nil {
		t.Fatalf("ReadBirth: %v", err)
	}
	if got == nil || *got != in {
		t.Errorf("ReadBirth = %+v, want %+v", got, in)
	}
}

func TestReadBirth_AbsentReturnsNil(t *testing.T) {
	dir := t.TempDir()
	got, err := wake.ReadBirth(dir)
	if err != nil {
		t.Fatalf("ReadBirth: %v", err)
	}
	if got != nil {
		t.Errorf("ReadBirth on empty dir = %+v, want nil", got)
	}
}

func TestClearBirth_RemovesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := wake.WriteBirth(dir, wake.BirthSignal{V: 1, TriggeredAt: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := wake.ClearBirth(dir); err != nil {
		t.Fatalf("first ClearBirth: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, wake.BirthFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("birth.json should be removed")
	}
	if err := wake.ClearBirth(dir); err != nil {
		t.Errorf("second ClearBirth (no-op) returned %v", err)
	}
}
