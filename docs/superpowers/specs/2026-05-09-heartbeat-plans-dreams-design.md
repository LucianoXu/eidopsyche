# HeartBeat, plan signals, and dream cycle

**Date**: 2026-05-09
**Status**: Draft (pending user review)
**Target**: dev (lands after MindForge v0)
**Scope**: Three coupled additions to MindForge v0 that give a mind-form autonomous time: a configurable HeartBeat cadence, agent-set plan signals that fire future wakes, and a voluntary dream cycle for memory consolidation.

This builds on the wake-loop architecture from `2026-05-09-mindforge-v0-design.md` and extends `internal/wake`, the supervisor, and the on-disk template. The three subsystems share a single combined design because they share the wake-signal infrastructure and the agent's wake-context payload — splitting them into three specs would edit the same files three times.

## 1. Problem statement

MindForge v0 wakes on three triggers: an inbound NIP-17 message, a fixed `0 */4 * * *` heartbeat, or a manual `eidos forge wake`. The mind-form has no way to:

- ask the framework to wake her at a specific future moment ("follow up on bob in two hours");
- consolidate episodic memory into semantic memory and skills as a deliberate offline activity (dreaming);
- have her heartbeat cadence reflect anything the operator chose, instead of every-four-hours.

`SPEC.md` names all three as part of MindForge but the v0 design defers them. This spec is the next layer.

The three are tightly coupled at the implementation level: they share `wake.Reason`, the agent's wake context, the supervisor's spawn surface, and the in-container `eidos forge` reflection commands. Designing them as one piece avoids three near-collisions in the same files.

## 2. Design intent

1. **Heartbeat is rhythm, not policy.** Crond stays as the heartbeat trigger — it's the boring right tool. The cadence becomes per-mindform config, with minute-precision support, and the crontab is rendered from config at container start.

2. **Plans are agent-authored future wakes.** A mind-form schedules her own follow-ups by writing a small file; a supervisor goroutine fires it when the time arrives. Same end-of-pipeline as every other wake reason.

3. **Dreams are voluntary.** The framework does not decide when a mind-form dreams. It surfaces hints in the wake context (`master_likely_asleep`, `dream_eligible`, `since_last_dream_seconds`) and provides two boundary commands (`forge dream begin/end`) so the framework can track state and `forge status` can show what she is doing. Per `SPEC.md`: 它也可以在 HeartBeat 时选择做梦.

4. **One combined call path.** Each of the three new operations has exactly one code path, surfaced through the in-container `eidos forge` CLI. Operators on the host use `docker exec` wrappers that call the same code. No daemon method-table involvement is needed because these operations are between the agent and her own supervisor — no remote callers exist.

5. **YAGNI over completeness.** No live config reload (stop-and-start re-applies). No active-plan cap (operators cancel from the host if needed). No host-side `plan add` (operators don't author plans on behalf of mind-forms in v0). No `forge dream` host surface (operators don't poke at her dreams). Each excluded feature has a concrete future trigger named below.

## 3. On-disk layout

```
/eidos/run/                          (existing — framework runtime state)
├── wake/                            (existing)
│   pending.json, active.json, .lock
├── plans/                           (NEW)
│   <id>.json                        active plans
│   fired/<id>.json                  audit trail
└── dream-state.json                 (NEW)

/eidos/gate/config.toml              (existing, gains new sections)
[heartbeat]
  interval = "4h"                    # default; minute precision allowed
[mindform]
  model = "..."                      # existing
  quiet_start = "22:00"              # HH:MM, optional; both required or neither
  quiet_end   = "06:00"
  tz = "Asia/Shanghai"               # IANA timezone
  dream_min_interval = "12h"         # ≥ 1h
```

`dream-state.json` stays in `/eidos/run/` (framework runtime, like wake/) rather than under `/eidos/ontology/` (essence). The mind-form does not read it directly; the agent-runner reads it at wake time and surfaces the derived fields in the wake context. The audit trail lives in the file format itself (`dream_count`, `last_dream_*`).

## 4. Wake protocol changes

### 4.1 New reason

`internal/wake/types.go`:

```go
const (
    ReasonMindGate  Reason = "mindgate"
    ReasonHeartBeat Reason = "heartbeat"
    ReasonPlanned   Reason = "planned"  // NEW
    ReasonManual    Reason = "manual"
)
```

`wake.Signal` is unchanged. Planned wakes carry the original plan's `Hint` (one-line agent-authored summary) and stamp `Context.PlanID` so the agent can correlate with `plans/fired/<id>.json` if it wants the full original payload.

### 4.2 Context additions

`wake.Context` gains four optional fields:

```go
type Context struct {
    // existing
    InboxUnread          int    `json:"inbox_unread"`
    FirstUnreadFromNpub  string `json:"first_unread_from_npub,omitempty"`
    FirstUnreadSummary   string `json:"first_unread_summary,omitempty"`
    SinceLastWakeSeconds int64  `json:"since_last_wake_seconds"`
    LastWakeReason       Reason `json:"last_wake_reason,omitempty"`
    Scheduled            bool   `json:"scheduled"`

    // new
    MasterLikelyAsleep    bool   `json:"master_likely_asleep,omitempty"`
    SinceLastDreamSeconds int64  `json:"since_last_dream_seconds,omitempty"`
    DreamEligible         bool   `json:"dream_eligible,omitempty"`
    PlanID                string `json:"plan_id,omitempty"`
}
```

These fields are computed by `agent-runner` immediately before invoking claude:

- `MasterLikelyAsleep` — true iff the current wall-clock time in the configured `tz` falls in `[quiet_start, quiet_end)`. Wrap-around (22:00–06:00) is supported. False if quiet hours are not configured.
- `SinceLastDreamSeconds` — `now - dream_state.last_dream_finished_at`, or `0` if the mind-form has never dreamed.
- `DreamEligible` — `true` iff there is no recorded dream, OR `SinceLastDreamSeconds >= dream_min_interval`.
- `PlanID` — populated only when `Reason == ReasonPlanned`; empty otherwise.

### 4.3 Wake-message string

The human-readable string passed to claude (`-p "..."`) gains conditional clauses:

> *"You have just woken. Reason: heartbeat. Inbox has 0 unread message(s). 4h12m since last wake. Master is likely asleep (quiet hours 22:00–06:00, Asia/Shanghai). 30h since your last dream — you are eligible to dream now."*

> *"You have just woken. Reason: planned. This wake was set 2 days ago: 'follow up on bob's question about the snowflake bug.'"*

The full structured `Context` is also written to the active wake file the agent reads, so the agent has access to precise numbers if it wants them.

## 5. Plan signals

### 5.1 Storage

Each active plan is one file at `/eidos/run/plans/<id>.json`:

```json
{
  "v": 1,
  "id": "20260509T123000Z-plan-7f2e",
  "at": 1715284800,
  "hint": "follow up on bob's question",
  "created_at": 1715277600
}
```

Files are written atomically (`tmp + rename`). The filename is the ID (`<UTC-timestamp>-plan-<4-hex>`), matching the scheme already used for `wake.Signal.ID`. Timestamp prefix gives lexicographic ordering by creation time, which is stable enough for `forge plan list` ordering. No new dependency needed.

### 5.2 Bounds and rules

- `at` must be in the future, between **60 seconds** and **30 days** out. Outside this range → CLI error.
- `hint` is required, ≤ 256 chars, single-line (no newlines). UTF-8 normalized.
- No active-plan cap. If the agent floods the queue, the operator cancels from the host. (See "Future enhancements" for the trigger to add a cap.)

### 5.3 The supervisor scheduler goroutine

Implemented as a new file `cmd/eidos/supervisor/scheduler.go`. Contract:

- One goroutine spawned at supervisor PID-1 startup, alongside crond and the gate daemon.
- Ticks every 30 seconds (using a real `time.Ticker`; tests inject a fake clock).
- Each tick:
  1. Calls `scheduler.ScanDue("/eidos/run/plans", time.Now())` → list of due plans.
  2. For each due plan, in ULID order:
     - Submits a wake signal: `wake.Submit(dir, Signal{Reason: ReasonPlanned, Hint: plan.Hint, Context: Context{PlanID: plan.ID}})`.
     - Renames the plan file from `plans/<id>.json` to `plans/fired/<id>.json` atomically. Failure to rename does **not** prevent fire — wake.Submit is idempotent under coalescing, so a re-fire next tick is harmless.
- The goroutine logs each fire to stderr (one line per fire) and respects `ctx.Done()` from the supervisor's root context.

Crashed-during-fire is handled by the natural idempotency of wake-coalescing plus the visible audit in `plans/fired/`. No explicit retry logic.

### 5.4 In-container CLI surface

Registered when `EIDOS_IN_CONTAINER=1`:

```
eidos forge plan add  --in DURATION   --hint "<one-line>"
eidos forge plan add  --at TIMESTAMP  --hint "<one-line>"   # RFC3339 or unix seconds
eidos forge plan list
eidos forge plan cancel <id>
eidos forge plan clear
```

`plan add` echoes the assigned ID and the resolved fire time:

```
plan 20260509T123000Z-plan-7f2e set for 2026-05-09T14:00:00+08:00 (in 1h57m)
```

`plan list` prints a small table:

```
ID                            AT                          IN       HINT
20260509T123000Z-plan-7f2e    2026-05-09T14:00:00+08:00   1h57m    follow up on bob's question
20260509T144000Z-plan-9a3c    2026-05-10T22:30:00+08:00   1d8h     check whether the snowflake bug returned
```

Times rendered in the mind-form's configured `tz` for human legibility. `cancel` and `clear` are quiet on success; they error on a missing ID.

### 5.5 Host-side surface

```
eidos forge plan list   <mindform>
eidos forge plan cancel <mindform> <id>
```

These `docker exec` into the container and run the in-container `eidos forge plan list/cancel`. Same code path. **No host-side `plan add`** — operators don't author plans on behalf of the mind-form in v0. (Trigger to add: an operator-driven onboarding flow that needs to schedule a first follow-up.)

## 6. Dream cycle

### 6.1 State file

`/eidos/run/dream-state.json`:

```json
{
  "v": 1,
  "last_dream_started_at": 1715281200,
  "last_dream_finished_at": 1715281800,
  "last_dream_note": "consolidated bob thread into semantic/relations.md",
  "last_dream_prose": "memory/episodic/2026/05/dream-007.md",
  "dream_count": 7,
  "currently_dreaming": false
}
```

Missing file → treated as zero state (no error). All writes are atomic (`tmp + rename`) under a per-file flock so two `forge dream` calls cannot interleave.

### 6.2 In-container CLI

```
eidos forge dream begin [--note "<intent, optional>"]
eidos forge dream end --note "<one-liner, required>" [--prose-path PATH]
```

`begin` sets `last_dream_started_at = now` and `currently_dreaming = true`. `end` sets `last_dream_finished_at = now`, increments `dream_count`, and clears `currently_dreaming`. `--note` on `end` is required; the next wake's wake-context surfaces it (`last_dream_note`) so the mind-form remembers what she just did. `--prose-path` is optional and validated to live under `memory/episodic/`; if given, it's recorded in `last_dream_prose`.

`end` without a prior `begin` is allowed — claude may have crashed mid-dream and we don't want the next clean wake to be blocked. The state machine is forgiving: `dream_count` only increments on `end`.

`begin` without a subsequent `end` (e.g., claude exits before finishing) leaves `currently_dreaming = true` until the next `forge dream end` or until a manual `dream begin` resets the start timestamp. The wake-context still computes `since_last_dream_seconds` from `last_dream_finished_at`, so a stuck `currently_dreaming = true` does not corrupt the dream-eligible logic.

### 6.3 No host-side dream commands

Operators do not begin or end dreams from the host. The `forge status` surface (§8) shows whether the mind-form is currently dreaming. (Trigger to add a host command: a need to forcibly end a stuck dream in production.)

### 6.4 CLAUDE.md template additions

The embedded `internal/ontology/template/CLAUDE.md` gains one new section, kept tight (≈ 25 lines):

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

This is the only edit to the template under this spec. The existing constitution is unchanged.

## 7. Heartbeat config

### 7.1 Interval rules

`[heartbeat] interval` parses as `time.ParseDuration` and must match the cron-expressible set:

- **Minute steps** (60 mod N == 0): `1m, 2m, 3m, 4m, 5m, 6m, 10m, 12m, 15m, 20m, 30m`
- **Hour steps** (24 mod N == 0): `1h, 2h, 3h, 4h, 6h, 8h, 12h, 24h`

Anything else (e.g., `90m`, `7m`, `5h`) is rejected at config load with the supported set listed in the error message.

Default is `4h`. The crontab template renderer maps:

| Interval        | Cron expression  |
|-----------------|------------------|
| `1m`..`30m` (Nm)| `*/N * * * *`    |
| `1h`            | `0 * * * *`      |
| `2h`..`12h` (Mh)| `0 */M * * *`    |
| `24h`           | `0 0 * * *`      |

### 7.2 Render-on-startup

The supervisor renders the crontab from config to `/var/spool/cron/crontabs/eidos` before spawning crond. This replaces the current static `docker/mindform/crontab` line. The rendered file's content is exactly:

```
{cron_expression} /usr/local/bin/eidos forge wake --reason heartbeat
```

The render is a single-line replacement; no template engine, no extra dependency. If config load fails (malformed TOML), the supervisor logs the error and falls back to the default `0 */4 * * *` so the mind-form still has a heartbeat. Falling back to a default is the right policy here because heartbeat is rhythm, not authority — losing it would leave the mind-form completely silent. Plan signals and the gate daemon do not use config fallbacks for the same reason; missing config there is a fatal error.

The crontab is **not** re-rendered on config change. Operators run `eidos forge stop && eidos forge start` to apply. (Trigger to add live reload: an operator who repeatedly tunes interval without wanting to restart.)

### 7.3 Quiet hours and timezone

`quiet_start`, `quiet_end` are HH:MM (24-hour). Both must be set or neither — half-set rejected. `tz` is an IANA name (`Asia/Shanghai`, `America/New_York`); validated against `time.LoadLocation`. Used only to compute `MasterLikelyAsleep`; no scheduling logic depends on quiet hours.

### 7.4 Dream minimum interval

`dream_min_interval` parses as `time.ParseDuration`, must be ≥ `1h`, default `12h`. Used only to compute `DreamEligible`.

## 8. forge status changes

`eidos forge status <name>` gains two extra lines, derived from `plans/`, `dream-state.json`, and config:

```
Plans:    2 active   (next: 2026-05-09T14:00:00+08:00 — follow up on bob)
Dreams:   7 total    (last: 29h42m ago, "consolidated bob thread")
```

The existing `state:` line gains a parenthetical when `currently_dreaming = true`:

```
state:    running (dreaming)
```

These pieces are computed inside the container (the host-side `forge status` command already execs `eidos forge whoami` and friends; we add an analogous in-container `forge status-detail` invocation that prints the new lines).

## 9. Components and file layout

**New packages:**

- `internal/scheduler/` — Plan type + `Add`/`List`/`Cancel`/`Clear`/`ScanDue`/`MarkFired` over `/eidos/run/plans/`. Pure filesystem ops; no goroutines or timers.
- `internal/dreamstate/` — State type + `Read`/`Begin`/`End`. Atomic writes under flock.

**New files:**

- `cmd/eidos/supervisor/scheduler.go` — the 30s tick goroutine that calls `scheduler.ScanDue` and submits wakes.
- `cmd/eidos/supervisor/crontab.go` — `RenderCrontab(interval) (string, error)` and a small wrapper that writes `/var/spool/cron/crontabs/eidos` from config at startup.
- `cmd/eidos/forge/plan.go` — in-container `plan add/list/cancel/clear`.
- `cmd/eidos/forge/plan_host.go` — host-side `plan list/cancel` wrappers (docker exec).
- `cmd/eidos/forge/dream.go` — in-container `dream begin/end`.

**Modified files:**

- `internal/wake/types.go` — `ReasonPlanned`, four new `Context` fields.
- `internal/config/config.go` — `Heartbeat`, `MindForm.QuietStart/QuietEnd/TZ/DreamMinInterval` keys with validators.
- `cmd/eidos/supervisor/run.go` — wire `scheduler.go` and `crontab.go` into PID-1 startup.
- `cmd/eidos/supervisor/agent_runner.go` — extend wake-context computation; extend `buildWakeMessage` for new fields.
- `cmd/eidos/forge/cmd.go` — register `plan` and `dream` subcommands.
- `cmd/eidos/forge/status.go` — extend output with plans + dream lines.
- `internal/ontology/template/CLAUDE.md` — add §"Heartbeat, plans, and dreams".
- `docker/mindform/crontab` — **deleted**; rendered at runtime instead.

**Test files** — unit tests next to their packages; one new integration test under `test/integration/`.

## 10. Testing strategy

### 10.1 Unit tests (CI, fast)

- `internal/scheduler` — `Add` enforces 60s..30d bounds, `List` excludes `fired/`, `Cancel` is idempotent on missing IDs, `ScanDue` is monotonic in time, `MarkFired` is atomic, two concurrent `Add` produce two distinct files.
- `internal/dreamstate` — missing-file → zero State without error; `Begin`+`End` round-trip; concurrent `Begin` doesn't interleave; `End` without prior `Begin` increments `dream_count`.
- `internal/config` — happy-path + malformed-input rejection for each new key; the heartbeat-interval validator's error message names the full supported set; `quiet_start` without `quiet_end` rejected; `tz` validated.
- `cmd/eidos/supervisor/crontab.go` — every value in the supported set produces the expected cron expression (table test); unsupported values produce an error that names the supported set. No external cron parser is needed because the supported set is a closed enumeration.
- `cmd/eidos/supervisor/agent_runner.go` — fake clock + fake config + fake `dream-state.json` produces expected `MasterLikelyAsleep` (incl. the wrap-around 22:00–06:00 case), `DreamEligible`, `SinceLastDreamSeconds`. Planned-wake path stamps `PlanID` from the active wake file.
- `cmd/eidos/supervisor/scheduler.go` — fake ticker fires due plans via a fake `wake.Submit`; plan moves to `fired/`; double-fire avoided across two ticks of the same plan ID.
- `cmd/eidos/forge` — cobra-level table tests for arg parsing, error formatting, RFC3339 vs duration vs unix-seconds handling on `plan add`.

### 10.2 Integration test (CI, gated by `-tags=integration`)

`test/integration/forge_plan_dream_test.go`:

1. Build a minimal mindform container with a stubbed `claude` binary (a shell script that echoes the wake message and exits 0).
2. Start it with `interval = "1m"`.
3. From the host, `docker exec` `eidos forge plan add --in 90s --hint "smoke"`.
4. Wait 120s.
5. Assert `plans/fired/` contains the file and the supervisor's stderr shows a planned-wake fire.
6. `docker exec` `eidos forge dream begin` then `dream end --note test`.
7. Read `/eidos/run/dream-state.json` and assert `dream_count == 1`, `last_dream_note == "test"`.
8. `docker exec` `eidos forge wake --reason manual` and assert the next active wake context contains `since_last_dream_seconds` near 0 and `dream_eligible: false`.

The stubbed `claude` binary is a 5-line shell script `test/integration/testdata/claude_stub.sh` that echoes its `-p` arg and exits. Tests do not rely on real claude.

### 10.3 Deployment test (manual, operator's machine)

`deploy-test/test_script_heartbeat_plans_dreams.sh` — a self-contained script the operator runs after merging this PR to validate behavior in their real environment without touching their running daemon.

Key properties:

- **Isolation**: uses `--state-dir /data/eidopsyche/deploy-test/dt-forge-host/` for the host gate; uses an isolated relay on `127.0.0.1:22897`; uses a disposable mindform name `dt-alice` and a separate Docker volume name. Nothing the script does can collide with the user's running daemon, gate, mindform, or relay.
- **No claude required**: passes `--no-login` to `forge create` and never invokes the agent. The script verifies framework infrastructure (plans fire, dream-state round-trips, heartbeat ticks at 1-minute precision) by reading files and the supervisor's wake-history. Validating that claude actually consolidates memory during a dream is out of scope — that's a human-in-the-loop check during normal use.
- **Cleanup**: `trap` on EXIT kills background processes; final step `forge purge --yes` removes the disposable mindform; volume and container removed. Re-running the script is safe.

The script is **not** run in CI (Docker daemon + port-binding requirements). The Go integration test is the CI path.

The script is the third leg of the test pyramid: unit tests prove each component works, the Go integration test proves the supervisor pipeline works under a stubbed claude, and the deployment test proves the operator's real environment works under fresh state — same role as `forge_smoke.sh` plays for v0.

## 11. Migration / backwards compatibility

- Existing mindforms that don't have the new config keys: defaults apply. `interval = 4h`, no quiet hours (so `MasterLikelyAsleep = false` always), `dream_min_interval = 12h`. They keep working without operator intervention.
- The static `docker/mindform/crontab` file is removed from the image. Existing volumes don't have a stale heartbeat — the supervisor renders fresh on every start.
- The `wake.Reason` enum gains a value but does not change wire shape; old wake files without `PlanID` are still readable as zero values.

No upgrade migration is needed.

## 12. Out of scope (with named future triggers)

- **Live config reload.** Trigger: an operator who repeatedly tunes interval without wanting to restart.
- **Active-plan cap.** Trigger: a real incident where an agent floods the queue.
- **Crashed-during-fire retry beyond coalescing.** Trigger: a real incident where a plan fails to fire and isn't picked up by the next tick.
- **Host-side `plan add`.** Trigger: an operator-driven onboarding flow that needs to schedule a first follow-up.
- **Host-side `dream begin/end`.** Trigger: a need to forcibly end a stuck dream in production.
- **Per-mindform model selection during a dream.** Trigger: evidence that a different model produces measurably better consolidation; mind-forms remain on the same model for all wakes in v1.
- **The agent's actual consolidation behavior.** Beyond the CLAUDE.md guidance section, this is the mind-form's territory, not the framework's.

## 13. Summary of decisions

| Area | Decision |
|---|---|
| Combined or separate specs | One spec, three subsystems |
| Dream model | Voluntary; agent-decided during HeartBeat |
| Plan storage | File-based under `/eidos/run/plans/`, audit in `fired/` |
| Plan firing mechanism | Supervisor goroutine, 30s tick |
| Heartbeat firing mechanism | crond, crontab rendered from config at start |
| Heartbeat interval precision | Minute, constrained to cron-expressible set |
| Plan time bounds | 60s ≤ at-future ≤ 30d |
| Plan cap | None in v0 |
| Operator surface | Host `plan list/cancel` only; no dream surface |
| Dream end requires note | Yes (one-liner surfaced in next wake) |
| Dream state file | `/eidos/run/dream-state.json` |
| Wake-context new fields | `MasterLikelyAsleep`, `SinceLastDreamSeconds`, `DreamEligible`, `PlanID` |
| CLAUDE.md changes | One new ≈ 25-line section |
| Tests | Unit (CI) + integration with stubbed claude (CI) + deploy-test script (manual) |
