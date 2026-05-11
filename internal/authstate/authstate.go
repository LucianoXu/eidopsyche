// Package authstate is the in-container marker for "claude auth has
// failed; the agent must not run again until the operator runs
// `eidos forge login <slug>`".
//
// File: /eidos/run/auth_required.json
//
// Producers:  agent-runner (writes on EXIT_AUTH_REQUIRED).
// Consumers:  agent-runner (self-gate at startup),
//
//	eidos forge status-detail (in-container, surfaces to host),
//	eidos forge login (clears on successful credential install).
//
// The file lives in /eidos/run/, which is in the mind-form's volume —
// so the host can clear it via a one-shot `docker run --rm --mount`
// without the container being up.
package authstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/fileops"
)

// defaultPath is the in-container path to the marker file. Hard-coded —
// the supervisor and the in-container `eidos` subcommands all run inside
// the volume's filesystem.
const defaultPath = "/eidos/run/auth_required.json"

// Path is the marker-file path used by Write/Read/Clear. Defaults to
// defaultPath; tests substitute a temp-file via SetPathForTest.
var Path = defaultPath

// SetPathForTest swaps the marker file path. Tests only.
func SetPathForTest(p string) { Path = p }

// ResetPathForTest restores Path to its default.
func ResetPathForTest() { Path = defaultPath }

// State is the on-disk payload. Minimal by design: timestamp only.
// The slug is implicit (one mind-form per volume) and reasoning lives
// in agent-runner / claude logs, not here.
type State struct {
	Since int64 `json:"since"`
}

// Write atomically writes the marker to Path. Pass time.Now() in
// production; tests substitute a fixed clock and/or path via
// SetPathForTest.
func Write(now time.Time) error {
	return WriteAt(Path, now)
}

// WriteAt is like Write but parameterises the path. Used by tests.
func WriteAt(path string, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(State{Since: now.Unix()}, "", "  ")
	if err != nil {
		return err
	}
	return fileops.AtomicWrite(path, body, 0o600)
}

// Read returns the current state, or (nil, nil) if absent. Errors
// reflect IO or JSON-parse failures.
func Read() (*State, error) {
	return ReadAt(Path)
}

// ReadAt is like Read but parameterises the path. Used by tests.
func ReadAt(path string) (*State, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return &s, nil
}

// Clear removes the marker. Safe to call when absent.
func Clear() error {
	return ClearAt(Path)
}

// ClearAt is like Clear but parameterises the path. Used by tests.
func ClearAt(path string) error {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
