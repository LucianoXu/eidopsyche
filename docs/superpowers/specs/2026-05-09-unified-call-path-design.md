# Unified call path for all caller surfaces — design

**Status:** draft, awaiting review
**Author:** Claude (with Yingte)
**Date:** 2026-05-09
**Scope:** `internal/daemon/`, `internal/dashboard/`, `cmd/eidos/gate/`, `internal/ipc/`
**Related specs:** `2026-05-07-dashboard-design.md`, `2026-05-08-dashboard-feature-parity-design.md`
**Implements** the SPEC.md "调用路径统一" section.

## §1 — Goal

Make CLI, dashboard webui, and future Agent MCP / NIP-46 thin-client surfaces funnel every action through the daemon's existing `methodTable` — the same dispatcher that backs the IPC unix socket. Each surface becomes a thin parameter-packing wrapper and renders the result. No surface implements the action itself.

The principle (now in SPEC.md):

> **每个能动作只允许有一条代码路径。** 所有调用方必须通过 daemon 内部的同一个分发器调用，传递同样打包格式的参数（`{method, params: JSON}`），获取同一组类型化错误码。

This document is the migration roadmap that brings the existing code into compliance.

## §2 — Why now

The 2026-05-08 dashboard feature-parity design explicitly stated "the dashboard handlers route into the *same* daemon code paths the CLI uses (via existing IPC methods plus a few new ones)". The implementation drifted. As of 2026-05-09 we observe:

| Operation | IPC method (CLI path) | Dashboard path | Drift |
|---|---|---|---|
| Send message | `send` (`methodTable["send"]`) | `dashboardAdapter.Send` — 78-line reimplementation | Skips `resolveTarget` (no npub→hex), skips contact-existence check, drops the two-phase outbox write, returns plain `fmt.Errorf` instead of `ipc.ErrNoRelaysReachable` |
| Set config key | (none) — CLI writes file directly via `config.Save` | `dashboardAdapter.ConfigSet` writes file directly under `a.d.configMu` | Three-way fork: no IPC method, two writers, mutex protects only one of them. Concurrent writes from CLI + dashboard race. |
| Get/set contact tier | (none — gap) | `dashboardAdapter.SetContactTier` calls `Repo.SetTier` directly | No IPC method exists. CLI cannot do it. |
| Get single contact | (none — only `contact.list`) | `dashboardAdapter.GetContact` → `Repo.Get` | No IPC method exists. |
| Add contact | `contact.add` (npub + relays + label + tier) | `dashboardAdapter.AddContact(cardURI, labelOverride)` — parses card, supports refresh-on-existing | API shape diverges; dashboard upserts where IPC errors with `CONTACT_EXISTS`. |
| Remove contact | `contact.remove` (uses `resolveTarget`) | `dashboardAdapter.RemoveContact(pubkey)` | Dashboard accepts hex only; CLI accepts npub/hex/label. |
| Card export | `card.export` | `dashboardAdapter.OwnCardURI` | Same logic copied. |
| Identity (label, npub) | `whoami` (combined) | `OwnPubkey` (field read) + `OwnLabel` (`SELECT … FROM meta`) | Field-read shortcut bypasses any future invariants `whoami` adds. |
| Set own label | `set-label` | `dashboardAdapter.SetOwnLabel` — copy of the handler body | Same logic copied. |
| Lifecycle ops | (none) | `dashboardAdapter.LifecycleRun` forks `os.Args[0] gate <subcmd>` | No IPC method; only the dashboard surface can run it. |
| Service status | (composes from `version` + lifecycle status snapshot) | `dashboardAdapter.Status` — direct field reads | No single IPC method covers it. |
| Card scan (parse + already-contact flag) | `card.parse` (no `already` flag) | `dashboardAdapter.ScanCard` adds the flag | IPC half-cover; dashboard adds the missing piece locally. |
| List own relays | `relay.list` (strips `AddedAt`) | `dashboardAdapter.ListOwnRelays` (keeps `AddedAt`) | Projection diverges. |
| List inbox / outbox | `inbox.list` / `outbox.list` (with `resolveTarget` for `from`/`to`) | `dashboardAdapter.ListInbox` / `ListOutbox` (no resolveTarget) | Dashboard cannot accept npub/label as filter. |
| Invite create / list / revoke / redeem | `invite.*` IPC | `dashboardAdapter.*Invite` — error-sentinel translation duplicated | Two translation tables. |
| Add / remove relay | `relay.add` / `relay.remove` | `dashboardAdapter.AddOwnRelay` / `RemoveOwnRelay` — error-sentinel translation duplicated | Two translation tables. |

The **structural** cause: `internal/dashboard` cannot import `internal/daemon` (cycle), so the adapter lives in `internal/daemon/dashboard_adapter.go`. Once the adapter sits inside the daemon package, calling `a.d.Repo.Get` / `a.d.Pool.Publish` / inline SQL is one keystroke shorter than going through the dispatcher. The path of least resistance is also the path of drift.

## §3 — Target architecture

```
                       caller surfaces
        ┌─────────────┬──────────────────┬──────────────┐
        │             │                  │              │
   eidos gate*     dashboard         MCP server     NIP-46 RPC
   (CLI cobra)    (HTTP handlers)   (MCP tools)    (Nostr-mediated)
        │             │                  │              │
        ▼             ▼                  ▼              ▼
   ipc.Client    daemon.Call    daemon.Call    bridge → daemon.Call
   (unix sock)   (in-process)   (in-process)   (Nostr-mediated)
        │             │                  │              │
        └────┬────────┴──────────────────┴──────────────┘
             ▼
        ┌────────────────────────────────────┐
        │   daemon.methodTable[name](…)      │   ← single source of truth
        │   internal/daemon/methods.go       │
        └────────────────────────────────────┘
                       │
                       ▼
            internal/{contacts,store,inbox,
            invitedb,nostr,identity,config,…}
```

Two guarantees this architecture provides:

1. **One implementation per action.** Adding contact-existence check / rate limiting / audit logging to a method changes its behavior for every surface at once. No drift.
2. **One stable contract.** The `(method, params, error code)` triple is the public schema. Any new surface (MCP, NIP-46, future mobile thin client) implements only `pack params → call → render result`.

## §4 — The in-process call helper

We add one method to `*daemon.Daemon`. It mirrors `ipc.Client.Call` so every caller (CLI over the socket, dashboard adapter / MCP server in-process) sees the same shape:

```go
// internal/daemon/inproc.go (new file)
package daemon

import (
    "context"
    "encoding/json"
    "github.com/LucianoXu/eidopsyche/internal/ipc"
)

// Call invokes a registered IPC method by name without going through the
// unix socket. params is marshaled to JSON, the method's handler runs, and
// the result is unmarshaled into out (which may be nil to discard).
//
// In-process callers (dashboard adapter, MCP server, future surfaces)
// MUST use Call rather than reaching into Daemon internals. The IPC
// socket and Call share the exact same handler functions, so any
// behavior change to a method takes effect for both transports.
//
// Naming note: this mirrors ipc.Client.Call's signature so the dashboard
// adapter and a future MCP server present the same shape as the CLI's
// transport-bound caller. The existing dispatchEnvelope on *Daemon is
// unrelated — it routes inbound Nostr rumors, not IPC method calls.
func (d *Daemon) Call(ctx context.Context, method string, params any, out any) error {
    fn, ok := methodTable[method]
    if !ok {
        return &ipc.Error{Code: ipc.ErrUnknownMethod, Message: method}
    }
    var raw json.RawMessage
    if params != nil {
        b, err := json.Marshal(params)
        if err != nil {
            return &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
        }
        raw = b
    }
    res, ipcErr := fn(ctx, d, nil, raw)  // conn=nil; streaming methods are excluded (see §4.1)
    if ipcErr != nil {
        return ipcErr
    }
    if out != nil && res != nil {
        b, err := json.Marshal(res)
        if err != nil {
            return &ipc.Error{Code: ipc.ErrInternal, Message: err.Error()}
        }
        if err := json.Unmarshal(b, out); err != nil {
            return &ipc.Error{Code: ipc.ErrInternal, Message: err.Error()}
        }
    }
    return nil
}
```

Notes on this contract:
- **`*ipc.Error` implements `error`** (already does — `Code` + `Message` are sufficient, but we add a `func (e *Error) Error() string` method if missing). Callers can `errors.As` it to recover the typed code.
- **The marshal/unmarshal round-trip is intentional.** It enforces that the in-process call is bound by the same JSON contract as the socket call: no Go-typed shortcut can sneak in. Cost is negligible (microseconds per call).
- **`conn` is nil.** See §4.1.

### §4.1 — Streaming methods stay out of `Call`

`inbox.tail` registers the caller's `*ipc.Conn` for push events. It has no in-process equivalent because the dashboard already gets push events through `*Daemon.subscribeDashboard()` (in-process channel). Likewise for the lifecycle stream.

Rule: streaming subscriptions are surface-specific and live outside `Call`. `Call` is unary RPC only. The list of streaming endpoints is short and stable: `inbox.tail` (CLI) and `lifecycle.line:<jobID>` SSE (dashboard).

### §4.2 — Typed param/result structs

Each method's params/results today are anonymous structs inside the handler (e.g. `var p struct { To string; Envelope *envelope.Envelope }`). Once `Call` has multiple callers building those payloads, the structs need names so callers don't pass `map[string]any{...}`.

We will:
- Promote each method's param struct to a named type next to the handler: `type SendParams struct { To string; Envelope *envelope.Envelope }`.
- Promote each method's result struct to a named type: `type SendResult struct { EventID string; AcceptedBy []string }`.
- Keep them in `internal/daemon/` (same package as the handler) — the IPC schema lives with the implementation.

Callers (CLI, dashboard, MCP) import `internal/daemon` only for the param/result types. They call via `Call` (dashboard / MCP) or `ipc.Client.Call` (CLI). No caller imports anything else from `internal/daemon`.

## §5 — IPC methods to add

These are the gaps where the dashboard does today's work entirely outside `methodTable`:

| New method | Params | Result | Replaces |
|---|---|---|---|
| `config.get` | `{path?: string}` | `{value: string}` or full snapshot | CLI `runConfigGet` direct file read; dashboard `ConfigSnapshot` |
| `config.set` | `{path: string, value: string}` | `{}` | CLI `runConfigSet` direct file write; dashboard `ConfigSet` direct file write under in-process mutex (the mutex moves into the handler) |
| `contact.get` | `{target: string}` (npub/hex/label, via `resolveTarget`) | full Contact | dashboard `GetContact` |
| `contact.set-tier` | `{target: string, tier: string}` | `{}` | dashboard `SetContactTier` (and CLI gains the command for free) |
| `card.scan` | `{uri: string}` | `{pubkey, npub, label, relay, already_contact}` | dashboard `ScanCard` |
| `service.status` | none | the existing `dashboard.ServiceStatus` shape | dashboard `Status` (CLI gains a structured `eidos gate status --json` for free) |
| `lifecycle.run` | `{args: []string}` | `{job_id: string}` | dashboard `LifecycleRun` (CLI use case is debatable, but the surface is now uniform) |
| `lifecycle.status` | `{job_id?: string}` | snapshot | composes with `service.status` |

A few existing methods get small upgrades:

- **`send`** — already correct. Dashboard's adapter just stops reimplementing it.
- **`whoami`** — already returns `{pubkey, npub, label, home_relays}`. Replace dashboard's `OwnPubkey` / `OwnLabel` with a `whoami` call that the dashboard caches per-request.
- **`relay.list`** — start returning `AddedAt` so the dashboard projection becomes a strict subset of the IPC projection. CLI ignores the extra field (it's already string-typed in the cobra unmarshal).
- **`inbox.list`** / **`outbox.list`** — already use `resolveTarget` for `from`/`to`. Dashboard adopts them as-is.

## §6 — Migration phases

Each phase ships an independent reviewable surface. After each phase the corresponding dashboard adapter method becomes a 3-line `Call` wrapper. We measure success by line count of `dashboard_adapter.go` shrinking phase-over-phase.

### Phase 1 — Dispatcher + the worst offender (`Send`)

- Add `internal/daemon/inproc.go` with `Call`.
- Promote `sendMessage`'s param/result to `SendParams` / `SendResult`.
- Rewrite `dashboardAdapter.Send` as `return a.d.Call(ctx, "send", SendParams{To: toPubkey, Envelope: &env}, &result)` (3 lines + result projection).
- Decode `pk` to hex inside the dashboard handler **before** calling Send, OR (preferred) let the IPC handler's `resolveTarget` accept the dashboard's raw input. Choose the latter — it's already in the handler.
- Tests: existing `sendMessage` tests stay authoritative; new dashboard send-to-non-contact test asserts `CONTACT_NOT_FOUND` surfaces as a 502 + readable toast.

This single phase fixes the bug that motivated the whole exercise (sending to unknown npub silently going through with fallback relays).

### Phase 2 — Config

- Add `config.get` / `config.set` IPC methods. The handler holds the existing `configMu` lock and emits `config.changed` on success.
- Convert `cmd/eidos/gate/config.go` `runConfigGet` / `runConfigSet` to `ipc.Client.Call`.
- Convert `dashboardAdapter.ConfigSnapshot` / `ConfigSet` to `Call` calls.
- Remove `Daemon.configMu` from the dashboard adapter (it migrates into the handler).
- Tests: a CLI-and-dashboard concurrent-write test that asserts no lost updates (this was previously impossible to write because the lock was process-internal to the adapter only).

### Phase 3 — Contact gaps

- Add `contact.get` / `contact.set-tier` IPC methods.
- Add a CLI subcommand `eidos gate contact set-tier <target> <tier>` (small UX win: tier was dashboard-only before).
- Convert `dashboardAdapter.GetContact` / `SetContactTier` to `Call`.

### Phase 4 — Card scan, service status, lifecycle

- Add `card.scan` (parses + checks already-contact). Replaces the half-coverage of `card.parse`.
- Add `service.status` returning the current dashboard `ServiceStatus` shape.
- Add `lifecycle.run` (returns job id; the existing in-process channel keeps streaming the lines to the SSE hub — `lifecycle.run` does not stream, it just kicks off and returns).
- Convert the four corresponding adapter methods to `Call`.

### Phase 5 — Remaining duplications

- Convert `OwnPubkey` / `OwnLabel` to a single cached `whoami` call per request.
- Convert `SetOwnLabel` to `Call("set-label", …)`.
- Convert `OwnCardURI` to `Call("card.export", …)`.
- Convert `RemoveContact` / `SetContactLabel` to `Call` (gain `resolveTarget` semantics).
- Convert `AddContact(cardURI)` to a new `contact.add-from-card` method that internally calls the existing `contact.add` after parsing — preserves the upsert-on-existing semantics the dashboard wants.
- Convert `ListContacts` / `ListInvites` / `ListInbox` / `ListOutbox` / `ListOwnRelays` / `ListRelayHealth` to `Call`. Projections are reconciled: dashboard accepts whatever shape IPC returns and projects further if needed.
- Convert `AddOwnRelay` / `RemoveOwnRelay` to `Call`. The two error-sentinel translation tables collapse into one inside the handler.
- Convert `Create / Revoke / Redeem Invite` to `Call`.

### Phase 6 — Lock-down

After all surfaces are uniform, prevent regression:

- Add a unit test in `internal/daemon` that scans `dashboard_adapter.go` AST and asserts every adapter method's body is one of:
  - a `Call` call,
  - a streaming subscription helper (whitelist: `SubscribeEvents`),
  - or commented `// surface-specific shim, no daemon work`.
- Add a `// adapter-shim:` comment marker for the small number of methods that legitimately don't need `Call` (e.g. read-only field exposure for already-public data — but we should aim for zero of these).

## §7 — CLI changes summary

The CLI is mostly untouched:
- `eidos gate config get/set` — moves from direct file I/O to `ipc.Client.Call` (Phase 2). Becomes the same shape as every other CLI subcommand.
- `eidos gate contact set-tier <target> <tier>` — new subcommand wrapping the new IPC method (Phase 3).
- `eidos gate status --json` — optional structured output backed by `service.status` (Phase 4). Existing `eidos gate status` text output is unchanged.

Nothing else in `cmd/eidos/gate/` changes.

## §8 — MCP server (forward-compatibility)

Out of scope for this design: the MCP server itself. But since this is the foundation it relies on, the design must allow it. The shape:

- A `cmd/eidos/gate/mcp.go` (or similar) starts an MCP server bound to the same `*daemon.Daemon`.
- Each MCP tool is `pack params → daemon.Call(ctx, method, params, &out) → MCP response`.
- The MCP tool surface is a strict subset of `methodTable`: most methods translate 1:1; lifecycle ops and irreversible destructive ops are gated by the MCP-level capability declaration.

No MCP work is performed in this migration. The migration is the prerequisite.

## §9 — NIP-46 thin clients (forward-compatibility)

Out of scope: the NIP-46 listener implementation. The shape is the same — a Nostr-side bridge translates incoming RPC envelopes into `Call` calls. Same param schemas. Same error codes. The bridge is a transport, not an action.

## §10 — Risks

- **Performance.** Marshal-then-unmarshal on every in-process call. Each call is microseconds; well below relay round-trip times that already dominate. No measured concern. We accept the cost as the price of contract uniformity.
- **Type ergonomics.** Named param/result structs are slightly more boilerplate than today's anonymous-struct-in-handler pattern. Worth it: callers stop building `map[string]any{...}`.
- **Concurrency invariants.** Methods that previously held in-process state (e.g. dashboard's `configMu`) move their locks into the handler. We need to audit handlers that mutate shared state — `config.set` is the only known case. Audit checklist: any new IPC method that writes shared state declares its lock in the handler.
- **Streaming endpoints stay outside `Call`.** A future surface (MCP) wanting push events needs its own subscription bridge. Acceptable: streaming is rare and surface-specific by design.
- **AGENTS.md / SPEC.md text drift.** The principle now lives in three places (SPEC, AGENTS, this doc). We treat SPEC as authoritative; AGENTS and this doc paraphrase. Keep them in sync at PR review time.

## §11 — Test strategy

- **Existing IPC method tests stay authoritative.** Action correctness is owned by the handler.
- **Dashboard adapter tests reduce in scope.** They assert: (a) request shape is correct, (b) error code translates correctly into HTTP / toast, (c) result projection is correct. They no longer assert action correctness.
- **Concurrent-write test (Phase 2).** A go test that fires N CLI-equivalent and N dashboard-equivalent `config.set` calls in parallel and asserts the on-disk file ends in one of the legal terminal states (no lost updates, no torn writes).
- **AST lint test (Phase 6).** Walks `dashboard_adapter.go`, fails CI if any non-whitelisted adapter method body contains a call to `a.d.<X>` other than `Call` or the streaming-subscription helpers.
- **Cross-surface parity test.** A new integration-tag test that, for each method in a chosen subset, runs the same params via (a) `ipc.Client.Call` and (b) `daemon.Call`, asserts identical results. Catches transport-specific divergences early.

## §12 — Out of scope

- The MCP server implementation.
- The NIP-46 listener.
- MindForge daemon (will adopt the same pattern when its daemon lands; that work is gated by MindForge v0).
- Any UI/UX changes. The migration is purely architectural; the dashboard's HTML and the CLI's text output are unchanged except for the bugs that get fixed as side effects (e.g. compose-to-unknown-npub now refuses with `CONTACT_NOT_FOUND`).

## §13 — Follow-up artifact

A bite-sized implementation plan with TDD-shaped tasks lives in `docs/superpowers/plans/2026-05-09-unified-call-path.md` (to be written when this design is approved). The plan walks Phase 1 in step-by-step detail; later phases will get their own plans as they become next-up.
