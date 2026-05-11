# Unified state interface (read + mutate + apply) — design

**Status:** draft
**Author:** Claude (with Yingte)
**Date:** 2026-05-11
**Scope:** `internal/config/`, `internal/daemon/`, `internal/state/` (new), `internal/cron/` (new), `internal/lifecycle/` (new, refactored from `cmd/eidos/supervisor/`), `cmd/eidos/{gate,supervisor,forge}/`
**Related specs:** `2026-05-09-unified-call-path-design.md` (predecessor — single CLI/dashboard call path), `2026-05-09-mindforge-v0-design.md`, `2026-05-11-forge-heartbeat-config.md` (the immediate trigger)

## §1 — Goal

Two outcomes, one architecture:

1. **Heartbeat hot-reload** — `eidos gate config set heartbeat.interval 1m` (issued by the mindform itself, or by the operator via `eidos forge config <name> --heartbeat-interval 1m`) takes effect **without `docker restart`**. The container's crond picks up the new cadence in-place.
2. **Generic operator/mindform adjustment + read interface** — every mutation funnels through one helper (`daemon.Mutate`) with a per-state-path Apply hook; every read funnels through one method (`state.get [path]`) with a path selector. **Operator and mindform use the same IPC method table against the same state schema**; the only structural asymmetry is that the mindform's container PID-1 additionally hosts lifecycle goroutines (wake / crontab / agent / planner) on top of the shared daemon core.

The architectural principle is added to `CLAUDE.md`:

> **Operator and mindform reach the same state through the same call path.** Every adjustment and every read goes through the daemon's `state.get` / `daemon.Mutate` framework. Surfaces (CLI, dashboard, `eidos forge` host wrappers, future MCP/NIP-46 thin-clients) are parameter-packing shims; they do not implement state semantics.

This strengthens the existing "Single Call Path" rule (CLI vs dashboard) by extending it across the operator/mindform boundary.

## §2 — Why now

Three signals from PR #57 (the forge heartbeat-config feature) and deploy-test 005:

1. **`eidos forge config <name> --heartbeat-interval` carries a `docker restart` shim** (`cmd/eidos/forge/config.go:115-129`). This is because supervisor reads `[heartbeat] interval` only at PID-1 startup (`cmd/eidos/supervisor/run.go:55-72`). The shim works for the operator surface but **has no mindform-side analogue** — a mindform inside its container has no docker socket, so it cannot self-adjust heartbeat. Deploy-test 005 exposed this: Alice can find the knob but cannot apply it without messaging her master.
2. **State reads are scattered by domain** — `whoami` / `config.get` / `contact.list` / `inbox.list` / `relays.health` / `service.status` / host-side `forge status` / in-container `forge runtime-state`. Each surface invents its own composition. No "show me my current state" verb.
3. **Daemon-vs-supervisor split is historical, not principled.** Today the supervisor is PID-1 but `config.toml`, contacts, relays, and Nostr state all live in the gate daemon child. The supervisor only owns the crontab and wake dir. When a config knob needs to touch a supervisor-owned resource, the codebase has to invent cross-process plumbing (today: `docker restart`; in earlier sketches: sentinel files, fsnotify, etc.). Each one of these is a leak of the underlying issue: **the container has two state owners pretending to be one**.

## §3 — Architecture overview (β: single PID-1)

Container PID-1 is a single Go process that combines:

- IPC server + state authority (the daemon core)
- Container lifecycle: wake-dir fsnotify, crontab render/install, planner loop, agent-runner spawn, dream-state bookkeeping

Host `eidos gate daemon` is the same shared core **without** the lifecycle layer.

```
┌────────────────────────── Host ──────────────────────────┐
│  CLI / dashboard / forge wrappers                        │
│           │ IPC unix socket                              │
│           ▼                                              │
│  eidos gate daemon (PID)                                 │
│   internal/daemon — IPC + state authority                │
└──────────────────────────────────────────────────────────┘

┌────────────────────────── Container ─────────────────────┐
│  PID-1: eidos supervisor run                             │
│   internal/daemon — same IPC + state authority           │
│           +                                              │
│   internal/lifecycle — fsnotify wake, crontab, planner,  │
│                        agent-runner, dream state         │
│           │                                              │
│           ├── crond (root child, busybox needs it)       │
│           └── agent-runner (short-lived per wake)        │
└──────────────────────────────────────────────────────────┘
```

**Operator/mindform asymmetry, made precise:**

| Surface | State authority interface | Lifecycle interface |
|---|---|---|
| Host `eidos gate daemon` | Yes (identical) | No |
| Mindform PID-1 | Yes (identical) | Yes |

The mindform has more state paths (`lifecycle.wakes`, `lifecycle.session`, `lifecycle.dream`, `container.*`) because it has more state to expose. The verbs and the schema for the **shared** paths are byte-identical.

**Fault model:** if any goroutine inside the merged PID-1 crashes, the process exits → docker restart-policy `unless-stopped` brings it back. Same semantics as today's supervisor watching the daemon child and cancelling on unexpected exit.

## §4 — Components

```
internal/
├── config/                   — Key registry (extended)
│   ├── config.go              Config struct (unchanged)
│   └── keys.go                Key struct + Contexts bitmask
│
├── state/                    — NEW: state tree + apply dispatch
│   ├── tree.go                dotted-path resolution, subtree extraction
│   ├── source.go              StateContributor interface
│   └── apply.go               ApplyHook registry + Dispatch
│
├── daemon/                   — IPC + state authority (shared core)
│   ├── daemon.go              Daemon struct; Run(ctx, Options)
│   ├── methods.go             methodTable
│   ├── methods_state.go       NEW: state.get implementation
│   ├── mutate.go              NEW: common Mutate helper
│   ├── methods_*.go           per-domain mutation handlers, all through Mutate
│   └── handler.go             dispatch (unchanged)
│
├── cron/                     — NEW: crontab rendering + installation
│   ├── render.go              busybox crontab body from heartbeat.interval
│   └── install.go             sudo tee + chmod 0600 (atomic via tmp+rename)
│
└── lifecycle/                — NEW: container-only (extracted from cmd/eidos/supervisor/)
    ├── wake.go                fsnotify wake-dir, drainPending, runAgentForWake
    ├── planner.go             plan signal scanner
    ├── agent.go               agent-runner spawn + transcript persistence
    ├── dream.go               dream-state bookkeeping
    ├── crontab.go             initial render at startup; apply hook takes over after
    ├── deps.go                ApplyDeps struct (concrete shape for container hooks)
    ├── submitter.go           Submitter wraps wake.Submit for apply-hook use
    └── attach.go              Attach(d *daemon.Daemon) — registers state contributors
                               + apply hooks for container context

cmd/eidos/
├── gate/
│   ├── daemon.go              host PID entry: daemon.Run(ctx, Options{Lifecycle: nil})
│   ├── state.go               NEW: `eidos gate state [path]` CLI
│   ├── config.go              kept; config get/set become facades over state.get/set
│   └── (other CLI)            whoami/send/inbox/contact become facades over state.get
│
├── supervisor/
│   └── run.go                 container PID-1 entry: daemon.Run(ctx, Options{
│                                Lifecycle: lifecycle.New()})
│                                lifecycle.Attach(d) registers contributors + apply hooks
│
└── forge/
    └── (host wrappers)        forge config / forge status / forge runtime-state etc.
                                become thin `docker exec eidos-mindform-<name> eidos
                                gate state ...` / `eidos gate config set ...`
                                The auto-restart shim is removed.
```

**Dependency arrows (all single-direction):**

```
cmd/eidos/supervisor/run.go
        │
        ▼
internal/lifecycle  ─►  internal/cron   ─►  internal/config (read schema only)
        │
        ▼
internal/daemon  ─►  internal/state  ─►  internal/config
```

`internal/config` and `internal/state` never reach into `internal/cron` or `internal/lifecycle`. This is what makes the Apply hook registration "at runtime by lifecycle" mandatory (§5.2).

## §5 — Key interfaces

### §5.1 Key registry (extended)

```go
// internal/config/keys.go
type Context uint8

const (
    HostCtx      Context = 1 << 0
    ContainerCtx Context = 1 << 1
    BothCtx              = HostCtx | ContainerCtx
)

type Key struct {
    Path        string
    Description string
    Get         func(*Config) string
    Set         func(*Config, string) error
    Contexts    Context  // NEW: which daemon contexts may set this key
    // Apply intentionally NOT here — registered at runtime by lifecycle
    // to keep internal/config independent of cron/lifecycle.
}
```

Existing keys get a `Contexts` value:
- `heartbeat.interval`, `mindform.model` → `ContainerCtx`
- `dashboard.enabled`, `dashboard.listen`, `log_level`, `daemon.socket`, `daemon.shutdown_grace_seconds` → `BothCtx`

### §5.2 Apply hook registry

The registry is an **instance** owned by the daemon (not a package-level global) so two daemons in the same process — e.g., a unit test spinning up a host-context and a container-context daemon back-to-back — don't share registrations.

`ApplyDeps` is intentionally typed as `any` (interface{}) so `internal/state` does **not** import `internal/daemon`, `internal/cron`, or `internal/lifecycle` — avoiding the import cycle that comes from "state knows what hooks need." The package that registers a hook also defines the concrete deps shape it expects and casts on entry.

```go
// internal/state/apply.go
package state

type ApplyFunc func(ctx context.Context, deps any, old, new any) error

// Optional opt-out for default rollback behavior on apply failure.
type Rollbacker interface{ Rollback() bool }

type ApplyRegistry struct{ ... }   // instance, not global

func NewApplyRegistry() *ApplyRegistry
func (r *ApplyRegistry) Register(path string, fn ApplyFunc)
func (r *ApplyRegistry) Dispatch(ctx context.Context, deps any, path string, old, new any) error
```

The concrete deps shape, defined by `internal/lifecycle` (the only place container apply hooks live today):

```go
// internal/lifecycle/deps.go
package lifecycle

type ApplyDeps struct {
    Daemon *daemon.Daemon   // for emitting events, accessing other state
    Cron   *cron.Installer  // present iff container lifecycle attached
    Wake   *Submitter
}
```

Hook implementations cast on entry:

```go
// Registered by lifecycle.Attach:
r.Register("config.heartbeat.interval", func(ctx, depsAny, old, new any) error {
    deps := depsAny.(*lifecycle.ApplyDeps)
    body, err := cron.Render(new.(string))
    if err != nil { return err }
    return deps.Cron.Install(ctx, body)
})
```

The daemon's `Mutate` passes whatever `applyDeps()` returns; host daemon passes `nil` (no hooks registered → no dispatch); container daemon passes `*lifecycle.ApplyDeps`.

### §5.3 State tree

```go
// internal/state/source.go
type StateContributor interface {
    Path() string                                   // mount path, e.g. "lifecycle.wakes"
    Snapshot(ctx context.Context) (any, error)
}

// internal/state/tree.go
type Tree struct{ ... }

func (t *Tree) Register(c StateContributor)
func (t *Tree) Snapshot(ctx context.Context, path string) (any, error)
// path == ""  → full root snapshot
// path == "config.heartbeat.interval" → walks tree, returns scalar
// path == "contacts" → returns the collection from the contacts contributor
// path == "contacts.<pubkey>" → returns one entry (if the contributor supports indexed access)
// unknown path → ipc.ErrPathNotFound
```

Contributors registered by the daemon core (host + container):
- `identity` → npub/hex/label snapshot
- `config` → full Config struct
- `contacts` → list with indexed access by pubkey
- `relays` → list with health states
- `inbox` → { unread_count, recent }
- `outbox` → { recent }
- `service` → { socket, started_at, version }
- `invites` → list

Container-only contributors registered by `internal/lifecycle/attach.go`:
- `lifecycle.wakes` → recent wake list (today's `forge runtime-state` wake-side fields)
- `lifecycle.session` → session id, started_at, wakes_in_session
- `lifecycle.dream` → dream-state and last-dream metadata
- `container` → uptime, image, started_at

### §5.4 Daemon options

```go
// internal/daemon/daemon.go
type Options struct {
    StateDir  string
    Context   config.Context  // HostCtx or ContainerCtx
    ApplyDeps any             // nil on host; *lifecycle.ApplyDeps in container
    Lifecycle Lifecycle       // nil on host; container provides hook attachment
}

type Lifecycle interface {
    Attach(d *Daemon) error   // registers state contributors + apply hooks
    Run(ctx context.Context) error  // long-running goroutines (wake/planner/etc.)
}

func Run(ctx context.Context, opts Options) error
```

`cmd/eidos/gate/daemon.go` calls `daemon.Run(ctx, Options{Context: HostCtx, Lifecycle: nil, ...})`. `cmd/eidos/supervisor/run.go` calls `daemon.Run(ctx, Options{Context: ContainerCtx, Lifecycle: lifecycle.New(), ApplyDeps: lcDeps, ...})`.

### §5.5 Mutation framework

```go
// internal/daemon/mutate.go
//
// Every mutation handler in methods_*.go funnels through Mutate.
// Sequence: context check → lock → write → apply → emit → unlock.
// On apply failure: default rollback via re-running write with old value.

func (d *Daemon) Mutate(
    ctx context.Context,
    path string,
    requires config.Context,  // which daemon context this mutation requires
    write func() (oldSnap any, newSnap any, err error),
) error {
    if d.ctx & requires == 0 {
        return d.contextMismatchError(path, requires)
    }
    d.stateMu.Lock()
    defer d.stateMu.Unlock()

    old, new, err := write()
    if err != nil {
        return err
    }

    if err := d.applyRegistry.Dispatch(ctx, d.applyDeps(), path, old, new); err != nil {
        if rollbackable(err) {
            if rbErr := d.rollback(path, old); rbErr != nil {
                return ipc.WrapInconsistent(err, rbErr)
            }
            return err
        }
        return err
    }

    d.emitStateChanged(path, new)
    return nil
}
```

Existing handlers (`configSet`, `contactAdd`, `relayAdd`, etc.) each shrink to: parse params, build a closure that does the per-domain write, and call `d.Mutate(ctx, statePath, requires, closure)`.

## §6 — Data flow

### §6.1 Read

```
shell                              daemon
─────                              ──────
eidos gate state config.heartbeat.interval
  │ IPC: {method: "state.get", params: {path: "config.heartbeat.interval"}}
  ▼
methods_state.go:stateGet
  │ tree.Snapshot(ctx, "config.heartbeat.interval")
  ▼
state.Tree
  │ resolve "config" contributor → load config.toml → Config struct
  │ walk remaining "heartbeat.interval" on struct → "1m"
  ▼
stdout: "1m"
```

No path → full root snapshot (JSON).

### §6.2 Mutation with apply (heartbeat hot-reload)

```
shell                              PID-1 (container, single proc)
─────                              ──────────────────────────────
eidos gate config set heartbeat.interval 1m
  │ IPC: {method: "config.set", params: {path: "heartbeat.interval", value: "1m"}}
  ▼
configSet
  │ key = config.KeyByPath("heartbeat.interval")  (Contexts=ContainerCtx)
  │ d.Mutate(ctx, "config.heartbeat.interval", ContainerCtx, write):
  │   ctx & ContainerCtx → ok
  │   stateMu.Lock()
  │   write():
  │     load config.toml
  │     key.Set(&cfg, "1m")     ─── validate + in-memory set
  │     save config.toml ────────────────────► /eidos/gate/config.toml
  │     return old="2h", new="1m"
  │   d.applyRegistry.Dispatch(ctx, deps, "config.heartbeat.interval", "2h", "1m"):
  │     hook registered by lifecycle.Attach():
  │       body = cron.Render("1m")
  │       cron.Install(ctx, body)  ────────────► /var/spool/cron/crontabs/eidos
  │                                              (sudo tee + chmod 0600, atomic)
  │                                              (busybox crond mtime → reload)
  │   emitStateChanged("config.heartbeat.interval", "1m")
  │   stateMu.Unlock()
  ▼
return ok  (new cadence is live by the time caller resumes)
```

### §6.3 Host → mindform (thin shim path)

```
operator shell (host)              mindform container PID-1
─────────────────────              ────────────────────────
eidos forge config alice --heartbeat-interval 1m
  │ forge config wrapper:
  │   docker exec eidos-mindform-alice eidos gate config set heartbeat.interval 1m
  │                              ─────────────► §6.2 path verbatim
  │ docker exec returns ok
  ▼
forge config returns ok (no docker restart step)
```

Mindform self-adjusting walks the identical path with the `docker exec` prefix removed.

### §6.4 Context mismatch (bitmask gate)

```
operator shell                     host gate daemon
──────────────                     ────────────────
eidos gate config set heartbeat.interval 1m
  │ IPC: config.set on HOST daemon
  ▼
configSet
  │ key.Contexts = ContainerCtx
  │ d.Mutate(..., requires=ContainerCtx, ...)
  │   d.ctx = HostCtx
  │   d.ctx & ContainerCtx == 0 → mismatch
  ▼
return ipc.Error{
  Code:    "CONTEXT_MISMATCH",
  Message: `heartbeat.interval is a mindform-only key.
            To set it on a specific mindform:
              eidos forge config <name> --heartbeat-interval 1m`,
}
```

### §6.5 State-changed event bus

After every successful `d.Mutate`, the daemon emits `state.changed{path, new_value}`. The dashboard SSE bus (existing) subscribes. Future CLI `--watch` modes and other internal subscribers use the same bus. The event does **not** carry the old value (avoids leaking privacy-sensitive state like contact labels into the event stream); subscribers needing the diff cache their last `state.get`.

## §7 — Error handling

### §7.1 Validate failure (`Key.Set` rejects)

Return `ipc.ErrInvalidParams` with the underlying validator message. No write, no lock, no apply.

### §7.2 Context mismatch

`daemon.Mutate` entrance, before locking. Return `ipc.ErrContextMismatch` with a message that points to the correct verb.

### §7.3 Write failure

`config.Save` uses tmp + rename; atomic. Contact / relay stores are sqlite — transaction rolled back on failure. Return `ipc.ErrIO`. No apply; no emit.

### §7.4 Apply failure (core difficulty)

Default policy: **rollback**. The framework re-invokes the write closure with the old value, re-saves, and **does not** re-dispatch apply (the rollback is "best-effort consistency with last-applied state," not a recursive mutation).

Apply hooks may opt out by returning an error that implements `Rollbacker` with `Rollback() == false`. Used when the side-effect is genuinely irreversible (e.g., a relay reconnect that's already been initiated).

Apply hook design rules (binding contract, enforced by review):
- Prefer idempotent + atomic operations (render → tmp → rename → reload signal).
- Two-phase shape ("prepare" + "commit") so prepare failure short-circuits without rollback.
- Must complete in <100 ms. Longer work spawns its own goroutine; apply returns ok and follows up via `state.changed`.

If rollback itself fails (e.g., disk full), the framework returns `ipc.ErrInconsistent` wrapping both errors and logs loudly. Docker restart is the final backstop — `lifecycle.Attach` re-runs initial apply at PID-1 startup, reconciling state.

### §7.5 Read failure

- Unknown path → `ipc.ErrPathNotFound`
- Contributor error → `ipc.ErrInternal` with contributor name in message
- Full-tree snapshot is fail-fast: any contributor error fails the whole call (avoids misleading partial views)

### §7.6 Lifecycle attach failure (container startup)

- Initial crontab render failure → log warning, fall back to `config.DefaultHeartbeatInterval` (matches today's `supervisor/run.go:65` policy). PID-1 continues; mindform still serves IPC.
- Apply hook registration failure → panic; should be impossible (in-memory map insertion); docker restarts as a recovery sentinel.

### §7.7 Panic recovery

`daemon.Mutate` wraps both the write closure and the apply dispatch in `defer recover()`. Panics convert to `ipc.ErrInternal` with stack trace in the daemon log. `stateMu` is released via the deferred `Unlock`.

## §8 — Testing strategy

### §8.1 Unit — state tree

`internal/state/tree_test.go`:
- `TestTree_RootSnapshot` — two fake contributors merge into root
- `TestTree_DottedPath` — scalar at `config.heartbeat.interval`
- `TestTree_PathNotFound` — unknown path returns `ErrPathNotFound`
- `TestTree_ContributorError` — one contributor errors → full snapshot fails; targeted path only fails for affected subtree
- `TestTree_NestedScalarVsCollection` — `contacts` returns list, `contacts.<pubkey>` returns one entry

### §8.2 Unit — Mutate framework

`internal/daemon/mutate_test.go`:
- `TestMutate_HappyPath` — fake key + fake apply hook; verify lock order, `emitStateChanged` called
- `TestMutate_ApplyFailure_DefaultRollback` — apply errors, no `Rollbacker`; verify rollback re-invoked, emit not sent
- `TestMutate_ApplyFailure_NoRollback` — apply returns error with `Rollback() == false`; verify no rollback, error propagates
- `TestMutate_ApplyFailure_RollbackAlsoFails` — both fail; verify `ErrInconsistent` composite
- `TestMutate_ContextMismatch` — container-only key, host context → no lock, no write, error includes correct verb
- `TestMutate_ConcurrentWrites` — two goroutines mutate same path; serialized; each apply sees its own old/new
- `TestMutate_PanicRecovery` — write panic / apply panic; lock released, error returned

### §8.3 Unit — cron package

`internal/cron/render_test.go`:
- `TestRender_AllSupportedIntervals` — all 18 valid intervals → assert cron expression shape
- `TestRender_DefaultFallback` — empty → 2h via `DefaultHeartbeatInterval`
- `TestRender_InvalidInterval` → error

`internal/cron/install_test.go` (installer override for sudo/tee path):
- `TestInstall_Success` — fake spool written; content + 0600 perm
- `TestInstall_SudoFailure` — fake sudo nonzero → error, original spool untouched (atomic tmp+rename)

### §8.4 Integration — heartbeat hot-reload

`test/integration/heartbeat_hot_reload_test.go` (build tag `integration`, requires docker):
- Start a real mindform container
- `docker exec eidos-mindform-test eidos gate config set heartbeat.interval 1m`
- Assert: (1) command returns ok synchronously; (2) `docker exec ... cat /var/spool/cron/crontabs/eidos` shows new cadence; (3) container `State.StartedAt` unchanged across the mutation (no docker restart)
- Reverse: `eidos gate config set heartbeat.interval 1m` on host daemon returns `ErrContextMismatch`

### §8.5 Existing tests carried forward

- `internal/firstcontact/phase3_5_cadence_test.go` — wizard logic untouched; should pass as-is
- `cmd/eidos/forge/config_test.go` — adapted to assert "single `docker exec`, no `docker restart`"
- `cmd/eidos/forge/create_test.go`, `init_volume_test.go` — heartbeat-into-config.toml at create time unchanged
- `cmd/eidos/supervisor/crontab_test.go` — render tests move to `internal/cron/render_test.go`; install tests reshape against new installer

### §8.6 Deploy test 003 — architecture-level acceptance

`deploy-test/003-mindform-heartbeat/wizard-orchestrator.py` is the end-to-end regression. The script's existing flow (Phase A operator wizard → Phase B alice-heartbeat at 2m → Phase C 2× 2m wakes → Phase D `forge config --heartbeat-interval 4m` → Phase E 2× 4m wakes) covers cold-render and hot-reload in one run.

New assertions to add (~15-20 lines of Python):
- Capture `docker inspect --format '{{.State.StartedAt}}'` before and after Phase D; assert equal. This is the bright-line proof that the auto-restart shim is gone.
- Immediately after Phase D, run `docker exec eidos-mindform-alice-heartbeat eidos gate state config.heartbeat.interval` and assert it returns `"4m"`. Proves state.get is wired and the write is synchronously visible.
- Record the wall-clock delay between Phase D completion and the first observed 4m wake. Useful for future tuning of busybox crond reload behavior.

### §8.7 Out of scope for testing

- busybox crond's mtime detection latency — upstream behavior, not ours
- Race conditions from `docker restart` — removed by this design

## §9 — Migration plan

The user has chosen one-shot cutover. No deprecation period.

**Read methods removed in this PR (migrate to `state.get <path>`):**
- `whoami` → `state.get identity`
- `card.export` → `state.get identity.card` (card is identity + relays serialized)
- `contact.list` → `state.get contacts`
- `contact.get` → `state.get contacts.<pubkey>`
- `relay.list` / `relays.health` → `state.get relays` (health field included in each entry)
- `inbox.list` / `inbox.tail` → `state.get inbox` (params for filter/limit)
- `outbox.list` → `state.get outbox`
- `config.get` → `state.get config[.path]`
- `service.status` → `state.get service`
- `lifecycle.status` → `state.get service.lifecycle`
- `version` → `state.get service.version`

Each removal accompanied by the corresponding CLI/dashboard surface migrating. Tests that exercised these methods migrate alongside.

**Pure-function utility RPCs (kept as-is, not state):**
- `card.parse`, `card.scan` — parse/validate an external card; no state read or write, no IPC change needed. They could just as well live in client code, but centralizing the parser avoids version drift.

**Mutation methods refactored (kept by name, internally rewired through `d.Mutate`):**
- `config.set`, `set-label`, `contact.add`, `contact.remove`, `contact.set-label`, `contact.set-tier`, `contact.add-from-card`, `relay.add`, `relay.remove`, `send`, `invite.create`, `invite.revoke`, `invite.redeem`, `subscribe.refresh`, `lifecycle.run`

Each handler reshapes to `d.Mutate(ctx, statePath, requires, writeFn)`.

**Special-purpose RPCs (not state mutations, kept as-is):**
- `daemon.exec-replace` — exec-replaces the daemon binary (self-update path); side-effect is process replacement, not state. Stays a one-off handler.
- `version` (today) — folds into `state.get service.version` and the standalone method is removed.

**Apply hooks registered by `internal/lifecycle/attach.go` (container only):**
- `config.heartbeat.interval` → render + install crontab (synchronous, atomic)
- `config.mindform.model` → no-op (agent-runner re-reads on next wake)
- `config.log_level` → no-op (logging reads on each emit)
- Other config keys → no-op (effective at next daemon startup; should be rare in steady state)
- Non-config paths (`contacts.*`, `relays.*`, ...) — no apply hooks in this PR; each domain's existing handler already does what apply would; we keep them inline for now to keep diff size bounded

**Host-side forge wrappers:**
- `eidos forge config <name>` — drops `docker restart` step; remains a `docker exec`-only shim into `gate config set`
- `eidos forge status <name>` — sources from `docker exec ... eidos gate state` (multiple subtree calls or one full snapshot) + pretty-print
- `eidos forge list` — sources from per-container `docker exec ... eidos gate state service,container` + tabular render
- `eidos forge watch <name>` — `--list` mode replaces internal `runtime-state` parsing with `gate state lifecycle.wakes`; follow mode reuses the existing transcript tail path (out of scope for this refactor; transcripts are not state in the sense covered here)

**In-container forge reflection commands:**
- Pure state reads (`forge whoami`, `forge inbox`, `forge memory`, `forge ontology-status`, `forge runtime-state`, `forge transcript-list`) become thin shells that internally call `eidos gate state <subtree>` and render. The mindform-facing CLI names are preserved — Alice's mental model and the constitution's references to `eidos forge whoami` don't change.
- Lifecycle verbs (`forge plan add/list/remove`, `forge dream begin/end`, `forge wake`) remain as today; they're mutations against `lifecycle.*` state paths and route through `daemon.Mutate` after the refactor. No CLI rename.
- `forge runtime-state` JSON output shape is preserved verbatim for backward compat with any external readers; internally it's now `state.get` selecting `lifecycle.wakes,session,container,service` and projecting into the old shape.

**CLAUDE.md addition:**
The "Single Call Path" section gets a new paragraph:

> **Operator/mindform symmetry.** Every adjustment and every read in the same state schema is reachable through the same IPC method table, regardless of whether the caller is the operator (against the host gate daemon or via `docker exec`) or the mindform (in-container against its own PID-1). Surfaces are parameter-packing wrappers; host-side `eidos forge config <name> ...` and in-container `eidos gate ...` MUST funnel through identical method calls. Don't add a host-only or container-only side-channel for state actions. State that exists only in one context (e.g., `lifecycle.*` on the mindform) is fine — the schema can have context-specific subtrees — but the verb that touches it is the same.

## §10 — Out of scope

- Splitting `internal/state` further (e.g., separate persistence per domain). The current `internal/daemon` already has per-domain stores; this design keeps them.
- Renaming `supervisor` (the binary subcommand `eidos supervisor run`) — the name remains; only the package boundaries inside change.
- Removing the in-container `eidos forge plan` / `eidos forge dream` subcommands. These remain as in-container CLIs because the mindform's natural verb is `forge plan add`, not `gate state.set lifecycle.plans.add` — they map to mutation methods today and continue to.
- Adding `Apply` hooks for non-config state paths (contacts, relays, etc.) in this PR. The mutation framework is in place; per-domain apply hooks land as need arises.
- Generalizing `Contexts` beyond Host/Container (e.g., per-mindform-instance). The bitmask is two bits; future work can grow it.
- Migrating `eidos forge status` JSON shape changes that aren't strictly required (e.g., renaming fields). We re-source the same data; we don't re-design the operator output.
