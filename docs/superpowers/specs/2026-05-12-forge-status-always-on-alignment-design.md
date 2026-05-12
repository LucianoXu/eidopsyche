# Forge status alignment with always-on agent-loop

- **Date**: 2026-05-12
- **Touches**: `cmd/eidos/forge/runtime_state.go` (struct + phase derivation), `cmd/eidos/forge/status.go` (output shape), `cmd/eidos/forge/list.go` (column values), `cmd/eidos/forge/watch.go` (text-only help/comment), `cmd/eidos/forge/config.go` (text-only help), `cmd/eidos/supervisor/cmd.go` (text-only short/comment), `cmd/eidos/supervisor/birth.go` (text-only comment), tests in `cmd/eidos/forge/` for the changed files, doc updates to `docs/superpowers/specs/2026-05-10-mindform-status-and-observer-design.md`, `docs/superpowers/specs/2026-05-10-persistent-wake-context-design.md`, and `SPEC.md` if its `唤醒上下文生命周期` section still references `sleeping`/`awake`.
- **Sequencing**: single PR. Pre-production (see `CLAUDE.md` "Project Stage"): no opt-in flag, no migration. JSON schema for `forge runtime-state` bumps `v: 1` → `v: 2` and breaks; the host-side parser is updated in lockstep.

## 1. Problem statement

After v0.14.0 (PR #71, "long-lived claude per mind-form"), several `eidos` / `forge` command outputs and help strings no longer describe what the system does:

- `eidos forge status <name>` derives `phase` from `active.json` presence (`cmd/eidos/forge/runtime_state.go:89-111`). Under always-on, `active.json` is written by `supervisor.drainPending`, forwarded to agent-loop's stdin, and cleared within milliseconds. By the time an operator checks status, `active.json` is almost always absent, so `phase` reads `sleeping` even while claude is mid-turn — directly contradicting the `thinking: yes` line on the next row of the same output.
- The same command's `session: <id> (age <t>, N wakes)` line reads `WakesInSession` from `session.json`. In always-on, `session.json` is the persistent crash-recovery snapshot updated on the `OnTurnEnd` callback; `agent-state.json` carries the live counter (`WakesInSession` field) updated on every state change. The `session.json` reader lags `agent-state.json` by up to one turn mid-stream.
- Several help strings and source comments still reference `agent-runner`, deleted in v0.14.0 (`6fb75ee refactor(supervisor): delete agent-runner — replaced by agent-loop`).
- The `supervisor` cobra `Short` says `"cron + gate daemon + per-wake agent spawn"`; the supervisor no longer spawns per-wake.

Deploy-test 004 on v0.14.0 reproduced the contradiction: `phase: sleeping` printed alongside `thinking: yes / last_active: 1s ago` while Alice was actively redeeming an invite and sending a Nostr message.

## 2. Design intent

- **One source of truth for the live phase: `agent-state.json`.** The agent-loop writes it on every state transition; the host already exposes it via the `agent.state` IPC method (`internal/daemon/agentstate.go`). `forge status` / `forge list` / `forge runtime-state` all converge on this file.
- **Drop `active.json` from the operator-visible phase derivation.** `active.json` remains the supervisor's internal mailbox file for the wake-forward path; it stops driving any externally visible field.
- **One counter on the operator surface: `turns`.** Renamed from `wakes` because under always-on every result-event is a turn, regardless of whether the trigger was a signal wake (mindgate / heartbeat) or a mailbox event (background-task completion). `session.json` stays the crash-recovery snapshot — never the display source.
- **Sweep stale text references.** Anywhere user-visible or in a comment that names `agent-runner` or `per-wake agent spawn`, rename to `agent-loop` / `long-lived agent-loop`.
- **Single call path is preserved.** No new IPC method; no parallel handler. The redesign rearranges what `runtime-state` returns and how `status` consumes it.
- **YAGNI.** No new phase values beyond what's needed for the five live cases. No backwards-compatible v1 fallback in the host parser — pre-production, no in-flight v1 callers.

## 3. Phase model

### 3.1 Phase values

The in-container `forge runtime-state` subcommand emits exactly one of these:

| Phase | Meaning | Source |
|---|---|---|
| `auth-required` | `claude` OAuth failed; agent-loop self-gated | `authstate.ReadAt(authStatePath)` returns non-nil |
| `crashed` | Supervisor's crash-loop guard halted restart (5 deaths in 60s) | `/eidos/run/agent-loop-crashed.json` present |
| `starting` | Container running but agent-loop hasn't written its first state yet (boot, post-restart) | `agent-state.json` missing |
| `dreaming` | Agent-loop in dream consolidation | `agentState.Dreaming == true` |
| `thinking` | Claude is actively processing a turn | `agentState.ClaudeBusy == true` |
| `idle` | Agent-loop is up and waiting for the next event (steady state) | otherwise |

`offline` is host-derived (set in `cmd/eidos/forge/status.go:computeStatus` and `cmd/eidos/forge/list.go:listPhase` when the container is not running) and is **never** emitted by the in-container subcommand.

The `crashed` phase carries two extra fields — `crashed_exit_code` and `crashed_last_error` — copied from `agent-loop-crashed.json`. `forge status` renders them as a `crashed: exit_code=<n> last_error="..."` line so the operator does not need to docker-exec in to learn why the loop is down. The supervisor's crash-loop guard is the only writer (`cmd/eidos/supervisor/run.go:writeAgentLoopCrashed`).

### 3.2 Derivation order

```go
func computeRuntimeState(now time.Time) RuntimeState {
    rs := RuntimeState{V: 2}
    rs.ContainerStartedAt = readContainerStartTime()

    if state, _ := authstate.ReadAt(authStatePath); state != nil {
        rs.AuthRequired = true
        rs.Phase = "auth-required"
        return rs
    }

    if crashed, ok := readAgentLoopCrashed(agentLoopCrashedPath); ok {
        rs.Phase = "crashed"
        rs.CrashedExitCode = crashed.ExitCode
        rs.CrashedLastError = crashed.LastError
        return rs
    }

    as, err := agentloop.ReadAgentState(agentStateRuntimePath)
    if err != nil || as.V == 0 {
        rs.Phase = "starting"
        return rs
    }

    switch {
    case as.Dreaming:
        rs.Phase = "dreaming"
    case as.ClaudeBusy:
        rs.Phase = "thinking"
    default:
        rs.Phase = "idle"
    }

    rs.SessionID = as.SessionID
    rs.SessionStartedAt = as.SessionStartedAt
    rs.Turns = as.WakesInSession
    rs.LastEventAt = as.LastEventAt
    return rs
}
```

`auth-required` short-circuits because the agent-loop is intentionally gated and the live `agent-state.json` fields would be stale or misleading. `crashed` short-circuits for the same reason: once the supervisor halts the restart loop, `agent-state.json` is a stale snapshot and surfacing `thinking` / `idle` from it would mislead. `auth-required` wins over `crashed` because it is the more actionable signal — a crash marker may itself be the downstream consequence of failed auth, in which case fixing auth is what unblocks the loop.

### 3.3 `RuntimeState` JSON shape (v2)

```go
type RuntimeState struct {
    V                  int    `json:"v"`                            // = 2
    Phase              string `json:"phase"`                        // see §3.1
    AuthRequired       bool   `json:"auth_required"`
    ContainerStartedAt int64  `json:"container_started_at"`
    CrashedExitCode    int    `json:"crashed_exit_code,omitempty"`  // phase=crashed only
    CrashedLastError   string `json:"crashed_last_error,omitempty"` // phase=crashed only
    SessionID          string `json:"session_id,omitempty"`
    SessionStartedAt   int64  `json:"session_started_at,omitempty"`
    Turns              int    `json:"turns,omitempty"`
    LastEventAt        int64  `json:"last_event_at,omitempty"`
}
```

Dropped from v1: `WakeReason`, `ActiveWakeID`, `SincePhaseChangeSeconds`, `Dreaming`, `WakesInSession`. The wake reason is no longer a phase modifier (a turn isn't a process boundary). `SincePhaseChangeSeconds` was derived from `active.json`'s mtime; with no `active.json`-based phase, the equivalent is `time.Since(LastEventAt)` computed host-side if needed.

### 3.4 Schema-version enforcement

`fetchRuntimeState` (host-side) rejects responses whose `v` does not match `runtimeStateSchemaVersion`. A v1-shaped response from a stale container image would otherwise flow through the v2 struct silently — emitting old phase strings like `sleeping` / `awake` (which v2 callers do not expect) and zeroing out `wakes_in_session` into `Turns=0`. On version mismatch the host falls back to the legacy `state: running` line, the same fallback path used when the in-container subcommand is missing entirely. This keeps the breaking schema bump honest.

## 4. Status output

`eidos forge status <name>` host-side formatting (`cmd/eidos/forge/status.go:computeStatus`):

```
name:    alice
phase:   thinking
session: 5ceaf9cd (age 4m38s, 12 turns)
last_active: 1s ago
[whoami stanza from `eidos forge whoami`]
[plans/dreams stanza from `eidos forge status-detail`]
```

Changes from current output:

- `phase` line: drawn from the new `RuntimeState.Phase`. Wake-reason parenthetical (`awake (mindgate)`) is gone.
- `session` line: `turns` replaces `wakes`. Source: `RuntimeState.Turns` (which is `agentState.WakesInSession`).
- The previous standalone `thinking: yes/no` line is **removed**; redundant with `phase: thinking` / `phase: idle`.
- `last_active: <t> ago` is rendered when `RuntimeState.LastEventAt > 0`. Same computation as today but reading `RuntimeState` rather than re-execing `agent-state`. One fewer round-trip.

Container-not-running case is unchanged: `phase: offline (<docker-state>)` printed and the function returns early.

## 5. `forge list` column

`listPhase` (`cmd/eidos/forge/list.go:38-54`) returns one of `offline | starting | auth-required | dreaming | thinking | idle`. Implementation unchanged except for the new possible values — it already calls `fetchRuntimeState` and reads `rs.Phase` verbatim.

## 6. Text-only sweep

| Item | File:Line | Replacement |
|---|---|---|
| #3 | `cmd/eidos/forge/watch.go:32-33` | `"...transcript captured by agent-runner."` → `"...transcript captured by agent-loop."` |
| #5 | `cmd/eidos/forge/watch.go:439` | `// readLineUnbounded matches the helper in supervisor/agent_runner.go:` → `// readLineUnbounded matches the helper in internal/agentloop/drain.go:` |
| #7 | `cmd/eidos/forge/config.go:35` | `"Pin the claude model used by agent-runner."` → `"Pin the claude model used by agent-loop."` |
| #9 | `cmd/eidos/supervisor/cmd.go:7` | `"Container PID 1 — cron + gate daemon + per-wake agent spawn"` → `"Container PID 1 — cron + gate daemon + long-lived agent-loop"` |
| #10 | `cmd/eidos/supervisor/birth.go:115,120` | Rephrase the parallel-to-`agent_runner` self-gate comment to reference `agentloop` as the runtime sibling whose self-gate similarly persists `auth_required.json`. No behavioural change. |
| #13 | `cmd/eidos/supervisor/cmd.go:12-17` | `"Subcommands (run, agent-runner)..."` → `"Subcommands (run, agent-loop)..."` matching `cmd_unix.go` registration. |

## 7. Doc updates

- `docs/superpowers/specs/2026-05-10-mindform-status-and-observer-design.md` §3.1 (phase rules) and §3.2 (`RuntimeState` field list) — rewrite to match the v2 schema and the new derivation table.
- `docs/superpowers/specs/2026-05-10-persistent-wake-context-design.md` — wherever it describes `session.json:WakesInSession` as the operator-visible counter, soften to "crash-recovery snapshot; the operator-visible live counter is `agent-state.json:WakesInSession`."
- `SPEC.md` — if its `唤醒上下文生命周期` section still mentions `sleeping` / `awake` phase vocabulary, update to the new value space.

## 8. Test plan

### 8.1 Unit tests (modified)

- `cmd/eidos/forge/runtime_state_test.go`:
  - Drop tests that inject `active.json` to force `phase: awake`. Replace with tests that inject `agent-state.json` (using `agentloop.WriteAgentState` against a temp path) and assert the five phase values.
  - One test per phase: `auth-required`, `starting`, `dreaming`, `thinking`, `idle`.
  - Auth-required precedence test: with both `authstate` present and a `ClaudeBusy=true` agent-state, expect `auth-required`.
  - Schema test: `V == 2`.
- `cmd/eidos/forge/status_test.go`:
  - Update fixture for the new output: `phase: thinking`, `session: ... 12 turns`, `last_active: 1s ago`. Assert the standalone `thinking:` line is absent.
- `cmd/eidos/forge/list_test.go`:
  - Update phase-column expectations for the five new values.

### 8.2 Unit tests (new)

- `cmd/eidos/forge/runtime_state_test.go::TestPhase_StartingWhenAgentStateMissing` — agent-state absent yields `starting`.
- `cmd/eidos/forge/runtime_state_test.go::TestPhase_AuthRequiredOverridesAgentBusy` — both signals present, `auth-required` wins.
- `cmd/eidos/forge/runtime_state_test.go::TestPhase_DreamingOverridesBusy` — `Dreaming=true && ClaudeBusy=true` yields `dreaming`.

### 8.3 Integration

Rerun deploy-test 004 (`deploy-test/004-mindform-introduction/`) after the change. Expected:
- `eidos forge status alice` shows `phase: thinking` while Alice is processing the inbound instruction.
- After Alice finishes sending both messages, `phase: idle`.
- `session: <id> (age <t>, N turns)` increments by N as wakes / mailbox events arrive.

## 9. Single-call-path check

The dashboard reads `agent-state.json` through the existing `agent.state` IPC method (`internal/daemon/agentstate.go`). That surface is already authoritative and needs no change — this redesign only touches the in-container `runtime-state` subcommand and its host-side parsers.

The redesign does **not** introduce a new IPC method. `runtime-state` remains a subprocess-exec channel (not an IPC method) because `forge status` runs against a container that may or may not have its gate daemon up; the docker-exec channel survives a downed daemon, whereas an IPC method would not.

## 10. Out of scope

Medium / low-confidence audit items deferred to a follow-up PR:
- #2 `forge status` session-wakes lagging by one turn — the schema-level fix here (drop `session.json` from the read path) closes most of this; any residual is in `OnTurnEnd` sequencing.
- #4 `forge watch --wake current` follow-loop modelling per-process boundaries.
- #6 `forge watch --list` empty-state message excludes mailbox-driven turns.
- #8 `forge wake --reason` help-text parenthetical.
- #12 In-container `forge wake` `Short` framing.

These are real but don't produce the contradictory output that motivated this PR; they can land independently.
