// Package dreamstate owns the on-disk dream-state.json file at
// /eidos/run/dream-state.json. The mind-form's `eidos forge dream
// begin/end` commands mutate this; agent-runner reads it at wake
// time to surface dream hints in the wake context.
//
// Atomic writes via tmp + rename. Per-file flock serialises concurrent
// Begin/End calls so two parallel agent invocations cannot interleave.
//
// See docs/superpowers/specs/2026-05-09-heartbeat-plans-dreams-design.md §6.
package dreamstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SchemaVersion is bumped when the on-disk format changes.
const SchemaVersion = 1

// State is the dream-state record.
type State struct {
	V                   int    `json:"v"`
	LastDreamStartedAt  int64  `json:"last_dream_started_at,omitempty"`
	LastDreamFinishedAt int64  `json:"last_dream_finished_at,omitempty"`
	LastDreamNote       string `json:"last_dream_note,omitempty"`
	LastDreamProse      string `json:"last_dream_prose,omitempty"`
	DreamCount          int    `json:"dream_count"`
	CurrentlyDreaming   bool   `json:"currently_dreaming,omitempty"`
}

// Read returns the state at path. A missing file yields a zero State
// without error; a corrupt file returns the unmarshal error.
func Read(path string) (State, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read dream-state: %w", err)
	}
	var st State
	if err := json.Unmarshal(body, &st); err != nil {
		return State{}, fmt.Errorf("unmarshal dream-state: %w", err)
	}
	return st, nil
}

// Begin marks the start of a dream. Sets LastDreamStartedAt = now.Unix()
// and CurrentlyDreaming = true. Optional note is recorded as a
// LastDreamNote prelude (overwritten by End's note when it lands).
//
// Acquires an exclusive flock on path+".lock" so two parallel agents
// cannot interleave begin/end.
func Begin(path string, now time.Time, note string) error {
	return updateState(path, func(st State) State {
		st.LastDreamStartedAt = now.Unix()
		st.CurrentlyDreaming = true
		if note != "" {
			st.LastDreamNote = note
		}
		return st
	})
}

// End marks dream completion. Note is required (one-line summary). If
// prosePath is non-empty, it must live under memory/episodic/ and is
// recorded so the next wake can show it. Increments DreamCount and
// clears CurrentlyDreaming.
//
// End without a prior Begin is allowed (claude crashed mid-dream and
// the agent recovered cleanly).
func End(path string, now time.Time, note, prosePath string) error {
	if note == "" {
		return errors.New("dream end note is required (--note)")
	}
	if prosePath != "" && !isUnderEpisodic(prosePath) {
		return fmt.Errorf("dream prose path must live under memory/episodic/, got %q", prosePath)
	}
	return updateState(path, func(st State) State {
		st.LastDreamFinishedAt = now.Unix()
		st.LastDreamNote = note
		if prosePath != "" {
			st.LastDreamProse = prosePath
		}
		st.DreamCount++
		st.CurrentlyDreaming = false
		return st
	})
}

// WriteDigest persists the digest of a completed dream cycle to the
// mind-form's ontology at dreams/YYYY-MM-DD.md (appending an HH:MM:SS
// UTC section for each cycle so multiple same-day dreams don't
// overwrite each other) and appends a one-line entry to
// dreams/DREAMS.md keyed by full timestamp. Both writes are
// best-effort: if the dreams/ directory is missing WriteDigest
// creates it. Callers should invoke this from `eidos forge dream end`
// flows so the operator gets a human-readable surface alongside the
// JSON state file.
//
// summary is the consolidation note (typically the dream-end --note
// plus a sentence about the prose file). The first line is also used
// as the DREAMS.md index hook (truncated to 120 chars).
func WriteDigest(ontologyDir string, when time.Time, summary string) error {
	dir := filepath.Join(ontologyDir, "dreams")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir dreams: %w", err)
	}

	utc := when.UTC()
	date := utc.Format("2006-01-02")
	dailyPath := filepath.Join(dir, date+".md")
	existingDaily, err := os.ReadFile(dailyPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", dailyPath, err)
	}
	var dailyBuilder strings.Builder
	if len(existingDaily) == 0 {
		fmt.Fprintf(&dailyBuilder, "# %s\n\n", date)
	} else {
		dailyBuilder.Write(existingDaily)
		if !strings.HasSuffix(string(existingDaily), "\n\n") {
			if strings.HasSuffix(string(existingDaily), "\n") {
				dailyBuilder.WriteString("\n")
			} else {
				dailyBuilder.WriteString("\n\n")
			}
		}
	}
	fmt.Fprintf(&dailyBuilder, "## %s UTC\n\n%s\n", utc.Format("15:04:05"), summary)
	if err := os.WriteFile(dailyPath, []byte(dailyBuilder.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dailyPath, err)
	}

	indexPath := filepath.Join(dir, "DREAMS.md")
	existing, err := os.ReadFile(indexPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read DREAMS.md: %w", err)
	}
	firstLine := strings.SplitN(strings.TrimSpace(summary), "\n", 2)[0]
	if len(firstLine) > 120 {
		firstLine = firstLine[:117] + "..."
	}
	entry := fmt.Sprintf("- [%s %s UTC](%s.md) — %s\n", date, utc.Format("15:04:05"), date, firstLine)
	newBody := string(existing) + entry
	if err := os.WriteFile(indexPath, []byte(newBody), 0o644); err != nil {
		return fmt.Errorf("write DREAMS.md: %w", err)
	}
	return nil
}

func isUnderEpisodic(p string) bool {
	clean := filepath.ToSlash(filepath.Clean(p))
	return strings.HasPrefix(clean, "memory/episodic/")
}

// updateState reads, mutates, and writes path under an exclusive
// flock so concurrent callers serialise.
func updateState(path string, mutate func(State) State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir dream-state dir: %w", err)
	}
	lockPath := path + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open dream-state lock: %w", err)
	}
	defer lf.Close()
	if err := platformLock(lf); err != nil {
		return fmt.Errorf("flock dream-state: %w", err)
	}
	defer func() { _ = platformUnlock(lf) }()

	st, err := Read(path)
	if err != nil {
		return err
	}
	st.V = SchemaVersion
	st = mutate(st)

	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dream-state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "dream-state-*.tmp")
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
		return fmt.Errorf("rename dream-state: %w", err)
	}
	cleanup = false
	return nil
}
