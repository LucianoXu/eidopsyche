// Package sessionstate owns the on-disk session.json file at
// /eidos/run/session.json. agent-runner mints a session.json (with a
// fresh UUID) on the first wake of a new session and resumes the same
// UUID on subsequent wakes; `eidos forge dream end` clears it so the
// next wake starts fresh.
//
// Atomic writes via tmp + rename; per-file flock for Mint /
// IncrementWake / Clear so a wake-in-progress and an out-of-band
// `forge dream end` (operator running the command via `eidos forge
// exec`) cannot interleave.
//
// See docs/superpowers/specs/2026-05-10-persistent-wake-context-design.md.
package sessionstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// SchemaVersion is bumped when the on-disk format changes.
const SchemaVersion = 1

// State is the session-state record.
type State struct {
	V                int    `json:"v"`
	SessionID        string `json:"session_id"`
	SessionStartedAt int64  `json:"session_started_at"`
	WakesInSession   int    `json:"wakes_in_session"`
}

// Read returns the state at path. A missing file yields a zero State
// without error; a corrupt file returns the unmarshal error so the
// caller can decide whether to fall back to a fresh session.
func Read(path string) (State, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read session-state: %w", err)
	}
	var st State
	if err := json.Unmarshal(body, &st); err != nil {
		return State{}, fmt.Errorf("unmarshal session-state: %w", err)
	}
	return st, nil
}

// Mint generates a new UUID, persists a fresh State{SessionID,
// SessionStartedAt: now.Unix()} atomically, and returns the new state.
func Mint(path string, now time.Time) (State, error) {
	st := State{
		V:                SchemaVersion,
		SessionID:        uuid.NewString(),
		SessionStartedAt: now.Unix(),
		WakesInSession:   0,
	}
	if err := writeState(path, st); err != nil {
		return State{}, err
	}
	return st, nil
}

// IncrementWake atomically bumps WakesInSession by 1. No-op (returns
// nil) when session.json is absent — the caller's wake completed after
// a concurrent `dream end` cleared the file.
func IncrementWake(path string) error {
	return updateState(path, func(st State, exists bool) (State, bool) {
		if !exists {
			return st, false
		}
		st.WakesInSession++
		return st, true
	})
}

// Clear removes session.json. Idempotent — missing file returns nil.
func Clear(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove session-state: %w", err)
	}
	return nil
}

// writeState atomically persists st to path under flock.
func writeState(path string, st State) error {
	return updateState(path, func(_ State, _ bool) (State, bool) {
		st.V = SchemaVersion
		return st, true
	})
}

// updateState reads the current state under flock, applies mutate, and
// writes the result iff mutate returns shouldWrite=true.
func updateState(path string, mutate func(st State, exists bool) (State, bool)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir session-state dir: %w", err)
	}
	lockPath := path + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open session-state lock: %w", err)
	}
	defer lf.Close()
	if err := platformLock(lf); err != nil {
		return fmt.Errorf("flock session-state: %w", err)
	}
	defer func() { _ = platformUnlock(lf) }()

	cur, exists, err := readUnderLock(path)
	if err != nil {
		return err
	}

	next, write := mutate(cur, exists)
	if !write {
		return nil
	}
	next.V = SchemaVersion

	body, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session-state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "session-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename session-state: %w", err)
	}
	cleanup = false
	return nil
}

// readUnderLock reads path while caller holds the flock. Returns
// (state, exists, err). Missing file → exists=false, nil err.
func readUnderLock(path string) (State, bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, false, nil
		}
		return State{}, false, fmt.Errorf("read session-state: %w", err)
	}
	var st State
	if err := json.Unmarshal(body, &st); err != nil {
		return State{}, false, fmt.Errorf("unmarshal session-state: %w", err)
	}
	return st, true, nil
}
