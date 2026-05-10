# HeartBeat, Plan signals, and Dream cycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Three coupled additions to MindForge v0 that give a mind-form autonomous time: a per-mindform configurable HeartBeat cadence with minute precision, agent-set plan signals that fire future wakes, and a voluntary dream cycle for memory consolidation.

**Architecture:** A new `wake.ReasonPlanned` joins the existing reason set. New `internal/scheduler` and `internal/dreamstate` packages own their respective on-disk file formats with pure filesystem operations. The supervisor gains one goroutine that polls plan files every 30s and fires wakes. The crontab is rendered from per-mindform `[heartbeat] interval` config at PID-1 startup, replacing the static `docker/mindform/crontab` file. The agent-runner extends wake-context computation with new fields derived from config and dream-state. Two new in-container `eidos forge` subcommands (`plan` and `dream`) round out the surface.

**Tech Stack:** Go 1.22+; `github.com/spf13/cobra`; `github.com/BurntSushi/toml` (existing); `github.com/fsnotify/fsnotify` (existing); standard library only for new code (no cron parser, no ULID lib).

**Spec:** `docs/superpowers/specs/2026-05-09-heartbeat-plans-dreams-design.md`

**Depends on:** MindForge v0 (`docs/superpowers/specs/2026-05-09-mindforge-v0-design.md`) and the non-root mindform image (`docs/superpowers/specs/2026-05-09-non-root-mindform-and-model-config-design.md`). Both have already shipped to main.

---

## File mapping

**Created — packages:**
- `internal/scheduler/types.go`
- `internal/scheduler/scheduler.go`
- `internal/scheduler/scheduler_test.go`
- `internal/dreamstate/dreamstate.go`
- `internal/dreamstate/dreamstate_test.go`

**Created — supervisor:**
- `cmd/eidos/supervisor/scheduler.go`
- `cmd/eidos/supervisor/scheduler_test.go`
- `cmd/eidos/supervisor/crontab.go`
- `cmd/eidos/supervisor/crontab_test.go`

**Created — forge:**
- `cmd/eidos/forge/plan.go` (in-container command implementations + host-side wrappers)
- `cmd/eidos/forge/plan_test.go`
- `cmd/eidos/forge/dream.go` (in-container only)
- `cmd/eidos/forge/dream_test.go`

**Created — tests:**
- `test/integration/forge_plan_dream_test.go` (build-tag `integration`)
- `test/integration/testdata/claude_stub.sh`
- `deploy-test/test_script_heartbeat_plans_dreams.sh`

**Modified:**
- `internal/wake/types.go` — add `ReasonPlanned`, four new `Context` fields.
- `internal/wake/wake_test.go` — extend round-trip test for new Context fields.
- `internal/config/config.go` — add `HeartbeatConfig`, extend `MindFormConfig` with quiet hours / tz / dream interval.
- `internal/config/config_test.go` — happy path + rejection cases for new keys.
- `internal/config/keys.go` (or new file `heartbeat.go`) — interval validator + cron expression mapping helper, all under `internal/config`.
- `internal/config/keys_test.go` (or new file) — table tests for the validator.
- `cmd/eidos/supervisor/run.go` — wire `scheduler.go` and crontab render into PID-1 startup.
- `cmd/eidos/supervisor/agent_runner.go` — extend wake-context computation; extend `buildWakeMessage` for new fields.
- `cmd/eidos/supervisor/agent_runner_test.go` — assert new context fields and message clauses.
- `cmd/eidos/forge/cmd.go` — register `plan` and `dream` subcommands (host + in-container).
- `cmd/eidos/forge/status.go` — extend `forge status` output with plans + dream lines.
- `cmd/eidos/forge/status_test.go` — assert new status lines render.
- `internal/ontology/template/CLAUDE.md` — add §"Heartbeat, plans, and dreams".
- `docker/mindform/Dockerfile` — drop the static crontab COPY.

**Deleted:**
- `docker/mindform/crontab` — replaced by the supervisor's runtime render.

---

## Phase ordering and dependency notes

Phases 1–4 build leaf-level pieces with no dependencies on each other and can land in any order; presented in the order an engineer would naturally read them.

Phase 5 depends on Phase 2 (scheduler) and Phase 3 (dreamstate).

Phase 6 depends on Phases 1, 2, 3, 4 (all leaf packages must exist) and is the largest phase because it wires everything into the supervisor PID-1 startup.

Phase 7 depends on Phase 5 (status command needs scheduler.List + dreamstate.Read) and Phase 6 (CLAUDE.md template lives next to template-rendering code modified in Phase 6).

Phase 8 depends on everything; this is the test-pyramid wrap-up.

Each phase ends with a commit. Within a phase, commits land at natural sub-boundaries (e.g., Phase 5 has separate commits for plan vs. dream).

---

## Phase 1 — `wake.ReasonPlanned` + Context fields

### Task 1.1: Add `ReasonPlanned` constant

**Files:**
- Modify: `internal/wake/types.go`
- Test: `internal/wake/wake_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/wake/wake_test.go`:

```go
func TestReasonPlannedConstant(t *testing.T) {
	if ReasonPlanned != "planned" {
		t.Errorf("ReasonPlanned = %q, want \"planned\"", ReasonPlanned)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/wake/... -run TestReasonPlannedConstant -v`
Expected: FAIL with `undefined: ReasonPlanned`.

- [ ] **Step 3: Add the constant**

In `internal/wake/types.go`, extend the const block:

```go
const (
	ReasonMindGate  Reason = "mindgate"
	ReasonHeartBeat Reason = "heartbeat"
	ReasonPlanned   Reason = "planned"
	ReasonManual    Reason = "manual"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/wake/... -run TestReasonPlannedConstant -v`
Expected: PASS.

### Task 1.2: Add new `Context` fields

**Files:**
- Modify: `internal/wake/types.go`
- Test: `internal/wake/wake_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/wake/wake_test.go`:

```go
func TestContextNewFieldsRoundTrip(t *testing.T) {
	in := Signal{
		Reason:      ReasonPlanned,
		TriggeredAt: 1715284800,
		Context: Context{
			InboxUnread:           0,
			SinceLastWakeSeconds:  3600,
			MasterLikelyAsleep:    true,
			SinceLastDreamSeconds: 30 * 3600,
			DreamEligible:         true,
			PlanID:                "20260509T123000Z-plan-7f2e",
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Signal
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Context.MasterLikelyAsleep {
		t.Error("MasterLikelyAsleep round-trip lost")
	}
	if out.Context.SinceLastDreamSeconds != 30*3600 {
		t.Errorf("SinceLastDreamSeconds = %d", out.Context.SinceLastDreamSeconds)
	}
	if !out.Context.DreamEligible {
		t.Error("DreamEligible round-trip lost")
	}
	if out.Context.PlanID != "20260509T123000Z-plan-7f2e" {
		t.Errorf("PlanID = %q", out.Context.PlanID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/wake/... -run TestContextNewFieldsRoundTrip -v`
Expected: FAIL with `unknown field MasterLikelyAsleep`.

- [ ] **Step 3: Extend the Context struct**

In `internal/wake/types.go`, replace the `Context` struct with:

```go
// Context is the situational snapshot the agent receives.
type Context struct {
	InboxUnread          int    `json:"inbox_unread"`
	FirstUnreadFromNpub  string `json:"first_unread_from_npub,omitempty"`
	FirstUnreadSummary   string `json:"first_unread_summary,omitempty"`
	SinceLastWakeSeconds int64  `json:"since_last_wake_seconds"`
	LastWakeReason       Reason `json:"last_wake_reason,omitempty"`
	Scheduled            bool   `json:"scheduled"`

	// Hints derived at wake time by agent-runner from
	// /eidos/gate/config.toml ([mindform] quiet_*) and
	// /eidos/run/dream-state.json. Surfaced so the agent can decide
	// whether this wake is a good moment to dream or to be quiet.
	MasterLikelyAsleep    bool   `json:"master_likely_asleep,omitempty"`
	SinceLastDreamSeconds int64  `json:"since_last_dream_seconds,omitempty"`
	DreamEligible         bool   `json:"dream_eligible,omitempty"`

	// PlanID is set only when Reason == ReasonPlanned. It identifies
	// the plan file that fired this wake; the agent can read
	// /eidos/run/plans/fired/<id>.json for the original payload.
	PlanID string `json:"plan_id,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/wake/... -run TestContextNewFieldsRoundTrip -v`
Expected: PASS.

### Task 1.3: Run all wake tests + commit

- [ ] **Step 1: Run the full wake package test**

Run: `go test ./internal/wake/...`
Expected: PASS, all tests.

- [ ] **Step 2: Verify no other call sites broke**

Run: `go build ./...`
Expected: build succeeds with no errors.

- [ ] **Step 3: gofmt and vet**

Run: `gofmt -l internal/wake && go vet ./internal/wake/...`
Expected: no output from gofmt; vet exits 0.

- [ ] **Step 4: Commit**

```bash
git add internal/wake/types.go internal/wake/wake_test.go
git commit -m "$(cat <<'EOF'
feat(wake): add ReasonPlanned and dream/quiet/plan-id context fields

Extends the wake-signal protocol so agent-runner can carry hints
the mind-form needs to decide whether to dream during a heartbeat
wake, and so a planned wake can be correlated back to its
originating plan file.

No backwards-incompatible change; new fields are omitempty and old
files round-trip cleanly through the JSON.

Refs docs/superpowers/specs/2026-05-09-heartbeat-plans-dreams-design.md
section 4.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 2 — `internal/scheduler` package

### Task 2.1: Plan type + ID generator

**Files:**
- Create: `internal/scheduler/types.go`
- Test: `internal/scheduler/scheduler_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/scheduler/scheduler_test.go`:

```go
package scheduler

import (
	"strings"
	"testing"
	"time"
)

func TestNewIDFormat(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 30, 0, 0, time.UTC)
	id := newIDAt(now)
	if !strings.HasPrefix(id, "20260509T123000Z-plan-") {
		t.Errorf("id prefix wrong: %q", id)
	}
	if len(id) != len("20260509T123000Z-plan-")+4 {
		t.Errorf("id length wrong: %d (got %q)", len(id), id)
	}
}

func TestNewIDUnique(t *testing.T) {
	now := time.Date(2026, 5, 9, 12, 30, 0, 0, time.UTC)
	seen := map[string]bool{}
	for i := 0; i < 256; i++ {
		id := newIDAt(now)
		if seen[id] {
			t.Fatalf("duplicate id at i=%d: %q", i, id)
		}
		seen[id] = true
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/scheduler/... -v`
Expected: FAIL with `undefined: newIDAt` (or "no Go files in directory").

- [ ] **Step 3: Create types and ID helper**

Create `internal/scheduler/types.go`:

```go
// Package scheduler owns the on-disk plan-file format used by the
// mind-form's supervisor to fire future wakes.
//
// Plans live under /eidos/run/plans/<id>.json. When the supervisor's
// scheduler goroutine fires a plan it submits a wake.Signal with
// Reason=planned and renames the file to plans/fired/<id>.json for
// audit. Pure filesystem operations live here; the goroutine itself
// lives in cmd/eidos/supervisor/scheduler.go.
package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// SchemaVersion is bumped when the on-disk format changes.
const SchemaVersion = 1

// Plan is the on-disk plan record.
type Plan struct {
	V         int    `json:"v"`
	ID        string `json:"id"`
	At        int64  `json:"at"`
	Hint      string `json:"hint"`
	CreatedAt int64  `json:"created_at"`
}

// newIDAt builds a plan ID with the project-wide
// "<UTC-timestamp>-<reason-slug>-<4-hex>" shape used by wake.Signal.ID.
// Format: 20260509T123000Z-plan-7f2e
func newIDAt(now time.Time) string {
	var rnd [2]byte
	_, _ = rand.Read(rnd[:])
	return fmt.Sprintf("%s-plan-%s",
		now.UTC().Format("20060102T150405Z"),
		hex.EncodeToString(rnd[:]),
	)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/scheduler/... -v`
Expected: PASS for both tests.

### Task 2.2: `Add` with bounds enforcement

**Files:**
- Create: `internal/scheduler/scheduler.go`
- Test: `internal/scheduler/scheduler_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/scheduler/scheduler_test.go`:

```go
func TestAddRejectsTooSoon(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	_, err := Add(dir, now, "too soon", now.Add(30*time.Second))
	if err == nil || !strings.Contains(err.Error(), "60s") {
		t.Errorf("expected 60s-bound error, got %v", err)
	}
}

func TestAddRejectsTooFar(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	_, err := Add(dir, now, "too far", now.Add(31*24*time.Hour))
	if err == nil || !strings.Contains(err.Error(), "30d") {
		t.Errorf("expected 30d-bound error, got %v", err)
	}
}

func TestAddRejectsEmptyHint(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	_, err := Add(dir, now, "", now.Add(2*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "hint") {
		t.Errorf("expected hint-required error, got %v", err)
	}
}

func TestAddRejectsHintWithNewline(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	_, err := Add(dir, now, "first\nsecond", now.Add(2*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "single-line") {
		t.Errorf("expected single-line error, got %v", err)
	}
}

func TestAddRejectsHintTooLong(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	long := strings.Repeat("x", 257)
	_, err := Add(dir, now, long, now.Add(2*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "256") {
		t.Errorf("expected length error, got %v", err)
	}
}

func TestAddWritesFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	plan, err := Add(dir, now, "follow up on bob", now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if plan.ID == "" {
		t.Fatal("missing ID")
	}
	if plan.At != now.Add(2*time.Hour).Unix() {
		t.Errorf("At = %d, want %d", plan.At, now.Add(2*time.Hour).Unix())
	}
	if plan.Hint != "follow up on bob" {
		t.Errorf("Hint = %q", plan.Hint)
	}
	if plan.CreatedAt != now.Unix() {
		t.Errorf("CreatedAt = %d", plan.CreatedAt)
	}
	// File on disk:
	path := filepath.Join(dir, plan.ID+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("plan file not on disk: %v", err)
	}
}
```

Add the imports to the test file (`filepath`, `os`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scheduler/... -v`
Expected: FAIL with `undefined: Add`.

- [ ] **Step 3: Implement Add**

Create `internal/scheduler/scheduler.go`:

```go
package scheduler

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

const (
	// MinFutureWindow is the minimum time-from-now a plan may be set for.
	MinFutureWindow = 60 * time.Second
	// MaxFutureWindow is the maximum time-from-now a plan may be set for.
	MaxFutureWindow = 30 * 24 * time.Hour
	// MaxHintLen caps the agent-authored hint length.
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
func Add(dir string, now, hint string, at time.Time) (Plan, error) {
	return Plan{}, errors.New("not yet implemented")
}
```

Wait — there's a parameter mismatch. Look again: tests call `Add(dir, now, "...", now.Add(2*time.Hour))` so the signature is `Add(dir string, now time.Time, hint string, at time.Time)`. Fix the stub:

```go
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

// suppress unused warnings until List/ScanDue land
var _ = fs.ModePerm
```

(The trailing `var _ = fs.ModePerm` is a placeholder; remove when `List` lands.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scheduler/... -v`
Expected: PASS for all `TestAddRejects*` and `TestAddWritesFile`.

### Task 2.3: `List`

**Files:**
- Modify: `internal/scheduler/scheduler.go`
- Test: `internal/scheduler/scheduler_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestListEmpty(t *testing.T) {
	dir := t.TempDir()
	plans, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 0 {
		t.Errorf("len = %d", len(plans))
	}
}

func TestListSortedByID(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		_, err := Add(dir, now.Add(time.Duration(i)*time.Second), "x", now.Add(time.Hour))
		if err != nil {
			t.Fatalf("Add[%d]: %v", i, err)
		}
	}
	plans, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 3 {
		t.Fatalf("len = %d", len(plans))
	}
	if !(plans[0].ID < plans[1].ID && plans[1].ID < plans[2].ID) {
		t.Errorf("not sorted: %v", []string{plans[0].ID, plans[1].ID, plans[2].ID})
	}
}

func TestListExcludesFired(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	plan, err := Add(dir, now, "live", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	// Move into fired/
	firedDir := filepath.Join(dir, "fired")
	if err := os.MkdirAll(firedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(
		filepath.Join(dir, plan.ID+".json"),
		filepath.Join(firedDir, plan.ID+".json"),
	); err != nil {
		t.Fatal(err)
	}
	plans, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 0 {
		t.Errorf("expected 0 active, got %d", len(plans))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scheduler/... -run TestList -v`
Expected: FAIL with `undefined: List`.

- [ ] **Step 3: Implement List**

Append to `internal/scheduler/scheduler.go` (and remove the `_ = fs.ModePerm` placeholder):

```go
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
```

Add `"sort"` to imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scheduler/... -v`
Expected: all tests PASS.

### Task 2.4: `Cancel` and `Clear`

**Files:**
- Modify: `internal/scheduler/scheduler.go`
- Test: `internal/scheduler/scheduler_test.go`

- [ ] **Step 1: Write the failing tests**

Append:

```go
func TestCancel(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	plan, err := Add(dir, now, "x", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := Cancel(dir, plan.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	plans, _ := List(dir)
	if len(plans) != 0 {
		t.Errorf("expected 0 after cancel, got %d", len(plans))
	}
}

func TestCancelMissingErrors(t *testing.T) {
	dir := t.TempDir()
	err := Cancel(dir, "20260509T123000Z-plan-dead")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found, got %v", err)
	}
}

func TestClear(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i := 0; i < 4; i++ {
		_, _ = Add(dir, now.Add(time.Duration(i)*time.Second), "x", now.Add(time.Hour))
	}
	n, err := Clear(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Errorf("Clear returned %d, want 4", n)
	}
	plans, _ := List(dir)
	if len(plans) != 0 {
		t.Errorf("plans still present after Clear: %d", len(plans))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scheduler/... -run "TestCancel|TestClear" -v`
Expected: FAIL with `undefined: Cancel`.

- [ ] **Step 3: Implement Cancel and Clear**

Append:

```go
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

// looksLikeID is a defensive check so Cancel("..", ...) cannot escape the dir.
// Real IDs match "<14 chars>Z-plan-<4 hex>".
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scheduler/... -v`
Expected: all tests PASS.

### Task 2.5: `ScanDue` and `MarkFired`

**Files:**
- Modify: `internal/scheduler/scheduler.go`
- Test: `internal/scheduler/scheduler_test.go`

- [ ] **Step 1: Write the failing tests**

Append:

```go
func TestScanDueOnlyDue(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	pastP, _ := Add(dir, now.Add(-2*time.Minute), "past", now.Add(-1*time.Minute))
	_ = pastP // we wrote it via Add at "two minutes ago"; due now
	_, _ = Add(dir, now, "future", now.Add(2*time.Minute))
	due, err := ScanDue(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("len(due) = %d, want 1; got %v", len(due), due)
	}
	if due[0].Hint != "past" {
		t.Errorf("due Hint = %q", due[0].Hint)
	}
}

func TestMarkFired(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	plan, _ := Add(dir, now, "x", now.Add(2*time.Minute))
	if err := MarkFired(dir, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, plan.ID+".json")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("active file still present")
	}
	if _, err := os.Stat(filepath.Join(dir, "fired", plan.ID+".json")); err != nil {
		t.Errorf("fired file missing: %v", err)
	}
}

func TestMarkFiredIdempotent(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	plan, _ := Add(dir, now, "x", now.Add(2*time.Minute))
	if err := MarkFired(dir, plan.ID); err != nil {
		t.Fatal(err)
	}
	// second MarkFired on a missing active file should not error
	if err := MarkFired(dir, plan.ID); err != nil {
		t.Errorf("second MarkFired returned %v, want nil", err)
	}
}
```

(`TestScanDueOnlyDue` first param to `Add` is "two minutes ago" but `Add` rejects in-the-past times. Adjust:)

Replace the test setup to inject a plan file directly bypassing `Add`'s bounds check, since past plans are real after a supervisor restart:

```go
func TestScanDueOnlyDue(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC)
	// Inject a past plan directly (past by definition violates Add bounds)
	pastPlan := Plan{
		V:         SchemaVersion,
		ID:        "20260509T095900Z-plan-aaaa",
		At:        now.Add(-1 * time.Minute).Unix(),
		Hint:      "past",
		CreatedAt: now.Add(-2 * time.Minute).Unix(),
	}
	if err := writePlan(dir, pastPlan); err != nil {
		t.Fatal(err)
	}
	_, _ = Add(dir, now, "future", now.Add(2*time.Minute))

	due, err := ScanDue(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("len(due) = %d, want 1; got %v", len(due), due)
	}
	if due[0].Hint != "past" {
		t.Errorf("due Hint = %q", due[0].Hint)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scheduler/... -run "TestScanDue|TestMarkFired" -v`
Expected: FAIL with `undefined: ScanDue`.

- [ ] **Step 3: Implement**

Append to `internal/scheduler/scheduler.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/scheduler/... -v`
Expected: all tests PASS.

### Task 2.6: Concurrency probe + commit

**Files:**
- Modify: `internal/scheduler/scheduler_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestAddConcurrentDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	const N = 32
	errCh := make(chan error, N)
	idCh := make(chan string, N)
	for i := 0; i < N; i++ {
		go func() {
			p, err := Add(dir, now, "x", now.Add(2*time.Minute))
			if err != nil {
				errCh <- err
				return
			}
			errCh <- nil
			idCh <- p.ID
		}()
	}
	for i := 0; i < N; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("Add[%d]: %v", i, err)
		}
	}
	close(idCh)
	seen := map[string]bool{}
	for id := range idCh {
		if seen[id] {
			t.Errorf("duplicate ID: %s", id)
		}
		seen[id] = true
	}
	plans, _ := List(dir)
	if len(plans) != N {
		t.Errorf("expected %d plans on disk, got %d", N, len(plans))
	}
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/scheduler/... -run TestAddConcurrentDistinctFiles -race -v`
Expected: PASS, no race.

(If it fails because two goroutines pick the same hex with same-second timestamps, increase the random suffix to 4 bytes (8 hex chars) — collision probability across 32 attempts within 1s remains low, but 8 hex is robust.)

- [ ] **Step 3: gofmt + vet**

Run: `gofmt -l internal/scheduler && go vet ./internal/scheduler/...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/scheduler/
git commit -m "$(cat <<'EOF'
feat(scheduler): plan-file format with Add/List/Cancel/ScanDue/MarkFired

New internal/scheduler package owning the on-disk plan format that
the supervisor's scheduler goroutine fires (next commit).

- ID format matches the project's existing wake.Signal.ID shape:
  <UTC-timestamp>-plan-<4-hex>. No new dependency.
- Bounds enforced at Add: 60s..30d future, hint single-line ≤ 256 chars.
- Atomic write via tmp + rename; concurrent Add produces distinct files.
- MarkFired is idempotent under crash-restart; fire-once is provided
  by wake-coalescing, not by this layer.

Pure filesystem operations; no goroutines or timers.

Refs docs/superpowers/specs/2026-05-09-heartbeat-plans-dreams-design.md §5.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 3 — `internal/dreamstate` package

### Task 3.1: State type, Read, missing-file behavior

**Files:**
- Create: `internal/dreamstate/dreamstate.go`
- Test: `internal/dreamstate/dreamstate_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/dreamstate/dreamstate_test.go`:

```go
package dreamstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadMissingReturnsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	st, err := Read(path)
	if err != nil {
		t.Fatalf("Read missing: %v", err)
	}
	if st.DreamCount != 0 || st.LastDreamFinishedAt != 0 {
		t.Errorf("zero state expected, got %+v", st)
	}
}

func TestReadCorruptReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Error("expected error on corrupt JSON")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/dreamstate/... -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement**

Create `internal/dreamstate/dreamstate.go`:

```go
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
	"syscall"
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/dreamstate/... -v`
Expected: PASS.

### Task 3.2: Begin and End

**Files:**
- Modify: `internal/dreamstate/dreamstate.go`
- Test: `internal/dreamstate/dreamstate_test.go`

- [ ] **Step 1: Write the failing tests**

Append:

```go
func TestBeginThenEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	t0 := time.Date(2026, 5, 9, 3, 0, 0, 0, time.UTC)
	t1 := t0.Add(11 * time.Minute)
	if err := Begin(path, t0, "consolidating bob"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	st, _ := Read(path)
	if !st.CurrentlyDreaming {
		t.Error("CurrentlyDreaming should be true after Begin")
	}
	if st.LastDreamStartedAt != t0.Unix() {
		t.Errorf("LastDreamStartedAt = %d", st.LastDreamStartedAt)
	}
	if err := End(path, t1, "done", "memory/episodic/2026/05/dream-001.md"); err != nil {
		t.Fatalf("End: %v", err)
	}
	st, _ = Read(path)
	if st.CurrentlyDreaming {
		t.Error("CurrentlyDreaming should be false after End")
	}
	if st.DreamCount != 1 {
		t.Errorf("DreamCount = %d", st.DreamCount)
	}
	if st.LastDreamFinishedAt != t1.Unix() {
		t.Errorf("LastDreamFinishedAt = %d", st.LastDreamFinishedAt)
	}
	if st.LastDreamNote != "done" {
		t.Errorf("LastDreamNote = %q", st.LastDreamNote)
	}
	if st.LastDreamProse != "memory/episodic/2026/05/dream-001.md" {
		t.Errorf("LastDreamProse = %q", st.LastDreamProse)
	}
}

func TestEndWithoutBegin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	t0 := time.Date(2026, 5, 9, 3, 0, 0, 0, time.UTC)
	if err := End(path, t0, "recovered", ""); err != nil {
		t.Fatalf("End without Begin: %v", err)
	}
	st, _ := Read(path)
	if st.DreamCount != 1 {
		t.Errorf("DreamCount = %d", st.DreamCount)
	}
}

func TestEndRequiresNote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	t0 := time.Now()
	if err := End(path, t0, "", ""); err == nil {
		t.Error("End should require a note")
	}
}

func TestEndProsePathMustBeUnderEpisodic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	t0 := time.Now()
	if err := End(path, t0, "ok", "secret/leaks.md"); err == nil {
		t.Error("End should reject prose path outside memory/episodic/")
	}
}

func TestBeginConcurrentSerial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dream-state.json")
	t0 := time.Date(2026, 5, 9, 3, 0, 0, 0, time.UTC)
	const N = 16
	errCh := make(chan error, N)
	for i := 0; i < N; i++ {
		go func(i int) {
			errCh <- Begin(path, t0.Add(time.Duration(i)*time.Second), "intent")
		}(i)
	}
	for i := 0; i < N; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("Begin[%d]: %v", i, err)
		}
	}
	st, _ := Read(path)
	if !st.CurrentlyDreaming {
		t.Error("expected CurrentlyDreaming=true after concurrent Begin")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/dreamstate/... -v`
Expected: FAIL with `undefined: Begin`.

- [ ] **Step 3: Implement Begin and End with flock**

Append:

```go
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
	if prosePath != "" {
		if !isUnderEpisodic(prosePath) {
			return fmt.Errorf("dream prose path must live under memory/episodic/, got %q", prosePath)
		}
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
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("flock dream-state: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) }()

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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/dreamstate/... -race -v`
Expected: all PASS, no race.

### Task 3.3: gofmt + commit

- [ ] **Step 1: gofmt + vet**

Run: `gofmt -l internal/dreamstate && go vet ./internal/dreamstate/...`
Expected: clean.

- [ ] **Step 2: Commit**

```bash
git add internal/dreamstate/
git commit -m "$(cat <<'EOF'
feat(dreamstate): dream-state.json owner with Begin/End

New internal/dreamstate package backing the mind-form's voluntary
dream cycle. Begin/End mutate /eidos/run/dream-state.json under a
per-file flock; missing-file reads yield a zero State without error
so a fresh mind-form is naturally "dream_eligible".

End without prior Begin still increments DreamCount — claude may
crash mid-dream and we don't want that to block recovery on the next
wake.

Refs docs/superpowers/specs/2026-05-09-heartbeat-plans-dreams-design.md §6.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 4 — Config extensions and crontab generator

### Task 4.1: Heartbeat interval validator + cron expression mapping

**Files:**
- Create: `internal/config/heartbeat.go`
- Test: `internal/config/heartbeat_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/config/heartbeat_test.go`:

```go
package config

import (
	"strings"
	"testing"
	"time"
)

func TestHeartbeatCronExpression(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{1 * time.Minute, "*/1 * * * *"},
		{2 * time.Minute, "*/2 * * * *"},
		{5 * time.Minute, "*/5 * * * *"},
		{15 * time.Minute, "*/15 * * * *"},
		{30 * time.Minute, "*/30 * * * *"},
		{1 * time.Hour, "0 * * * *"},
		{2 * time.Hour, "0 */2 * * *"},
		{4 * time.Hour, "0 */4 * * *"},
		{12 * time.Hour, "0 */12 * * *"},
		{24 * time.Hour, "0 0 * * *"},
	}
	for _, c := range cases {
		got, err := HeartbeatCronExpression(c.in)
		if err != nil {
			t.Errorf("HeartbeatCronExpression(%s): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("HeartbeatCronExpression(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHeartbeatCronExpressionRejects(t *testing.T) {
	bad := []time.Duration{
		0,
		7 * time.Minute,
		90 * time.Minute,
		5 * time.Hour,
		25 * time.Hour,
		35 * time.Second,
	}
	for _, d := range bad {
		_, err := HeartbeatCronExpression(d)
		if err == nil {
			t.Errorf("HeartbeatCronExpression(%s): expected error, got nil", d)
			continue
		}
		// Error should name the supported set so users know what to put.
		if !strings.Contains(err.Error(), "supported") {
			t.Errorf("HeartbeatCronExpression(%s) error doesn't mention 'supported': %v", d, err)
		}
	}
}

func TestValidateHeartbeatInterval(t *testing.T) {
	if err := ValidateHeartbeatInterval("4h"); err != nil {
		t.Errorf("ValidateHeartbeatInterval(4h): %v", err)
	}
	if err := ValidateHeartbeatInterval("90m"); err == nil {
		t.Error("ValidateHeartbeatInterval(90m) should fail")
	}
	if err := ValidateHeartbeatInterval("not-a-duration"); err == nil {
		t.Error("ValidateHeartbeatInterval(garbage) should fail")
	}
	if err := ValidateHeartbeatInterval(""); err != nil {
		t.Errorf("empty (= use default) should pass, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/... -run "TestHeartbeat|TestValidateHeartbeat" -v`
Expected: FAIL with `undefined: HeartbeatCronExpression`.

- [ ] **Step 3: Implement**

Create `internal/config/heartbeat.go`:

```go
package config

import (
	"fmt"
	"time"
)

// DefaultHeartbeatInterval is the cadence used when [heartbeat] interval
// is unset. Matches the static crontab line from MindForge v0.
const DefaultHeartbeatInterval = 4 * time.Hour

// supportedMinuteSteps is {N : 60 mod N == 0, N <= 30}, sorted.
var supportedMinuteSteps = []int{1, 2, 3, 4, 5, 6, 10, 12, 15, 20, 30}

// supportedHourSteps is {M : 24 mod M == 0, 1 <= M <= 24}, sorted.
var supportedHourSteps = []int{1, 2, 3, 4, 6, 8, 12, 24}

// HeartbeatCronExpression returns the busybox-cron expression that fires
// at the given interval. Accepted intervals are listed in the error
// message when the input is unsupported.
func HeartbeatCronExpression(d time.Duration) (string, error) {
	if d < time.Minute {
		return "", supportedSetError(d)
	}
	// Minute-step branch: < 1h and divides 60.
	if d < time.Hour {
		mins := int(d / time.Minute)
		if d != time.Duration(mins)*time.Minute {
			return "", supportedSetError(d)
		}
		for _, n := range supportedMinuteSteps {
			if n == mins {
				return fmt.Sprintf("*/%d * * * *", n), nil
			}
		}
		return "", supportedSetError(d)
	}
	// Hour-step branch: divides 24.
	hours := int(d / time.Hour)
	if d != time.Duration(hours)*time.Hour {
		return "", supportedSetError(d)
	}
	for _, m := range supportedHourSteps {
		if m == hours {
			switch {
			case m == 1:
				return "0 * * * *", nil
			case m == 24:
				return "0 0 * * *", nil
			default:
				return fmt.Sprintf("0 */%d * * *", m), nil
			}
		}
	}
	return "", supportedSetError(d)
}

// ValidateHeartbeatInterval returns nil for an empty string (defaulting
// to DefaultHeartbeatInterval) and for a duration in the supported set;
// otherwise an error explaining the supported values.
func ValidateHeartbeatInterval(s string) error {
	if s == "" {
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if _, err := HeartbeatCronExpression(d); err != nil {
		return err
	}
	return nil
}

func supportedSetError(d time.Duration) error {
	return fmt.Errorf("heartbeat interval %s is not in the supported set "+
		"(supported minute steps: 1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m; "+
		"supported hour steps: 1h,2h,3h,4h,6h,8h,12h,24h)", d)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/... -run "TestHeartbeat|TestValidateHeartbeat" -v`
Expected: PASS.

### Task 4.2: Quiet hours + tz validators

**Files:**
- Modify: `internal/config/heartbeat.go`
- Test: `internal/config/heartbeat_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/heartbeat_test.go`:

```go
func TestValidateQuietHours(t *testing.T) {
	cases := []struct {
		start, end string
		ok         bool
	}{
		{"", "", true},                  // both unset → ok
		{"22:00", "06:00", true},        // wrap-around → ok
		{"08:00", "20:00", true},        // same-day → ok
		{"22:00", "", false},            // half-set rejected
		{"", "06:00", false},
		{"25:00", "06:00", false},       // bad hour
		{"22:00", "06:60", false},       // bad minute
		{"22-00", "06:00", false},       // wrong separator
	}
	for _, c := range cases {
		err := ValidateQuietHours(c.start, c.end)
		if c.ok && err != nil {
			t.Errorf("ValidateQuietHours(%q,%q) unexpectedly errored: %v", c.start, c.end, err)
		}
		if !c.ok && err == nil {
			t.Errorf("ValidateQuietHours(%q,%q) should have errored", c.start, c.end)
		}
	}
}

func TestValidateTZ(t *testing.T) {
	if err := ValidateTZ(""); err != nil {
		t.Errorf("empty TZ should be ok, got %v", err)
	}
	if err := ValidateTZ("Asia/Shanghai"); err != nil {
		t.Errorf("valid IANA tz: %v", err)
	}
	if err := ValidateTZ("Atlantis/Lemuria"); err == nil {
		t.Error("bogus tz should fail")
	}
}

func TestInQuietHours(t *testing.T) {
	loc := time.UTC
	// same-day window 08:00..20:00
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, loc)
	if !InQuietHours(now, "08:00", "20:00", loc) {
		t.Error("12:00 should be in 08:00..20:00")
	}
	if InQuietHours(time.Date(2026, 5, 9, 21, 0, 0, 0, loc), "08:00", "20:00", loc) {
		t.Error("21:00 should NOT be in 08:00..20:00")
	}
	// wrap-around 22:00..06:00
	if !InQuietHours(time.Date(2026, 5, 9, 23, 0, 0, 0, loc), "22:00", "06:00", loc) {
		t.Error("23:00 should be in 22:00..06:00")
	}
	if !InQuietHours(time.Date(2026, 5, 9, 3, 0, 0, 0, loc), "22:00", "06:00", loc) {
		t.Error("03:00 should be in 22:00..06:00")
	}
	if InQuietHours(time.Date(2026, 5, 9, 12, 0, 0, 0, loc), "22:00", "06:00", loc) {
		t.Error("12:00 should NOT be in 22:00..06:00")
	}
	// unset → never quiet
	if InQuietHours(now, "", "", loc) {
		t.Error("unset hours should yield false")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/... -run "TestValidateQuiet|TestValidateTZ|TestInQuiet" -v`
Expected: FAIL with `undefined: ValidateQuietHours`.

- [ ] **Step 3: Implement**

Append to `internal/config/heartbeat.go`:

```go
import "strings"  // ensure imported (collapse with existing imports)

// ValidateQuietHours accepts both empty (no quiet hours configured) or
// both set in HH:MM 24-hour form. Wrap-around (start > end) is allowed.
func ValidateQuietHours(start, end string) error {
	if start == "" && end == "" {
		return nil
	}
	if start == "" || end == "" {
		return fmt.Errorf("quiet_start and quiet_end must both be set or neither (got start=%q end=%q)", start, end)
	}
	if _, err := parseHHMM(start); err != nil {
		return fmt.Errorf("quiet_start %q: %w", start, err)
	}
	if _, err := parseHHMM(end); err != nil {
		return fmt.Errorf("quiet_end %q: %w", end, err)
	}
	return nil
}

// ValidateTZ accepts empty (no timezone) or any IANA tz that
// time.LoadLocation accepts.
func ValidateTZ(tz string) error {
	if tz == "" {
		return nil
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return nil
}

// InQuietHours reports whether now falls in [start, end) interpreted in
// loc. Wrap-around (start > end) is supported. Empty start/end yields false.
func InQuietHours(now time.Time, start, end string, loc *time.Location) bool {
	if start == "" || end == "" {
		return false
	}
	startMin, err := parseHHMM(start)
	if err != nil {
		return false
	}
	endMin, err := parseHHMM(end)
	if err != nil {
		return false
	}
	local := now.In(loc)
	cur := local.Hour()*60 + local.Minute()
	if startMin <= endMin {
		return cur >= startMin && cur < endMin
	}
	// wrap-around
	return cur >= startMin || cur < endMin
}

func parseHHMM(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, fmt.Errorf("not HH:MM")
	}
	hh, err := strconv.Atoi(s[:2])
	if err != nil || hh < 0 || hh > 23 {
		return 0, fmt.Errorf("invalid hour")
	}
	mm, err := strconv.Atoi(s[3:])
	if err != nil || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("invalid minute")
	}
	return hh*60 + mm, nil
}
```

Add `"strconv"` to the imports of `heartbeat.go`. (Remove the standalone `import "strings"` line if it was added separately — collapse into the import block.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/... -run "TestValidateQuiet|TestValidateTZ|TestInQuiet" -v`
Expected: PASS.

### Task 4.3: Dream interval validator

**Files:**
- Modify: `internal/config/heartbeat.go`
- Test: `internal/config/heartbeat_test.go`

- [ ] **Step 1: Write the failing tests**

Append:

```go
func TestValidateDreamMinInterval(t *testing.T) {
	if err := ValidateDreamMinInterval(""); err != nil {
		t.Errorf("empty: %v", err)
	}
	if err := ValidateDreamMinInterval("12h"); err != nil {
		t.Errorf("12h: %v", err)
	}
	if err := ValidateDreamMinInterval("30m"); err == nil {
		t.Error("30m should be rejected (< 1h)")
	}
	if err := ValidateDreamMinInterval("garbage"); err == nil {
		t.Error("garbage should be rejected")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/... -run TestValidateDreamMinInterval -v`
Expected: FAIL with `undefined: ValidateDreamMinInterval`.

- [ ] **Step 3: Implement**

Append:

```go
// DefaultDreamMinInterval is used when [mindform] dream_min_interval is unset.
const DefaultDreamMinInterval = 12 * time.Hour

// ValidateDreamMinInterval accepts empty (use default) or any duration ≥ 1h.
func ValidateDreamMinInterval(s string) error {
	if s == "" {
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if d < time.Hour {
		return fmt.Errorf("dream_min_interval %s is too short: minimum 1h", d)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/... -run TestValidateDreamMinInterval -v`
Expected: PASS.

### Task 4.4: Wire new keys into Config struct

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go` (assume the file exists; if not, create with the standard test scaffold):

```go
func TestLoadHeartbeatAndQuietHours(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `
log_level = "info"

[heartbeat]
interval = "30m"

[mindform]
model = "claude-sonnet-4-7"
quiet_start = "22:00"
quiet_end = "06:00"
tz = "Asia/Shanghai"
dream_min_interval = "8h"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Heartbeat.Interval != "30m" {
		t.Errorf("Heartbeat.Interval = %q", cfg.Heartbeat.Interval)
	}
	if cfg.MindForm.QuietStart != "22:00" || cfg.MindForm.QuietEnd != "06:00" {
		t.Errorf("Quiet = %q..%q", cfg.MindForm.QuietStart, cfg.MindForm.QuietEnd)
	}
	if cfg.MindForm.TZ != "Asia/Shanghai" {
		t.Errorf("TZ = %q", cfg.MindForm.TZ)
	}
	if cfg.MindForm.DreamMinInterval != "8h" {
		t.Errorf("DreamMinInterval = %q", cfg.MindForm.DreamMinInterval)
	}
}
```

(Imports: `path/filepath`, `os`, `testing`. The package `config_test.go` may already import these.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/... -run TestLoadHeartbeatAndQuietHours -v`
Expected: FAIL — fields don't exist yet.

- [ ] **Step 3: Extend Config + MindFormConfig**

Modify `internal/config/config.go`. Replace the `MindFormConfig` block and add a `HeartbeatConfig` block; add the field to `Config`:

```go
type Config struct {
	StateDir string `toml:"state_dir"`
	LogLevel string `toml:"log_level"`

	Daemon    DaemonConfig    `toml:"daemon"`
	Publish   PublishConfig   `toml:"publish"`
	Subscribe SubscribeConfig `toml:"subscribe"`
	Dashboard DashboardConfig `toml:"dashboard"`
	Wake      WakeConfig      `toml:"wake"`
	MindForm  MindFormConfig  `toml:"mindform"`
	Heartbeat HeartbeatConfig `toml:"heartbeat"`
}

// HeartbeatConfig is the in-container gate's mind-form heartbeat cadence.
// Lives in /eidos/gate/config.toml. Host gates leave Heartbeat at its
// zero value because the [heartbeat] block is absent from their config.
type HeartbeatConfig struct {
	// Interval is a Go duration string like "4h" or "30m". Empty = use
	// DefaultHeartbeatInterval. Must be in the supported set; see
	// ValidateHeartbeatInterval.
	Interval string `toml:"interval"`
}

// MindFormConfig is the in-container gate's mind-form-runtime settings.
// Lives in /eidos/gate/config.toml; host gates leave MindForm at its
// zero value because the [mindform] block is absent from their config.
type MindFormConfig struct {
	Model string `toml:"model"`

	// Quiet hours feed agent-runner's wake-context computation: when
	// [now in TZ] falls in [QuietStart, QuietEnd), the wake context
	// surfaces master_likely_asleep=true. Both must be set or neither.
	QuietStart string `toml:"quiet_start"`
	QuietEnd   string `toml:"quiet_end"`
	TZ         string `toml:"tz"`

	// DreamMinInterval is the floor for "you may dream now". Empty =
	// DefaultDreamMinInterval (12h).
	DreamMinInterval string `toml:"dream_min_interval"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/... -run TestLoadHeartbeatAndQuietHours -v`
Expected: PASS.

### Task 4.5: Validate-on-load wrapper

**Files:**
- Create: `internal/config/heartbeat.go` (extend) — add a `ValidateMindFormConfig` helper
- Test: `internal/config/heartbeat_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestValidateMindFormConfigRejectsBadInterval(t *testing.T) {
	cfg := Config{
		Heartbeat: HeartbeatConfig{Interval: "90m"},
	}
	if err := ValidateMindFormConfig(cfg); err == nil {
		t.Error("90m interval should be rejected")
	}
}

func TestValidateMindFormConfigRejectsHalfQuietHours(t *testing.T) {
	cfg := Config{
		MindForm: MindFormConfig{QuietStart: "22:00"},
	}
	if err := ValidateMindFormConfig(cfg); err == nil {
		t.Error("half-set quiet hours should be rejected")
	}
}

func TestValidateMindFormConfigOK(t *testing.T) {
	cfg := Config{
		Heartbeat: HeartbeatConfig{Interval: "4h"},
		MindForm: MindFormConfig{
			QuietStart:       "22:00",
			QuietEnd:         "06:00",
			TZ:               "Asia/Shanghai",
			DreamMinInterval: "12h",
		},
	}
	if err := ValidateMindFormConfig(cfg); err != nil {
		t.Errorf("ok config rejected: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/... -run TestValidateMindFormConfig -v`
Expected: FAIL with `undefined: ValidateMindFormConfig`.

- [ ] **Step 3: Implement**

Append to `internal/config/heartbeat.go`:

```go
// ValidateMindFormConfig walks every new mind-form config knob and
// returns the first failure. Used by the supervisor at PID-1 startup
// (after Load) so a bad config fails fast with a precise message.
func ValidateMindFormConfig(cfg Config) error {
	if err := ValidateHeartbeatInterval(cfg.Heartbeat.Interval); err != nil {
		return err
	}
	if err := ValidateQuietHours(cfg.MindForm.QuietStart, cfg.MindForm.QuietEnd); err != nil {
		return err
	}
	if err := ValidateTZ(cfg.MindForm.TZ); err != nil {
		return err
	}
	if err := ValidateDreamMinInterval(cfg.MindForm.DreamMinInterval); err != nil {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/... -v`
Expected: all PASS.

### Task 4.6: Vet, build, commit

- [ ] **Step 1: gofmt + vet**

Run: `gofmt -l internal/config && go vet ./internal/config/...`
Expected: clean.

- [ ] **Step 2: Confirm whole workspace still builds**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 3: Commit**

```bash
git add internal/config/
git commit -m "$(cat <<'EOF'
feat(config): heartbeat interval, quiet hours, dream-min-interval

Adds the [heartbeat] section and four [mindform] keys consumed by the
supervisor at PID-1 startup and by agent-runner at wake time:

- [heartbeat] interval — minute or hour cadence in the cron-expressible
  set; HeartbeatCronExpression maps it to a busybox-cron line.
- [mindform] quiet_start, quiet_end, tz — feed master_likely_asleep
  in the wake context.
- [mindform] dream_min_interval — floor for dream_eligible.

ValidateMindFormConfig fails fast with a message that names the
supported set so the operator knows what to change.

Refs docs/superpowers/specs/2026-05-09-heartbeat-plans-dreams-design.md §7.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 5 — `forge plan` and `forge dream` CLIs

### Task 5.1: In-container `forge plan add`

**Files:**
- Create: `cmd/eidos/forge/plan.go`
- Test: `cmd/eidos/forge/plan_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/eidos/forge/plan_test.go`:

```go
package forge

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/scheduler"
)

func TestPlanAddInDuration(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	plan, msg, err := planAdd(dir, now, "follow up on bob", "2h", "")
	if err != nil {
		t.Fatalf("planAdd: %v", err)
	}
	if plan.Hint != "follow up on bob" {
		t.Errorf("Hint = %q", plan.Hint)
	}
	if plan.At != now.Add(2*time.Hour).Unix() {
		t.Errorf("At = %d", plan.At)
	}
	if !strings.Contains(msg, plan.ID) {
		t.Errorf("msg %q lacks plan id", msg)
	}
}

func TestPlanAddAtRFC3339(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	at := now.Add(3 * time.Hour).Format(time.RFC3339)
	plan, _, err := planAdd(dir, now, "x", "", at)
	if err != nil {
		t.Fatalf("planAdd: %v", err)
	}
	if plan.At != now.Add(3*time.Hour).Unix() {
		t.Errorf("At = %d", plan.At)
	}
}

func TestPlanAddAtUnixSeconds(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	at := now.Add(3 * time.Hour).Unix()
	plan, _, err := planAdd(dir, now, "x", "", strconvFormatInt(at))
	if err != nil {
		t.Fatalf("planAdd: %v", err)
	}
	if plan.At != at {
		t.Errorf("At = %d", plan.At)
	}
}

func TestPlanAddRejectsBothInAndAt(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	_, _, err := planAdd(dir, now, "x", "2h", "1715284800")
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("expected exactly-one error, got %v", err)
	}
}

func TestPlanAddRejectsNeither(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	_, _, err := planAdd(dir, now, "x", "", "")
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("expected exactly-one error, got %v", err)
	}
}

// strconvFormatInt is a tiny helper so the test file doesn't import strconv twice.
func strconvFormatInt(n int64) string {
	return fmtInt64(n)
}
func fmtInt64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// confirm scheduler is reachable
var _ = scheduler.Plan{}

// suppress unused
var _ = bytes.Buffer{}
var _ = os.WriteFile
var _ = filepath.Join
```

(Cleanup the unused-import warding once the file shape settles.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/eidos/forge/... -run TestPlanAdd -v`
Expected: FAIL with `undefined: planAdd`.

- [ ] **Step 3: Implement `planAdd`**

Create `cmd/eidos/forge/plan.go`:

```go
package forge

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/scheduler"
	"github.com/spf13/cobra"
)

// plansDir is the in-container dir where plan files live.
const plansDir = "/eidos/run/plans"

// planAdd is the CLI-agnostic implementation of `forge plan add`. The
// cobra command in newPlanCmd is a thin wrapper.
//
// Exactly one of inDuration / atSpec must be non-empty. atSpec is parsed
// first as RFC3339, then as a unix-seconds integer.
func planAdd(dir string, now time.Time, hint, inDuration, atSpec string) (scheduler.Plan, string, error) {
	hasIn := inDuration != ""
	hasAt := atSpec != ""
	if hasIn == hasAt {
		return scheduler.Plan{}, "", errors.New("exactly one of --in or --at is required")
	}
	var at time.Time
	if hasIn {
		d, err := time.ParseDuration(inDuration)
		if err != nil {
			return scheduler.Plan{}, "", fmt.Errorf("invalid --in duration %q: %w", inDuration, err)
		}
		at = now.Add(d)
	} else {
		var err error
		at, err = parseAtSpec(atSpec)
		if err != nil {
			return scheduler.Plan{}, "", err
		}
	}
	plan, err := scheduler.Add(dir, now, hint, at)
	if err != nil {
		return scheduler.Plan{}, "", err
	}
	delta := at.Sub(now).Truncate(time.Second)
	msg := fmt.Sprintf("plan %s set for %s (in %s)\n",
		plan.ID, at.Format(time.RFC3339), delta)
	return plan, msg, nil
}

func parseAtSpec(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Unix(n, 0), nil
	}
	return time.Time{}, fmt.Errorf("--at must be RFC3339 (e.g. 2026-05-09T14:00:00+08:00) or unix seconds, got %q", s)
}

// ensure forge/plan.go compiles even before the cobra wiring lands
var _ = filepath.Join
var _ = strings.HasPrefix
var _ = cobra.Command{}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/eidos/forge/... -run TestPlanAdd -v`
Expected: PASS.

### Task 5.2: `planList`, `planCancel`, `planClear` impls

**Files:**
- Modify: `cmd/eidos/forge/plan.go`
- Test: `cmd/eidos/forge/plan_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `plan_test.go`:

```go
func TestPlanListFormat(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	_, _, _ = planAdd(dir, now, "first", "2h", "")
	_, _, _ = planAdd(dir, now.Add(time.Second), "second", "4h", "")
	out, err := planList(dir, now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Errorf("plans missing from output:\n%s", out)
	}
	// header present
	if !strings.Contains(out, "ID") || !strings.Contains(out, "AT") || !strings.Contains(out, "HINT") {
		t.Errorf("header missing:\n%s", out)
	}
	// ordered: first comes before second
	if strings.Index(out, "first") > strings.Index(out, "second") {
		t.Errorf("not in id order:\n%s", out)
	}
}

func TestPlanListEmpty(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	out, err := planList(dir, now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no plans") {
		t.Errorf("empty list should say so, got %q", out)
	}
}

func TestPlanCancelByID(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	plan, _, _ := planAdd(dir, now, "x", "2h", "")
	if err := planCancel(dir, plan.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	out, _ := planList(dir, now, time.UTC)
	if !strings.Contains(out, "no plans") {
		t.Errorf("plan still present after cancel:\n%s", out)
	}
}

func TestPlanClearReportsCount(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	_, _, _ = planAdd(dir, now, "a", "2h", "")
	_, _, _ = planAdd(dir, now.Add(time.Second), "b", "3h", "")
	msg, err := planClear(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "2") {
		t.Errorf("clear count missing: %q", msg)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/eidos/forge/... -run "TestPlanList|TestPlanCancel|TestPlanClear" -v`
Expected: FAIL with `undefined: planList` etc.

- [ ] **Step 3: Implement**

Append to `plan.go` (and remove the trailing `var _ =` warding):

```go
// planList renders the active plan table in tz, matching the spec format.
func planList(dir string, now time.Time, tz *time.Location) (string, error) {
	plans, err := scheduler.List(dir)
	if err != nil {
		return "", err
	}
	if len(plans) == 0 {
		return "no plans\n", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-30s %-26s %-8s %s\n", "ID", "AT", "IN", "HINT")
	for _, p := range plans {
		at := time.Unix(p.At, 0).In(tz)
		delta := at.Sub(now).Truncate(time.Second)
		if delta < 0 {
			delta = 0
		}
		fmt.Fprintf(&b, "%-30s %-26s %-8s %s\n",
			p.ID, at.Format(time.RFC3339), formatDuration(delta), p.Hint)
	}
	return b.String(), nil
}

// planCancel cancels a plan by ID. Returns the scheduler error verbatim.
func planCancel(dir, id string) error {
	return scheduler.Cancel(dir, id)
}

// planClear removes every active plan and returns a one-line summary.
func planClear(dir string) (string, error) {
	n, err := scheduler.Clear(dir)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "no plans to clear\n", nil
	}
	return fmt.Sprintf("cleared %d plans\n", n), nil
}

// formatDuration is a compact "1h57m" / "1d8h" form for the IN column.
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "now"
	}
	d = d.Truncate(time.Second)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	mins := int(d / time.Minute)
	d -= time.Duration(mins) * time.Minute
	secs := int(d / time.Second)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm%ds", mins, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/eidos/forge/... -run "TestPlanList|TestPlanCancel|TestPlanClear" -v`
Expected: PASS.

### Task 5.3: Cobra wiring for in-container `forge plan`

**Files:**
- Modify: `cmd/eidos/forge/plan.go`
- Modify: `cmd/eidos/forge/cmd.go`

- [ ] **Step 1: Write a smoke test for the cobra command tree**

Append to `plan_test.go`:

```go
func TestNewPlanInContainerCmdHasSubcommands(t *testing.T) {
	cmd := newPlanInContainerCmd()
	want := []string{"add", "list", "cancel", "clear"}
	got := map[string]bool{}
	for _, sub := range cmd.Commands() {
		got[sub.Name()] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing subcommand %q", w)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/eidos/forge/... -run TestNewPlanInContainerCmd -v`
Expected: FAIL with `undefined: newPlanInContainerCmd`.

- [ ] **Step 3: Implement the cobra command**

Append to `plan.go`:

```go
// newPlanInContainerCmd registers `eidos forge plan {add,list,cancel,clear}`
// when running inside the mind-form container (EIDOS_IN_CONTAINER=1).
func newPlanInContainerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Set, list, or cancel future wake signals",
	}
	cmd.AddCommand(newPlanAddCmd())
	cmd.AddCommand(newPlanListCmd())
	cmd.AddCommand(newPlanCancelCmd())
	cmd.AddCommand(newPlanClearCmd())
	return cmd
}

func newPlanAddCmd() *cobra.Command {
	var inDur, atSpec, hint string
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Schedule a future wake. Use --in DURATION or --at TIMESTAMP.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, msg, err := planAdd(plansDir, time.Now(), hint, inDur, atSpec)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), msg)
			return nil
		},
	}
	cmd.Flags().StringVar(&inDur, "in", "", "fire in this much time (e.g. 2h, 30m)")
	cmd.Flags().StringVar(&atSpec, "at", "", "fire at RFC3339 timestamp or unix seconds")
	cmd.Flags().StringVar(&hint, "hint", "", "single-line agent-authored note (required)")
	_ = cmd.MarkFlagRequired("hint")
	return cmd
}

func newPlanListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List active plans (excludes already-fired plans)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			tz, _ := loadMindFormTZ()
			out, err := planList(plansDir, time.Now(), tz)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
}

func newPlanCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel an active plan by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return planCancel(plansDir, args[0])
		},
	}
}

func newPlanClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Cancel every active plan",
		RunE: func(cmd *cobra.Command, _ []string) error {
			msg, err := planClear(plansDir)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), msg)
			return nil
		},
	}
}

// loadMindFormTZ returns the configured timezone or time.Local on error.
// Used only for human-friendly output formatting.
func loadMindFormTZ() (*time.Location, error) {
	cfg, err := loadGateConfig()
	if err != nil || cfg.MindForm.TZ == "" {
		return time.Local, err
	}
	loc, err := time.LoadLocation(cfg.MindForm.TZ)
	if err != nil {
		return time.Local, err
	}
	return loc, nil
}
```

The `loadGateConfig()` helper reads `/eidos/gate/config.toml`. Add it (or reuse if the file already has one — check `cmd/eidos/forge/config.go`):

Search-and-add: if `loadGateConfig` doesn't already exist in package `forge`, add it to `plan.go`:

```go
import "github.com/LucianoXu/eidopsyche/internal/config"

const gateConfigPath = "/eidos/gate/config.toml"

func loadGateConfig() (config.Config, error) {
	return config.Load(gateConfigPath)
}
```

(If `gateConfigPath` is already declared in another forge file, drop the duplicate.)

- [ ] **Step 4: Register the command tree from cmd.go**

Open `cmd/eidos/forge/cmd.go` and find where in-container commands are registered. Add `cmd.AddCommand(newPlanInContainerCmd())` to the in-container branch. (The exact edit depends on existing structure; the test in step 5 will verify it works.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/eidos/forge/... -run TestNewPlanInContainerCmd -v && go build ./...`
Expected: PASS + clean build.

### Task 5.4: Host-side `forge plan list/cancel`

**Files:**
- Modify: `cmd/eidos/forge/plan.go`
- Test: `cmd/eidos/forge/plan_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
func TestNewPlanHostCmdHasOnlyListAndCancel(t *testing.T) {
	cmd := newPlanHostCmd()
	got := map[string]bool{}
	for _, sub := range cmd.Commands() {
		got[sub.Name()] = true
	}
	if !got["list"] || !got["cancel"] {
		t.Errorf("host plan missing list/cancel: %v", got)
	}
	if got["add"] || got["clear"] {
		t.Errorf("host plan should not expose add/clear: %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/eidos/forge/... -run TestNewPlanHostCmd -v`
Expected: FAIL with `undefined: newPlanHostCmd`.

- [ ] **Step 3: Implement the host-side command**

Append to `plan.go`:

```go
// newPlanHostCmd registers the operator's host-side `eidos forge plan
// {list,cancel}` subcommands. Each execs into the named mind-form's
// container and runs the same in-container code path.
func newPlanHostCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Inspect or cancel a mind-form's scheduled wakes",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list <name>",
		Short: "List active plans in <name>'s container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return execInForge(cmd.Context(), args[0],
				[]string{"eidos", "forge", "plan", "list"})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "cancel <name> <id>",
		Short: "Cancel a plan in <name>'s container",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return execInForge(cmd.Context(), args[0],
				[]string{"eidos", "forge", "plan", "cancel", args[1]})
		},
	})
	return cmd
}

// execInForge runs argv inside the named mind-form's container via the
// existing forgectl docker client. Output flows through to the operator's
// terminal verbatim.
func execInForge(ctx context.Context, name string, argv []string) error {
	if err := forgectl.ValidateName(name); err != nil {
		return err
	}
	c, err := forgectl.New()
	if err != nil {
		return err
	}
	res, err := c.ContainerExec(ctx, forgectl.ContainerName(name), argv)
	if err != nil {
		return err
	}
	if len(res.Stdout) > 0 {
		fmt.Print(string(res.Stdout))
	}
	if len(res.Stderr) > 0 {
		fmt.Fprint(os.Stderr, string(res.Stderr))
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("forge plan exited %d", res.ExitCode)
	}
	return nil
}
```

Imports needed in `plan.go`: `context`, `os`, plus `github.com/LucianoXu/eidopsyche/internal/forgectl`.

- [ ] **Step 4: Register the host command**

Open `cmd/eidos/forge/cmd.go` and add `cmd.AddCommand(newPlanHostCmd())` to the host-side branch (mirror the existing pattern for `newWakeHostCmd()`).

- [ ] **Step 5: Run tests + build**

Run: `go test ./cmd/eidos/forge/... -v && go build ./...`
Expected: PASS + clean build.

### Task 5.5: Commit plan CLIs

- [ ] **Step 1: gofmt + vet**

Run: `gofmt -l cmd/eidos/forge && go vet ./cmd/eidos/forge/...`
Expected: clean.

- [ ] **Step 2: Commit**

```bash
git add cmd/eidos/forge/plan.go cmd/eidos/forge/plan_test.go cmd/eidos/forge/cmd.go
git commit -m "$(cat <<'EOF'
feat(forge): plan add/list/cancel/clear (in-container) + plan list/cancel (host)

In-container `eidos forge plan add` is the surface mind-forms call to
schedule their own future wakes; list/cancel/clear round out the
self-management surface. The host operator gets list and cancel only:
authoring plans on behalf of a mind-form is out of v0 scope.

Host commands exec into the container and reuse the in-container code
path, matching the project's single-call-path principle.

Refs docs/superpowers/specs/2026-05-09-heartbeat-plans-dreams-design.md §5.4-5.5.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

### Task 5.6: In-container `forge dream begin/end`

**Files:**
- Create: `cmd/eidos/forge/dream.go`
- Test: `cmd/eidos/forge/dream_test.go`

- [ ] **Step 1: Write the failing tests**

Create `cmd/eidos/forge/dream_test.go`:

```go
package forge

import (
	"strings"
	"testing"
)

func TestNewDreamCmdHasBeginAndEnd(t *testing.T) {
	cmd := newDreamCmd()
	got := map[string]bool{}
	for _, sub := range cmd.Commands() {
		got[sub.Name()] = true
	}
	if !got["begin"] || !got["end"] {
		t.Errorf("dream missing begin/end: %v", got)
	}
}

func TestNewDreamCmdEndRequiresNote(t *testing.T) {
	cmd := newDreamCmd()
	for _, sub := range cmd.Commands() {
		if sub.Name() != "end" {
			continue
		}
		if !sub.Flag("note").Annotations[cobraRequiredKey][0]
		// not all cobra versions surface required via Annotations; we'll
		// instead exercise the run-time path in a separate test.
		_ = sub
	}
	// The functional check is in TestDreamEndFunctional below.
	_ = cmd
}

const cobraRequiredKey = "cobra_annotation_bash_completion_one_required_flag"
```

The above is fragile across cobra versions. Replace `TestNewDreamCmdEndRequiresNote` with a direct test of the underlying `dreamEnd` function:

```go
func TestDreamEndRequiresNote(t *testing.T) {
	dir := t.TempDir()
	if err := dreamEnd(dir+"/state.json", "", ""); err == nil {
		t.Error("dreamEnd with empty note should fail")
	}
}

func TestDreamBeginThenEndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"
	if err := dreamBegin(path, "intent"); err != nil {
		t.Fatal(err)
	}
	if err := dreamEnd(path, "summary", ""); err != nil {
		t.Fatal(err)
	}
	// Pull state with the dreamstate package (already covered by its own tests)
}

func TestDreamMessages(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"
	out, err := dreamBeginEcho(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "dreaming since") {
		t.Errorf("dreamBeginEcho msg: %q", out)
	}
	out, err = dreamEndEcho(path, "summary", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "dream ended") {
		t.Errorf("dreamEndEcho msg: %q", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/eidos/forge/... -run "TestNewDream|TestDream" -v`
Expected: FAIL with `undefined: dreamBegin` etc.

- [ ] **Step 3: Implement**

Create `cmd/eidos/forge/dream.go`:

```go
package forge

import (
	"fmt"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/dreamstate"
	"github.com/spf13/cobra"
)

const dreamStatePath = "/eidos/run/dream-state.json"

func newDreamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dream",
		Short: "Mark the start or end of a dream (memory consolidation)",
	}
	cmd.AddCommand(newDreamBeginCmd())
	cmd.AddCommand(newDreamEndCmd())
	return cmd
}

func newDreamBeginCmd() *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "begin",
		Short: "Mark the start of a dream",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := dreamBeginEcho(dreamStatePath, note)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "optional intent line")
	return cmd
}

func newDreamEndCmd() *cobra.Command {
	var note, prosePath string
	cmd := &cobra.Command{
		Use:   "end",
		Short: "Mark the end of a dream (note required)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := dreamEndEcho(dreamStatePath, note, prosePath)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "one-line summary of what the dream consolidated (required)")
	cmd.Flags().StringVar(&prosePath, "prose-path", "", "relative path to the dream prose under memory/episodic/")
	_ = cmd.MarkFlagRequired("note")
	return cmd
}

func dreamBegin(path, note string) error {
	return dreamstate.Begin(path, time.Now(), note)
}

func dreamEnd(path, note, prosePath string) error {
	return dreamstate.End(path, time.Now(), note, prosePath)
}

// dreamBeginEcho returns the human-readable confirmation line.
func dreamBeginEcho(path, note string) (string, error) {
	prev, _ := dreamstate.Read(path)
	now := time.Now()
	if err := dreamBegin(path, note); err != nil {
		return "", err
	}
	if prev.LastDreamFinishedAt > 0 {
		gap := now.Sub(time.Unix(prev.LastDreamFinishedAt, 0)).Truncate(time.Second)
		return fmt.Sprintf("dreaming since %s\n%s since last dream\n",
			now.Format(time.RFC3339), formatDuration(gap)), nil
	}
	return fmt.Sprintf("dreaming since %s (no prior dream recorded)\n",
		now.Format(time.RFC3339)), nil
}

// dreamEndEcho returns the human-readable confirmation line.
func dreamEndEcho(path, note, prosePath string) (string, error) {
	prev, _ := dreamstate.Read(path)
	now := time.Now()
	if err := dreamEnd(path, note, prosePath); err != nil {
		return "", err
	}
	if prev.LastDreamStartedAt > 0 {
		dur := now.Sub(time.Unix(prev.LastDreamStartedAt, 0)).Truncate(time.Second)
		st, _ := dreamstate.Read(path)
		return fmt.Sprintf("dream ended after %s — recorded as #%d\n",
			formatDuration(dur), st.DreamCount), nil
	}
	st, _ := dreamstate.Read(path)
	return fmt.Sprintf("dream ended (no prior begin) — recorded as #%d\n", st.DreamCount), nil
}
```

- [ ] **Step 4: Wire into cmd.go**

Add `cmd.AddCommand(newDreamCmd())` to the in-container branch of `cmd/eidos/forge/cmd.go` (next to `newPlanInContainerCmd()`).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/eidos/forge/... -run "TestNewDream|TestDream" -v`
Expected: PASS.

### Task 5.7: Commit dream CLI

- [ ] **Step 1: gofmt + vet**

Run: `gofmt -l cmd/eidos/forge && go vet ./cmd/eidos/forge/...`
Expected: clean.

- [ ] **Step 2: Commit**

```bash
git add cmd/eidos/forge/dream.go cmd/eidos/forge/dream_test.go cmd/eidos/forge/cmd.go
git commit -m "$(cat <<'EOF'
feat(forge): dream begin/end (in-container)

Adds the boundary commands a mind-form calls to mark the start and end
of a dream. End requires a one-line --note that the next wake's
context surfaces as last_dream_note. Optional --prose-path is
validated to live under memory/episodic/ so the framework cannot be
abused to point at private essence files.

Refs docs/superpowers/specs/2026-05-09-heartbeat-plans-dreams-design.md §6.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 6 — Supervisor wiring

This phase is the largest because it brings everything together: the scheduler goroutine, crontab rendering, and the agent-runner wake-context computation.

### Task 6.1: Crontab renderer

**Files:**
- Create: `cmd/eidos/supervisor/crontab.go`
- Test: `cmd/eidos/supervisor/crontab_test.go`

- [ ] **Step 1: Write the failing tests**

Create `cmd/eidos/supervisor/crontab_test.go`:

```go
//go:build !windows

package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

func TestRenderCrontabFromConfig_Default(t *testing.T) {
	cfg := config.Config{} // interval empty → default 4h
	got, err := renderCrontab(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := "0 */4 * * * /usr/local/bin/eidos forge wake --reason heartbeat\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderCrontabFromConfig_30m(t *testing.T) {
	cfg := config.Config{Heartbeat: config.HeartbeatConfig{Interval: "30m"}}
	got, err := renderCrontab(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "*/30 * * * * ") {
		t.Errorf("expected */30 prefix, got %q", got)
	}
}

func TestRenderCrontabFromConfig_BadIntervalFallsBack(t *testing.T) {
	cfg := config.Config{Heartbeat: config.HeartbeatConfig{Interval: "90m"}}
	got, err := renderCrontab(cfg)
	if err == nil {
		t.Error("renderCrontab should return error on unsupported interval")
	}
	// got is the default fallback, so the supervisor caller can still
	// install a working crontab even when the operator misconfigured.
	want := "0 */4 * * * /usr/local/bin/eidos forge wake --reason heartbeat\n"
	if got != want {
		t.Errorf("fallback got:\n%s\nwant:\n%s", got, want)
	}
}

func TestInstallCrontabWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crontabs", "eidos")
	if err := installCrontab(path, "*/5 * * * * /bin/true\n"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "*/5 * * * * /bin/true\n" {
		t.Errorf("body = %q", string(body))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/eidos/supervisor/... -run "TestRenderCrontab|TestInstallCrontab" -v`
Expected: FAIL with `undefined: renderCrontab`.

- [ ] **Step 3: Implement**

Create `cmd/eidos/supervisor/crontab.go`:

```go
//go:build !windows

package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
)

// crontabPath is the path the supervisor writes the eidos user's
// crontab to before spawning crond. Matches the busybox crond `-c`
// argument in startChildren.
const crontabPath = "/var/spool/cron/crontabs/eidos"

const crontabHeartbeatLine = "%s /usr/local/bin/eidos forge wake --reason heartbeat\n"

// renderCrontab returns the file body for /var/spool/cron/crontabs/eidos.
// On a malformed [heartbeat] interval the function returns the default
// 4h crontab body AND a non-nil error; the caller decides whether to
// install the fallback. (Heartbeat is rhythm, not authority — losing it
// would leave the mind-form completely silent.)
func renderCrontab(cfg config.Config) (string, error) {
	defaultBody := fmt.Sprintf(crontabHeartbeatLine, "0 */4 * * *")
	interval := cfg.Heartbeat.Interval
	if interval == "" {
		return defaultBody, nil
	}
	d, err := time.ParseDuration(interval)
	if err != nil {
		return defaultBody, fmt.Errorf("parse [heartbeat] interval %q: %w", interval, err)
	}
	cronExpr, err := config.HeartbeatCronExpression(d)
	if err != nil {
		return defaultBody, err
	}
	return fmt.Sprintf(crontabHeartbeatLine, cronExpr), nil
}

// installCrontab writes body to path, creating the parent directory.
// File mode 0o600 matches busybox crond's expected permissions for a
// user crontab (per the comment in run.go's startChildren).
func installCrontab(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir crontabs dir: %w", err)
	}
	return os.WriteFile(path, []byte(body), 0o600)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/eidos/supervisor/... -run "TestRenderCrontab|TestInstallCrontab" -v`
Expected: PASS.

### Task 6.2: Scheduler goroutine

**Files:**
- Create: `cmd/eidos/supervisor/scheduler.go`
- Test: `cmd/eidos/supervisor/scheduler_test.go`

- [ ] **Step 1: Write the failing tests**

Create `cmd/eidos/supervisor/scheduler_test.go`:

```go
//go:build !windows

package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/scheduler"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

func TestPlannerFiresDuePlans(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	// Inject one due plan + one future plan.
	pastPlan := scheduler.Plan{
		V:         scheduler.SchemaVersion,
		ID:        "20260509T000000Z-plan-aaaa",
		At:        now.Add(-time.Minute).Unix(),
		Hint:      "due",
		CreatedAt: now.Add(-2 * time.Minute).Unix(),
	}
	if err := scheduler.WritePlanForTest(dir, pastPlan); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Add(dir, now, "future", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var fired []wake.Signal
	var mu sync.Mutex
	submit := func(_ string, sig wake.Signal) error {
		mu.Lock()
		defer mu.Unlock()
		fired = append(fired, sig)
		return nil
	}

	if err := plannerTick(dir, now, submit); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 1 {
		t.Fatalf("len(fired) = %d, want 1; fired=%v", len(fired), fired)
	}
	if fired[0].Reason != wake.ReasonPlanned {
		t.Errorf("Reason = %q", fired[0].Reason)
	}
	if fired[0].Hint != "due" {
		t.Errorf("Hint = %q", fired[0].Hint)
	}
	if fired[0].Context.PlanID != pastPlan.ID {
		t.Errorf("PlanID = %q", fired[0].Context.PlanID)
	}
}

func TestPlannerMovesPlanToFired(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	pastPlan := scheduler.Plan{
		V:         scheduler.SchemaVersion,
		ID:        "20260509T000000Z-plan-bbbb",
		At:        now.Add(-time.Minute).Unix(),
		Hint:      "x",
		CreatedAt: now.Add(-2 * time.Minute).Unix(),
	}
	if err := scheduler.WritePlanForTest(dir, pastPlan); err != nil {
		t.Fatal(err)
	}
	noopSubmit := func(_ string, _ wake.Signal) error { return nil }
	if err := plannerTick(dir, now, noopSubmit); err != nil {
		t.Fatal(err)
	}
	plans, _ := scheduler.List(dir)
	if len(plans) != 0 {
		t.Errorf("active plans after tick: %d", len(plans))
	}
	firedDir := filepath.Join(dir, "fired")
	entries, _ := readDirNames(firedDir)
	if len(entries) != 1 {
		t.Errorf("fired entries: %v", entries)
	}
}

// readDirNames is a tiny helper for tests.
func readDirNames(dir string) ([]string, error) {
	entries, err := osReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out, nil
}

func TestPlannerLoopRespectsContext(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	noopSubmit := func(_ string, _ wake.Signal) error { return nil }

	done := make(chan error, 1)
	go func() { done <- plannerLoop(ctx, dir, time.Millisecond, noopSubmit) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("plannerLoop returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Error("plannerLoop did not exit on cancel")
	}
}
```

The test imports `osReadDir` — add a tiny shim at the bottom of the test file:

```go
import "os"
func osReadDir(name string) ([]os.DirEntry, error) { return os.ReadDir(name) }
```

It also uses `scheduler.WritePlanForTest`, which doesn't exist. **Add it** as an exported test helper in `internal/scheduler/scheduler.go`:

```go
// WritePlanForTest writes p into dir bypassing Add's bounds. Test-only;
// callers in production should always go through Add.
func WritePlanForTest(dir string, p Plan) error {
	return writePlan(dir, p)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/eidos/supervisor/... -run "TestPlanner" -v`
Expected: FAIL with `undefined: plannerTick`.

- [ ] **Step 3: Implement**

Create `cmd/eidos/supervisor/scheduler.go`:

```go
//go:build !windows

package supervisor

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/scheduler"
	"github.com/LucianoXu/eidopsyche/internal/wake"
)

// PlanScanInterval is how often the supervisor scans /eidos/run/plans for
// due files. Visible to tests; production runs at 30s.
const PlanScanInterval = 30 * time.Second

// plansDir is the in-container path the supervisor watches for plan files.
const supervisorPlansDir = "/eidos/run/plans"

// submitFunc abstracts wake.Submit so tests can capture fires.
type submitFunc func(dir string, sig wake.Signal) error

// plannerLoop runs until ctx is cancelled, scanning dir every interval.
// Each tick fires due plans via submit and renames them into dir/fired/.
func plannerLoop(ctx context.Context, dir string, interval time.Duration, submit submitFunc) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := plannerTick(dir, time.Now(), submit); err != nil {
				log.Printf("planner tick: %v", err)
			}
		}
	}
}

// plannerTick is one pass of the loop; tests call it directly with a
// fixed clock.
func plannerTick(dir string, now time.Time, submit submitFunc) error {
	due, err := scheduler.ScanDue(dir, now)
	if err != nil {
		return fmt.Errorf("scan due: %w", err)
	}
	for _, p := range due {
		sig := wake.Signal{
			V:           wake.SchemaVersion,
			ID:          fmt.Sprintf("%d-%s", now.Unix(), wake.ReasonPlanned),
			Reason:      wake.ReasonPlanned,
			TriggeredAt: now.Unix(),
			Hint:        p.Hint,
			Context: wake.Context{
				PlanID: p.ID,
			},
		}
		// Submit before MarkFired: if Submit fails we want the plan
		// to remain active so the next tick retries. wake.Submit is
		// idempotent under coalescing, so a re-fire is harmless.
		if err := submit(wakeDir, sig); err != nil {
			log.Printf("planner submit %s: %v", p.ID, err)
			continue
		}
		if err := scheduler.MarkFired(dir, p.ID); err != nil {
			log.Printf("planner mark-fired %s: %v", p.ID, err)
			// fall through; next tick will see the still-active file
			// and re-submit. Coalescing absorbs the duplicate.
		}
		log.Printf("planner: fired plan %s — %s", p.ID, p.Hint)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/eidos/supervisor/... -run "TestPlanner" -v`
Expected: PASS.

### Task 6.3: Wire crontab + planner into PID-1 startup

**Files:**
- Modify: `cmd/eidos/supervisor/run.go`

- [ ] **Step 1: Read the current `startChildren` body**

Run: `cat cmd/eidos/supervisor/run.go | head -80`
Expected: see `startChildren` and `watchWakes`.

- [ ] **Step 2: Modify `startChildren` to render the crontab and start the planner**

Replace `startChildren` body with:

```go
// startChildren spawns long-running children (crond + gate daemon)
// after rendering the crontab from config and starts the planner
// goroutine that fires due plans.
func startChildren(ctx context.Context, sp ChildSpawner) error {
	cfg, err := config.Load(filepath.Join(gateDir, "config.toml"))
	if err != nil {
		log.Printf("supervisor: config load: %v (continuing with defaults)", err)
		cfg = config.Defaults()
	}
	if err := config.ValidateMindFormConfig(cfg); err != nil {
		log.Printf("supervisor: config validation: %v (continuing with defaults where possible)", err)
	}

	body, rerr := renderCrontab(cfg)
	if rerr != nil {
		log.Printf("supervisor: crontab render: %v (using default 4h)", rerr)
	}
	if ierr := installCrontab(crontabPath, body); ierr != nil {
		return fmt.Errorf("install crontab: %w", ierr)
	}

	// Per existing comment: crond must be root, crontab must be root-owned.
	if err := sp.Spawn(ctx, "sudo", "-n", "crond", "-f", "-c", "/var/spool/cron/crontabs"); err != nil {
		return err
	}
	if err := sp.Spawn(ctx, "eidos", "gate", "daemon", "--state-dir", gateDir); err != nil {
		return err
	}

	// Start the planner goroutine. Errors are logged inside; cancellation
	// of ctx is the only way out.
	go func() {
		if err := plannerLoop(ctx, supervisorPlansDir, PlanScanInterval, wake.Submit); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("planner exited: %v", err)
		}
	}()
	return nil
}
```

Required new imports for `run.go`:
- `errors` (for `errors.Is`)
- `github.com/LucianoXu/eidopsyche/internal/config`

(You may need to drop the now-unused inline crontab comment from `startChildren`'s old body.)

- [ ] **Step 3: Build and confirm no other call site broke**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 4: Run all supervisor tests**

Run: `go test ./cmd/eidos/supervisor/... -v`
Expected: PASS, including the existing supervisor tests untouched.

### Task 6.4: Extend `agent-runner` wake-context computation

**Files:**
- Modify: `cmd/eidos/supervisor/agent_runner.go`
- Test: `cmd/eidos/supervisor/agent_runner_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `cmd/eidos/supervisor/agent_runner_test.go`:

```go
func TestComputeContext_QuietHours(t *testing.T) {
	cfg := config.Config{
		MindForm: config.MindFormConfig{
			QuietStart: "22:00",
			QuietEnd:   "06:00",
			TZ:         "UTC",
		},
	}
	now := time.Date(2026, 5, 9, 23, 0, 0, 0, time.UTC)
	ctx := computeContext(wake.Signal{Context: wake.Context{}}, cfg, dreamstate.State{}, now)
	if !ctx.MasterLikelyAsleep {
		t.Error("23:00 UTC should be in 22:00..06:00 UTC quiet window")
	}
}

func TestComputeContext_DreamEligibleWhenNeverDreamt(t *testing.T) {
	cfg := config.Config{}
	now := time.Now()
	ctx := computeContext(wake.Signal{Context: wake.Context{}}, cfg, dreamstate.State{}, now)
	if !ctx.DreamEligible {
		t.Error("never dreamt → DreamEligible should be true")
	}
	if ctx.SinceLastDreamSeconds != 0 {
		t.Errorf("SinceLastDreamSeconds = %d", ctx.SinceLastDreamSeconds)
	}
}

func TestComputeContext_DreamNotEligibleRecently(t *testing.T) {
	cfg := config.Config{
		MindForm: config.MindFormConfig{DreamMinInterval: "12h"},
	}
	now := time.Now()
	ds := dreamstate.State{LastDreamFinishedAt: now.Add(-3 * time.Hour).Unix()}
	ctx := computeContext(wake.Signal{Context: wake.Context{}}, cfg, ds, now)
	if ctx.DreamEligible {
		t.Error("3h since last dream + 12h floor → not eligible")
	}
	if ctx.SinceLastDreamSeconds < 3*3600-2 || ctx.SinceLastDreamSeconds > 3*3600+2 {
		t.Errorf("SinceLastDreamSeconds = %d", ctx.SinceLastDreamSeconds)
	}
}

func TestComputeContext_PlanIDPropagated(t *testing.T) {
	now := time.Now()
	sig := wake.Signal{
		Reason: wake.ReasonPlanned,
		Context: wake.Context{
			PlanID: "20260509T123000Z-plan-7f2e",
		},
	}
	ctx := computeContext(sig, config.Config{}, dreamstate.State{}, now)
	if ctx.PlanID != "20260509T123000Z-plan-7f2e" {
		t.Errorf("PlanID = %q", ctx.PlanID)
	}
}
```

Add `dreamstate` import.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/eidos/supervisor/... -run "TestComputeContext" -v`
Expected: FAIL with `undefined: computeContext`.

- [ ] **Step 3: Implement**

In `cmd/eidos/supervisor/agent_runner.go`, add after `runAgent`:

```go
import "github.com/LucianoXu/eidopsyche/internal/dreamstate"

// computeContext folds quiet-hours, dream-state, and plan-id into the
// signal's Context. Pure function so it's testable without IO.
func computeContext(sig wake.Signal, cfg config.Config, ds dreamstate.State, now time.Time) wake.Context {
	ctx := sig.Context

	if cfg.MindForm.QuietStart != "" && cfg.MindForm.QuietEnd != "" {
		tz := time.UTC
		if cfg.MindForm.TZ != "" {
			if loc, err := time.LoadLocation(cfg.MindForm.TZ); err == nil {
				tz = loc
			}
		}
		ctx.MasterLikelyAsleep = config.InQuietHours(now, cfg.MindForm.QuietStart, cfg.MindForm.QuietEnd, tz)
	}

	if ds.LastDreamFinishedAt > 0 {
		ctx.SinceLastDreamSeconds = now.Unix() - ds.LastDreamFinishedAt
		floor := config.DefaultDreamMinInterval
		if s := cfg.MindForm.DreamMinInterval; s != "" {
			if d, err := time.ParseDuration(s); err == nil {
				floor = d
			}
		}
		ctx.DreamEligible = ctx.SinceLastDreamSeconds >= int64(floor.Seconds())
	} else {
		ctx.DreamEligible = true
	}

	// PlanID flows through unchanged from the wake signal.
	return ctx
}
```

Need to add `"github.com/LucianoXu/eidopsyche/internal/config"` to imports if not present.

- [ ] **Step 4: Wire into `runAgent`**

Replace the existing wake-message construction in `runAgent` (currently uses raw `sig.Context.InboxUnread` etc.) so it flows through `computeContext`:

Before the `msg := buildWakeMessage(...)` line, add:

```go
cfgPath := filepath.Join(filepath.Dir(filepath.Dir(wakeFile)), "gate", "config.toml")
// wakeFile lives at /eidos/run/wake/active.json; we want /eidos/gate/config.toml.
// Use the constant gateConfigPath instead — it's the canonical path.
cfg, _ := config.Load(gateConfigPath)
ds, _ := dreamstate.Read(dreamStateRuntimePath)
sig.Context = computeContext(sig, cfg, ds, time.Now())
```

Add the constant near the top of `agent_runner.go`:

```go
const dreamStateRuntimePath = "/eidos/run/dream-state.json"
```

(Drop the unused `cfgPath` variable; only the canonical constant is needed.)

Then update `buildWakeMessage` call to also see the new fields:

Replace:

```go
msg := buildWakeMessage(wakePromptInput{
    Reason:               string(sig.Reason),
    Hint:                 sig.Hint,
    InboxUnread:          sig.Context.InboxUnread,
    SinceLastWakeSeconds: sig.Context.SinceLastWakeSeconds,
})
```

with:

```go
msg := buildWakeMessage(wakePromptInput{
    Reason:                string(sig.Reason),
    Hint:                  sig.Hint,
    InboxUnread:           sig.Context.InboxUnread,
    SinceLastWakeSeconds:  sig.Context.SinceLastWakeSeconds,
    MasterLikelyAsleep:    sig.Context.MasterLikelyAsleep,
    QuietStart:            cfg.MindForm.QuietStart,
    QuietEnd:              cfg.MindForm.QuietEnd,
    TZ:                    cfg.MindForm.TZ,
    SinceLastDreamSeconds: sig.Context.SinceLastDreamSeconds,
    DreamEligible:         sig.Context.DreamEligible,
    LastDreamNote:         ds.LastDreamNote,
    PlanID:                sig.Context.PlanID,
})
```

- [ ] **Step 5: Extend `wakePromptInput` and `buildWakeMessage`**

Update the `wakePromptInput` struct:

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
}
```

Replace `buildWakeMessage` body:

```go
func buildWakeMessage(in wakePromptInput) string {
	var sb strings.Builder
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

func tzSuffix(tz string) string {
	if tz == "" {
		return ""
	}
	return ", " + tz
}
```

Update existing `TestBuildWakeMessage` to construct via the new struct fields (it should still pass; new fields default to zero/false which produce empty clauses).

- [ ] **Step 6: Run all supervisor tests**

Run: `go test ./cmd/eidos/supervisor/... -v`
Expected: PASS.

### Task 6.5: Commit Phase 6

- [ ] **Step 1: gofmt + vet**

Run: `gofmt -l cmd/eidos/supervisor && go vet ./cmd/eidos/supervisor/...`
Expected: clean.

- [ ] **Step 2: Commit**

```bash
git add cmd/eidos/supervisor/
git commit -m "$(cat <<'EOF'
feat(supervisor): planner goroutine + crontab render + dream/quiet wake context

Three coupled changes that wire Phases 1-5 into PID-1 startup:

- renderCrontab + installCrontab: read [heartbeat] interval from config
  at startup and write /var/spool/cron/crontabs/eidos before spawning
  crond. A bad interval falls back to the default 4h with a logged
  warning rather than leaving the mind-form silent.
- plannerLoop: a 30s-tick goroutine that scans /eidos/run/plans for
  due files and submits planned wakes via wake.Submit. MarkFired
  failures are logged but harmless because wake-coalescing absorbs
  duplicates.
- computeContext: agent-runner now folds quiet-hours and dream-state
  into the wake context before invoking claude. Wake-message string
  surfaces the new fields conditionally.

Refs docs/superpowers/specs/2026-05-09-heartbeat-plans-dreams-design.md §4-7.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 7 — CLAUDE.md template + status surface

### Task 7.1: Add the dream-practice section to the template

**Files:**
- Modify: `internal/ontology/template/CLAUDE.md`

- [ ] **Step 1: Read the current template**

Run: `cat internal/ontology/template/CLAUDE.md`
Expected: see existing content; identify the bottom of the file.

- [ ] **Step 2: Append the new section**

Append to `internal/ontology/template/CLAUDE.md`:

```markdown

## Heartbeat, plans, and dreams

You wake periodically. The wake context tells you why and when. Three rhythms shape your time:

- **HeartBeat** is your default cadence. Every few hours, you wake with no specific
  errand. Decide what to do: catch up on inbox, attend to a thread you left open,
  rest, dream, or simply set a plan and sleep again.

- **Plan signals** are wakes you schedule for yourself. If a thread will need
  follow-up in two hours, or you want to check on something tomorrow morning, run
  `eidos forge plan add --in 2h --hint "<one-line reminder>"`. Use plans sparingly;
  too many is noise.

- **Dreams** are voluntary consolidation. The wake context will tell you when one
  is appropriate (`dream_eligible: true`, master likely asleep, inbox quiet).
  During a dream you do not respond to the outside. You re-read recent episodic
  logs, distill recurring patterns into `memory/semantic/`, form or revise a
  `.claude/skills/<name>.md` if a method has crystallized, and write a single
  prose paragraph in `memory/episodic/<YYYY>/<MM>/dream-<NNN>.md` in your own
  voice. End with a git commit.

  Mark the boundaries:
    eidos forge dream begin
    ... your consolidation work ...
    eidos forge dream end --note "<one-line>" --prose-path <path-to-prose>

  Don't dream more than once per wake. If your master messages you mid-dream, you
  may finish the dream first or stop and reply — there is no rule.
```

- [ ] **Step 3: Confirm the template still builds via go test**

Run: `go test ./internal/ontology/...`
Expected: existing template tests still PASS.

### Task 7.2: Extend `forge status` output

**Files:**
- Modify: `cmd/eidos/forge/status.go`
- Test: `cmd/eidos/forge/status_test.go`

- [ ] **Step 1: Read existing status.go**

Run: `cat cmd/eidos/forge/status.go`
Expected: see how the host-side status command shells out for the in-container view.

- [ ] **Step 2: Decide where the new lines go**

The status command runs `docker exec` of an in-container summary. The cleanest extension is to add a new in-container `forge status-detail` command that prints the new lines, OR add the new lines to whatever in-container summary is currently rendered. We extend `forge status-detail` (a new in-container subcommand) for clarity.

Skip ahead to step 3 — the task creates `forge status-detail` for the in-container side and modifies the host `forge status` to append its output.

- [ ] **Step 3: Add `forge status-detail` in-container subcommand**

In `cmd/eidos/forge/status.go` (or a new sibling file `status_detail.go` if status.go is large), add:

```go
func newStatusDetailCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "status-detail",
		Short:  "Internal: print plans + dreams summary lines for forge status",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tz, _ := loadMindFormTZ()
			plans, _ := scheduler.List(plansDir)
			ds, _ := dreamstate.Read(dreamStatePath)
			now := time.Now()

			// Plans line
			if len(plans) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Plans:    none")
			} else {
				next := plans[0]
				for _, p := range plans {
					if p.At < next.At {
						next = p
					}
				}
				at := time.Unix(next.At, 0).In(tz)
				fmt.Fprintf(cmd.OutOrStdout(), "Plans:    %d active   (next: %s — %s)\n",
					len(plans), at.Format(time.RFC3339), next.Hint)
			}

			// Dreams line
			if ds.DreamCount == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Dreams:   none yet")
			} else {
				gap := now.Sub(time.Unix(ds.LastDreamFinishedAt, 0)).Truncate(time.Second)
				note := ds.LastDreamNote
				if note == "" {
					note = "(no note)"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Dreams:   %d total    (last: %s ago, %q)\n",
					ds.DreamCount, formatDuration(gap), note)
			}
			if ds.CurrentlyDreaming {
				fmt.Fprintln(cmd.OutOrStdout(), "Dream:    currently dreaming")
			}
			return nil
		},
	}
}
```

Imports needed: `time`, `github.com/LucianoXu/eidopsyche/internal/scheduler`, `github.com/LucianoXu/eidopsyche/internal/dreamstate`.

- [ ] **Step 4: Register `status-detail` in cmd.go**

Add `cmd.AddCommand(newStatusDetailCmd())` to the in-container branch of `cmd/eidos/forge/cmd.go`.

- [ ] **Step 5: Modify host-side `forge status` to append the detail output**

Find the current host-side `forge status` command. It already does some `docker exec` for the in-container `whoami`. After printing the existing status output, add a call to exec `eidos forge status-detail` and print the result.

The exact diff depends on existing structure. Pseudocode:

```go
// after existing status output
res, _ := c.ContainerExec(ctx, forgectl.ContainerName(name),
    []string{"eidos", "forge", "status-detail"})
if len(res.Stdout) > 0 {
    fmt.Print(string(res.Stdout))
}
```

- [ ] **Step 6: Add a test for the new subcommand**

Append to `cmd/eidos/forge/status_test.go`:

```go
func TestStatusDetail_NoPlansNoDreams(t *testing.T) {
	// Override the in-container constants temporarily.
	oldPlans := plansDir
	oldDream := dreamStatePath
	plansDir = t.TempDir()
	dreamStatePath = filepath.Join(t.TempDir(), "dream-state.json")
	defer func() {
		plansDir = oldPlans
		dreamStatePath = oldDream
	}()
	cmd := newStatusDetailCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "Plans:    none") {
		t.Errorf("missing plans-none line: %q", out)
	}
	if !strings.Contains(out, "Dreams:   none yet") {
		t.Errorf("missing dreams-none line: %q", out)
	}
}
```

The test mutates package-level constants. To make this work, change the constants from `const` to `var` in `plan.go` and `dream.go`:

In `cmd/eidos/forge/plan.go` change:
```go
const plansDir = "/eidos/run/plans"
```
to:
```go
var plansDir = "/eidos/run/plans"
```

In `cmd/eidos/forge/dream.go` change:
```go
const dreamStatePath = "/eidos/run/dream-state.json"
```
to:
```go
var dreamStatePath = "/eidos/run/dream-state.json"
```

(The `var` indirection only affects tests; production behavior is identical.)

- [ ] **Step 7: Run the new test**

Run: `go test ./cmd/eidos/forge/... -run TestStatusDetail -v`
Expected: PASS.

### Task 7.3: Drop the static crontab file

**Files:**
- Delete: `docker/mindform/crontab`
- Modify: `docker/mindform/Dockerfile`

- [ ] **Step 1: Find and remove the COPY line**

Run: `grep -n "crontab" docker/mindform/Dockerfile`
Expected: see one or more lines mentioning the crontab.

- [ ] **Step 2: Edit the Dockerfile**

Remove the `COPY docker/mindform/crontab /var/spool/cron/crontabs/eidos` line and any chown/chmod that referred specifically to it. The renderer in `crontab.go` now writes the file at runtime under root via the same sudo path.

- [ ] **Step 3: Delete the file**

Run: `git rm docker/mindform/crontab`
Expected: the file is staged for deletion.

- [ ] **Step 4: Build everything**

Run: `go build ./...`
Expected: clean build.

### Task 7.4: Commit Phase 7

- [ ] **Step 1: gofmt + vet**

Run: `gofmt -l . && go vet ./...`
Expected: clean.

- [ ] **Step 2: Commit**

```bash
git add internal/ontology/template/CLAUDE.md cmd/eidos/forge/ docker/mindform/Dockerfile
git rm docker/mindform/crontab 2>/dev/null || true
git commit -m "$(cat <<'EOF'
feat(forge,template): dream-practice section + status plans/dreams lines

Adds the three-rhythms section to the embedded mind-form constitution
so claude knows when to consider dreaming and how to mark the
boundaries. Extends `eidos forge status` with two new lines (Plans,
Dreams) plus a "currently dreaming" marker. Drops the static
docker/mindform/crontab now that supervisor renders it at runtime.

Refs docs/superpowers/specs/2026-05-09-heartbeat-plans-dreams-design.md §6.4 and §8.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 8 — Integration test + deploy-test script

### Task 8.1: Stubbed claude binary for integration tests

**Files:**
- Create: `test/integration/testdata/claude_stub.sh`

- [ ] **Step 1: Create the stub**

```bash
#!/bin/sh
# Minimal stub of `claude` for forge_plan_dream integration tests.
# Echoes its -p argument and exits 0. Swallows other flags.
while [ $# -gt 0 ]; do
  case "$1" in
    -p)
      shift
      printf '%s\n' "$1"
      shift
      ;;
    --append-system-prompt|--model)
      shift; shift
      ;;
    --dangerously-skip-permissions)
      shift
      ;;
    *)
      shift
      ;;
  esac
done
exit 0
```

Run: `chmod +x test/integration/testdata/claude_stub.sh`

### Task 8.2: Integration test for plan + dream

**Files:**
- Create: `test/integration/forge_plan_dream_test.go`

- [ ] **Step 1: Read the existing forge_smoke pattern**

Run: `head -40 test/integration/dashboard_lifecycle_phase5_test.go`
Expected: see how integration tests assemble Docker setups.

- [ ] **Step 2: Write the test**

Create `test/integration/forge_plan_dream_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPlanFiresInContainer exercises the supervisor's planner goroutine
// against a real container running our stubbed claude. The test takes
// roughly 2 minutes (60s for plan to come due + 30s for the next planner
// tick + headroom).
func TestPlanFiresInContainer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	name := "eidos-it-plan-" + strings.ReplaceAll(t.Name(), "/", "-")
	cleanup := func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
		_ = exec.Command("docker", "volume", "rm", "eidos-mindform-"+name).Run()
	}
	cleanup()
	t.Cleanup(cleanup)

	// Spin up a mind-form with --no-login and a 1m heartbeat for fast
	// signal-of-life. Operator gate setup mirrors forge_smoke.sh.
	stub, _ := filepath.Abs("testdata/claude_stub.sh")
	t.Logf("claude stub at %s", stub)

	t.Skip("Integration scaffold present; run requires the matching " +
		"mind-form image with the claude stub mounted, which the smoke " +
		"deploy-test exercises end-to-end. CI will pick this up once " +
		"the image-stub harness lands (tracked separately).")
	_ = json.Unmarshal // keep imports referenced
}
```

Note: this test is intentionally a scaffolding stub that `t.Skip`s. The full Docker harness for stubbing `claude` inside a real mindform image is non-trivial and is not the highest-leverage piece for this PR. The deploy-test script in Task 8.3 is the operator-level end-to-end. We commit the scaffold so future work has a clear hook.

(If the engineer wants to wire the full harness now: build a tag `eidos-mindform:test` that mounts the claude stub into `/usr/local/bin/claude`, create a fresh state-dir for the host gate, run `eidos forge create --no-login`, exec `eidos forge plan add --in 90s`, sleep 100s, and assert via docker exec that `plans/fired/` is non-empty.)

- [ ] **Step 3: Confirm it compiles**

Run: `go test -tags=integration ./test/integration/... -run TestPlanFires -v`
Expected: SKIP (compiles cleanly, passes via t.Skip).

### Task 8.3: Deployment test script

**Files:**
- Create: `deploy-test/test_script_heartbeat_plans_dreams.sh`

- [ ] **Step 1: Verify `forge create --no-login` exists**

Run: `grep -n "no-login\|noLogin" cmd/eidos/forge/create.go`
Expected: confirm the flag exists.

If it does not, add it as a separate sub-task (it's small — a `--no-login` bool flag that skips the `claude /login` step in `forge create`'s orchestration).

- [ ] **Step 2: Create the script**

Create `deploy-test/test_script_heartbeat_plans_dreams.sh`:

```bash
#!/usr/bin/env bash
#
# Deployment test for the heartbeat-plans-dreams PR.
#
# What this script does (in order):
#   1. Builds eidos from the current worktree.
#   2. Stands up a fully isolated host gate + relay using --state-dir.
#   3. Creates a disposable mindform "dt-alice" with --no-login.
#   4. Patches its config.toml inside the volume to enable a 1m
#      heartbeat, all-day quiet hours, and a 30s dream cooldown so we
#      can exercise eligibility quickly.
#   5. Starts the container.
#   6. Submits a plan via `forge plan add --in 90s` and waits for it
#      to fire (verified via plans/fired/).
#   7. Drives `forge dream begin/end` and asserts dream-state.json
#      reflects it.
#   8. Forces a manual wake and asserts the wake context surfaces the
#      new dream fields.
#   9. Stops + purges the disposable mindform.
#
# Isolation properties:
#   - Uses --state-dir under deploy-test/dt-forge-host so the user's
#     ~/.config/eidos and running daemon are untouched.
#   - Listens on 127.0.0.1:22897 (separate from the user's typical
#     22895/22896 ports used by alice/bob smoke tests).
#   - Uses a unique mindform name "dt-alice" to avoid colliding with
#     any production mindform.
#
# Prereqs: docker daemon running, Go toolchain, no other process
# bound to 127.0.0.1:22897.
#
# Cleanup: trap on EXIT plus an explicit `forge purge --yes`.

set -euo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
DT="$REPO/deploy-test/dt-forge-host"
NAME=dt-alice
RELAY=ws://127.0.0.1:22897

cd "$REPO"

echo "==> Building eidos"
go build -o "$REPO/bin/eidos" ./cmd/eidos
export PATH="$REPO/bin:$PATH"

echo "==> Cleaning previous run"
rm -rf "$DT"
docker rm -f "eidos-mindform-$NAME" 2>/dev/null || true
docker volume rm "eidos-mindform-$NAME" 2>/dev/null || true

echo "==> Starting isolated host gate + relay"
eidos gate --state-dir "$DT" init \
  --label dt-host \
  --home "$RELAY" \
  --with-local-relay \
  --listen 127.0.0.1:22897
eidos gate --state-dir "$DT" relay  &
RELPID=$!
eidos gate --state-dir "$DT" daemon &
DMNPID=$!
trap 'kill $RELPID $DMNPID 2>/dev/null || true; rm -rf "$DT"' EXIT

# Give the daemon a moment to come up.
sleep 2
HOST_NPUB=$(eidos gate --state-dir "$DT" whoami | awk '/Npub:/{print $2}')
echo "Host npub: $HOST_NPUB"

echo "==> Creating disposable mindform $NAME (--no-login)"
eidos forge --state-dir "$DT" create "$NAME" \
  --owner "$HOST_NPUB" \
  --relay "$RELAY" \
  --no-login

echo "==> Patching mindform config.toml for fast iteration"
docker run --rm -v "eidos-mindform-$NAME:/eidos" alpine sh -c '
  cat >> /eidos/gate/config.toml <<EOF

[heartbeat]
interval = "1m"

[mindform]
quiet_start = "00:00"
quiet_end = "23:59"
tz = "UTC"
dream_min_interval = "30s"
EOF
'

echo "==> Starting mindform"
eidos forge --state-dir "$DT" start "$NAME"
sleep 5

echo "==> Submitting plan (--in 90s)"
docker exec "eidos-mindform-$NAME" eidos forge plan add --in 90s --hint "deploy-test"

echo "==> Waiting 100s for plan to fire"
sleep 100

echo "==> Asserting plan moved to fired/"
FIRED=$(docker exec "eidos-mindform-$NAME" sh -c '
  ls /eidos/run/plans/fired/ 2>/dev/null | wc -l
' | tr -d '[:space:]')
if [ "$FIRED" = "0" ]; then
  echo "FAIL: no plan in /eidos/run/plans/fired/"
  exit 1
fi
echo "OK: $FIRED plan(s) in fired/"

echo "==> Driving dream begin/end"
docker exec "eidos-mindform-$NAME" eidos forge dream begin --note "deploy-test-intent"
docker exec "eidos-mindform-$NAME" eidos forge dream end \
  --note "consolidated dt"

echo "==> Asserting dream-state.json"
docker exec "eidos-mindform-$NAME" sh -c '
  grep -q "\"dream_count\": *1" /eidos/run/dream-state.json
' || { echo "FAIL: dream_count != 1"; exit 1; }
docker exec "eidos-mindform-$NAME" sh -c '
  grep -q "consolidated dt" /eidos/run/dream-state.json
' || { echo "FAIL: dream note missing"; exit 1; }
echo "OK: dream-state.json correct"

echo "==> Forcing manual wake and inspecting context"
docker exec "eidos-mindform-$NAME" eidos forge wake --reason manual --hint "dt-final"
sleep 3
docker exec "eidos-mindform-$NAME" sh -c '
  for f in /eidos/run/wake/active.json /eidos/run/wake/pending.json; do
    [ -f "$f" ] || continue
    cat "$f"
  done
' | grep -q "since_last_dream_seconds" \
  || { echo "FAIL: wake context lacks since_last_dream_seconds"; exit 1; }
echo "OK: wake context surfaces dream fields"

echo "==> Cleaning up disposable mindform"
eidos forge --state-dir "$DT" stop "$NAME"
eidos forge --state-dir "$DT" purge "$NAME" --yes

echo "==> ALL CHECKS PASSED"
```

- [ ] **Step 3: Make it executable**

Run: `chmod +x deploy-test/test_script_heartbeat_plans_dreams.sh`

- [ ] **Step 4: Commit Phase 8**

Run: `gofmt -l . && go vet ./...`
Expected: clean.

```bash
git add test/integration/ deploy-test/test_script_heartbeat_plans_dreams.sh
git commit -m "$(cat <<'EOF'
test(forge): integration scaffold + deploy-test for plans/dreams

- test/integration/forge_plan_dream_test.go: scaffold (currently
  t.Skip) for the future in-CI Docker harness with a stubbed claude
  binary. Compiles and is discoverable.
- test/integration/testdata/claude_stub.sh: minimal stub binary that
  echoes -p and exits zero — used by the future harness above.
- deploy-test/test_script_heartbeat_plans_dreams.sh: operator-runnable
  end-to-end test using --state-dir isolation. Builds eidos fresh,
  spins up an isolated host gate + relay on 127.0.0.1:22897, creates
  a disposable mindform with --no-login, patches its config for fast
  iteration (1m heartbeat, all-day quiet, 30s dream cooldown), then
  exercises plan firing, dream begin/end, and wake-context propagation.
  Tear-down on trap + explicit forge purge.

Refs docs/superpowers/specs/2026-05-09-heartbeat-plans-dreams-design.md §10.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 9 — CI parity, push, open PR

### Task 9.1: Run the full local CI suite

- [ ] **Step 1: gofmt**

Run: `gofmt -l .`
Expected: empty output. If not, run `gofmt -w .` and re-check.

- [ ] **Step 2: go vet**

Run: `go vet ./...`
Expected: exit 0.

- [ ] **Step 3: staticcheck**

Run: `staticcheck ./...` (if installed; else `go install honnef.co/go/tools/cmd/staticcheck@latest` first).
Expected: no findings. Address any that surface.

- [ ] **Step 4: Unit tests**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Integration tests (if Docker available)**

Run: `go test -tags=integration ./test/integration/...`
Expected: PASS (the new test is t.Skip).

### Task 9.2: Push and open the PR

- [ ] **Step 1: Confirm clean status**

Run: `git status`
Expected: nothing to commit, branch ahead of main.

- [ ] **Step 2: Push the branch**

Run: `git push -u origin feat/heartbeat-plans-dreams`

- [ ] **Step 3: Open the PR**

```bash
gh pr create --title "feat(forge): heartbeat config, plan signals, dream cycle" --body "$(cat <<'EOF'
## Summary

Three coupled additions to MindForge v0 that give a mind-form autonomous time:

- **HeartBeat**: per-mindform configurable interval with minute precision; crontab rendered from `[heartbeat] interval` config at PID-1 startup; quiet-hours config feeds the wake context as a hint.
- **Plan signals**: agent-authored future wakes via `eidos forge plan add`; supervisor goroutine fires due plans every 30s; host operator gets `plan list/cancel`.
- **Dream cycle**: voluntary consolidation surfaced through `eidos forge dream begin/end`; dream-state.json round-trips; wake context surfaces `master_likely_asleep` / `dream_eligible` / `since_last_dream_seconds`; CLAUDE.md template teaches the practice.

Spec: `docs/superpowers/specs/2026-05-09-heartbeat-plans-dreams-design.md`
Plan: `docs/superpowers/plans/2026-05-09-heartbeat-plans-dreams.md`

## Test plan

- [x] gofmt -l . / go vet ./... / staticcheck ./... clean
- [x] go test ./... passes (unit)
- [x] go test -tags=integration ./test/integration/... passes
- [ ] Manual: deploy-test/test_script_heartbeat_plans_dreams.sh ALL CHECKS PASSED on selene
- [ ] Manual: claude actually receives the new wake-context fields in a real wake

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 4: Watch CI**

Run: `gh pr checks --watch`
Expected: all checks pass.

- [ ] **Step 5: Print PR URL**

Run: `gh pr view --json url -q .url`
Expected: prints the PR URL for the user.

---

## Self-review checklist (run after writing the plan)

1. **Spec coverage:** Each spec section has at least one task — §3 layout (Phases 2, 3, 6.1), §4 wake protocol (Phase 1, 6.4), §5 plan signals (Phases 2, 5), §6 dream cycle (Phases 3, 5), §7 heartbeat config (Phases 4, 6.1), §8 status surface (Phase 7), §10 testing (Phase 8), §11 migration (verified by Phase 7 dropping the static crontab + Phase 6 fallback logic). ✅
2. **Placeholder scan:** Search for "TBD", "TODO", vague "appropriate". None found. ✅
3. **Type consistency:** `wake.Reason`, `Plan`, `Plan.ID`, `Plan.At`, `dreamstate.State`, `config.Config{Heartbeat, MindForm}`, `wakePromptInput`, `submitFunc`, `plannerLoop` — names line up across phases. ✅
