package wake

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/LucianoXu/eidopsyche/internal/fileops"
)

// BirthSchemaVersion is bumped when the BirthSignal on-disk format changes.
const BirthSchemaVersion = 1

// BirthFileName is the filename (within the wake directory) where the
// host wizard places the one-shot birth signal. Sibling of pending.json
// and active.json. Consumed exactly once per MindForm.
const BirthFileName = "birth.json"

// BirthSignal is the on-disk payload that hands the new MindForm its
// summoning book + calling-words at first start. It is parallel to
// Signal — not a refinement of it — because the inputs and the
// supervisor's handling are distinct from the heartbeat / mindgate /
// manual flow. The supervisor consumes this file at most once and is
// guarded by an essence/born_at marker: once the agent has written
// born_at, any stale birth.json is silently cleared without re-running.
type BirthSignal struct {
	V                 int    `json:"v"`
	OperatorNpub      string `json:"operator_npub"`
	SummoningBookPath string `json:"summoning_book_path"`
	CallingWordsPath  string `json:"calling_words_path"`
	ResponsePath      string `json:"response_path"`
	TriggeredAt       int64  `json:"triggered_at"`
}

// WriteBirth atomically writes sig to <dir>/birth.json.
func WriteBirth(dir string, sig BirthSignal) error {
	body, err := json.MarshalIndent(sig, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal birth signal: %w", err)
	}
	return fileops.AtomicWrite(filepath.Join(dir, BirthFileName), body, 0o600)
}

// ReadBirth returns the parsed birth.json, or (nil, nil) if absent.
func ReadBirth(dir string) (*BirthSignal, error) {
	body, err := os.ReadFile(filepath.Join(dir, BirthFileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var sig BirthSignal
	if err := json.Unmarshal(body, &sig); err != nil {
		return nil, fmt.Errorf("decode birth signal: %w", err)
	}
	return &sig, nil
}

// ClearBirth removes <dir>/birth.json. Safe to call when absent.
func ClearBirth(dir string) error {
	err := os.Remove(filepath.Join(dir, BirthFileName))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
