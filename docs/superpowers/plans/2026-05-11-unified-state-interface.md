# Unified State Interface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the unified `state.get` + `Mutate` framework + container PID-1 merger so heartbeat hot-reload works (no docker restart) and operator/mindform share one IPC method table against one state schema.

**Architecture:** β model from the spec. Container PID-1 is a single Go process combining IPC + state authority + lifecycle goroutines; host gate daemon is the same core minus lifecycle. New packages `internal/state` (tree + apply registry), `internal/cron` (render + install), `internal/lifecycle` (wake/planner/agent/dream). Existing `internal/daemon` gains `Mutate` helper + `state.get` method; all read methods removed in favor of state.get; all mutations refactored to funnel through Mutate.

**Tech Stack:** Go 1.25, fsnotify, busybox crond, BurntSushi/toml, existing IPC unix-socket protocol.

**Reference spec:** `docs/superpowers/specs/2026-05-11-unified-state-interface-design.md`

---

## File structure (locked in here, referenced by tasks)

### New files
```
internal/cron/
├── render.go        — RenderCrontab(interval string) (string, error)
├── render_test.go
├── install.go       — Install(ctx, body string) error (sudo tee + chmod)
└── install_test.go

internal/state/
├── tree.go          — Tree + StateContributor interface + Snapshot(path)
├── tree_test.go
├── apply.go         — ApplyRegistry + ApplyFunc + Rollbacker
└── apply_test.go

internal/lifecycle/
├── lifecycle.go     — Lifecycle struct implementing daemon.Lifecycle interface
├── deps.go          — ApplyDeps (concrete shape for container apply hooks)
├── submitter.go     — Submitter (thin wrapper over wake.Submit)
├── wake.go          — moved from cmd/eidos/supervisor/run.go (watchWakes etc.)
├── planner.go       — moved from cmd/eidos/supervisor (plannerLoop etc.)
├── agent.go         — moved from cmd/eidos/supervisor (runAgentForWake etc.)
├── dream.go         — moved from cmd/eidos/supervisor (dream-state helpers)
├── crontab.go       — initial-render-at-startup; small wrapper over internal/cron
├── attach.go        — Attach(d) registers contributors + apply hooks
└── *_test.go
```

### Modified files (key ones)
```
internal/config/keys.go               — add Context bitmask + Contexts field on Key
internal/daemon/daemon.go             — Daemon gains applyRegistry, Tree, Context, ApplyDeps
internal/daemon/methods.go            — methodTable: register state.get, drop read methods
internal/daemon/methods_state.go      — NEW: stateGet handler
internal/daemon/mutate.go             — NEW: Daemon.Mutate helper
internal/daemon/methods_*.go          — refactor each mutation to use Mutate
internal/daemon/dashboard_adapter.go  — migrate reads to state.get
cmd/eidos/gate/state.go               — NEW: `eidos gate state [path]` CLI
cmd/eidos/gate/{config,contact,inbox,relays,whoami,status,...}.go  — facades over state.get
cmd/eidos/supervisor/run.go           — calls daemon.Run with Options{Lifecycle: lifecycle.New()}
cmd/eidos/forge/config.go             — drop docker restart step
cmd/eidos/forge/{status,runtime_state,whoami,inbox,memory,ontology_status}.go  — state.get facades
internal/ipc/protocol.go              — add ErrContextMismatch, ErrPathNotFound, ErrInconsistent
CLAUDE.md                             — add operator/mindform symmetry paragraph
SPEC.md                               — update the heartbeat section to reflect hot-reload
deploy-test/003-mindform-heartbeat/wizard-orchestrator.py — add StartedAt + state.get assertions
```

### Files deleted (cleanup)
```
cmd/eidos/forge/runtime_state.go (logic moves; in-container `forge runtime-state` becomes a state.get facade in a renamed file or stays as a thin wrapper — see Task E.3)
cmd/eidos/supervisor/crontab.go (logic moves to internal/cron)
```

---

## Execution order

Six phases. Each phase ends with: `go vet ./...`, `go test ./...`, and a commit. Phase boundaries are also reasonable PR boundaries — if review requests it, we can land in pieces.

- **Phase A**: Foundation packages, no behavior change yet
- **Phase B**: state.get unification (reads)
- **Phase C**: Mutate framework (writes)
- **Phase D**: Container PID-1 merger + heartbeat hot-reload
- **Phase E**: Cleanup, CLAUDE.md, deploy-test
- **Phase F**: Validation (CI + deploy-test 003 + PR)

---

# Phase A — Foundation

## Task A.1: Add Context bitmask to Key registry

**Files:**
- Modify: `internal/config/keys.go`
- Test: `internal/config/keys_context_test.go` (new)

- [ ] **Step A.1.1: Write the failing test**

```go
// internal/config/keys_context_test.go
package config

import "testing"

func TestContextBitmask_Defaults(t *testing.T) {
    k, ok := KeyByPath("heartbeat.interval")
    if !ok { t.Fatal("heartbeat.interval not registered") }
    if k.Contexts == 0 {
        t.Errorf("heartbeat.interval.Contexts must be nonzero; got %d", k.Contexts)
    }
    if k.Contexts&ContainerCtx == 0 {
        t.Errorf("heartbeat.interval must be valid in ContainerCtx; got %d", k.Contexts)
    }
}

func TestContextBitmask_Both(t *testing.T) {
    k, ok := KeyByPath("log_level")
    if !ok { t.Fatal("log_level not registered") }
    if k.Contexts != BothCtx {
        t.Errorf("log_level.Contexts should be BothCtx; got %d", k.Contexts)
    }
}

func TestContextBitmask_ContainerOnly(t *testing.T) {
    k, ok := KeyByPath("mindform.model")
    if !ok { t.Fatal("mindform.model not registered") }
    if k.Contexts&HostCtx != 0 {
        t.Errorf("mindform.model must not have HostCtx; got %d", k.Contexts)
    }
}
```

- [ ] **Step A.1.2: Run test, confirm it fails**

Run: `go test ./internal/config/ -run TestContextBitmask -v`
Expected: FAIL — `Contexts` field doesn't exist yet on `Key`.

- [ ] **Step A.1.3: Add Context type + Contexts field**

Edit `internal/config/keys.go`. Above the `Key` struct add:

```go
// Context identifies in which daemon context a config key is valid. The
// host daemon and the in-container PID-1 share most keys but a few
// (e.g. heartbeat.interval, mindform.model) only make sense inside a
// mindform. The Mutate framework rejects mismatched writes with
// CONTEXT_MISMATCH and points the operator to the correct verb.
type Context uint8

const (
    HostCtx      Context = 1 << 0
    ContainerCtx Context = 1 << 1
    BothCtx              = HostCtx | ContainerCtx
)
```

Add `Contexts Context` field to the `Key` struct (just after `Set`).

- [ ] **Step A.1.4: Populate Contexts on each existing register() call**

In the `init()` body of `internal/config/keys.go`, add `Contexts:` to every register call:
- `log_level` → `BothCtx`
- `daemon.socket` → `BothCtx`
- `daemon.shutdown_grace_seconds` → `BothCtx`
- `dashboard.enabled` → `BothCtx`
- `dashboard.listen` → `BothCtx`
- `mindform.model` → `ContainerCtx`
- `heartbeat.interval` → `ContainerCtx`

(Plus any others that exist — grep `register(Key{` to find them all.)

- [ ] **Step A.1.5: Run tests**

Run: `go test ./internal/config/ -v`
Expected: PASS for all tests including the new context tests.

- [ ] **Step A.1.6: Commit**

```bash
git add internal/config/keys.go internal/config/keys_context_test.go
git commit -m "feat(config): add Context bitmask to Key registry

Marks each settable key with HostCtx / ContainerCtx / BothCtx. The
upcoming Mutate framework consults this to reject mismatched writes
(e.g. host setting heartbeat.interval) with a helpful error pointing
to the correct verb.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

## Task A.2: Extract cron package

**Files:**
- Create: `internal/cron/render.go`
- Create: `internal/cron/render_test.go`
- Create: `internal/cron/install.go`
- Create: `internal/cron/install_test.go`
- Modify: `cmd/eidos/supervisor/crontab.go` (will become thin wrapper or be deleted)
- Modify: `cmd/eidos/supervisor/crontab_test.go` (point at internal/cron or move)

- [ ] **Step A.2.1: Read the current crontab rendering logic**

Read `cmd/eidos/supervisor/crontab.go` to understand the current API (`renderCrontab`, `installCrontabAsRoot`, etc.). Note the line that pins `EIDOS_IN_CONTAINER=1` inline.

- [ ] **Step A.2.2: Write render test against the new package**

```go
// internal/cron/render_test.go
package cron

import (
    "strings"
    "testing"
)

func TestRender_OneMinute(t *testing.T) {
    body, err := Render("1m")
    if err != nil { t.Fatal(err) }
    if !strings.Contains(body, "*/1 * * * *") {
        t.Errorf("expected */1 * * * * cron expression; got:\n%s", body)
    }
    if !strings.Contains(body, "EIDOS_IN_CONTAINER=1") {
        t.Errorf("missing EIDOS_IN_CONTAINER=1 inline env; got:\n%s", body)
    }
}

func TestRender_TwoHour(t *testing.T) {
    body, err := Render("2h")
    if err != nil { t.Fatal(err) }
    if !strings.Contains(body, "0 */2 * * *") {
        t.Errorf("expected 0 */2 * * * cron expression; got:\n%s", body)
    }
}

func TestRender_EmptyUsesDefault(t *testing.T) {
    body, err := Render("")
    if err != nil { t.Fatal(err) }
    // Default is 2h
    if !strings.Contains(body, "0 */2 * * *") {
        t.Errorf("expected default 2h fallback; got:\n%s", body)
    }
}

func TestRender_Invalid(t *testing.T) {
    if _, err := Render("garbage"); err == nil {
        t.Error("expected error for invalid interval")
    }
}
```

- [ ] **Step A.2.3: Implement internal/cron/render.go**

```go
// Package cron renders busybox crond crontab bodies from heartbeat
// interval strings and installs them to the user spool. The supervisor
// (initial render at PID-1 startup) and the heartbeat.interval apply
// hook (post-config-change re-render) both call into here so the
// rendering logic has one home.
package cron

import (
    "fmt"
    "strings"

    "github.com/LucianoXu/eidopsyche/internal/config"
)

// Render returns the crontab body for an interval string. Empty
// interval falls back to config.DefaultHeartbeatInterval (currently 2h).
// The body includes the EIDOS_IN_CONTAINER=1 inline env on the heartbeat
// line so the child eidos sees the container marker even when busybox
// crond's setuid-into-eidos path drops PID-1's environment.
func Render(interval string) (string, error) {
    iv := strings.TrimSpace(interval)
    if iv == "" {
        iv = config.DefaultHeartbeatInterval
    }
    if err := config.ValidateHeartbeatInterval(iv); err != nil {
        return "", fmt.Errorf("invalid heartbeat interval %q: %w", iv, err)
    }
    expr, err := cronExpression(iv)
    if err != nil { return "", err }
    return fmt.Sprintf("%s EIDOS_IN_CONTAINER=1 /usr/local/bin/eidos forge wake --reason heartbeat\n", expr), nil
}

// cronExpression maps an interval string ("1m", "2h", ...) to a busybox-
// compatible 5-field cron expression. The supported set is enforced
// upstream by config.ValidateHeartbeatInterval so we can assume the
// value is one of {1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m,1h,2h,3h,4h,6h,8h,12h,24h}.
func cronExpression(iv string) (string, error) {
    switch iv {
    case "1m":  return "*/1 * * * *", nil
    case "2m":  return "*/2 * * * *", nil
    case "3m":  return "*/3 * * * *", nil
    case "4m":  return "*/4 * * * *", nil
    case "5m":  return "*/5 * * * *", nil
    case "6m":  return "*/6 * * * *", nil
    case "10m": return "*/10 * * * *", nil
    case "12m": return "*/12 * * * *", nil
    case "15m": return "*/15 * * * *", nil
    case "20m": return "*/20 * * * *", nil
    case "30m": return "*/30 * * * *", nil
    case "1h":  return "0 */1 * * *", nil
    case "2h":  return "0 */2 * * *", nil
    case "3h":  return "0 */3 * * *", nil
    case "4h":  return "0 */4 * * *", nil
    case "6h":  return "0 */6 * * *", nil
    case "8h":  return "0 */8 * * *", nil
    case "12h": return "0 */12 * * *", nil
    case "24h": return "0 0 * * *", nil
    default:
        return "", fmt.Errorf("unsupported interval %q", iv)
    }
}
```

- [ ] **Step A.2.4: Run render tests**

Run: `go test ./internal/cron/ -run TestRender -v`
Expected: PASS.

- [ ] **Step A.2.5: Write install test**

```go
// internal/cron/install_test.go
package cron

import (
    "context"
    "os"
    "path/filepath"
    "testing"
)

func TestInstall_WritesAtomically(t *testing.T) {
    dir := t.TempDir()
    spool := filepath.Join(dir, "eidos")
    inst := &Installer{
        SpoolPath: spool,
        // SudoCommand="" → use direct write (test mode)
    }
    if err := inst.Install(context.Background(), "MAILTO=\"\"\n*/1 * * * * /bin/true\n"); err != nil {
        t.Fatal(err)
    }
    info, err := os.Stat(spool)
    if err != nil { t.Fatal(err) }
    if info.Mode().Perm() != 0o600 {
        t.Errorf("expected 0600; got %o", info.Mode().Perm())
    }
    body, err := os.ReadFile(spool)
    if err != nil { t.Fatal(err) }
    if string(body) == "" { t.Error("empty spool") }
}
```

- [ ] **Step A.2.6: Implement install.go**

```go
package cron

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
)

// Installer writes crontab bodies to the busybox crond spool. The
// daemon's apply hook for heartbeat.interval and the supervisor's
// initial-render path both go through this.
type Installer struct {
    // SpoolPath is the per-user crontab file (e.g. /var/spool/cron/crontabs/eidos).
    SpoolPath string
    // SudoCommand is the binary to escalate with. Production: "sudo".
    // Tests leave it empty to write directly (skips sudo + chmod).
    SudoCommand string
}

// Install writes body to SpoolPath atomically (tmp + rename) and sets
// 0600. When SudoCommand is set we escalate via `sudo tee` + `sudo
// chmod` (busybox crond requires root-owned 0600 spool entries). When
// empty (test mode) we write directly.
func (i *Installer) Install(ctx context.Context, body string) error {
    if i.SudoCommand == "" {
        return writeDirect(i.SpoolPath, body)
    }
    return writeViaSudo(ctx, i.SudoCommand, i.SpoolPath, body)
}

func writeDirect(path, body string) error {
    dir := filepath.Dir(path)
    tmp, err := os.CreateTemp(dir, ".crontab-*")
    if err != nil { return err }
    tmpPath := tmp.Name()
    cleanup := func() { _ = os.Remove(tmpPath) }
    if _, err := tmp.WriteString(body); err != nil {
        tmp.Close(); cleanup(); return err
    }
    if err := tmp.Close(); err != nil { cleanup(); return err }
    if err := os.Chmod(tmpPath, 0o600); err != nil { cleanup(); return err }
    return os.Rename(tmpPath, path)
}

func writeViaSudo(ctx context.Context, sudoBin, path, body string) error {
    cmd := exec.CommandContext(ctx, sudoBin, "-n", "tee", path)
    cmd.Stdin = strings.NewReader(body)
    cmd.Stdout = nil
    cmd.Stderr = os.Stderr
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("sudo tee %s: %w", path, err)
    }
    chmod := exec.CommandContext(ctx, sudoBin, "-n", "chmod", "0600", path)
    chmod.Stdout = nil
    chmod.Stderr = os.Stderr
    if err := chmod.Run(); err != nil {
        return fmt.Errorf("sudo chmod 0600 %s: %w", path, err)
    }
    return nil
}
```

- [ ] **Step A.2.7: Run install test**

Run: `go test ./internal/cron/ -v`
Expected: PASS for all cron tests.

- [ ] **Step A.2.8: Repoint supervisor to use internal/cron**

Modify `cmd/eidos/supervisor/run.go` to import `"github.com/LucianoXu/eidopsyche/internal/cron"` and replace `renderCrontab(cfg)` with `cron.Render(cfg.Heartbeat.Interval)`, replace `installCrontabAsRoot` references with an `cron.Installer{SpoolPath: crontabPath, SudoCommand: "sudo"}.Install(ctx, body)`.

Delete `cmd/eidos/supervisor/crontab.go` (rendering+install logic now in internal/cron).

Update `cmd/eidos/supervisor/crontab_test.go` — either delete it (tests already covered by internal/cron/) or rewrite as a thin integration sanity that the supervisor calls into cron.

- [ ] **Step A.2.9: Run supervisor + cron tests**

Run: `go test ./cmd/eidos/supervisor/ ./internal/cron/ -v`
Expected: PASS.

- [ ] **Step A.2.10: Run all tests, confirm no regressions**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: no fmt complaints, no vet errors, all tests pass.

- [ ] **Step A.2.11: Commit**

```bash
git add internal/cron/ cmd/eidos/supervisor/
git commit -m "refactor(cron): extract crontab render+install into internal/cron

Same logic, new home. The supervisor's initial PID-1 render and the
upcoming heartbeat.interval apply hook will both call into this
package, so the rendering lives once. busybox spool semantics
(root-owned 0600, sudo tee path) preserved; tests substitute a direct
write mode.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

## Task A.3: Create internal/state package skeleton

**Files:**
- Create: `internal/state/tree.go`
- Create: `internal/state/apply.go`
- Create: `internal/state/tree_test.go`
- Create: `internal/state/apply_test.go`

- [ ] **Step A.3.1: Write tree tests first**

```go
// internal/state/tree_test.go
package state

import (
    "context"
    "errors"
    "testing"
)

type fakeContrib struct {
    path string
    data map[string]any
    err  error
}

func (f *fakeContrib) Path() string { return f.path }
func (f *fakeContrib) Snapshot(_ context.Context) (any, error) {
    if f.err != nil { return nil, f.err }
    return f.data, nil
}

func TestTree_RootSnapshot(t *testing.T) {
    tr := NewTree()
    tr.Register(&fakeContrib{path: "config", data: map[string]any{"log_level": "info"}})
    tr.Register(&fakeContrib{path: "identity", data: map[string]any{"label": "alice"}})
    snap, err := tr.Snapshot(context.Background(), "")
    if err != nil { t.Fatal(err) }
    m, ok := snap.(map[string]any)
    if !ok { t.Fatalf("expected map; got %T", snap) }
    if m["config"] == nil || m["identity"] == nil {
        t.Errorf("missing top-level keys: %+v", m)
    }
}

func TestTree_DottedPath(t *testing.T) {
    tr := NewTree()
    tr.Register(&fakeContrib{path: "config", data: map[string]any{
        "heartbeat": map[string]any{"interval": "1m"},
    }})
    v, err := tr.Snapshot(context.Background(), "config.heartbeat.interval")
    if err != nil { t.Fatal(err) }
    if v != "1m" { t.Errorf("got %v", v) }
}

func TestTree_PathNotFound(t *testing.T) {
    tr := NewTree()
    _, err := tr.Snapshot(context.Background(), "nope.x")
    if !errors.Is(err, ErrPathNotFound) {
        t.Errorf("expected ErrPathNotFound; got %v", err)
    }
}

func TestTree_ContributorError_FailsFull(t *testing.T) {
    tr := NewTree()
    tr.Register(&fakeContrib{path: "broken", err: errors.New("boom")})
    tr.Register(&fakeContrib{path: "ok", data: map[string]any{"x": 1}})
    _, err := tr.Snapshot(context.Background(), "")
    if err == nil { t.Error("expected error from broken contributor") }
}
```

- [ ] **Step A.3.2: Run tests, confirm fail**

Run: `go test ./internal/state/ -v`
Expected: FAIL (Tree not implemented).

- [ ] **Step A.3.3: Implement tree.go**

```go
// Package state defines the state authority abstractions: a Tree of
// state contributors that the daemon snapshots through `state.get`, and
// an apply hook registry that the Mutate helper dispatches against
// after each successful write.
package state

import (
    "context"
    "errors"
    "fmt"
    "reflect"
    "sort"
    "strings"
    "sync"
)

var ErrPathNotFound = errors.New("state path not found")

// StateContributor exposes a named subtree of the daemon's state. Each
// contributor owns one top-level key (e.g. "config", "contacts",
// "lifecycle.wakes" — dots are allowed in contributor paths and the
// Tree mounts them nested).
type StateContributor interface {
    Path() string
    Snapshot(ctx context.Context) (any, error)
}

type Tree struct {
    mu     sync.RWMutex
    contrs []StateContributor // sorted by Path() longest-first for deterministic resolution
}

func NewTree() *Tree { return &Tree{} }

func (t *Tree) Register(c StateContributor) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.contrs = append(t.contrs, c)
    sort.Slice(t.contrs, func(i, j int) bool {
        return len(t.contrs[i].Path()) > len(t.contrs[j].Path())
    })
}

// Snapshot resolves a dotted path against the registered contributors.
// Empty path → full root. Path == contributor.Path() → that contributor's
// snapshot verbatim. Path nested under a contributor → walk the
// contributor's snapshot via reflection / map indexing.
func (t *Tree) Snapshot(ctx context.Context, path string) (any, error) {
    t.mu.RLock()
    defer t.mu.RUnlock()

    if path == "" {
        return t.fullRoot(ctx)
    }
    for _, c := range t.contrs {
        cp := c.Path()
        if path == cp {
            return c.Snapshot(ctx)
        }
        if strings.HasPrefix(path, cp+".") {
            snap, err := c.Snapshot(ctx)
            if err != nil { return nil, fmt.Errorf("%s contributor: %w", cp, err) }
            remainder := strings.TrimPrefix(path, cp+".")
            v, ok := walk(snap, remainder)
            if !ok { return nil, ErrPathNotFound }
            return v, nil
        }
    }
    return nil, ErrPathNotFound
}

func (t *Tree) fullRoot(ctx context.Context) (any, error) {
    out := map[string]any{}
    for _, c := range t.contrs {
        snap, err := c.Snapshot(ctx)
        if err != nil { return nil, fmt.Errorf("%s contributor: %w", c.Path(), err) }
        // Mount snap at c.Path() (which may be dotted itself for container-only paths).
        mount(out, c.Path(), snap)
    }
    return out, nil
}

func mount(root map[string]any, dotted string, v any) {
    parts := strings.Split(dotted, ".")
    m := root
    for i := 0; i < len(parts)-1; i++ {
        next, ok := m[parts[i]].(map[string]any)
        if !ok {
            next = map[string]any{}
            m[parts[i]] = next
        }
        m = next
    }
    m[parts[len(parts)-1]] = v
}

// walk descends `v` along the dotted remainder. Supports map[string]any
// and struct field walk (case-insensitive on field name). Returns
// (value, true) on success, (nil, false) on miss.
func walk(v any, remainder string) (any, bool) {
    cur := v
    for _, segment := range strings.Split(remainder, ".") {
        next, ok := step(cur, segment)
        if !ok { return nil, false }
        cur = next
    }
    return cur, true
}

func step(v any, segment string) (any, bool) {
    if m, ok := v.(map[string]any); ok {
        x, hit := m[segment]
        return x, hit
    }
    rv := reflect.ValueOf(v)
    for rv.Kind() == reflect.Pointer { rv = rv.Elem() }
    if rv.Kind() == reflect.Struct {
        for i := 0; i < rv.NumField(); i++ {
            f := rv.Type().Field(i)
            if !f.IsExported() { continue }
            if strings.EqualFold(f.Name, segment) || strings.EqualFold(tomlTag(f.Tag.Get("toml")), segment) || strings.EqualFold(jsonTag(f.Tag.Get("json")), segment) {
                return rv.Field(i).Interface(), true
            }
        }
    }
    return nil, false
}

func tomlTag(t string) string {
    if i := strings.IndexByte(t, ','); i >= 0 { return t[:i] }
    return t
}
func jsonTag(t string) string {
    if i := strings.IndexByte(t, ','); i >= 0 { return t[:i] }
    return t
}
```

- [ ] **Step A.3.4: Run tree tests**

Run: `go test ./internal/state/ -run TestTree -v`
Expected: PASS.

- [ ] **Step A.3.5: Write apply tests**

```go
// internal/state/apply_test.go
package state

import (
    "context"
    "errors"
    "testing"
)

func TestApply_DispatchSuccess(t *testing.T) {
    r := NewApplyRegistry()
    called := false
    r.Register("config.heartbeat.interval", func(_ context.Context, _ any, old, new any) error {
        called = true
        if old != "2h" || new != "1m" { t.Errorf("bad values old=%v new=%v", old, new) }
        return nil
    })
    if err := r.Dispatch(context.Background(), nil, "config.heartbeat.interval", "2h", "1m"); err != nil {
        t.Fatal(err)
    }
    if !called { t.Error("hook not invoked") }
}

func TestApply_NoHookForPath(t *testing.T) {
    r := NewApplyRegistry()
    // Dispatch with no registration should be a no-op (no error).
    if err := r.Dispatch(context.Background(), nil, "config.log_level", nil, "info"); err != nil {
        t.Fatal(err)
    }
}

type rbErr struct{ allow bool }
func (e *rbErr) Error() string { return "apply failed" }
func (e *rbErr) Rollback() bool { return e.allow }

func TestApply_RollbackerSurfaced(t *testing.T) {
    r := NewApplyRegistry()
    r.Register("p", func(_ context.Context, _ any, _, _ any) error {
        return &rbErr{allow: false}
    })
    err := r.Dispatch(context.Background(), nil, "p", nil, nil)
    var rb Rollbacker
    if !errors.As(err, &rb) {
        t.Fatalf("error should implement Rollbacker; got %T", err)
    }
    if rb.Rollback() {
        t.Error("Rollback() should be false")
    }
}
```

- [ ] **Step A.3.6: Implement apply.go**

```go
package state

import (
    "context"
    "sync"
)

// ApplyFunc is invoked by the daemon's Mutate helper after a successful
// write. deps is opaque (interface{}); the caller-side package that
// registers a hook defines its own concrete deps shape and casts here.
// This is intentional to keep internal/state independent of cron /
// lifecycle / daemon (which would otherwise produce import cycles).
type ApplyFunc func(ctx context.Context, deps any, old, new any) error

// Rollbacker is an optional interface on errors returned from ApplyFunc.
// If the returned error implements Rollbacker and Rollback() reports
// false, the Mutate helper SKIPS its default rollback. Default behavior
// (no Rollbacker, or Rollback() == true) is to rollback.
type Rollbacker interface { Rollback() bool }

type ApplyRegistry struct {
    mu    sync.RWMutex
    hooks map[string]ApplyFunc
}

func NewApplyRegistry() *ApplyRegistry {
    return &ApplyRegistry{hooks: map[string]ApplyFunc{}}
}

func (r *ApplyRegistry) Register(path string, fn ApplyFunc) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.hooks[path] = fn
}

// Dispatch looks up the hook for path and runs it. A missing hook is
// not an error (most paths have no apply behavior). The caller wraps
// any returned error and decides on rollback.
func (r *ApplyRegistry) Dispatch(ctx context.Context, deps any, path string, old, new any) error {
    r.mu.RLock()
    fn, ok := r.hooks[path]
    r.mu.RUnlock()
    if !ok { return nil }
    return fn(ctx, deps, old, new)
}
```

- [ ] **Step A.3.7: Run apply tests**

Run: `go test ./internal/state/ -v`
Expected: PASS for all state tests.

- [ ] **Step A.3.8: Commit**

```bash
git add internal/state/
git commit -m "feat(state): tree + apply registry skeleton

Tree mounts named contributors at dotted paths and resolves
state.get [path] queries. ApplyRegistry runs per-path hooks after
mutations succeed; ApplyFunc deps are typed any to keep internal/state
independent of daemon/cron/lifecycle (avoiding the import cycle).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

## Task A.4: Daemon.Mutate helper (no behavior change yet)

**Files:**
- Create: `internal/daemon/mutate.go`
- Create: `internal/daemon/mutate_test.go`
- Modify: `internal/daemon/daemon.go` (add applyRegistry, tree, Context, applyDeps fields)
- Modify: `internal/daemon/methods_config.go` (configSet routes through Mutate)
- Modify: `internal/ipc/protocol.go` (add ErrContextMismatch, ErrInconsistent)

- [ ] **Step A.4.1: Add new error codes to ipc.protocol**

Read `internal/ipc/protocol.go`. Add:
```go
ErrContextMismatch = "CONTEXT_MISMATCH"
ErrPathNotFound    = "PATH_NOT_FOUND"
ErrInconsistent    = "STATE_INCONSISTENT"
```

Add `WrapInconsistent(applyErr, rollbackErr error) *Error` helper.

- [ ] **Step A.4.2: Add Daemon fields**

Modify `internal/daemon/daemon.go` (the Daemon struct):
```go
type Daemon struct {
    // ... existing fields
    Context        config.Context  // HostCtx or ContainerCtx; set by Run options
    applyRegistry  *state.ApplyRegistry
    stateTree      *state.Tree
    applyDepsValue any  // returned by applyDeps()
}

// applyDeps returns the opaque deps value passed to Apply hooks. Host
// daemon returns nil; container PID-1 returns *lifecycle.ApplyDeps.
func (d *Daemon) applyDeps() any { return d.applyDepsValue }
```

Initialize in the constructor:
```go
applyRegistry: state.NewApplyRegistry(),
stateTree:     state.NewTree(),
```

- [ ] **Step A.4.3: Write Mutate test**

```go
// internal/daemon/mutate_test.go
package daemon

import (
    "context"
    "errors"
    "sync/atomic"
    "testing"

    "github.com/LucianoXu/eidopsyche/internal/config"
    "github.com/LucianoXu/eidopsyche/internal/state"
)

func TestMutate_HappyPath(t *testing.T) {
    d := newDaemonForTest(t)
    d.Context = config.ContainerCtx
    applied := atomic.Bool{}
    d.applyRegistry.Register("p", func(_ context.Context, _ any, old, new any) error {
        applied.Store(true)
        return nil
    })
    err := d.Mutate(context.Background(), "p", config.ContainerCtx,
        func() (any, any, error) { return "old", "new", nil })
    if err != nil { t.Fatal(err) }
    if !applied.Load() { t.Error("apply not run") }
}

func TestMutate_ContextMismatch(t *testing.T) {
    d := newDaemonForTest(t)
    d.Context = config.HostCtx
    err := d.Mutate(context.Background(), "p", config.ContainerCtx,
        func() (any, any, error) { t.Error("write fn should not run"); return nil, nil, nil })
    if err == nil { t.Fatal("expected error") }
    // CONTEXT_MISMATCH carries a hint in the message — minimal assertion here.
    if !strings.Contains(err.Error(), "container") {
        t.Errorf("error should hint at container; got %v", err)
    }
}

func TestMutate_ApplyFailure_RollbackDefault(t *testing.T) {
    d := newDaemonForTest(t)
    d.Context = config.ContainerCtx
    rollback := atomic.Bool{}
    d.applyRegistry.Register("p", func(_ context.Context, _ any, old, new any) error {
        return errors.New("apply boom")
    })
    err := d.Mutate(context.Background(), "p", config.ContainerCtx,
        func() (any, any, error) {
            if rollback.Load() { return "old", "old", nil } // second call = rollback
            rollback.Store(true)
            return "old", "new", nil
        })
    if err == nil { t.Error("expected error from apply") }
    if !rollback.Load() { t.Error("rollback should have re-invoked write fn") }
}

// newDaemonForTest stubs a Daemon with just enough fields for Mutate to run.
// Reuse existing test helpers in internal/daemon/testhelpers_test.go if compatible.
```

(Adapt `newDaemonForTest` to the actual constructor — the existing test files have helpers; reuse them.)

- [ ] **Step A.4.4: Implement Mutate**

```go
// internal/daemon/mutate.go
package daemon

import (
    "context"
    "errors"
    "fmt"

    "github.com/LucianoXu/eidopsyche/internal/config"
    "github.com/LucianoXu/eidopsyche/internal/ipc"
    "github.com/LucianoXu/eidopsyche/internal/state"
)

// Mutate is the common wrapper every mutation handler funnels through.
// It checks context, locks the state mutex, runs the write closure,
// dispatches the apply hook, emits state.changed, and unlocks.
//
// write returns (oldSnapshot, newSnapshot, error). When apply fails and
// the error is rollbackable (default), Mutate re-invokes write with a
// rollback marker so handlers can restore the old value. The write
// closure must be idempotent w.r.t. rollback.
func (d *Daemon) Mutate(
    ctx context.Context,
    path string,
    requires config.Context,
    write func() (any, any, error),
) error {
    if d.Context&requires == 0 {
        return contextMismatchError(path, requires)
    }
    d.stateMu.Lock()
    defer d.stateMu.Unlock()

    old, new, err := write()
    if err != nil { return err }

    if applyErr := d.applyRegistry.Dispatch(ctx, d.applyDeps(), path, old, new); applyErr != nil {
        if rollbackable(applyErr) {
            if _, _, rbErr := write(); rbErr != nil {
                return ipc.WrapInconsistent(applyErr, rbErr).Err()
            }
        }
        return applyErr
    }
    d.emitDashEvent(dashboardStateChangedEvent(path, new))
    return nil
}

func rollbackable(err error) bool {
    var rb state.Rollbacker
    if errors.As(err, &rb) {
        return rb.Rollback()
    }
    return true
}

func contextMismatchError(path string, requires config.Context) error {
    return fmt.Errorf("%s requires %s context; if you meant to set it on a mindform, run `eidos forge config <name>` instead", path, contextName(requires))
}

func contextName(c config.Context) string {
    switch c {
    case config.ContainerCtx: return "container"
    case config.HostCtx:      return "host"
    default:                  return "any"
    }
}

func dashboardStateChangedEvent(path string, value any) dashboard.Event {
    return dashboard.Event{
        Kind: "state.changed",
        // Stick to existing dashboard.Event fields; add a generic StateChange struct if needed.
    }
}
```

(NB: `emitDashEvent` already exists; reuse it. The `dashboard.Event` shape may need an extension to carry `path`/`value` — make that change here.)

- [ ] **Step A.4.5: Run Mutate tests**

Run: `go test ./internal/daemon/ -run TestMutate -v`
Expected: PASS.

- [ ] **Step A.4.6: Wire configSet through Mutate**

Edit `internal/daemon/methods_config.go`. Replace the body of `configSet` with:
```go
func configSet(_ context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
    var p ConfigSetParams
    if err := json.Unmarshal(params, &p); err != nil {
        return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
    }
    key, ok := config.KeyByPath(p.Path)
    if !ok {
        return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "unknown config key: " + p.Path}
    }
    err := d.Mutate(context.Background(), "config."+p.Path, key.Contexts,
        func() (any, any, error) {
            cfg, err := config.Load(d.configPath())
            if err != nil { return nil, nil, err }
            oldVal := key.Get(&cfg)
            if err := key.Set(&cfg, p.Value); err != nil { return nil, nil, err }
            if err := config.Save(d.configPath(), cfg); err != nil { return nil, nil, err }
            newVal := key.Get(&cfg)
            return oldVal, newVal, nil
        })
    if err != nil {
        return nil, errToIPCError(err)
    }
    cfg, _ := config.Load(d.configPath())
    return cfg, nil
}

func errToIPCError(err error) *ipc.Error {
    // Map known sentinels to typed codes; otherwise ErrInternal.
    var ipcErr *ipc.Error
    if errors.As(err, &ipcErr) { return ipcErr }
    return internalErr(err)
}
```

- [ ] **Step A.4.7: Run all tests**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: PASS. No behavior change yet (apply registry is empty in production).

- [ ] **Step A.4.8: Commit**

```bash
git add internal/daemon/mutate.go internal/daemon/mutate_test.go internal/daemon/daemon.go internal/daemon/methods_config.go internal/ipc/
git commit -m "feat(daemon): Mutate helper + Context bitmask gate

Adds Daemon.Mutate(ctx, path, requires, write) — the future single
funnel for all state mutations. configSet rewired to use it. Apply
hooks are not yet registered (registry is empty in production), so
behavior is unchanged. ipc adds ErrContextMismatch / ErrPathNotFound /
ErrInconsistent.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

# Phase B — state.get unification

## Task B.1: state.get IPC method + CLI

**Files:**
- Create: `internal/daemon/methods_state.go`
- Create: `cmd/eidos/gate/state.go`
- Modify: `internal/daemon/methods.go` (register state.get)

- [ ] **Step B.1.1: Test the IPC method dispatches**

```go
// internal/daemon/methods_state_test.go
package daemon

import (
    "context"
    "encoding/json"
    "testing"
)

func TestStateGet_Registered(t *testing.T) {
    if _, ok := methodTable["state.get"]; !ok {
        t.Fatal("state.get must be registered in methodTable")
    }
}

func TestStateGet_FullSnapshot(t *testing.T) {
    d := newDaemonForTest(t)
    d.stateTree.Register(&fakeContrib{path: "x", data: map[string]any{"k": "v"}})
    raw, _ := json.Marshal(map[string]any{})
    out, ipcErr := stateGet(context.Background(), d, nil, raw)
    if ipcErr != nil { t.Fatal(ipcErr) }
    m, _ := out.(map[string]any)
    if m["x"] == nil { t.Errorf("missing x: %+v", m) }
}
```

- [ ] **Step B.1.2: Implement stateGet handler**

```go
// internal/daemon/methods_state.go
package daemon

import (
    "context"
    "encoding/json"
    "errors"

    "github.com/LucianoXu/eidopsyche/internal/ipc"
    "github.com/LucianoXu/eidopsyche/internal/state"
)

type StateGetParams struct {
    Path string `json:"path,omitempty"`
}

func stateGet(ctx context.Context, d *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
    var p StateGetParams
    if len(params) > 0 {
        if err := json.Unmarshal(params, &p); err != nil {
            return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
        }
    }
    snap, err := d.stateTree.Snapshot(ctx, p.Path)
    if err != nil {
        if errors.Is(err, state.ErrPathNotFound) {
            return nil, &ipc.Error{Code: ipc.ErrPathNotFound, Message: p.Path}
        }
        return nil, internalErr(err)
    }
    return snap, nil
}
```

Register in `methods.go init()`: `register("state.get", stateGet)`.

- [ ] **Step B.1.3: Implement CLI**

```go
// cmd/eidos/gate/state.go
package gate

import (
    "encoding/json"
    "fmt"

    "github.com/spf13/cobra"
)

var stateCmd = &cobra.Command{
    Use:   "state [path]",
    Short: "Print state subtree (dotted path) or full snapshot",
    Args:  cobra.MaximumNArgs(1),
    RunE:  runState,
}

func init() { rootCmd.AddCommand(stateCmd) }

func runState(cmd *cobra.Command, args []string) error {
    c, err := newClient()
    if err != nil { return err }
    defer c.Close()
    params := map[string]string{}
    if len(args) == 1 { params["path"] = args[0] }
    var out any
    if err := mustOK(c.Call("state.get", params, &out)); err != nil { return err }
    body, _ := json.MarshalIndent(out, "", "  ")
    fmt.Println(string(body))
    return nil
}
```

- [ ] **Step B.1.4: Run tests**

Run: `go test ./internal/daemon/ -run TestStateGet -v && go vet ./...`
Expected: PASS.

- [ ] **Step B.1.5: Commit**

```bash
git add internal/daemon/methods_state.go internal/daemon/methods_state_test.go internal/daemon/methods.go cmd/eidos/gate/state.go
git commit -m "feat(daemon,gate): state.get IPC method + CLI

Empty contributor registry → empty snapshot; next commits register
contributors per domain. CLI: \`eidos gate state [path]\` prints JSON.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

## Task B.2: Register state contributors (read side migration)

Each task below: write contributor → register at daemon init → swap callers → remove the old method. Group commits per domain.

- [ ] **Step B.2.1: identity contributor**

Create `internal/daemon/contrib_identity.go`:
```go
package daemon

import "context"

type identityContrib struct{ d *Daemon }
func (c identityContrib) Path() string { return "identity" }
func (c identityContrib) Snapshot(ctx context.Context) (any, error) {
    // Same body as whoami() handler.
    return c.d.identitySnapshot(ctx)
}
```

Register in Daemon constructor: `d.stateTree.Register(identityContrib{d: d})`.

Update `cmd/eidos/gate/whoami.go` to call `state.get identity` internally. Remove `whoami` from `methodTable` (and the old handler file). Tests that hit `whoami` IPC migrate.

Commit: `refactor(daemon): identity via state.get; remove whoami IPC method`

- [ ] **Step B.2.2: config contributor**

Repeat pattern for config. The contributor's Snapshot returns the full Config struct; the Tree's struct-walk in `walk()` handles `config.heartbeat.interval` etc. Remove `config.get` from methodTable. Update `eidos gate config get` to call `state.get config[.path]`.

- [ ] **Step B.2.3: contacts contributor**

```go
type contactsContrib struct{ d *Daemon }
func (c contactsContrib) Path() string { return "contacts" }
func (c contactsContrib) Snapshot(ctx context.Context) (any, error) {
    list, err := c.d.Repo.List(ctx)
    if err != nil { return nil, err }
    // Return as map keyed by pubkey so state.get contacts.<pubkey> works.
    out := map[string]any{}
    for _, k := range list { out[k.Pubkey] = k }
    return out, nil
}
```

Remove `contact.list` and `contact.get` methods. Update CLI accordingly.

- [ ] **Step B.2.4: relays contributor (with health)**

Include `Health` field per relay. Remove `relay.list` and `relays.health`.

- [ ] **Step B.2.5: inbox / outbox contributors**

`state.get inbox` returns `{unread, recent}`. Remove `inbox.list`, `inbox.tail`, `outbox.list`. CLI `eidos gate inbox` calls state.get; supports `--limit`, `--unread-only` flags passed via params.

- [ ] **Step B.2.6: service contributor (status + version + lifecycle status)**

Single contributor covering daemon socket, started_at, version, plus the existing `service.status` and `lifecycle.status` fields. Remove `service.status`, `lifecycle.status`, `version`.

- [ ] **Step B.2.7: invites contributor**

Single read; remove `invite.list`. (`invite.create/revoke/redeem` stay as mutations.)

- [ ] **Step B.2.8: card via identity**

`state.get identity.card` returns the TOML card body. Remove `card.export`. `card.parse` and `card.scan` are pure utility RPCs — kept as-is (see spec §9).

- [ ] **Step B.2.9: dashboard adapter migration**

Update `internal/daemon/dashboard_adapter.go` so each read method calls `a.d.Call(ctx, "state.get", {path: "x"}, &dest)` rather than the old per-domain IPC methods. Each `internal/dashboard/handlers_*.go` keeps its shape.

- [ ] **Step B.2.10: Run all tests + lint after each step above**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: PASS. Some tests will need rewriting per step.

---

# Phase C — Mutate framework (writes)

Every remaining mutation handler reshapes to `d.Mutate(...)` with appropriate state path + requires.

- [ ] **Step C.1: contact.add via Mutate**

State path: `contacts.<pubkey>`. requires: BothCtx.

- [ ] **Step C.2: contact.remove via Mutate**

Same path; old=snapshot, new=nil-marker.

- [ ] **Step C.3: contact.set-label / contact.set-tier via Mutate**

Path: `contacts.<pubkey>.label` / `contacts.<pubkey>.tier`.

- [ ] **Step C.4: contact.add-from-card via Mutate**

Path: `contacts.<pubkey>`. Same as contact.add internally.

- [ ] **Step C.5: relay.add / relay.remove via Mutate**

Path: `relays.<url>`.

- [ ] **Step C.6: send via Mutate**

Path: `outbox`. The mutation is "outbox.append". Apply hook is no-op (sending happens inside the write fn).

- [ ] **Step C.7: invite.create / invite.revoke / invite.redeem via Mutate**

Path: `invites.<id>`.

- [ ] **Step C.8: set-label via Mutate**

Path: `identity.label`.

- [ ] **Step C.9: subscribe.refresh / lifecycle.run via Mutate**

These are actions with side effects. Path: `service.subscriptions` for refresh; `lifecycle.jobs.<id>` for run.

- [ ] **Step C.10: Verify host rejects mindform-only keys**

Add integration test in `internal/daemon/mutate_integration_test.go`: with Daemon.Context = HostCtx, `configSet("heartbeat.interval","1m")` returns CONTEXT_MISMATCH with helpful message.

- [ ] **Step C.11: gofmt/vet/test + commit per logical group**

Commits roughly per domain:
- `refactor(daemon): contacts mutations via Mutate`
- `refactor(daemon): relays mutations via Mutate`
- `refactor(daemon): send/inbox/outbox mutations via Mutate`
- `refactor(daemon): invites mutations via Mutate`
- `refactor(daemon): identity + subscribe mutations via Mutate`

---

# Phase D — Container PID-1 merger + heartbeat hot-reload

## Task D.1: Move supervisor goroutines to internal/lifecycle

**Files:**
- Create: `internal/lifecycle/lifecycle.go`
- Create: `internal/lifecycle/wake.go`, `planner.go`, `agent.go`, `dream.go`, `submitter.go`, `deps.go`, `crontab.go`, `attach.go`
- Modify: `cmd/eidos/supervisor/run.go` (delegates to internal/lifecycle)

- [ ] **Step D.1.1: Define Lifecycle interface in daemon**

```go
// internal/daemon/daemon.go
type Lifecycle interface {
    Attach(d *Daemon) error
    Run(ctx context.Context) error
}

type Options struct {
    StateDir  string
    Context   config.Context
    Lifecycle Lifecycle
    ApplyDeps any
}

func Run(ctx context.Context, opts Options) error {
    d, err := New(opts)
    if err != nil { return err }
    if opts.Lifecycle != nil {
        if err := opts.Lifecycle.Attach(d); err != nil { return err }
        go func() { _ = opts.Lifecycle.Run(ctx) }()
    }
    return d.serve(ctx)
}
```

- [ ] **Step D.1.2: Move files into internal/lifecycle**

Copy from `cmd/eidos/supervisor/`:
- `run.go` → split into `lifecycle.go` (struct + Run), `wake.go` (watchWakes, drainPending), `planner.go`, `agent.go`, `dream.go`
- `crontab.go` → `lifecycle/crontab.go` (now just calls `internal/cron`)

Repackage as `package lifecycle`.

- [ ] **Step D.1.3: Implement Lifecycle struct**

```go
// internal/lifecycle/lifecycle.go
package lifecycle

import (
    "context"
    "github.com/LucianoXu/eidopsyche/internal/cron"
    "github.com/LucianoXu/eidopsyche/internal/daemon"
)

type Lifecycle struct {
    cron *cron.Installer
    // wake, planner state, etc.
}

func New(stateDir string) *Lifecycle {
    return &Lifecycle{
        cron: &cron.Installer{SpoolPath: "/var/spool/cron/crontabs/eidos", SudoCommand: "sudo"},
    }
}

func (l *Lifecycle) Attach(d *daemon.Daemon) error {
    // Register state contributors for container-only paths.
    d.RegisterStateContributor(&wakesContrib{l: l})
    d.RegisterStateContributor(&sessionContrib{l: l})
    d.RegisterStateContributor(&dreamContrib{l: l})
    d.RegisterStateContributor(&containerContrib{l: l})
    // Register apply hooks.
    d.RegisterApply("config.heartbeat.interval", l.applyHeartbeat)
    return nil
}

func (l *Lifecycle) Run(ctx context.Context) error {
    // Initial crontab render.
    cfg, _ := loadConfig(...)
    body, err := cron.Render(cfg.Heartbeat.Interval)
    if err == nil { _ = l.cron.Install(ctx, body) }
    // Spawn crond.
    // Start fsnotify wake watcher.
    // Start planner loop.
    return watchWakes(ctx)
}

func (l *Lifecycle) applyHeartbeat(ctx context.Context, _ any, _, new any) error {
    body, err := cron.Render(new.(string))
    if err != nil { return err }
    return l.cron.Install(ctx, body)
}
```

- [ ] **Step D.1.4: Add daemon helpers RegisterStateContributor / RegisterApply**

In `internal/daemon/daemon.go`:
```go
func (d *Daemon) RegisterStateContributor(c state.StateContributor) { d.stateTree.Register(c) }
func (d *Daemon) RegisterApply(path string, fn state.ApplyFunc)     { d.applyRegistry.Register(path, fn) }
```

- [ ] **Step D.1.5: Update cmd/eidos/supervisor/run.go**

```go
package supervisor

import (
    "github.com/LucianoXu/eidopsyche/internal/config"
    "github.com/LucianoXu/eidopsyche/internal/daemon"
    "github.com/LucianoXu/eidopsyche/internal/lifecycle"
)

func newRunCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "run",
        Short: "Run as PID 1: container daemon + lifecycle",
        RunE: func(cmd *cobra.Command, _ []string) error {
            ctx := cmd.Context()
            lc := lifecycle.New("/eidos/gate")
            return daemon.Run(ctx, daemon.Options{
                StateDir:  "/eidos/gate",
                Context:   config.ContainerCtx,
                Lifecycle: lc,
                ApplyDeps: lc.Deps(),
            })
        },
    }
}
```

- [ ] **Step D.1.6: Update cmd/eidos/gate/daemon.go**

Similarly, host-side daemon:
```go
return daemon.Run(ctx, daemon.Options{
    StateDir: stateDir,
    Context:  config.HostCtx,
    // Lifecycle: nil
})
```

- [ ] **Step D.1.7: Run all tests**

Run: `gofmt -l . && go vet ./... && go test ./...`
Expected: PASS. Supervisor's existing tests (which moved to lifecycle) should pass against the new structure.

- [ ] **Step D.1.8: Commit**

```bash
git add internal/lifecycle/ internal/daemon/ cmd/eidos/supervisor/ cmd/eidos/gate/
git commit -m "refactor: merge supervisor into daemon (container PID-1)

Container PID-1 is now one Go process: daemon core + lifecycle
goroutines (wake/planner/agent/dream) + crond child. Host gate daemon
remains pure daemon-core with no lifecycle. Apply hook for
heartbeat.interval is registered by lifecycle.Attach and synchronously
re-renders+installs the crontab — no more docker restart.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

## Task D.2: Remove docker restart from forge config

**Files:**
- Modify: `cmd/eidos/forge/config.go`
- Modify: `cmd/eidos/forge/config_test.go`

- [ ] **Step D.2.1: Delete restartContainerForHeartbeat**

Remove the function and its call site in `RunE`.

- [ ] **Step D.2.2: Update test**

`config_test.go` previously asserted docker restart was called. Update to assert that **only** `docker exec` is called for heartbeat-interval mutations.

- [ ] **Step D.2.3: Run tests + commit**

```bash
go test ./cmd/eidos/forge/ -v
git add cmd/eidos/forge/config.go cmd/eidos/forge/config_test.go
git commit -m "feat(forge): drop docker restart shim; heartbeat hot-reload via apply hook"
```

## Task D.3: Integration test — heartbeat hot-reload end to end

**Files:**
- Create: `test/integration/heartbeat_hot_reload_test.go`

- [ ] **Step D.3.1: Write the test**

```go
//go:build integration

package integration

import (
    "context"
    "os/exec"
    "strings"
    "testing"
    "time"
)

func TestHeartbeatHotReload(t *testing.T) {
    name := "eidos-mindform-hottest-" + strings.ReplaceAll(time.Now().UTC().Format("20060102T150405"), ":", "")
    // create container... (use forge create or docker run directly with the test image)
    defer exec.Command("docker", "rm", "-f", name).Run()
    // ... start container
    started1 := dockerStartedAt(t, name)
    out, err := exec.Command("docker", "exec", name, "eidos", "gate", "config", "set", "heartbeat.interval", "1m").CombinedOutput()
    if err != nil { t.Fatalf("set heartbeat: %v\n%s", err, out) }
    started2 := dockerStartedAt(t, name)
    if started1 != started2 {
        t.Errorf("container restarted: %s -> %s (should be hot reload only)", started1, started2)
    }
    // Read back via state.get
    out, err = exec.Command("docker", "exec", name, "eidos", "gate", "state", "config.heartbeat.interval").CombinedOutput()
    if err != nil { t.Fatalf("state.get: %v\n%s", err, out) }
    if !strings.Contains(string(out), "1m") {
        t.Errorf("state.get config.heartbeat.interval did not return 1m: %s", out)
    }
    // Inspect the spool body.
    out, _ = exec.Command("docker", "exec", name, "cat", "/var/spool/cron/crontabs/eidos").Output()
    if !strings.Contains(string(out), "*/1 *") {
        t.Errorf("crontab not updated to 1m cadence: %s", out)
    }
}

func dockerStartedAt(t *testing.T, name string) string {
    t.Helper()
    out, err := exec.Command("docker", "inspect", "--format", "{{.State.StartedAt}}", name).Output()
    if err != nil { t.Fatal(err) }
    return strings.TrimSpace(string(out))
}
```

- [ ] **Step D.3.2: Run integration test**

Run: `go test -tags=integration -run TestHeartbeatHotReload ./test/integration/ -v`
Expected: PASS.

- [ ] **Step D.3.3: Commit**

```bash
git add test/integration/heartbeat_hot_reload_test.go
git commit -m "test(integration): heartbeat hot-reload end-to-end"
```

---

# Phase E — Cleanup + CLAUDE.md + deploy-test

## Task E.1: Migrate forge in-container reflection commands to state.get facades

For each of `eidos forge whoami`, `forge inbox`, `forge memory`, `forge ontology-status`, `forge runtime-state`, `forge transcript-list`:

- [ ] **Step E.1.x**: rewrite the command's RunE to:
  1. Open IPC client (`newClient()`)
  2. Call `state.get` with the appropriate path
  3. Render the result in the same shape the command used before (preserve output format)
  4. Update tests to mock `state.get` rather than the underlying file reads

For `runtime-state` specifically, the JSON output shape is externally observable (forge status parses it); preserve byte-for-byte. Project from `state.get` (`lifecycle.wakes,session,container,service`) into the existing `RuntimeState` struct.

For `forge plan add/list/remove` and `forge dream begin/end`: keep as mutations through `Mutate` (path `lifecycle.plans.<id>` / `lifecycle.dream`). No CLI rename.

- [ ] **Step E.1.commit**: per command or grouped per session — `refactor(forge): in-container reads through state.get`

## Task E.2: Update CLAUDE.md

- [ ] **Step E.2.1: Add operator/mindform symmetry paragraph**

Open `CLAUDE.md`. After the "Single Call Path" section, add:

```markdown
### Operator/mindform symmetry

Every adjustment and every read in the same state schema is reachable
through the same IPC method table, regardless of whether the caller is
the operator (against the host gate daemon or via `docker exec` into a
mindform's PID-1) or the mindform itself (in-container against its own
PID-1). Surfaces are parameter-packing wrappers; host-side
`eidos forge config <name> ...` and in-container `eidos gate ...`
funnel through identical method calls.

**Don't add a host-only or container-only side-channel for state
actions.** State that exists only in one context (e.g. `lifecycle.*`
on the mindform) is fine — the schema can have context-specific
subtrees — but the verb that touches it is the same.
```

- [ ] **Step E.2.2: Update SPEC.md heartbeat section**

Edit the heartbeat description (around line 90 of SPEC.md) — note that the change is now hot-reload (no docker restart) via the apply hook framework. Three surfaces remain but `forge config` no longer restarts the container.

- [ ] **Step E.2.3: Commit**

```bash
git add CLAUDE.md SPEC.md
git commit -m "docs: add operator/mindform symmetry principle; update heartbeat to hot-reload"
```

## Task E.3: Update deploy-test 003

**Files:**
- Modify: `deploy-test/003-mindform-heartbeat/wizard-orchestrator.py`

- [ ] **Step E.3.1: Add StartedAt capture + state.get verification**

In the Python script's Phase D, around the `eidos forge config ... --heartbeat-interval 4m` call:

```python
def container_started_at(name):
    out = subprocess.run(
        ["docker", "inspect", "--format", "{{.State.StartedAt}}", f"eidos-mindform-{name}"],
        capture_output=True, text=True, timeout=15, check=True,
    ).stdout.strip()
    return out

# Before Phase D:
started_before = container_started_at(MINDFORM_NAME)
banner(f"[D-pre] StartedAt = {started_before}")

# Run forge config:
r = subprocess.run(
    ["eidos", "forge", "config", MINDFORM_NAME, "--heartbeat-interval", "4m"],
    timeout=120,
)
if r.returncode != 0: ...

# Immediately after:
started_after = container_started_at(MINDFORM_NAME)
banner(f"[D-post] StartedAt = {started_after}")

if started_before != started_after:
    print(f"FAIL: container restarted ({started_before} -> {started_after})", file=sys.stderr)
    return 5

# Verify state.get sees the new value:
out = subprocess.run(
    ["docker", "exec", f"eidos-mindform-{MINDFORM_NAME}",
     "eidos", "gate", "state", "config.heartbeat.interval"],
    capture_output=True, text=True, timeout=15, check=True,
).stdout.strip()
if "4m" not in out:
    print(f"FAIL: state.get returned {out!r}, expected 4m", file=sys.stderr)
    return 6
banner(f"[D-verify] state.get config.heartbeat.interval = {out}")
```

Also record wall-clock delay from Phase D completion to first observed 4m wake (cosmetic — for future tuning).

- [ ] **Step E.3.2: Commit**

```bash
git add deploy-test/003-mindform-heartbeat/wizard-orchestrator.py
git commit -m "test(deploy): 003 asserts no container restart + state.get visibility"
```

---

# Phase F — Validation + PR

## Task F.1: Full local CI run

- [ ] **Step F.1.1**: `gofmt -l . && go vet ./...` — clean
- [ ] **Step F.1.2**: `go test ./...` — green
- [ ] **Step F.1.3**: `go test -tags=integration ./...` — green (requires docker daemon)
- [ ] **Step F.1.4**: `go build -o /tmp/eidos ./cmd/eidos` — builds

## Task F.2: Container image + deploy to selene

- [ ] **Step F.2.1**: `make image` — builds mindform container image with new binary
- [ ] **Step F.2.2**: `docker push` to ghcr.io if needed (or skip if local-only)
- [ ] **Step F.2.3**: Deploy `/tmp/eidos` to selene (binary-only upgrade per user's deploy-test memory; on this script though, the test instructs purge — follow script step 1)
- [ ] **Step F.2.4**: Run deploy-test 003: `python3 deploy-test/003-mindform-heartbeat/wizard-orchestrator.py` on selene
- [ ] **Step F.2.5**: Confirm script exits 0; capture log

## Task F.3: Open PR

- [ ] **Step F.3.1**: Push branch
- [ ] **Step F.3.2**: `gh pr create --title "feat: unified state interface (state.get + Mutate + container PID-1 merger)"`
- [ ] **Step F.3.3**: Watch CI: `gh pr checks` until green
- [ ] **Step F.3.4**: If copilot reviewer leaves comments, address them
- [ ] **Step F.3.5**: Print PR URL when complete

---

## Notes for executor

- **Test discipline**: TDD where possible (new code: test first). For refactors of existing handlers, the existing tests serve as regression guards — they should keep passing through each refactor step.
- **Commit cadence**: at the bottom of each task. Don't accumulate.
- **If a step fails**: investigate root cause; don't paper over. If a refactor breaks a test in a way that reveals an intentional behavior change, update the test deliberately (in a commit that explains why).
- **gofmt + vet on every commit**: `gofmt -l .` should always return empty.
- **Cross-file references**: when a step refers to a type/method defined in another task, verify the earlier task is complete first.
- **Phase boundaries are PR boundaries** if review insists; default is one big PR to keep the architectural change atomic.
