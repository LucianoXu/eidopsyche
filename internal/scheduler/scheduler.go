package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// MinFutureWindow is the minimum time-from-now a plan may be set for.
	MinFutureWindow = 60 * time.Second
	// MaxFutureWindow is the maximum time-from-now a plan may be set for.
	MaxFutureWindow = 30 * 24 * time.Hour
	// MaxHintLen caps the agent-authored hint length (in runes).
	MaxHintLen = 256
)

const firedSubdir = "fired"

// Add writes a new active plan file under dir. Returns the populated Plan.
//
// Bounds:
//   - hint must be non-empty, single-line, ≤ MaxHintLen runes (UTF-8 chars).
//   - at must be in [now+MinFutureWindow, now+MaxFutureWindow].
//
// The write is atomic (tmp + rename). Filename is plan.ID+".json".
func Add(dir string, now time.Time, hint string, at time.Time) (Plan, error) {
	if hint == "" {
		return Plan{}, errors.New("hint is required")
	}
	if strings.ContainsAny(hint, "\r\n") {
		return Plan{}, errors.New("hint must be single-line (no \\n or \\r)")
	}
	if len([]rune(hint)) > MaxHintLen {
		return Plan{}, fmt.Errorf("hint too long: %d runes > 256", len([]rune(hint)))
	}
	delta := at.Sub(now)
	if delta < MinFutureWindow {
		return Plan{}, fmt.Errorf("plan time too soon: must be at least 60s in the future, got %s", delta.Truncate(time.Second))
	}
	if delta > MaxFutureWindow {
		return Plan{}, fmt.Errorf("plan time too far: must be at most 30d in the future, got %s", delta.Truncate(time.Second))
	}

	plan := Plan{
		V:         SchemaVersion,
		ID:        newIDAt(now),
		At:        at.Unix(),
		Hint:      hint,
		CreatedAt: now.Unix(),
	}
	if err := writePlan(dir, plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// WritePlanForTest writes p into dir bypassing Add's bounds. Test-only;
// callers in production should always go through Add.
func WritePlanForTest(dir string, p Plan) error {
	return writePlan(dir, p)
}

func writePlan(dir string, p Plan) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir plans dir: %w", err)
	}
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal plan: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "plan-*.tmp")
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
	final := filepath.Join(dir, p.ID+".json")
	if err := os.Rename(tmpName, final); err != nil {
		return fmt.Errorf("rename plan: %w", err)
	}
	cleanup = false
	return nil
}

// List returns active plans (those still under dir, not yet under fired/),
// sorted lexicographically by ID. ID is timestamp-prefixed, so this is
// effectively creation-time order.
func List(dir string) ([]Plan, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("readdir plans: %w", err)
	}
	out := make([]Plan, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		var p Plan
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, fmt.Errorf("unmarshal %s: %w", name, err)
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Cancel deletes an active plan by ID. Returns an error if the plan does
// not exist or has already fired.
func Cancel(dir, id string) error {
	if !looksLikeID(id) {
		return fmt.Errorf("invalid plan id: %q", id)
	}
	path := filepath.Join(dir, id+".json")
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("plan %s not found (already fired or never existed)", id)
		}
		return fmt.Errorf("remove plan: %w", err)
	}
	return nil
}

// Clear deletes every active plan and returns the count removed.
func Clear(dir string) (int, error) {
	plans, err := List(dir)
	if err != nil {
		return 0, err
	}
	for _, p := range plans {
		if err := os.Remove(filepath.Join(dir, p.ID+".json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, fmt.Errorf("remove %s: %w", p.ID, err)
		}
	}
	return len(plans), nil
}

// ScanDue returns plans whose At <= now.Unix(), in ID order. Does not
// mutate the directory.
func ScanDue(dir string, now time.Time) ([]Plan, error) {
	plans, err := List(dir)
	if err != nil {
		return nil, err
	}
	cutoff := now.Unix()
	var due []Plan
	for _, p := range plans {
		if p.At <= cutoff {
			due = append(due, p)
		}
	}
	return due, nil
}

// MarkFired moves dir/<id>.json to dir/fired/<id>.json atomically.
// If the active file is already gone (because a previous tick raced and
// fired it, or because the supervisor crashed mid-rename and a fresh
// instance is running), MarkFired returns nil — fire-once across crashes
// is provided by wake-coalescing, not by this rename.
func MarkFired(dir, id string) error {
	src := filepath.Join(dir, id+".json")
	firedDir := filepath.Join(dir, firedSubdir)
	dst := filepath.Join(firedDir, id+".json")
	if err := os.MkdirAll(firedDir, 0o700); err != nil {
		return fmt.Errorf("mkdir fired: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("rename to fired: %w", err)
	}
	return nil
}

// looksLikeID is a defensive check so Cancel("..", ...) cannot escape the dir.
// Real IDs match "<timestamp>Z-plan-<hex>".
func looksLikeID(id string) bool {
	if len(id) < len("20260509T123000Z-plan-") || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' ||
			r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' ||
			r == '-') {
			return false
		}
	}
	return true
}
