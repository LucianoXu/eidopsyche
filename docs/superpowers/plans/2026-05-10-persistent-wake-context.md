# Persistent Wake Context Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every wake resume the previous Claude `session.jsonl` within a "session" bounded by `dream-end`. The first wake after a dream-end (or on a fresh ontology) starts a new session. Surface session info in `forge status`, `forge watch --list`, and the streamed wake renderer.

**Architecture:** A new `internal/sessionstate/` package owns `/eidos/run/session.json` (active session UUID + bookkeeping). `agent-runner` decides `--session-id <new>` vs `--resume <existing>` per wake based on session.json + dream-state, with a one-shot pre-flight + stderr-fallback retry on resume failure. `dreamEndEcho` clears session.json after `dreamstate.End`. Transcript `Entry` gains an optional `SessionID` field; `runtime-state`, `forge status`, and the watch surface read it.

**Tech Stack:** Go 1.22 workspace; `github.com/google/uuid` (already an indirect dep, promoted to direct); existing `internal/dreamstate` flock template re-applied to `internal/sessionstate`.

**Spec:** `docs/superpowers/specs/2026-05-10-persistent-wake-context-design.md`

---

## File Map

**New files (in `internal/sessionstate/`):**
- `sessionstate.go` — `State`, `Read`, `Mint`, `IncrementWake`, `Clear`, internal `updateState`
- `sessionstate_lock_unix.go` — `platformLock` / `platformUnlock` (flock)
- `sessionstate_lock_windows.go` — `platformLock` / `platformUnlock` (LockFileEx)
- `sessionstate_test.go` — round-trip + concurrency tests

**Modified files:**
- `cmd/eidos/supervisor/agent_runner.go` — session decision in `runAgent`; `SessionMode` arg into `buildClaudeArgs`; `IsFirstWakeOfNewSession` arg into `buildWakeMessage`; pre-flight stat + retry-on-resume-failure
- `cmd/eidos/supervisor/agent_runner_test.go` — extend buildClaudeArgs/wakemessage tests for the new fields
- `cmd/eidos/supervisor/agent_runner_stream_test.go` — extend the streamFixture-based tests to cover session NEW vs RESUME and the retry path
- `cmd/eidos/forge/dream.go` — `dreamEndEcho` calls `sessionstate.Clear` after `dreamstate.End`
- `cmd/eidos/forge/dream_test.go` — assert `sessionstate.Clear` is invoked
- `cmd/eidos/forge/runtime_state.go` — add `SessionID` / `SessionStartedAt` / `WakesInSession` to `RuntimeState`; read from session.json
- `cmd/eidos/forge/runtime_state_test.go` — assert new fields present/omitted
- `cmd/eidos/forge/status.go` — render `session: <prefix> (age <d>, <n> wakes)` line
- `cmd/eidos/forge/status_test.go` — assert the session line
- `internal/transcript/store.go` — add `SessionID` field to `Entry` (with `omitempty`)
- `internal/transcript/events.go` — no change (events come from Claude; `SessionID` is metadata, not an event field)
- `internal/transcript/store_test.go` — assert round-trip + v1-without-field reads cleanly
- `cmd/eidos/forge/transcript_list.go` — render SESSION column
- `cmd/eidos/forge/transcript_list_test.go` — assert column present
- `cmd/eidos/forge/watch_render.go` — per-wake header with session ordinal; session boundary line
- `cmd/eidos/forge/watch_render_test.go` — assert header + boundary
- `cmd/eidos/forge/watch.go` — `runWatchTail` follow loop tracks previous `SessionID`
- `cmd/eidos/forge/watch_test.go` — extend for boundary tracking
- `SPEC.md` — section on wake context lifecycle
- `EXAMPLE.md` — note that dream-end resets working memory next wake
- `go.mod` / `go.sum` — promote `github.com/google/uuid` to direct dep

---

### Task 1: New `internal/sessionstate/` package — types + Read

**Files:**
- Create: `internal/sessionstate/sessionstate.go`
- Create: `internal/sessionstate/sessionstate_lock_unix.go`
- Create: `internal/sessionstate/sessionstate_lock_windows.go`
- Test: `internal/sessionstate/sessionstate_test.go`

- [ ] **Step 1: Write the failing tests for `Read`**

Create `internal/sessionstate/sessionstate_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sessionstate/...`
Expected: FAIL — package does not exist yet.

- [ ] **Step 3: Implement `State` and `Read`**

Create `internal/sessionstate/sessionstate.go`:

```go
// Package sessionstate owns the on-disk session.json file at
// /eidos/run/session.json. agent-runner mints a session.json (with a
// fresh UUID) on the first wake of a new session and resumes the same
// UUID on subsequent wakes; `eidos forge dream end` clears it so the
// next wake starts fresh.
//
// Atomic writes via tmp + rename; per-file flock for Mint / IncrementWake
// / Clear so a wake-in-progress and an out-of-band `forge dream end`
// (operator running the command via `eidos forge exec`) cannot interleave.
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

// IncrementWake atomically bumps WakesInSession by 1. No-op (returns nil)
// when session.json is absent — the caller's wake completed after a
// concurrent `dream end` cleared the file.
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
// writes the result iff mutate returns shouldWrite=true. Used by both
// Mint (overwrites unconditionally) and IncrementWake (skips when file
// is absent).
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
// (state, exists, err). Missing file is exists=false, nil err.
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
```

- [ ] **Step 4: Create the lock platform shims**

Create `internal/sessionstate/sessionstate_lock_unix.go`:

```go
//go:build unix

package sessionstate

import (
	"os"
	"syscall"
)

// platformLock takes a blocking exclusive flock on f.
func platformLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// platformUnlock releases a previously-held flock.
func platformUnlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
```

Create `internal/sessionstate/sessionstate_lock_windows.go`:

```go
//go:build windows

package sessionstate

import (
	"os"

	"golang.org/x/sys/windows"
)

// platformLock takes a blocking exclusive lock on f via LockFileEx.
func platformLock(f *os.File) error {
	overlapped := new(windows.Overlapped)
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK
	return windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, ^uint32(0), ^uint32(0), overlapped)
}

// platformUnlock releases a previously-held lock via UnlockFileEx.
func platformUnlock(f *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), overlapped)
}
```

- [ ] **Step 5: Promote `github.com/google/uuid` to a direct dep**

Run from worktree root:
```bash
go get github.com/google/uuid
go mod tidy
```

Expected: `go.mod` line for `github.com/google/uuid` no longer has `// indirect`.

- [ ] **Step 6: Run the Read tests; expect PASS**

Run: `go test ./internal/sessionstate/...`
Expected: PASS (only the three TestRead_* tests run since Mint/IncrementWake/Clear have no tests yet).

- [ ] **Step 7: Commit**

```bash
git add internal/sessionstate/ go.mod go.sum
git commit -m "feat(sessionstate): add Read with atomic-write update scaffolding"
```

---

### Task 2: `sessionstate.Mint`

**Files:**
- Test: `internal/sessionstate/sessionstate_test.go` (extend)
- Modify: (Mint implementation already in Task 1; this task adds tests)

- [ ] **Step 1: Write the failing tests for `Mint`**

Append to `internal/sessionstate/sessionstate_test.go`:

```go
import "time" // already needed; ensure it's imported once at the top

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

	// Read it back from disk; same contents.
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
```

- [ ] **Step 2: Run the tests to verify they pass**

Run: `go test ./internal/sessionstate/... -run Mint -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/sessionstate/sessionstate_test.go
git commit -m "test(sessionstate): cover Mint round-trip and uniqueness"
```

---

### Task 3: `sessionstate.IncrementWake` and `sessionstate.Clear`

**Files:**
- Test: `internal/sessionstate/sessionstate_test.go` (extend)

- [ ] **Step 1: Write the failing tests for IncrementWake and Clear**

Append to `internal/sessionstate/sessionstate_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests; expect PASS**

Run: `go test ./internal/sessionstate/... -v`
Expected: PASS for all sessionstate tests.

- [ ] **Step 3: Commit**

```bash
git add internal/sessionstate/sessionstate_test.go
git commit -m "test(sessionstate): cover IncrementWake and Clear"
```

---

### Task 4: `sessionstate` concurrency under flock

**Files:**
- Test: `internal/sessionstate/sessionstate_test.go` (extend)

- [ ] **Step 1: Write the concurrency test**

Append to `internal/sessionstate/sessionstate_test.go`:

```go
import "sync"

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
```

Note: top-of-file imports should now have `"sync"` and `"time"` collected once. If your editor or `goimports` complains, consolidate the imports block before running.

- [ ] **Step 2: Run the test; expect PASS**

Run: `go test ./internal/sessionstate/... -run Parallel -v -race`
Expected: PASS, no data race.

- [ ] **Step 3: Commit**

```bash
git add internal/sessionstate/sessionstate_test.go
git commit -m "test(sessionstate): assert flock serialises parallel IncrementWake"
```

---

### Task 5: Add `SessionID` field to transcript `Entry`

**Files:**
- Modify: `internal/transcript/store.go` (line ~38, the `Entry` struct)
- Test: `internal/transcript/store_test.go` (extend)

- [ ] **Step 1: Write the failing tests for round-trip + backwards-compat**

Append to `internal/transcript/store_test.go`:

```go
func TestEntry_SessionIDRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Entry{
		ID:        "abc12345",
		SessionID: "7d2f6f2e-1b2a-4c3d-9e8f-aabbccddeeff",
		Reason:    "heartbeat",
		StartedAt: 1700000000,
		EndedAt:   1700000010,
		OK:        true,
		ExitCode:  0,
		SizeBytes: 0,
	}
	if err := s.WriteIndex(Index{V: 1, Wakes: []Entry{want}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Wakes) != 1 || got.Wakes[0].SessionID != want.SessionID {
		t.Fatalf("SessionID round-trip: got %+v", got.Wakes)
	}
}

func TestEntry_LegacyIndexWithoutSessionID(t *testing.T) {
	dir := t.TempDir()
	// Hand-write a v1 index that predates the SessionID field.
	body := []byte(`{
  "v": 1,
  "wakes": [
    {
      "id": "abc12345",
      "reason": "heartbeat",
      "started_at": 1700000000,
      "ended_at": 1700000010,
      "ok": true,
      "exit_code": 0,
      "cost_usd": null,
      "tool_use_count": 0,
      "thinking_blocks": 0,
      "size_bytes": 0
    }
  ]
}
`)
	if err := os.WriteFile(filepath.Join(dir, IndexFile), body, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := s.ReadIndex()
	if err != nil {
		t.Fatalf("legacy ReadIndex: %v", err)
	}
	if len(idx.Wakes) != 1 || idx.Wakes[0].SessionID != "" {
		t.Fatalf("legacy entry should have empty SessionID, got %+v", idx.Wakes)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/transcript/... -run SessionID -v`
Expected: FAIL — `Entry.SessionID` does not exist.

- [ ] **Step 3: Add `SessionID` to `Entry`**

Modify `internal/transcript/store.go`. Find the `Entry` struct (around line 38) and insert the `SessionID` field after `ID`:

```go
// Entry is one row in Index.Wakes — a finalized wake's metadata.
type Entry struct {
	ID             string   `json:"id"`
	SessionID      string   `json:"session_id,omitempty"`
	Reason         string   `json:"reason"`
	StartedAt      int64    `json:"started_at"`
	EndedAt        int64    `json:"ended_at"`
	OK             bool     `json:"ok"`
	ExitCode       int      `json:"exit_code"`
	CostUSD        *float64 `json:"cost_usd"`
	ToolUseCount   int      `json:"tool_use_count"`
	ThinkingBlocks int      `json:"thinking_blocks"`
	SizeBytes      int64    `json:"size_bytes"`
}
```

- [ ] **Step 4: Run the tests; expect PASS**

Run: `go test ./internal/transcript/... -v`
Expected: PASS for all transcript tests (existing + new).

- [ ] **Step 5: Commit**

```bash
git add internal/transcript/store.go internal/transcript/store_test.go
git commit -m "feat(transcript): add optional SessionID to wake Entry"
```

---

### Task 6: `SessionMode` type and `buildClaudeArgs` plumbing

**Files:**
- Modify: `cmd/eidos/supervisor/agent_runner.go` (around line 547, `buildClaudeArgs`)
- Test: `cmd/eidos/supervisor/agent_runner_test.go` (extend)

- [ ] **Step 1: Write the failing tests for the new `SessionMode` arg**

Append to `cmd/eidos/supervisor/agent_runner_test.go`:

```go
func TestBuildClaudeArgs_SessionNew(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	args := buildClaudeArgs("identity body", "wake msg", cfgPath, false, SessionMode{
		Kind: SessionNew,
		UUID: "00000000-1111-2222-3333-444444444444",
	})
	if !contains(args, "--session-id") {
		t.Fatalf("want --session-id in args, got %v", args)
	}
	if !contains(args, "00000000-1111-2222-3333-444444444444") {
		t.Fatalf("want UUID in args, got %v", args)
	}
	if contains(args, "--resume") {
		t.Fatalf("must not pass --resume on SessionNew, got %v", args)
	}
}

func TestBuildClaudeArgs_SessionResume(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	args := buildClaudeArgs("identity body", "wake msg", cfgPath, false, SessionMode{
		Kind: SessionResume,
		UUID: "00000000-1111-2222-3333-444444444444",
	})
	if !contains(args, "--resume") {
		t.Fatalf("want --resume in args, got %v", args)
	}
	if !contains(args, "00000000-1111-2222-3333-444444444444") {
		t.Fatalf("want UUID in args, got %v", args)
	}
	if contains(args, "--session-id") {
		t.Fatalf("must not pass --session-id on SessionResume, got %v", args)
	}
}

// contains is a small slice helper for the new tests above. If
// agent_runner_test.go already defines a similar helper, reuse it
// instead of duplicating.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
```

If a `contains` helper already exists in `agent_runner_test.go` (check before duplicating), reuse it instead.

You also need to update existing `TestBuildClaudeArgs_*` tests so they pass a `SessionMode` argument. For each existing call to `buildClaudeArgs(identity, msg, cfgPath, streamJSON)`, append `, SessionMode{Kind: SessionNew, UUID: "test-uuid"}`. There are calls in:
- `TestBuildClaudeArgs_NoModel`
- `TestBuildClaudeArgs_StreamJSON`
- `TestBuildClaudeArgs_WithModel`
- `TestBuildClaudeArgs_MissingConfig`

Add an assertion in each of those updated tests that `--session-id` appears (since they all pass `SessionNew`).

- [ ] **Step 2: Run tests; expect FAIL**

Run: `go test ./cmd/eidos/supervisor/... -run BuildClaudeArgs -v`
Expected: FAIL — `SessionMode` undefined; existing tests fail to compile.

- [ ] **Step 3: Implement `SessionMode` and update `buildClaudeArgs`**

Modify `cmd/eidos/supervisor/agent_runner.go`. Above `buildClaudeArgs`, add:

```go
// SessionKind selects how a wake's claude invocation is bound to a
// Claude Code session.
type SessionKind int

const (
	// SessionNew creates a new session with the given UUID via
	// `--session-id <UUID>`. Used for the first wake of a session
	// (fresh ontology, post-purge, or first wake after dream-end).
	SessionNew SessionKind = iota
	// SessionResume continues an existing session via
	// `--resume <UUID>`. Used for every wake within a session.
	SessionResume
)

// SessionMode describes how the upcoming claude invocation should bind
// to a Claude Code session.
type SessionMode struct {
	Kind SessionKind
	UUID string // required for both Kinds
}
```

Change the `buildClaudeArgs` signature and emission:

```go
// buildClaudeArgs constructs the argv passed to `claude` for one wake.
// Reads the mind-form config to pick up an optional model pin.
//
// Config-load errors are tolerated: a missing or malformed config.toml
// drops us back to claude's subscription default rather than bricking
// the wake. Once a config loads, the model id is passed through
// verbatim — host-side commands (forge create / forge config) validate
// the id; a stale / hand-edited config with an unknown id surfaces at
// the next wake when claude itself rejects it.
//
// streamJSON=true appends `--output-format stream-json --verbose
// --include-partial-messages` so the supervisor can capture the
// structured event stream into the per-wake transcript file.
//
// sess controls session continuity: SessionNew creates a session with
// the given UUID; SessionResume picks up an existing one. Either way
// the UUID is the agent-runner's record of which session.jsonl on disk
// corresponds to this wake.
func buildClaudeArgs(identity, msg, configPath string, streamJSON bool, sess SessionMode) []string {
	args := []string{
		"--append-system-prompt", identity,
		"--dangerously-skip-permissions",
	}
	switch sess.Kind {
	case SessionNew:
		args = append(args, "--session-id", sess.UUID)
	case SessionResume:
		args = append(args, "--resume", sess.UUID)
	}
	if cfg, err := config.Load(configPath); err == nil {
		if model := cfg.MindForm.Model; model != "" {
			args = append(args, "--model", model)
		}
	}
	if streamJSON {
		args = append(args, "--output-format", "stream-json", "--verbose", "--include-partial-messages")
	}
	args = append(args, "-p", msg)
	return args
}
```

Update every other call site of `buildClaudeArgs`. Find them:

```bash
grep -rn "buildClaudeArgs(" cmd/eidos/supervisor/
```

In `runAgent` (around line 144), the existing call is:
```go
args := buildClaudeArgs(string(identity), msg, gateConfigPath, streamJSON)
```
Replace with a placeholder for now (actual session decision lands in Task 8):
```go
args := buildClaudeArgs(string(identity), msg, gateConfigPath, streamJSON, SessionMode{
    Kind: SessionNew,
    UUID: "00000000-0000-0000-0000-000000000000", // overwritten in Task 8
})
```
This keeps the build green between Task 6 and Task 8.

Also update `plainClaudeArgs` in `agent_runner.go` (around line 411): the helper currently strips stream-json flags. Add stripping for `--session-id` / `--resume` if those should be retained — they should be retained, so **no change needed** to plainClaudeArgs. Verify by reading the code; the stripper only targets stream-json flags. Add a comment if helpful.

- [ ] **Step 4: Run all supervisor tests; expect PASS**

Run: `go test ./cmd/eidos/supervisor/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/supervisor/agent_runner.go cmd/eidos/supervisor/agent_runner_test.go
git commit -m "feat(supervisor): thread SessionMode through buildClaudeArgs"
```

---

### Task 7: `IsFirstWakeOfNewSession` in `buildWakeMessage`

**Files:**
- Modify: `cmd/eidos/supervisor/agent_runner.go` (the `wakePromptInput` struct + `buildWakeMessage`, around line 478)
- Test: `cmd/eidos/supervisor/agent_runner_test.go` (extend)

- [ ] **Step 1: Write the failing test**

Append to `cmd/eidos/supervisor/agent_runner_test.go`:

```go
func TestBuildWakeMessage_FirstWakeOfNewSession_AfterDream(t *testing.T) {
	msg := buildWakeMessage(wakePromptInput{
		Reason:                  "heartbeat",
		IsFirstWakeOfNewSession: true,
		DreamCount:              5,
		LastDreamFinishedAt:     1700000000,
	})
	if !strings.Contains(msg, "first wake of a new session") {
		t.Fatalf("want first-wake prefix, got %q", msg)
	}
	if !strings.Contains(msg, "dream #5") {
		t.Fatalf("want dream #N reference, got %q", msg)
	}
	// The status snapshot still follows.
	if !strings.Contains(msg, "Reason: heartbeat") {
		t.Fatalf("want status snapshot after prefix, got %q", msg)
	}
}

func TestBuildWakeMessage_FirstWakeNoPriorDream(t *testing.T) {
	msg := buildWakeMessage(wakePromptInput{
		Reason:                  "heartbeat",
		IsFirstWakeOfNewSession: true,
		DreamCount:              0,
		LastDreamFinishedAt:     0,
	})
	if !strings.Contains(msg, "no prior dream") {
		t.Fatalf("want no-prior-dream variant, got %q", msg)
	}
	if !strings.Contains(msg, "Reason: heartbeat") {
		t.Fatalf("want status snapshot after prefix, got %q", msg)
	}
}

func TestBuildWakeMessage_NotFirstWake_NoPrefix(t *testing.T) {
	msg := buildWakeMessage(wakePromptInput{
		Reason:                  "heartbeat",
		IsFirstWakeOfNewSession: false,
		DreamCount:              5,
		LastDreamFinishedAt:     1700000000,
	})
	if strings.Contains(msg, "first wake of a new session") {
		t.Fatalf("must not include prefix for non-first wake, got %q", msg)
	}
}
```

- [ ] **Step 2: Run tests; expect FAIL**

Run: `go test ./cmd/eidos/supervisor/... -run WakeMessage_First -v`
Expected: FAIL — `IsFirstWakeOfNewSession` field does not exist.

- [ ] **Step 3: Add the new fields and prefix logic**

Modify `cmd/eidos/supervisor/agent_runner.go`. Update `wakePromptInput`:

```go
type wakePromptInput struct {
	Reason                string
	Hint                  string
	InboxUnread           int
	SinceLastWakeSeconds  int64
	MasterLikelyAsleep    bool
	QuietStart            string
	QuietEnd              string
	TZ                    string
	SinceLastDreamSeconds int64
	DreamEligible         bool
	LastDreamNote         string
	PlanID                string

	// IsFirstWakeOfNewSession marks this wake as the first one in a
	// freshly minted Claude session — either the very first wake of
	// the mind-form, or the first wake after a dream-end. When set,
	// buildWakeMessage prepends a one-paragraph prefix telling the
	// mind-form that working memory was reset and on-disk state is
	// authoritative.
	IsFirstWakeOfNewSession bool
	// DreamCount is the most recently completed dream's index (0 if
	// no dream has ever finished). Surfaced in the first-wake prefix.
	DreamCount int
	// LastDreamFinishedAt is the unix-second timestamp of the most
	// recent dream-end (0 if never). Surfaced in the first-wake prefix.
	LastDreamFinishedAt int64
}
```

Update `buildWakeMessage`:

```go
func buildWakeMessage(in wakePromptInput) string {
	var sb strings.Builder
	if in.IsFirstWakeOfNewSession {
		if in.LastDreamFinishedAt > 0 {
			fmt.Fprintf(&sb,
				"This is the first wake of a new session (your prior working memory was consolidated in dream #%d at %s; on-disk memory/journal/essence are intact, refer to them as needed).\n\n",
				in.DreamCount,
				time.Unix(in.LastDreamFinishedAt, 0).UTC().Format(time.RFC3339),
			)
		} else {
			fmt.Fprintf(&sb,
				"This is the first wake of a new session (no prior dream — this is the mind-form's first session; on-disk substrate is intact).\n\n",
			)
		}
	}
	fmt.Fprintf(&sb, "You have just woken. Reason: %s.", in.Reason)
	if in.Hint != "" {
		fmt.Fprintf(&sb, " %s.", in.Hint)
	}
	fmt.Fprintf(&sb, " Inbox has %d unread message(s).", in.InboxUnread)
	if in.SinceLastWakeSeconds > 0 {
		fmt.Fprintf(&sb, " %ds since last wake.", in.SinceLastWakeSeconds)
	}
	if in.MasterLikelyAsleep && in.QuietStart != "" {
		fmt.Fprintf(&sb, " Master is likely asleep (quiet hours %s–%s%s).",
			in.QuietStart, in.QuietEnd, tzSuffix(in.TZ))
	}
	if in.SinceLastDreamSeconds > 0 {
		hours := in.SinceLastDreamSeconds / 3600
		fmt.Fprintf(&sb, " %dh since your last dream.", hours)
	}
	if in.DreamEligible && in.SinceLastDreamSeconds > 0 {
		fmt.Fprintf(&sb, " You are eligible to dream now.")
	}
	if in.LastDreamNote != "" {
		fmt.Fprintf(&sb, " Last dream: %q.", in.LastDreamNote)
	}
	if in.PlanID != "" {
		fmt.Fprintf(&sb, " (Planned wake; plan id %s.)", in.PlanID)
	}
	return sb.String()
}
```

- [ ] **Step 4: Run tests; expect PASS**

Run: `go test ./cmd/eidos/supervisor/... -run WakeMessage -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/supervisor/agent_runner.go cmd/eidos/supervisor/agent_runner_test.go
git commit -m "feat(supervisor): wake-message prefix on first wake of new session"
```

---

### Task 8: `runAgent` session decision + persistence

**Files:**
- Modify: `cmd/eidos/supervisor/agent_runner.go` (the `runAgent` function around line 90; add session decision; new constant for `sessionStatePath`)
- Test: `cmd/eidos/supervisor/agent_runner_stream_test.go` (extend)

- [ ] **Step 1: Convert path consts to vars and add a `decideSessionMode` helper**

In `cmd/eidos/supervisor/agent_runner.go`, change `agentLockPath` and `dreamStateRuntimePath` from `const` to `var` so tests can rebind them — mirrors the existing `transcriptsRuntimeDir` pattern. Add `sessionStatePath` as a new var.

Also add a pure helper `decideSessionMode` extracted from the (planned) runAgent change. This makes the decision logic unit-testable without filesystem orchestration:

```go
// decideSessionMode picks NEW vs RESUME based on already-read state.
// Pure: no I/O. Returns (mode, isFirstWake) where mode.UUID may need
// to be filled in by the caller after a Mint when isFirstWake.
func decideSessionMode(sess sessionstate.State, sessReadErr error, ds dreamstate.State) (mode SessionMode, isFirstWake bool) {
	switch {
	case sessReadErr != nil, sess.SessionID == "":
		return SessionMode{Kind: SessionNew}, true
	case ds.LastDreamFinishedAt > sess.SessionStartedAt:
		return SessionMode{Kind: SessionNew}, true
	default:
		return SessionMode{Kind: SessionResume, UUID: sess.SessionID}, false
	}
}
```

- [ ] **Step 2: Write the failing tests for `decideSessionMode` and the runWithTranscript SessionID stamp**

Append to `cmd/eidos/supervisor/agent_runner_test.go`:

```go
import "github.com/LucianoXu/eidopsyche/internal/sessionstate"
import "github.com/LucianoXu/eidopsyche/internal/dreamstate"

func TestDecideSessionMode_NewWhenAbsent(t *testing.T) {
	mode, first := decideSessionMode(sessionstate.State{}, nil, dreamstate.State{})
	if !first || mode.Kind != SessionNew {
		t.Fatalf("absent state → NEW; got %+v first=%v", mode, first)
	}
}

func TestDecideSessionMode_NewWhenCorrupt(t *testing.T) {
	mode, first := decideSessionMode(sessionstate.State{}, errors.New("corrupt"), dreamstate.State{})
	if !first || mode.Kind != SessionNew {
		t.Fatalf("corrupt state → NEW; got %+v first=%v", mode, first)
	}
}

func TestDecideSessionMode_ResumeWhenSessionFresh(t *testing.T) {
	sess := sessionstate.State{SessionID: "u-1", SessionStartedAt: 200}
	ds := dreamstate.State{LastDreamFinishedAt: 100} // dream older than session
	mode, first := decideSessionMode(sess, nil, ds)
	if first || mode.Kind != SessionResume || mode.UUID != "u-1" {
		t.Fatalf("fresh-session+old-dream → RESUME; got %+v first=%v", mode, first)
	}
}

func TestDecideSessionMode_NewWhenDreamFinishedAfterSession(t *testing.T) {
	sess := sessionstate.State{SessionID: "u-1", SessionStartedAt: 100}
	ds := dreamstate.State{LastDreamFinishedAt: 200} // dream newer than session
	mode, first := decideSessionMode(sess, nil, ds)
	if !first || mode.Kind != SessionNew {
		t.Fatalf("stale-session+new-dream → NEW; got %+v first=%v", mode, first)
	}
}
```

Append to `cmd/eidos/supervisor/agent_runner_stream_test.go`:

```go
func TestRunWithTranscript_StampsSessionIDOntoEntry(t *testing.T) {
	trDir := streamFixture(t, `printf '%s\n' \
  '{"type":"system","subtype":"init","model":"stub","tools":[]}' \
  '{"type":"result","subtype":"success","total_cost_usd":0,"duration_ms":1,"is_error":false,"num_turns":1}'
exit 0`)

	const wakeID = "stamp-wake-1"
	const sessionUUID = "00000000-1111-2222-3333-444444444444"
	sig := wake.Signal{V: 1, ID: wakeID, Reason: wake.ReasonHeartBeat, TriggeredAt: 1}
	if err := runWithTranscript(sig, t.TempDir(), []string{"-p", "ignored"}, sessionUUID); err != nil {
		t.Fatalf("runWithTranscript: %v", err)
	}
	store, _ := transcript.NewStore(trDir)
	idx, err := store.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Wakes) != 1 || idx.Wakes[0].SessionID != sessionUUID {
		t.Fatalf("entry.SessionID = %q, want %q (idx=%+v)", idx.Wakes[0].SessionID, sessionUUID, idx)
	}
}
```

Existing tests in `agent_runner_stream_test.go` that call `runWithTranscript(sig, dir, args)` need to add the empty-string sessionUUID arg: `runWithTranscript(sig, dir, args, "")`. (Empty UUID means no SessionID stamp on the entry — keeps legacy test assertions intact.)

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./cmd/eidos/supervisor/... -run "DecideSessionMode|StampsSessionID" -v`
Expected: FAIL — `decideSessionMode` undefined; `runWithTranscript` doesn't take a sessionUUID arg.

- [ ] **Step 4: Wire `runWithTranscript` to take and stamp `sessionUUID`**

Modify `cmd/eidos/supervisor/agent_runner.go`. Add the `sessionStatePath` var alongside the now-var-converted `dreamStateRuntimePath` and `agentLockPath`:

```go
// sessionStatePath is the in-container session.json path. agent-runner
// reads it at wake start to decide between --resume and --session-id;
// `forge dream end` clears it to force a fresh session next wake. var
// (not const) so tests can substitute a temp file.
var sessionStatePath = "/eidos/run/session.json"
```

Add an import for `sessionstate`:

```go
import (
    // ... existing imports ...
    "github.com/LucianoXu/eidopsyche/internal/sessionstate"
)
```

Update `runWithTranscript`'s signature and the entry it builds for Finalize:

```go
func runWithTranscript(sig wake.Signal, ontologyDir string, args []string, sessionUUID string) error {
    // ... (top unchanged) ...
    // At Finalize site, stamp the SessionID:
    entry := transcript.Entry{
        ID:           wakeID,
        SessionID:    sessionUUID,
        Reason:       string(sig.Reason),
        StartedAt:    startedAt,
        EndedAt:      time.Now().Unix(),
        OK:           ok,
        ExitCode:     exitCode,
        // ... rest unchanged ...
    }
    // ...
}
```

Update `runAgent` to call the orchestration logic — read state, decide, mint, build msg+args, run, increment:

Replace the section in `runAgent` from the existing `identity` read down through the `runWith*` calls with:

```go
identity, _ := os.ReadFile(filepath.Join(ontologyDir, "self/identity.md"))

sess, sessErr := sessionstate.Read(sessionStatePath)
if sessErr != nil {
    log.Printf("agent-runner: session.json corrupt (%v); minting new", sessErr)
}
mode, isFirstWake := decideSessionMode(sess, sessErr, ds)
sessionUUID := mode.UUID
if isFirstWake {
    if sessErr == nil && sess.SessionID != "" {
        // dream-end didn't get a chance to clear; do it now.
        if cErr := sessionstate.Clear(sessionStatePath); cErr != nil {
            log.Printf("agent-runner: session-state clear before mint: %v", cErr)
        }
    }
    fresh, mErr := sessionstate.Mint(sessionStatePath, time.Now())
    if mErr != nil {
        return fmt.Errorf("session-state mint: %w", mErr)
    }
    sessionUUID = fresh.SessionID
    mode.UUID = fresh.SessionID
}

msg := rebuildFirstWakeMessage(sig, cfg, ds, isFirstWake)

streamJSON := claudeSupportsStreamJSON(claudeBin)
if !streamJSON {
    log.Printf("agent-runner: stream-json unsupported (claude version below %d.%d.%d); transcripts disabled for this wake",
        claudeMinVersion[0], claudeMinVersion[1], claudeMinVersion[2])
}

args := buildClaudeArgs(string(identity), msg, gateConfigPath, streamJSON, mode)

var runErr error
if streamJSON {
    runErr = runWithTranscript(sig, ontologyDir, args, sessionUUID)
} else {
    runErr = runWithoutTranscript(ontologyDir, args)
}
if runErr != nil {
    return runErr
}

if err := sessionstate.IncrementWake(sessionStatePath); err != nil {
    log.Printf("agent-runner: IncrementWake: %v", err)
}
return nil
```

Also extract the `wakePromptInput{...}` block into the helper `rebuildFirstWakeMessage` (defined in Task 9, but since runAgent now calls it, define it now next to `buildWakeMessage`):

```go
// rebuildFirstWakeMessage assembles the wake-message from already-read
// state. Used by runAgent and by the resume-fallback path which needs
// to re-build the message after flipping IsFirstWakeOfNewSession.
func rebuildFirstWakeMessage(sig wake.Signal, cfg config.Config, ds dreamstate.State, isFirstWake bool) string {
    return buildWakeMessage(wakePromptInput{
        Reason:                  string(sig.Reason),
        Hint:                    sig.Hint,
        InboxUnread:             sig.Context.InboxUnread,
        SinceLastWakeSeconds:    sig.Context.SinceLastWakeSeconds,
        MasterLikelyAsleep:      sig.Context.MasterLikelyAsleep,
        QuietStart:              cfg.MindForm.QuietStart,
        QuietEnd:                cfg.MindForm.QuietEnd,
        TZ:                      cfg.MindForm.TZ,
        SinceLastDreamSeconds:   sig.Context.SinceLastDreamSeconds,
        DreamEligible:           sig.Context.DreamEligible,
        LastDreamNote:           ds.LastDreamNote,
        PlanID:                  sig.Context.PlanID,
        IsFirstWakeOfNewSession: isFirstWake,
        DreamCount:              ds.DreamCount,
        LastDreamFinishedAt:     ds.LastDreamFinishedAt,
    })
}
```

The pre-flight stat + retry comes in Task 9; this task lands the happy path.

- [ ] **Step 5: Run tests; expect PASS**

Run: `go test ./cmd/eidos/supervisor/... -v`
Expected: PASS — including the new `DecideSessionMode_*` and `StampsSessionIDOntoEntry` tests, and the existing tests (which now pass an empty sessionUUID to `runWithTranscript`).

- [ ] **Step 6: Commit**

```bash
git add cmd/eidos/supervisor/agent_runner.go cmd/eidos/supervisor/agent_runner_test.go cmd/eidos/supervisor/agent_runner_stream_test.go
git commit -m "feat(supervisor): mint or resume Claude session per wake"
```

---

### Task 9: Pre-flight stat + retry on missing jsonl

**Files:**
- Modify: `cmd/eidos/supervisor/agent_runner.go`
- Test: `cmd/eidos/supervisor/agent_runner_test.go` (extend with helper unit tests)

- [ ] **Step 1: Write the failing tests for the path helpers**

Append to `cmd/eidos/supervisor/agent_runner_test.go`:

```go
func TestEncodeCWD(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/eidos/ontology", "-eidos-ontology"},
		{"abc123", "abc123"},
		{"/path/with-dash", "-path-with-dash"},
	}
	for _, c := range cases {
		if got := encodeCWD(c.in); got != c.want {
			t.Errorf("encodeCWD(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSessionJsonlPath(t *testing.T) {
	got := sessionJsonlPath("/eidos/ontology", "abc-uuid")
	want := "/eidos/ontology/.claude/projects/-eidos-ontology/abc-uuid.jsonl"
	if got != want {
		t.Errorf("sessionJsonlPath: got %q, want %q", got, want)
	}
	if got := sessionJsonlPath("", "abc-uuid"); got != "" {
		t.Errorf("sessionJsonlPath with empty ontology: got %q, want empty", got)
	}
}
```

- [ ] **Step 2: Run the tests; expect FAIL**

Run: `go test ./cmd/eidos/supervisor/... -run "EncodeCWD|SessionJsonlPath" -v`
Expected: FAIL — helpers undefined.

- [ ] **Step 3: Add the helpers + pre-flight integration in runAgent**

In `cmd/eidos/supervisor/agent_runner.go`, add the helpers below `buildClaudeArgs`:

```go
// encodeCWD replaces every non-alphanumeric char with '-', per Claude
// Code's documented session-storage scheme. Idempotent. Documented at
// /docs/en/agent-sdk/sessions.
func encodeCWD(cwd string) string {
	var b strings.Builder
	b.Grow(len(cwd))
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// sessionJsonlPath is the absolute path Claude Code uses for a session
// jsonl: <CLAUDE_DIR>/projects/<encoded-cwd>/<uuid>.jsonl with
// CLAUDE_DIR = <ontologyDir>/.claude (matches the env we export for
// the claude subprocess). Returns "" when ontologyDir is empty.
func sessionJsonlPath(ontologyDir, uuid string) string {
	if ontologyDir == "" {
		return ""
	}
	claudeDir := filepath.Join(ontologyDir, ".claude")
	return filepath.Join(claudeDir, "projects", encodeCWD(ontologyDir), uuid+".jsonl")
}
```

In `runAgent`, after the `decideSessionMode` block (and before invoking claude), add the pre-flight check on the resume path:

```go
if !isFirstWake {
    jsonl := sessionJsonlPath(ontologyDir, sessionUUID)
    if _, statErr := os.Stat(jsonl); errors.Is(statErr, fs.ErrNotExist) {
        log.Printf("agent-runner: session jsonl for %s missing; resetting and retrying as new session", sessionUUID)
        if cErr := sessionstate.Clear(sessionStatePath); cErr != nil {
            log.Printf("agent-runner: session-state clear before mint: %v", cErr)
        }
        fresh, mErr := sessionstate.Mint(sessionStatePath, time.Now())
        if mErr != nil {
            return fmt.Errorf("session-state mint after fallback: %w", mErr)
        }
        sessionUUID = fresh.SessionID
        isFirstWake = true
        mode = SessionMode{Kind: SessionNew, UUID: sessionUUID}
        msg = rebuildFirstWakeMessage(sig, cfg, ds, true)
        args = buildClaudeArgs(string(identity), msg, gateConfigPath, streamJSON, mode)
    }
}
```

- [ ] **Step 4: Run the tests; expect PASS**

Run: `go test ./cmd/eidos/supervisor/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/supervisor/agent_runner.go cmd/eidos/supervisor/agent_runner_test.go
git commit -m "feat(supervisor): pre-flight stat + reset on missing session jsonl"
```

---

### Task 10: Stderr-based session-not-found retry

**Files:**
- Modify: `cmd/eidos/supervisor/agent_runner.go`
- Test: `cmd/eidos/supervisor/agent_runner_stream_test.go` (extend)

- [ ] **Step 1: Write the failing test for the stderr matcher**

Append to `cmd/eidos/supervisor/agent_runner_test.go`:

```go
func TestMatchSessionNotFound(t *testing.T) {
	cases := []struct {
		stderr string
		want   bool
	}{
		{"Error: session not found\n", true},
		{"Could not find session abc-123\n", true},
		{"no such session\n", true},
		{"some other error\n", false},
		{"", false},
	}
	for _, c := range cases {
		if got := matchSessionNotFound(c.stderr); got != c.want {
			t.Errorf("matchSessionNotFound(%q) = %v, want %v", c.stderr, got, c.want)
		}
	}
}
```

Append to `cmd/eidos/supervisor/agent_runner_stream_test.go`:

```go
func TestRunWithTranscript_ReturnsSessionNotFoundOnStderr(t *testing.T) {
	streamFixture(t, `echo "Error: session not found" 1>&2
exit 1`)
	sig := wake.Signal{V: 1, ID: "snf-wake-1", Reason: wake.ReasonHeartBeat, TriggeredAt: 1}
	err := runWithTranscript(sig, t.TempDir(), []string{"-p", "ignored"}, "some-uuid")
	if err == nil {
		t.Fatal("expected error from stub exit 1; got nil")
	}
	if !errors.Is(err, errSessionNotFound) {
		t.Fatalf("expected errSessionNotFound; got %v", err)
	}
}
```

- [ ] **Step 2: Run the tests; expect FAIL**

Run: `go test ./cmd/eidos/supervisor/... -run "MatchSessionNotFound|SessionNotFoundOnStderr" -v`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement stderr capture + sentinel error + retry**

In `cmd/eidos/supervisor/agent_runner.go`, add:

```go
// errSessionNotFound is returned by runWithTranscript / runWithoutTranscript
// when claude exited non-zero and stderr indicated the resumed session is
// missing or unreadable. runAgent catches this once and retries with a
// fresh session UUID.
var errSessionNotFound = errors.New("claude reports session not found")

// matchSessionNotFound is a case-insensitive substring scan over claude's
// stderr for any marker we know it uses to signal a missing / unreadable
// session file. Tolerant on additions — substring match.
func matchSessionNotFound(stderr string) bool {
	low := strings.ToLower(stderr)
	for _, m := range []string{"session not found", "could not find session", "no such session"} {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}
```

In `runWithTranscript` (find the `c.Stderr = os.Stderr` line near where `exec.Command` is built), replace with:

```go
stderrBuf := &strings.Builder{}
c.Stderr = io.MultiWriter(os.Stderr, stderrBuf)
```

After `cmd.Wait()` returns and you check the exit error, before the existing `handleClaudeExit` fall-through, add:

```go
if werr != nil {
    if matchSessionNotFound(stderrBuf.String()) {
        return errors.Join(errSessionNotFound, werr)
    }
    // ... existing handleClaudeExit path ...
}
```

Apply the same change to `runWithoutTranscript` (it's a much shorter function — wire stderr to a tee and check on exit error).

In `runAgent`, after the first run completes, wrap the runErr handling:

```go
if runErr != nil {
    if errors.Is(runErr, errSessionNotFound) && !isFirstWake {
        log.Printf("agent-runner: claude reports session %s not found; resetting and retrying", sessionUUID)
        if cErr := sessionstate.Clear(sessionStatePath); cErr != nil {
            log.Printf("agent-runner: session-state clear after stderr fallback: %v", cErr)
        }
        fresh, mErr := sessionstate.Mint(sessionStatePath, time.Now())
        if mErr != nil {
            return fmt.Errorf("session-state mint after stderr-fallback: %w", mErr)
        }
        sessionUUID = fresh.SessionID
        isFirstWake = true
        mode = SessionMode{Kind: SessionNew, UUID: sessionUUID}
        msg = rebuildFirstWakeMessage(sig, cfg, ds, true)
        args = buildClaudeArgs(string(identity), msg, gateConfigPath, streamJSON, mode)
        if streamJSON {
            runErr = runWithTranscript(sig, ontologyDir, args, sessionUUID)
        } else {
            runErr = runWithoutTranscript(ontologyDir, args)
        }
    }
    if runErr != nil {
        return runErr
    }
}
```

- [ ] **Step 4: Run the tests; expect PASS**

Run: `go test ./cmd/eidos/supervisor/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/supervisor/agent_runner.go cmd/eidos/supervisor/agent_runner_test.go cmd/eidos/supervisor/agent_runner_stream_test.go
git commit -m "feat(supervisor): retry once with fresh session on claude session-not-found"
```

---

### Task 11: `dreamEndEcho` clears `sessionstate`

**Files:**
- Modify: `cmd/eidos/forge/dream.go`
- Test: `cmd/eidos/forge/dream_test.go` (extend)

- [ ] **Step 1: Write the failing test**

Append to `cmd/eidos/forge/dream_test.go`:

```go
import "github.com/LucianoXu/eidopsyche/internal/sessionstate"

func TestDreamEnd_ClearsSessionState(t *testing.T) {
	dir := t.TempDir()
	dreamPath := filepath.Join(dir, "dream-state.json")
	sessPath := filepath.Join(dir, "session.json")

	// Override package-level paths.
	prevDream := dreamStatePath
	prevSess := sessionStateForgePath
	dreamStatePath = dreamPath
	sessionStateForgePath = sessPath
	t.Cleanup(func() {
		dreamStatePath = prevDream
		sessionStateForgePath = prevSess
	})

	// Seed an active session.
	if _, err := sessionstate.Mint(sessPath, time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}

	if _, err := dreamEndEcho(dreamPath, "consolidated test memory", ""); err != nil {
		t.Fatalf("dreamEndEcho: %v", err)
	}

	if _, err := os.Stat(sessPath); !os.IsNotExist(err) {
		t.Fatalf("session.json should be cleared after dream-end; stat err=%v", err)
	}
}
```

- [ ] **Step 2: Run the test; expect FAIL**

Run: `go test ./cmd/eidos/forge/... -run DreamEnd_ClearsSessionState -v`
Expected: FAIL — `sessionStateForgePath` undefined; `dreamEndEcho` doesn't touch session-state.

- [ ] **Step 3: Wire `dreamEndEcho` to clear session-state**

Modify `cmd/eidos/forge/dream.go`. Add a package-level var below `dreamStatePath`:

```go
// sessionStateForgePath is the in-container session.json path. The
// dream-end command clears it so the next wake starts a fresh session.
// var (not const) so tests can substitute a temp file.
var sessionStateForgePath = "/eidos/run/session.json"
```

Add an import for `sessionstate`:

```go
import (
    // ... existing imports ...
    "github.com/LucianoXu/eidopsyche/internal/sessionstate"
)
```

Modify `dreamEndEcho`:

```go
func dreamEndEcho(path, note, prosePath string) (string, error) {
    prev, _ := dreamstate.Read(path)
    now := time.Now()
    if err := dreamstate.End(path, now, note, prosePath); err != nil {
        return "", err
    }
    // Clear the session-state so the next wake starts fresh. Failure
    // here is not fatal: the next wake's fallback rule
    // (LastDreamFinishedAt > SessionStartedAt) catches a stale
    // session.json automatically.
    if err := sessionstate.Clear(sessionStateForgePath); err != nil {
        log.Printf("dream end: sessionstate.Clear failed (%v); next wake will reset via fallback", err)
    }
    st, _ := dreamstate.Read(path)
    if prev.LastDreamStartedAt > 0 {
        dur := now.Sub(time.Unix(prev.LastDreamStartedAt, 0)).Truncate(time.Second)
        return fmt.Sprintf("dream ended after %s — recorded as #%d\n",
            formatDuration(dur), st.DreamCount), nil
    }
    return fmt.Sprintf("dream ended (no prior begin) — recorded as #%d\n", st.DreamCount), nil
}
```

If `log` is not yet imported in this file, add it.

- [ ] **Step 4: Run the tests; expect PASS**

Run: `go test ./cmd/eidos/forge/... -run Dream -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/forge/dream.go cmd/eidos/forge/dream_test.go
git commit -m "feat(forge): clear session-state on dream-end"
```

---

### Task 12: `runtime-state` emits session fields

**Files:**
- Modify: `cmd/eidos/forge/runtime_state.go`
- Test: `cmd/eidos/forge/runtime_state_test.go` (extend)

- [ ] **Step 1: Write the failing tests**

Append to `cmd/eidos/forge/runtime_state_test.go`:

```go
import (
    "github.com/LucianoXu/eidopsyche/internal/sessionstate"
    "time"
)

func TestComputeRuntimeState_SessionFieldsPresent(t *testing.T) {
	dir := t.TempDir()
	prev := sessionStateRuntimePath
	sessionStateRuntimePath = filepath.Join(dir, "session.json")
	t.Cleanup(func() { sessionStateRuntimePath = prev })

	st, err := sessionstate.Mint(sessionStateRuntimePath, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	_ = sessionstate.IncrementWake(sessionStateRuntimePath)

	rs := computeRuntimeState(time.Unix(1700001000, 0))
	if rs.SessionID != st.SessionID {
		t.Fatalf("SessionID = %q, want %q", rs.SessionID, st.SessionID)
	}
	if rs.SessionStartedAt != 1700000000 {
		t.Fatalf("SessionStartedAt = %d", rs.SessionStartedAt)
	}
	if rs.WakesInSession != 1 {
		t.Fatalf("WakesInSession = %d", rs.WakesInSession)
	}
}

func TestComputeRuntimeState_SessionFieldsOmittedWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	prev := sessionStateRuntimePath
	sessionStateRuntimePath = filepath.Join(dir, "session.json")
	t.Cleanup(func() { sessionStateRuntimePath = prev })

	rs := computeRuntimeState(time.Unix(1700000000, 0))
	if rs.SessionID != "" || rs.SessionStartedAt != 0 || rs.WakesInSession != 0 {
		t.Fatalf("expected zero session fields when session.json absent; got %+v", rs)
	}
}
```

- [ ] **Step 2: Run the tests; expect FAIL**

Run: `go test ./cmd/eidos/forge/... -run RuntimeState_Session -v`
Expected: FAIL — `sessionStateRuntimePath` undefined; struct missing fields.

- [ ] **Step 3: Add fields and read session.json**

Modify `cmd/eidos/forge/runtime_state.go`:

Add the new var alongside the existing `wakeDir`/`authStatePath` block (around line 20):

```go
var (
    wakeDir                = "/eidos/run/wake"
    authStatePath          = authstate.Path
    procStatPath           = "/proc/1/stat"
    procBootTimePath       = "/proc/stat"
    sessionStateRuntimePath = "/eidos/run/session.json"
)
```

Add fields to the `RuntimeState` struct:

```go
type RuntimeState struct {
    V                       int    `json:"v"`
    Phase                   string `json:"phase"`
    WakeReason              string `json:"wake_reason,omitempty"`
    ActiveWakeID            string `json:"active_wake_id,omitempty"`
    Dreaming                bool   `json:"dreaming"`
    AuthRequired            bool   `json:"auth_required"`
    SincePhaseChangeSeconds *int64 `json:"since_phase_change_seconds,omitempty"`
    ContainerStartedAt      int64  `json:"container_started_at"`

    // Session fields, omitted when session.json is absent.
    SessionID        string `json:"session_id,omitempty"`
    SessionStartedAt int64  `json:"session_started_at,omitempty"`
    WakesInSession   int    `json:"wakes_in_session,omitempty"`
}
```

Add an import for `sessionstate`:

```go
import (
    // ... existing imports ...
    "github.com/LucianoXu/eidopsyche/internal/sessionstate"
)
```

Extend `computeRuntimeState` to read session.json:

```go
// At the end of computeRuntimeState, before "return rs":
if sess, err := sessionstate.Read(sessionStateRuntimePath); err == nil && sess.SessionID != "" {
    rs.SessionID = sess.SessionID
    rs.SessionStartedAt = sess.SessionStartedAt
    rs.WakesInSession = sess.WakesInSession
}
```

- [ ] **Step 4: Run tests; expect PASS**

Run: `go test ./cmd/eidos/forge/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/forge/runtime_state.go cmd/eidos/forge/runtime_state_test.go
git commit -m "feat(forge): surface session info in runtime-state"
```

---

### Task 13: `forge status` renders session line

**Files:**
- Modify: `cmd/eidos/forge/status.go`
- Test: `cmd/eidos/forge/status_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/eidos/forge/status_test.go`:

```go
func TestStatusAwake_WithSessionFields(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":1,"phase":"awake","wake_reason":"heartbeat","active_wake_id":"abc","dreaming":false,"auth_required":false,"container_started_at":1700000000,"session_id":"7d2f6f2e-1b2a-4c3d-9e8f-aabbccddeeff","session_started_at":1700000000,"wakes_in_session":47}`)},
			"whoami":        {Stdout: []byte("npub: npub1mfx\n")},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "session: 7d2f6f2e") {
		t.Errorf("missing session line: %q", out)
	}
	if !strings.Contains(out, "47 wakes") {
		t.Errorf("missing wake count: %q", out)
	}
}

func TestStatusSleeping_NoSessionLineWhenAbsent(t *testing.T) {
	f := &statusFake{
		state: "running",
		execResponses: map[string]forgectl.ExecResult{
			"runtime-state": {Stdout: []byte(`{"v":1,"phase":"sleeping","dreaming":false,"auth_required":false,"container_started_at":0}`)},
		},
	}
	out, err := computeStatus(context.Background(), f, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "session:") {
		t.Errorf("session line should be omitted when absent: %q", out)
	}
}
```

- [ ] **Step 2: Run the test; expect FAIL**

Run: `go test ./cmd/eidos/forge/... -run Status_WithSession -v`
Expected: FAIL — the session line isn't being emitted.

- [ ] **Step 3: Render the session line in `computeStatus`**

Modify `cmd/eidos/forge/status.go`. The existing function emits `phase:` via `fetchRuntimeState` then writes whoami / status-detail. Insert the session line right after the phase line. Replace the existing `if rs, ok := fetchRuntimeState(...)` block with:

```go
if rs, ok := fetchRuntimeState(ctx, c, cont); ok {
    fmt.Fprintf(&sb, "phase:   %s\n", formatPhase(rs))
    if rs.SessionID != "" {
        short := rs.SessionID
        if len(short) > 8 {
            short = short[:8]
        }
        age := time.Since(time.Unix(rs.SessionStartedAt, 0)).Truncate(time.Second)
        fmt.Fprintf(&sb, "session: %s (age %s, %d wakes)\n", short, age, rs.WakesInSession)
    }
} else {
    fmt.Fprintf(&sb, "state:   %s\n", state)
}
```

Add `time` to the imports if not already present.

- [ ] **Step 4: Run the tests; expect PASS**

Run: `go test ./cmd/eidos/forge/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/forge/status.go cmd/eidos/forge/status_test.go
git commit -m "feat(forge): show session info in `forge status`"
```

---

### Task 14: `transcript-list` SESSION column

**Files:**
- Modify: `cmd/eidos/forge/transcript_list.go`
- Modify: `cmd/eidos/forge/watch_render.go` (the `renderListTableForWatch` helper)
- Test: `cmd/eidos/forge/transcript_list_test.go` and/or `watch_render_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/eidos/forge/watch_render_test.go`:

```go
func TestRenderListTableForWatch_HasSessionColumn(t *testing.T) {
	idx := transcript.Index{V: 1, Wakes: []transcript.Entry{
		{ID: "abc12345", SessionID: "7d2f6f2e1b2a4c3d", Reason: "heartbeat", StartedAt: 1700000000, EndedAt: 1700000010, OK: true, ExitCode: 0},
		{ID: "abc12344", SessionID: "", Reason: "heartbeat", StartedAt: 1699999000, EndedAt: 1699999005, OK: true, ExitCode: 0},
	}}
	lines := renderListTableForWatch(idx, 0)
	if len(lines) < 2 {
		t.Fatalf("expected at least header + one row; got %v", lines)
	}
	if !strings.Contains(lines[0], "SESSION") {
		t.Fatalf("header missing SESSION column: %q", lines[0])
	}
	if !strings.Contains(lines[1], "7d2f6f2e") {
		t.Fatalf("row 1 should contain session prefix; got %q", lines[1])
	}
	if !strings.Contains(lines[2], "-") {
		t.Fatalf("row 2 (legacy entry) should render `-` in SESSION; got %q", lines[2])
	}
}
```

- [ ] **Step 2: Run the test; expect FAIL**

Run: `go test ./cmd/eidos/forge/... -run List -v`
Expected: FAIL.

- [ ] **Step 3: Add the SESSION column**

Modify `cmd/eidos/forge/transcript_list.go` — `renderListTable`:

```go
func renderListTable(cmd *cobra.Command, idx transcript.Index) error {
    out := cmd.OutOrStdout()
    if len(idx.Wakes) == 0 {
        fmt.Fprintln(out, "no wakes recorded yet")
        return nil
    }
    fmt.Fprintln(out, "ID         SESSION    REASON      STARTED              DUR    COST      STATUS")
    for _, w := range idx.Wakes {
        id := w.ID
        if len(id) > 8 {
            id = id[:8]
        }
        sess := "-"
        if w.SessionID != "" {
            sess = w.SessionID
            if len(sess) > 8 {
                sess = sess[:8]
            }
        }
        started := time.Unix(w.StartedAt, 0).UTC().Format("2006-01-02 15:04:05")
        dur := "-"
        if w.EndedAt > w.StartedAt {
            dur = (time.Duration(w.EndedAt-w.StartedAt) * time.Second).Truncate(time.Second).String()
        }
        cost := "-"
        if w.CostUSD != nil && *w.CostUSD > 0 {
            cost = fmt.Sprintf("$%.4f", *w.CostUSD)
        }
        status := "ok"
        if !w.OK {
            status = "crashed"
            if w.ExitCode != -1 && w.ExitCode != 0 {
                status = fmt.Sprintf("failed(%d)", w.ExitCode)
            }
        }
        fmt.Fprintf(out, "%-10s %-10s %-11s %-20s %-6s %-9s %s\n",
            id, sess, w.Reason, started, dur, cost, status)
    }
    return nil
}
```

Mirror the same change in `watch_render.go`'s `renderListTableForWatch`:

```go
func renderListTableForWatch(idx transcript.Index, limit int) []string {
    wakes := idx.Wakes
    if limit > 0 && len(wakes) > limit {
        wakes = wakes[:limit]
    }
    if len(wakes) == 0 {
        return []string{"no wakes recorded yet"}
    }
    out := []string{"ID         SESSION    REASON      STARTED              DUR    COST      STATUS"}
    for _, w := range wakes {
        id := w.ID
        if len(id) > 8 {
            id = id[:8]
        }
        sess := "-"
        if w.SessionID != "" {
            sess = w.SessionID
            if len(sess) > 8 {
                sess = sess[:8]
            }
        }
        started := unixToISOZ(w.StartedAt)
        dur := "-"
        if w.EndedAt > w.StartedAt {
            dur = fmt.Sprintf("%ds", w.EndedAt-w.StartedAt)
        }
        cost := "-"
        if w.CostUSD != nil && *w.CostUSD > 0 {
            cost = fmt.Sprintf("$%.4f", *w.CostUSD)
        }
        status := "ok"
        if !w.OK {
            if w.ExitCode > 0 {
                status = fmt.Sprintf("failed(%d)", w.ExitCode)
            } else {
                status = "crashed"
            }
        }
        out = append(out, fmt.Sprintf("%-10s %-10s %-11s %-20s %-6s %-9s %s",
            id, sess, w.Reason, started, dur, cost, status))
    }
    return out
}
```

- [ ] **Step 4: Run the tests; expect PASS**

Run: `go test ./cmd/eidos/forge/... -v`
Expected: PASS — including any existing list tests that may need their expected-output strings updated to include the SESSION column.

If pre-existing tests break because they assert exact column header text, update those expected strings.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/forge/transcript_list.go cmd/eidos/forge/watch_render.go cmd/eidos/forge/watch_render_test.go cmd/eidos/forge/transcript_list_test.go
git commit -m "feat(forge): add SESSION column to wake list"
```

---

### Task 15: Per-wake header with session ordinal

**Files:**
- Modify: `cmd/eidos/forge/watch_render.go`
- Test: `cmd/eidos/forge/watch_render_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/eidos/forge/watch_render_test.go`:

```go
func TestRenderSystem_WithSessionOrdinal(t *testing.T) {
	ev := transcript.Event{Type: transcript.TypeSystem, Subtype: "init", Model: "claude-sonnet-4-6"}
	ctx := wakeRenderCtx{
		WakeID:    "abc12345",
		SessionID: "7d2f6f2e",
		Ordinal:   47,
	}
	lines := renderSystemWithCtx(ev, ctx)
	if len(lines) == 0 || !strings.Contains(lines[0], "wake abc12345") {
		t.Fatalf("expected wake-id in header; got %q", lines)
	}
	if !strings.Contains(lines[0], "(47 of session 7d2f6f2e)") {
		t.Fatalf("expected ordinal + session in header; got %q", lines)
	}
}

func TestRenderSystem_FallsBackWhenNoSession(t *testing.T) {
	ev := transcript.Event{Type: transcript.TypeSystem, Subtype: "init", Model: "claude-sonnet-4-6"}
	lines := renderSystemWithCtx(ev, wakeRenderCtx{}) // empty ctx
	if !strings.Contains(lines[0], "wake started") {
		t.Fatalf("expected legacy header when no session ctx; got %q", lines)
	}
}
```

- [ ] **Step 2: Run the test; expect FAIL**

Run: `go test ./cmd/eidos/forge/... -run RenderSystem -v`
Expected: FAIL.

- [ ] **Step 3: Add the per-wake context**

Modify `cmd/eidos/forge/watch_render.go`:

```go
// wakeRenderCtx carries per-wake metadata that the renderer needs to
// stamp on the wake's header line. Empty zero-value falls back to the
// legacy header.
type wakeRenderCtx struct {
    WakeID    string
    SessionID string // 8-char prefix (or empty for legacy wakes)
    Ordinal   int    // 1-based count within the session (0 = unknown)
}

func renderSystemWithCtx(ev transcript.Event, ctx wakeRenderCtx) []string {
    if ev.Subtype != "init" {
        return nil
    }
    header := fmt.Sprintf("━━━ wake started · model=%s ━━━", strDefault(ev.Model, "?"))
    if ctx.WakeID != "" && ctx.SessionID != "" && ctx.Ordinal > 0 {
        header = fmt.Sprintf("━━━ wake %s (%d of session %s) · model=%s ━━━",
            ctx.WakeID, ctx.Ordinal, ctx.SessionID, strDefault(ev.Model, "?"))
    }
    parts := []string{header}
    subline := "    tools: "
    if len(ev.Tools) == 0 {
        subline += "(none)"
    } else {
        subline += strings.Join(ev.Tools, " ")
    }
    if len(ev.MCPServers) > 0 {
        names := make([]string, len(ev.MCPServers))
        for i, m := range ev.MCPServers {
            names[i] = m.Name
        }
        subline += " · MCP: " + strings.Join(names, ",")
    }
    return append(parts, subline)
}
```

Keep the existing `renderSystem(ev)` as a thin wrapper for callers that don't have per-wake context yet (so old callers compile). Eventually replace those callers; they're in `renderEvent`.

In `renderEvent`, change the `case transcript.TypeSystem:` to take the context. This requires extending `renderEvent`'s signature:

```go
func renderEvent(ev transcript.Event, opts renderOpts, ctx wakeRenderCtx) []string {
    switch ev.Type {
    case transcript.TypeSystem:
        return renderSystemWithCtx(ev, ctx)
    case transcript.TypeAssistant:
        return renderAssistant(ev, opts)
    case transcript.TypeUser:
        return renderUserToolResult(ev)
    case transcript.TypeResult:
        return renderResult(ev)
    }
    return nil
}
```

Update the call site in `watch.go`'s `renderStream`:

```go
func renderStream(out io.Writer, src io.Reader, raw bool, opts renderOpts, ctx wakeRenderCtx) error {
    // ... in the inner loop, replace renderEvent(ev, opts) with:
    for _, l := range renderEvent(ev, opts, ctx) {
        fmt.Fprintln(out, l)
    }
}
```

And the `streamTranscript` caller must thread `ctx` through:

```go
func streamTranscript(ctx context.Context, out io.Writer, c forgectl.Client, cont string, args []string, raw bool, opts renderOpts, wctx wakeRenderCtx) (emitted bool, err error) {
    // ... pass wctx into renderStream ...
}
```

`runWatchTail` is responsible for building the `wakeRenderCtx`. For each wake it tails, it should:
1. Resolve the wake's index entry (ContainerExec `transcript-list --json`).
2. Compute ordinal = number of entries with the same SessionID and StartedAt ≤ this wake's StartedAt.
3. Build `wakeRenderCtx{WakeID: w.ID[:8], SessionID: w.SessionID[:8], Ordinal: ord}`.

Add a small helper `lookupWakeRenderCtx(ctx, c, cont, wakeID) wakeRenderCtx` that does this. When the lookup fails or the wake has no SessionID, return zero-value `wakeRenderCtx` so the legacy header path kicks in.

- [ ] **Step 4: Run the tests; expect PASS**

Run: `go test ./cmd/eidos/forge/... -v`
Expected: PASS — including any existing watch_render tests, which may need their expected strings updated if they asserted exact "wake started" header text.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/forge/watch_render.go cmd/eidos/forge/watch.go cmd/eidos/forge/watch_render_test.go
git commit -m "feat(forge): show session ordinal in per-wake watch header"
```

---

### Task 16: Session boundary in follow mode

**Files:**
- Modify: `cmd/eidos/forge/watch.go` (the `runWatchTail` follow loop)
- Test: `cmd/eidos/forge/watch_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/eidos/forge/watch_test.go` (or `watch_render_test.go`):

```go
func TestRenderSessionBoundary(t *testing.T) {
	lines := renderSessionBoundary("1a3b8821")
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "═") {
		t.Fatalf("boundary should use box drawing: %q", got)
	}
	if !strings.Contains(got, "1a3b8821") {
		t.Fatalf("boundary missing new session id: %q", got)
	}
}

func TestRenderSessionBoundary_EmptyOrSameSessionRendersNothing(t *testing.T) {
	if got := shouldRenderBoundary("", "1a3b8821"); got {
		t.Fatalf("no boundary when previous SessionID is empty")
	}
	if got := shouldRenderBoundary("7d2f6f2e", "7d2f6f2e"); got {
		t.Fatalf("no boundary when SessionID unchanged")
	}
	if got := shouldRenderBoundary("7d2f6f2e", ""); got {
		t.Fatalf("no boundary when next SessionID is empty (legacy)")
	}
	if got := shouldRenderBoundary("7d2f6f2e", "1a3b8821"); !got {
		t.Fatalf("boundary expected for differing SessionIDs")
	}
}
```

- [ ] **Step 2: Run the tests; expect FAIL**

Run: `go test ./cmd/eidos/forge/... -run Boundary -v`
Expected: FAIL.

- [ ] **Step 3: Add the helpers and integrate**

In `cmd/eidos/forge/watch_render.go` (or a new `watch_session.go`), add:

```go
// shouldRenderBoundary reports whether a session-boundary separator
// should be drawn between two consecutive wakes' SessionIDs.
func shouldRenderBoundary(prev, next string) bool {
    if prev == "" || next == "" {
        return false
    }
    return prev != next
}

// renderSessionBoundary returns the lines for a session-boundary
// separator. Bare format — only the new session's short UUID, no dream
// note.
func renderSessionBoundary(nextSessionID string) []string {
    return []string{
        "",
        "═══════════════════════════════════════════════════",
        "   New session " + nextSessionID,
        "═══════════════════════════════════════════════════",
        "",
    }
}
```

In `cmd/eidos/forge/watch.go`'s `runWatchTail`, track the previous wake's `SessionID`. After fetching the per-wake context for a new wake but before streaming it, render the boundary if `shouldRenderBoundary(prev, ctx.SessionID)`.

Concretely, around the existing follow-loop iteration that resolves the next wake to render:

```go
// Inside runWatchTail's loop, after computing wctx for the new wake:
if shouldRenderBoundary(prevSessionID, wctx.SessionID) {
    for _, l := range renderSessionBoundary(wctx.SessionID) {
        fmt.Fprintln(out, l)
    }
}
prevSessionID = wctx.SessionID
```

Initialize `prevSessionID` to `""` at the top of the function so the first wake never renders a boundary.

- [ ] **Step 4: Run the tests; expect PASS**

Run: `go test ./cmd/eidos/forge/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/eidos/forge/watch.go cmd/eidos/forge/watch_render.go cmd/eidos/forge/watch_test.go
git commit -m "feat(forge): render session boundary in watch follow mode"
```

---

### Task 17: Documentation updates

**Files:**
- Modify: `SPEC.md`
- Modify: `EXAMPLE.md`

- [ ] **Step 1: Add a "Wake context lifecycle" section to SPEC.md**

Open `SPEC.md`. Find the existing section about wakes / dreams (search for `dream` or `wake`). Add a new subsection:

````markdown
### Wake context lifecycle

Each Claude invocation for a mind-form runs in a **session** — the span of wakes between two consecutive `dream-end` calls. Within a session, every wake resumes the previous Claude conversation (via `claude --resume <UUID>`), so the model's working memory carries forward. The first wake after `dream-end` (or on a fresh ontology) starts a new session via `claude --session-id <new-UUID>`.

Why two layers:

- The **session jsonl** (Claude's own conversation log at `<ontology>/.claude/projects/<encoded-cwd>/<UUID>.jsonl`) is what gives the model continuity of thought.
- The **on-disk substrate** (`memory/`, `journal/`, `essence/`, `inbox/`) is what gives the mind-form continuity of identity across sessions and survives container churn or session corruption.

A dream is the explicit moment of memory consolidation: the mind-form writes anything worth keeping into `memory/` (or other on-disk locations) before calling `dream end`. The next wake then starts fresh — the model has no working memory of the prior session, only what it explicitly wrote to disk.

Operator-facing surfaces:

- `eidos forge status <name>` shows the active session UUID, age, and accumulated wake count.
- `eidos forge watch <name> --list` shows a SESSION column so session boundaries are visible at a glance.
- `eidos forge watch <name>` (follow mode) renders a separator line between sessions, and stamps each wake's header with `(N of session <prefix>)`.

Failure modes (transparent to the operator):

- Missing or corrupt `session.json` → `agent-runner` mints a new session next wake.
- Missing session jsonl on disk despite `session.json` claiming a UUID → `agent-runner` falls back to a fresh session, logs a warning.
- `dream end` succeeds but session-state clear fails → next wake's fallback rule (`LastDreamFinishedAt > SessionStartedAt`) catches it.
````

- [ ] **Step 2: Update EXAMPLE.md**

Open `EXAMPLE.md`. Find the section that walks through dream-end (or, if absent, the section on wake lifecycle). Add one paragraph:

```markdown
After `eidos forge dream end`, the next wake starts a fresh Claude session: the model's working memory from the prior session is gone, so anything the mind-form wants to remember must live on disk (`memory/`, `journal/`, `essence/`). The session UUID changes; you can verify this with `eidos forge status <name>` (the `session:` line) or `eidos forge watch <name>` (which renders a boundary separator).
```

- [ ] **Step 3: Commit docs**

```bash
git add SPEC.md EXAMPLE.md
git commit -m "docs: explain dream-bounded session lifecycle"
```

---

### Task 18: Lint, vet, full test pass, manual smoke

**Files:** none (verification only)

- [ ] **Step 1: gofmt + go vet**

Run from worktree root:

```bash
gofmt -l . && go vet ./...
```

Expected: no output from `gofmt`, no findings from `go vet`. If `gofmt` lists files, run `gofmt -w <file>` and re-commit.

- [ ] **Step 2: Full unit test run**

```bash
go test ./...
```

Expected: PASS for every package.

- [ ] **Step 3: Race-detector run on the new package and supervisor**

```bash
go test -race ./internal/sessionstate/... ./cmd/eidos/supervisor/...
```

Expected: PASS, no races.

- [ ] **Step 4: Build the binary to confirm no compile-time regressions**

```bash
go build -o bin/eidos ./cmd/eidos
```

Expected: success.

- [ ] **Step 5: Manual smoke (optional but recommended)**

If a docker daemon is available, run the deploy-test or a minimal local instance:

```bash
./bin/eidos forge create test
./bin/eidos forge start test
./bin/eidos forge logs test &
./bin/eidos forge status test
# Wait for two heartbeats:
docker exec eidos-test cat /eidos/run/session.json
# UUID should match across both heartbeats.
./bin/eidos forge watch test --list
# Should see SESSION column.
```

Tear down with `./bin/eidos forge purge test` when done.

- [ ] **Step 6: Push the branch**

```bash
git push -u origin feat/persistent-wake-context
```

- [ ] **Step 7: Open the PR**

```bash
gh pr create --title "feat(supervisor): persistent wake context across wakes (dream-bounded sessions)" --body "$(cat <<'EOF'
## Summary
- Each wake resumes the previous Claude session within a "session" bounded by `dream-end`.
- New `internal/sessionstate/` package owns `/eidos/run/session.json`.
- Surface session info in `forge status`, `forge watch --list`, and the streamed wake renderer.
- Fall back silently to a fresh session (with a warning) when resume cannot find the jsonl or claude reports session-not-found.

Spec: `docs/superpowers/specs/2026-05-10-persistent-wake-context-design.md`

## Test plan
- [ ] `go test ./...` passes
- [ ] `go test -race ./internal/sessionstate/... ./cmd/eidos/supervisor/...` passes
- [ ] `gofmt -l . && go vet ./...` clean
- [ ] (optional) Manual smoke: two heartbeats share a session UUID; dream-end clears it; next heartbeat mints a new one
- [ ] `forge status` shows the `session:` line
- [ ] `forge watch --list` shows the SESSION column

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Print the PR URL when `gh pr create` returns it.

- [ ] **Step 8: Codex review**

Run from the worktree root:

```bash
codex review
```

(Or whatever the project-standard invocation is — see `CLAUDE.md` "Commit & Pull Request Guidelines" step 2.)

Address any findings as follow-up commits on the same branch. Push, then verify CI on the PR via `gh pr checks <PR-number>`.

- [ ] **Step 9: Watch CI**

```bash
gh pr checks --watch
```

If CI fails, read the failure, fix locally, commit, push, and re-watch.

- [ ] **Step 10: Wait for Copilot's auto-review (if assigned), address comments**

```bash
gh pr view --comments
```

Address actionable comments; close out the PR loop.
