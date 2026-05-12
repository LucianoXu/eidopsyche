# Always-on mind-form: long-lived claude process driven by an event mailbox

- **Date**: 2026-05-12
- **Touches**: new package `internal/agentloop/` (state machine, stdin forwarder, stream-json drainer, dream fsnotify watcher, transcript writer, stub claude in `testfake/`); `cmd/eidos/supervisor/{run,children,cmd_unix,birth}.go` (Forward callback, restart=always, agent-loop subcommand registration); deletes `cmd/eidos/supervisor/agent_runner.go` + tests; new IPC method registered with gate daemon for `agent.state`; new config key registered in `internal/config/keys.go`; documentation updates in `template/CLAUDE.md`, `SPEC.md` ("唤醒上下文生命周期" section), `EXAMPLE.md` (where wake/dream behavior is described); reads-only callers of `internal/{wake,sessionstate,dreamstate,transcript,prompts,claudeexec,authstate,claudeauth}` move from agent-runner into agent-loop (no API change in those packages)
- **Sequencing**: ordered build, single landed cutover. The project is pre-production (see `CLAUDE.md` "Project Stage") — no opt-in flags, no migration path, no parallel old/new modes. When the new code lands, the old agent-runner is deleted.

## 1. Problem statement

Today every wake spawns a fresh `claude -p <wake_msg>` process via `agent-runner`, runs one turn, and exits. The mind-form's claude session has no in-process lifetime beyond that single turn. This makes three SPEC-intended capabilities effectively unreachable:

- **Sub-agents that work across wakes.** Claude Code's `Agent` tool dispatches a sub-conversation that lives only as long as its parent process. With per-wake spawn, a sub-agent dispatched in wake N is force-killed at wake N's exit — it cannot deliver results to wake N+1.
- **Background tasks that complete asynchronously.** `Bash {run_in_background: true}`, `Monitor`, `ScheduleWakeup`, in-process `CronCreate` — all rely on the claude process staying alive to consume the mailbox events that signal completion. Per-wake spawn kills these handles before they can fire.
- **A true train of thought between dreams.** Although `--resume <UUID>` carries the conversation history across wakes, the model's runtime (its scratch state, its in-flight tool dispatches, its mailbox) is reborn every time. The mind-form spends every wake re-orienting from disk rather than picking up where it left off.

The fix: keep one claude process alive per mind-form, deliver wake events into its mailbox without restarting it, and rotate the session only at dream boundaries.

## 2. Design intent

- **One persistent claude process per mind-form, for the lifetime of a session.** Session boundaries are dream-end events. Within a session, the same claude PID handles every wake.
- **Stream-json input mode is the standard SDK transport.** `claude --input-format stream-json --output-format stream-json --verbose --include-partial-messages`. stdin carries JSONL user messages forever; the process exits only at stdin EOF or external signal.
- **Wake protocol is the unchanged producer interface.** Gate daemon, cron heartbeat, planner, operator manual wake — every producer calls `wake.Submit` exactly as today. Coalescing-in-`pending.json` semantics survive.
- **Wake delivery is fire-and-forget.** supervisor does not block waiting for claude to finish a turn before delivering the next wake. Claude SDK's stdin queue handles ordering. The mind-form sees back-to-back wakes as a sequence of turns and decides at the model level how to respond coherently.
- **Dream is the only session boundary.** Only the mind-form decides when to dream (per SPEC: "心智体在做梦的时候整理…"). The framework does not threshold, does not force-rotate, does not impose dream cadence.
- **Crash recovery is automatic and uses `--resume`.** claude crash → agent-loop resumes the same session UUID. agent-loop crash → supervisor restarts it; new agent-loop reads session.json and resumes. supervisor crash → container restart by docker policy → same recovery path.
- **All runtime state lives in the named volume.** session.json, dream-state.json, wake/, transcripts/, agent.lock, the new agent-state.json — all in `/eidos/run/` which is part of the `/eidos` volume mount. Container restart and cross-host volume migration are zero-effort.
- **Single call path is preserved.** The one new IPC method (`agent.state`) routes through the daemon method table exactly like every other operation.
- **YAGNI.** No framework-side dream trigger, no force-rotation, no cross-mind-form shared agent-loop, no real-time SSE for transcripts, no token-cap-based dream prompts, no parallel mode flag.

## 3. Architecture

### 3.1 Process tree (inside the mind-form container)

```
supervisor (PID 1, eidos supervisor run)
├── sudo crond                            (long-lived; no auto-restart)
├── eidos gate daemon                     (long-lived; no auto-restart)
├── eidos supervisor agent-loop           (long-lived; supervisor auto-restarts on failure)
│   └── claude --input-format stream-json --output-format stream-json ...
│                                          (long-lived; agent-loop re-spawns on dream / crash)
└── plannerLoop                            (goroutine inside supervisor)
```

### 3.2 Responsibility split

| Component | Owns |
|---|---|
| **supervisor** | Container init; crontab install; spawn and supervise the three long-lived children; auto-restart agent-loop on failure; `fsnotify` on `/eidos/run/wake/` driving the `Forward` callback; planner goroutine; birth handler; `recoverStaleActive` |
| **agent-loop** | Single-instance lock `/eidos/run/agent.lock`; spawn and own claude; consume stdin (wake JSONL from supervisor) and forward to claude stdin; consume claude stdout (stream-json) and produce transcript + agent-state.json; `fsnotify` on `/eidos/run/dream-state.json` driving session rotation; session.json reads / mints / clears |
| **claude** | Stream-json input/output for the entire session; agent loop semantics (tool dispatch, sub-agent, mailbox); emit `result` events at turn boundaries |

### 3.3 What is preserved from the current codebase

- `internal/wake`: Signal struct, Submit/PromoteToActive/ClearActive, flock, coalescing — **no semantic change**.
- `internal/sessionstate`: Mint/Read/Clear/IncrementWake interface — **no semantic change**; only the call site moves from "per wake in agent-runner" to "at agent-loop start / dream rotation".
- `internal/dreamstate`: Begin/End/Read interface — **no semantic change**; only the consumption pattern changes (agent-loop fsnotifies dream-state.json).
- `internal/transcript`: per-wake NDJSON write, finalize, Counter — **no semantic change**; agent-loop is the new writer (same store layout, same Entry schema).
- `internal/prompts.BuildWake`: WakeInput → message string, `IsFirstWakeOfNewSession` semantics — **no semantic change**; agent-loop is the new caller.
- `internal/claudeexec`, `internal/authstate`, `internal/claudeauth`: exit classification, OAuth self-gate, env injection — **no semantic change**; agent-loop reuses them.

### 3.4 What is deleted

- `cmd/eidos/supervisor/agent_runner.go` and its test files. agent-runner subcommand and the per-wake-fork path go away entirely. The runAgent function's logic that *is* still relevant (sessionstate decision, identity file read, BuildWake call, transcript write, exit classification, EXIT_AUTH_REQUIRED self-gate) migrates into agent-loop's startup and per-turn handlers.

## 4. Wake delivery

### 4.1 Channels and shape

| Direction | Channel | Payload |
|---|---|---|
| Producer → supervisor | `wake.Submit(dir, sig)` → `/eidos/run/wake/pending.json` | `wake.Signal` JSON, atomically merged with any existing pending (coalescing rule unchanged) |
| supervisor → agent-loop | agent-loop's stdin (pipe owned by supervisor) | `wake.Signal` serialized as a single JSONL line ending in `\n` |
| agent-loop → claude | claude's stdin (pipe owned by agent-loop) | Anthropic SDK stream-json user message: `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<BuildWake output>"}]}}` per line |
| claude → agent-loop | claude's stdout (read by agent-loop) | Stream-json events: `system`, `assistant`, `user` (tool_results), `result` |
| agent-loop → operator/observers | `/eidos/run/agent-state.json` (atomic write) and `/eidos/run/transcripts/wake-<turn_id>.ndjson` plus the transcripts index file | State machine snapshot; per-turn transcript (file naming follows the existing `internal/transcript` store layout) |

### 4.2 supervisor's forwarder

`watchWakesIn(ctx, dir, forward Forward)` keeps its current loop shape; only the `forward` callback changes from "exec child and wait for exit" to "write JSONL line to agent-loop's stdin pipe, then return". `Forward` is fire-and-forget — no ack, no completion wait.

```go
type Forward func(ctx context.Context, sig wake.Signal) error

// In supervisor's spawn loop:
//   PromoteToActive(dir)        // existing
//   forward(ctx, sig)           // write JSONL to agent-loop stdin
//   ClearActive(dir)            // existing — immediate, no ack wait
```

`Forward` returning an error means the pipe write failed (`EPIPE`, agent-loop dead, context cancelled). The error path triggers supervisor's child-restart logic for agent-loop (§6.2); the active.json marker is left in place for `recoverStaleActive` to pick up after restart.

### 4.3 Coalescing

Producers race on `pending.json` under flock. Multiple submits while no consumer has drained accumulate into one coalesced `wake.Signal` with `CoalescedCount` / `CoalescedFrom` populated. When supervisor next reads pending, it delivers that single coalesced wake. Coalescing happens **at the producer layer only** in the new model; agent-loop and claude do no further merging.

### 4.4 Wake handling during a dream

Between dream-begin and dream-end, agent-loop must not forward new wakes to claude (they would land in claude's stdin queue and be processed as post-dream-but-pre-rotation turns). Mechanism:

- agent-loop maintains an atomic `dreaming bool` flag (in-process, goroutine-safe).
- The fsnotify watcher on dream-state.json:
  - On `CurrentlyDreaming` false→true: `dreaming = true`.
  - On `CurrentlyDreaming` true→false **and** `LastDreamFinishedAt` grew: enqueue a rotation request. The flag stays `true` through rotation; it is flipped back to `false` only at the end of the rotation goroutine after the new claude is ready.
- The stdin reader, before forwarding each wake to claude stdin, reads `dreaming` atomically. If true, append the wake to an in-memory backlog slice instead of forwarding.
- After rotation completes, drain the backlog to new claude in order. The first wake from the backlog has `IsFirstWakeOfNewSession=true`; remaining backlog wakes use normal rendering.

This eliminates the dream-begin-to-dream-flag-update race entirely except for in-process goroutine scheduling, which is sub-microsecond.

### 4.5 What the model sees

`BuildWake` rendering is unchanged. The mind-form sees the same per-wake user message structure: reason, hint, inbox stats, dream-eligibility hints, optional first-wake-of-new-session preamble. The mind-form does not perceive any architectural change — only that its tools now retain state across wakes.

A new sentence is appended to `template/CLAUDE.md` (and authors' choice for prefab CLAUDE.md files):

> You are continuously online between dreams. Sub-agents you dispatch, bg-tasks you start, and timers you schedule survive across wakes within the same session. Before invoking `eidos forge dream end`, you MUST clean up all in-flight sub-agents and background tasks: `TaskStop` them, or wait for them to complete and inline their findings into your journal. Any in-flight task at dream-end is lost. Multiple wakes can arrive in quick succession; if a wake's situation has not changed from the previous one, a short acknowledgment is fine.

## 5. Session lifecycle & dream rotation

### 5.1 Agent-loop startup decision

```
agent-loop starts (container boot or supervisor-driven restart)
  ↓ acquire /eidos/run/agent.lock (fail-fast on contention)
  ↓ read /eidos/run/session.json
  ├─ absent or SessionID empty
  │     → Mint(new UUID) → claude --session-id <UUID>
  ├─ SessionID present
  │   ├─ pre-flight: <ontology>/.claude/projects/<encoded-cwd>/<UUID>.jsonl exists?
  │   │     no  → Clear + Mint(new) → same as "absent" branch
  │   │     yes → continue
  │   ├─ dreamstate.LastDreamFinishedAt > sess.SessionStartedAt
  │   │     → Clear + Mint(new) → same as "absent" branch
  │   │       (dream-end completed but rotation didn't, e.g. agent-loop
  │   │        crashed between Clear and respawn)
  │   └─ otherwise → claude --resume <UUID>
```

This is the existing `decideSessionMode` (`agent_runner.go:228-237`) logic, ported into agent-loop's startup.

### 5.2 Per-wake handling (steady state)

```
read JSONL line from supervisor stdin
  ↓ parse as wake.Signal
  ↓ if dreaming: append to backlog, continue
  ↓ compute first-wake flag (true on the first wake after rotation, false thereafter)
  ↓ build user-message text via prompts.BuildWake(WakeInput{...})
  ↓ open transcript handle for this wake.ID
  ↓ write user message line to claude stdin
  ↓ sessionstate.IncrementWake
  ↓ (continue stdin reader loop; the stdout drainer is in a separate goroutine)
```

The stdout drainer goroutine, in parallel, consumes claude's stream-json events and:
- writes each event line to the current turn's NDJSON transcript file (file selection rules below);
- maintains the busy/idle state machine (§5.4);
- detects session-not-found / OAuth-required errors and triggers the appropriate recovery (§5.5, §6).

Transcript file selection follows the existing `internal/transcript` per-turn-NDJSON layout (`<dir>/wake-<ID>.ndjson` plus the index). One file per turn. The `ID` field:

- **Wake-driven turn**: the wake.ID from the wake that initiated this turn. agent-loop opens the transcript handle the moment it writes the user-message line to claude stdin, then "owns" that handle until the next `result` event closes the turn.
- **Mailbox-driven turn**: when the drainer sees a non-`system` event arrive while no wake handle is open (i.e., `outstanding_wakes_sent == results_seen` from the state machine), agent-loop allocates a synthetic ID `mailbox-<unix_nano>`, opens a transcript handle for it, and the turn's Entry is written with `Reason = "mailbox"` (a transcript-level string constant local to `internal/agentloop`; mailbox turns never appear in pending.json, so `internal/wake.Reason` does not need a new value).

Turn boundaries are: open at the first event after `result` (or at the user message write for wake-driven turns), close on the next `result`. The `system init` event is written to whichever handle is currently open at session start (the first wake-driven turn after `--session-id`/`--resume`), per today's convention.

### 5.3 Dream rotation

**Main thread (claude in turn)**
```
mind-form (inside its turn) calls `eidos forge dream end --note ...`
  → gate daemon IPC handler: dreamstate.End writes dream-state.json
  → tool_result returned to claude — the mind-form's turn continues
  → mind-form finishes the turn (final assistant text)
  → claude emits `result` event
```

**agent-loop's fsnotify watcher (concurrent with the above)**
```
fsnotify event on dream-state.json fires (typically between
  the IPC write and the turn's `result` event)
  → re-read dream-state.json
  → if CurrentlyDreaming flipped false→true (dream-begin):
       atomic dreaming flag → true
       (new wakes from supervisor get buffered to backlog from now on)
  → if CurrentlyDreaming flipped true→false AND LastDreamFinishedAt
    grew (dream-end):
       enqueue a rotation request onto the rotation goroutine's channel
       (dreaming flag stays true through rotation so backlog buffering
        continues until rotation completes)
```

**agent-loop's rotation goroutine (drains rotation requests)**
```
receive rotation request
  → close claude's stdin (parent's write end)
  → wait for claude to exit, up to mindform.dream_close_grace
    (default 60s, configurable per §7.4)
  ├─ claude exits cleanly → continue
  └─ grace elapsed → SIGTERM → wait 5s → SIGKILL
  → flush in-flight transcript handles
  → sessionstate.Clear
  → sessionstate.Mint(new UUID)
  → reset state-machine counters, transcripts state
  → set nextWakeIsFirstOfNewSession = true
  → spawn new claude --session-id <new UUID> ...
  → drain dream-backlog to new claude in arrival order
    (first item gets first-wake-of-new-session)
  → atomic dreaming flag set false (release backlog buffering)
  → ready: stdin reader and stdout drainer resume normal operation
```

### 5.4 Claude busy/idle state machine

Implemented in agent-loop's stdout drainer goroutine. Drives `/eidos/run/agent-state.json` writes.

```
state := idle
on event from claude stdout:
  case system / subtype=="init":
    // session init; no state change
  case assistant, user:
    if state == idle: state = busy, since = now
  case result:
    state = idle, since = now
write agent-state.json atomically (tmp+rename) on every transition and on a
floor heartbeat (every 5s) so observers get fresh `last_event_at` even if
nothing transitions.
```

`agent-state.json` schema:
```json
{
  "v": 1,
  "claude_busy": true,
  "since_unix": 1715500000,
  "last_event_type": "assistant",
  "last_event_at": 1715500005,
  "session_id": "uuid-of-current-session",
  "session_started_at": 1715499500,
  "wakes_in_session": 7,
  "outstanding_wakes_sent": 8,
  "results_seen": 7,
  "dreaming": false
}
```

`outstanding_wakes_sent` − `results_seen` indicates whether claude is mid-turn on a wake or mid-mailbox-turn. The field is exposed for display; nothing in the design uses it for correctness.

### 5.5 Claude crash within an active session

```
agent-loop's claude.Wait() returns non-zero
  ↓ scan claude's stderr for known markers; classify exit (claudeexec.ClassifyClaudeExit)
  ├─ ClaudeAuthRequired → authstate.Write, exit agent-loop with EXIT_AUTH_REQUIRED
  │                       (supervisor sees this exit code, halts auto-restart,
  │                        surfaces via forge status — same as today's gating)
  ├─ session-not-found marker in stderr (matchSessionNotFound, agent_runner.go:293)
  │                     → sessionstate.Clear + Mint(new UUID)
  │                       → spawn claude --session-id <new UUID>
  │                       → set nextWakeIsFirstOfNewSession = true so the
  │                         next user message tells the mind-form its
  │                         working memory was reset
  └─ otherwise → log + respawn
       ↓ read session.json (still has SessionID — we did not Clear)
       ↓ spawn claude --resume <UUID>
       ↓ resume reading stdin from supervisor and forwarding to new claude
```

agent-loop depends on Claude SDK's `--resume` self-healing of orphan
`tool_use` / `tool_result` pairs (the SDK fabricates an "interrupted"
tool_result for any orphan when reconstructing the messages array on
resume). If a future SDK version regresses this, the symptom is
repeated `--resume` failures — caught by the session-not-found
fallback above, which mints a fresh session. No eidos-side jsonl
rewriting today.

## 6. Crash recovery & lifecycle boundaries

### 6.1 Failure matrix

| Component fails | What survives | Recovery |
|---|---|---|
| claude (the long-lived child of agent-loop) | session.json, session jsonl on disk, transcripts written so far, agent-state.json (last write), agent-loop process | agent-loop respawns claude with `--resume <UUID>`; SDK heals tool_use orphans; agent-state writes resume |
| agent-loop | session.json, session jsonl, transcripts, dream-state.json — everything on disk. Wakes already ClearActive'd by supervisor but not yet processed by claude are lost (see §11 durability rule) | supervisor's `restart=always` child policy spawns new agent-loop; new instance reads session.json and `--resume`s. If agent-loop died with `Forward` mid-pipe-write the active.json marker is still present — supervisor's next loop iteration runs `recoverStaleActive` (folds it back into pending with the "interrupted" hint) before retrying the forward |
| supervisor (PID 1) | All on-disk state (volume-backed) | docker restart policy restarts container → supervisor starts → standard startup sequence (§6.3) |

### 6.2 supervisor auto-restart for agent-loop

`cmd/eidos/supervisor/children.go` (`processSpawner` / `ChildSpawner`) gains a `restart=always` option. Only agent-loop opts in. Restart policy:

- agent-loop never exits cleanly during normal operation. Dream rotation respawns the claude child in-process; agent-loop itself stays alive. Any agent-loop exit is therefore "abnormal", and the restart policy is uniform:
  - **`EXIT_AUTH_REQUIRED` (47)**: do not restart. authstate.json is the source of truth; operator runs `eidos forge login` to clear and then `eidos forge restart <name>`.
  - **Any other exit code (including 0)**: restart after exponential backoff (start 1s, cap 30s).
- Crash-loop guard: if agent-loop dies more than 5 times within 60s, stop restarting, log loud at supervisor stderr, and write `/eidos/run/agent-loop-crashed.json` with the last exit code and the tail of stderr so `forge status` can surface it.

crond and gate daemon retain their current "no auto-restart" behavior; this refactor does not expand that scope.

### 6.3 Startup sequence

```
container starts (docker / docker run)
  ↓ PID 1 = eidos supervisor run
  ↓ render and install crontab (via internal/cron)
  ↓ spawn crond (no-restart)
  ↓ spawn gate daemon (no-restart)
  ↓ spawn agent-loop (restart=always)
     ↓ agent-loop: acquire /eidos/run/agent.lock
     ↓ agent-loop: read session.json + dream-state.json
     ↓ agent-loop: spawn claude (--session-id or --resume per §5.1)
     ↓ agent-loop: start stdout drainer goroutine
     ↓ agent-loop: start fsnotify watcher on dream-state.json
     ↓ agent-loop: signal "ready" by writing agent-state.json with state=idle
  ↓ supervisor: connect agent-loop's stdin pipe to its forwarder
  ↓ supervisor: start plannerLoop goroutine
  ↓ supervisor: drainBirthIfPresent (handles First-Contact birth.json)
  ↓ supervisor: recoverStaleActive (folds any leftover active.json into pending)
  ↓ supervisor: fsnotify on /eidos/run/wake/, drain on every event
```

Ordering constraint: supervisor must connect the stdin pipe **before** any wake is forwarded. agent-loop blocks on stdin read; supervisor blocks on its own startup until the pipe is wired. No timing race in practice.

### 6.4 Graceful stop = sleep

`eidos forge stop <name>` / `docker stop <container>` → SIGTERM to supervisor (PID 1) → supervisor kills children → container exits. No "drain in-flight wakes" semantics; the mind-form's `/eidos` volume preserves everything. On next start, agent-loop reads session.json and `--resume`s. This matches SPEC's "容器停止 = 睡眠" framing. claude's force-kill may leave tool_use orphans in the session jsonl, healed by `--resume` on next start.

### 6.5 Cross-host migration

`/eidos` is a named docker volume. Migration = move the volume + start a fresh container on the new host. All state files (session.json, dream-state.json, wake/, transcripts/, agent.lock, agent-state.json) are inside the volume and travel together. agent-loop on new host runs the same startup sequence and `--resume`s the session. Zero design-level migration cost — this is a consequence of the volume layout, not new infrastructure introduced by this refactor.

## 7. IPC and command surface

### 7.1 Preserved IPC methods

`dream.begin`, `dream.end`, `state.get <path>`, `config.set` / `config.get`, all `inbox.*`, all `contact.*`, all `send` / `outbox.*` — every existing method keeps its signature and semantics.

### 7.2 New IPC method

| Method | Input | Output | Implementation |
|---|---|---|---|
| `agent.state` | empty | `{claude_busy, since_unix, last_event_type, last_event_at, session_id, session_started_at, wakes_in_session, outstanding_wakes_sent, results_seen, dreaming}` (matches agent-state.json schema) | gate daemon handler reads `/eidos/run/agent-state.json` and returns its contents |

Registered in the daemon method table per the Single Call Path constraint. Host-side and in-container callers both reach it via the same handler.

### 7.3 Host command surface

| Command | Change |
|---|---|
| `eidos forge status <name>` | Adds `thinking` (claude_busy yes/no) and `last_active` (seconds since last_event_at) lines. Reads via `agent.state` IPC |
| `eidos forge watch <name> --list` | New `THINKING` column (●/○ glyph) |
| `eidos forge watch <name>` follow mode | Renders an event line on every claude_busy true↔false transition |
| `eidos forge dream end ...` | Unchanged surface; new effect — current claude exits, new claude with new session-id starts within seconds (versus today's "next wake mints a new session") |
| `eidos forge wake <name>` | Unchanged (writes pending.json via the in-container helper) |
| `eidos forge logs <name>` | Unchanged (docker logs picks up agent-loop stderr) |

### 7.4 New config keys

| Key | Default | Purpose | Context |
|---|---|---|---|
| `mindform.dream_close_grace` | `60s` | How long to wait for claude to exit cleanly after stdin close during dream rotation. SIGTERM then SIGKILL after grace+5s | ContainerCtx only |

Registered in `internal/config/keys.go`. Surfaced via `eidos forge config <name> --dream-close-grace ...` (host) and `eidos gate config set mindform.dream_close_grace ...` (container).

### 7.5 Container-side reflexivity (`eidos forge` inside container)

`eidos forge whoami`, `memory`, `skills`, `journal`, `config get/set`, `dream begin/end` — every existing reflexivity command keeps its interface. The mind-form does not perceive an architectural change; it perceives a capability expansion. No new reflexivity command is added by this refactor.

## 8. Testing strategy

### 8.1 Reused existing tests

- `internal/wake/*_test.go`: zero change. Wake protocol semantics are untouched.
- `internal/sessionstate/sessionstate_test.go`: zero change.
- `internal/dreamstate/dreamstate_test.go`: zero change.
- `internal/transcript/*_test.go`: zero change to the package-level tests; the agent-loop tests below replace the agent-runner-driven coverage of integration with this package.
- `cmd/eidos/supervisor/scheduler_test.go` (planner): zero change.
- `cmd/eidos/supervisor/birth_test.go`: minor reorder — birth handler completes before agent-loop spawn in startup tests.

### 8.2 New tests

**`internal/agentloop/state_test.go`** — state-machine drainer:
- Table-driven: stream-json event sequence → expected agent-state.json transitions.
- Covers: session-init handling, assistant/user/result transitions, mailbox-synthesized turns (assistant without preceding stdin write), multi-turn sequences.

**`internal/agentloop/startup_test.go`** — startup decision:
- Table-driven port of `decideSessionMode` cases.
- session.json absent, present-but-empty, present-with-stale-jsonl, present-with-fresh-jsonl, dreamstate-newer-than-session.
- Asserts the generated claude args (`--session-id` vs `--resume`).

**`internal/agentloop/dream_test.go`** — rotation:
- Drive dream-state.json transitions, assert agent-loop closes stdin, waits, mints, respawns.
- Grace-timeout case: fake claude that doesn't exit on stdin close → SIGTERM after grace, SIGKILL after grace+5s.
- Dream-backlog case: wakes arriving during dreaming flag → buffered → drained to new claude after rotation (first one with `IsFirstWakeOfNewSession=true`).

**`internal/agentloop/crash_test.go`** — claude failure paths:
- Fake claude exits non-zero (not auth) → agent-loop respawns with `--resume`.
- Fake claude exits with EXIT_AUTH_REQUIRED → agent-loop writes authstate, exits cleanly.
- Fake claude hangs after a turn → not detected by agent-loop directly; tests document that hang detection is operator-visible via agent-state.json `last_event_at` growing stale.

**`cmd/eidos/supervisor/forward_test.go`** — supervisor's forwarder:
- Fake agent-loop process (or in-process pipe) → supervisor writes wake JSONL → asserts non-blocking, asserts ClearActive happens immediately.
- Broken-pipe path: agent-loop dies between writes → supervisor detects, triggers restart, recoverStaleActive folds active.json back to pending.

**`cmd/eidos/supervisor/children_restart_test.go`** — auto-restart:
- Fake agent-loop dies → supervisor respawns within backoff.
- 5-deaths-in-60s → crash-loop guard kicks in → `/eidos/run/agent-loop-crashed.json` written → no further restart.

### 8.3 Test double: stream-json claude stub

`internal/agentloop/testfake/claude_stub.go` — a small Go program built as a binary, replaces the production `claude` binary in tests (via `var claudeBin = "claude"` substitution). Modes selected by CLI flag:

- `--mode normal`: read each stdin JSONL → emit `system init` (once) + `assistant` + `result`.
- `--mode dream-then-exit`: normal until stdin EOF → exit cleanly.
- `--mode crash-after-N`: exit non-zero after N turns.
- `--mode hang-after-N`: stop emitting output after N turns, keep reading stdin.
- `--mode mailbox-burst`: between turn K and K+1, emit a spontaneous `assistant`+`result` pair (no preceding stdin), simulating a mailbox-synthesized turn.
- `--mode auth-required`: exit 47 with the expected stderr marker on first turn.

This stub replaces today's shell-script `claude` test double (which only handled plain `-p`).

### 8.4 Integration tests (`-tags=integration`)

`test/integration/agent_loop_test.go`:

- Real mind-form container + real claude (subscription auth via `EIDOS_TEST_CLAUDE_TOKEN`, same harness as existing birth integration tests).
- **Happy path**: spawn container → fire wake → observe agent-state.json transitions through busy → idle → check transcript file written → check session.json unchanged.
- **Dream rotation**: trigger `dream begin` + `dream end` via in-container `docker exec` → observe new session.json UUID → observe new transcripts directory under the new session.
- **Crash recovery**: `docker exec ... kill -9 <claude_pid>` → observe agent-loop respawns claude with `--resume`, transcripts continue, session.json unchanged.
- **Burst wakes**: enqueue 5 wakes in quick succession → observe 5 turns in transcript (claude SDK FIFO ordering), each correctly attributed.

### 8.5 Out-of-scope for testing

- Real Anthropic-side mailbox / sub-agent persistence behavior. We test our state machine on top of stub events; the SDK's actual mailbox guarantees are validated by manual deploy-test runs (`deploy-test/` per CLAUDE.md), not gated by CI.
- Prompt cache hit rate. Black-box on Anthropic's side; observable via cost/latency in manual runs.
- Long-lived sub-agent across many wakes (would require multi-hour CI runs). Smoke-covered by single-wake sub-agent dispatch + completion-receipt test.

## 9. Implementation order

The build is partitioned into coherent chunks for code-review tractability, not for shippable intermediate states. The full system goes from "current architecture" to "always-on" in one cutover; intermediate commits compile and pass package-level tests but the binary's behavior changes at the final commit.

| Order | Chunk | Lands |
|---|---|---|
| 1 | New `internal/agentloop/` package skeleton: state machine, agent-state.json writer, transcripts integration, claude spawn helpers, stream-json drainer, stdin forwarder, fsnotify on dream-state.json | Package compiles and is unit-tested in isolation. Not yet wired into supervisor |
| 2 | Stream-json claude stub in `internal/agentloop/testfake/`. All §8.2 unit tests pass | Stub binary; unit tests pass |
| 3 | `eidos supervisor agent-loop` subcommand wrapping `internal/agentloop` | Subcommand exists, runnable in isolation against the stub |
| 4 | supervisor wire-up: `Forward` callback replaces `SpawnAgent`; processSpawner gains `restart=always`; agent-loop joins the long-lived children; agent-runner subcommand and code deleted | supervisor uses agent-loop end-to-end; integration test §8.4 happy-path passes |
| 5 | `agent.state` IPC method + `forge status` / `forge watch` surface updates; `mindform.dream_close_grace` config key registered; `template/CLAUDE.md` updated with the always-on guidance sentence | All operator surfaces reflect the new model |
| 6 | Documentation: `SPEC.md` "唤醒上下文生命周期" rewritten; `EXAMPLE.md` walkthrough updated where needed | Docs current |

Each chunk is a logically coherent commit. Whether they go to a single PR or split across 2–3 PRs is a code-review-bandwidth decision, not a design decision.

## 10. Out of scope

Each item below is **not** built by this refactor. The trigger that would unblock it is named.

| Deferred | Trigger to revisit |
|---|---|
| Framework-side dream trigger (forge threshold injects a `reason=dream` wake) | Operator observes mind-forms forgetting to dream and session bloat causing real-world cost or context-window failures |
| Force-rotation if mind-form ignores dream | Same trigger as above |
| `agent.state` exposed cross-container (host queries another host's mind-form) | Cross-host fleet operations become a goal — currently single-host per operator |
| Real-time SSE stream of transcripts to dashboard | Dashboard needs sub-second live indicators that NDJSON-on-disk-polled-via-fsnotify cannot meet |
| Hot-swap of mind-form framework code without container restart | Mind-form self-evolution of `eidopsyche/` source becomes an active feature (SPEC mentions this as a later iteration) |
| Sub-agent identity / lifecycle elevation to "sub-mind-form" | A clear use case where Claude Code's process-not-entity sub-agent semantics demonstrably fail the SPEC's vision |
| Token-cap-based proactive dream prompting | Anthropic's `context_management.edits` server-side compression proves insufficient and we observe wake failures from context overflow |
| Prompt-cache hit-rate monitoring infrastructure | Cost or latency observations suggest cache thrash is meaningful |

## 11. Known design properties

- **Coalescing strength**: producer-layer only. Multiple wakes during a long claude turn coalesce in pending.json. Wakes that are forwarded before claude finishes the previous turn do not coalesce — they queue at the SDK level as separate turns. This is intentional: in always-on mode the marginal cost of an extra turn within the same session is small (prompt cache reuse), and the mind-form can soft-deduplicate at the model level.
- **Wake durability rule**: a wake is durable on disk (in pending.json or active.json) up to the moment supervisor calls `ClearActive` after a successful `Forward`. After that, the wake lives only in agent-loop's process memory or claude's stdin pipe buffer — both lost on agent-loop crash. The mind-form is resilient to occasional missed wakes: heartbeat re-fires, inbox/contacts/dreamstate are all read fresh from disk on the next wake. We do not introduce ack-based durability to close this window (see §2 "Wake delivery is fire-and-forget").
- **Dream backlog**: wakes arriving between dream-begin and rotation-complete are buffered in agent-loop memory and drained to the new claude in order. This is a separate buffer from the durability gap above; producer-layer wake.Submit calls during this window land in pending.json (durable) and supervisor forwards them to agent-loop's stdin, where agent-loop redirects them to the backlog instead of to claude. The backlog itself is not persisted (same loss profile as any other agent-loop process state).
- **No model perception of architecture**: every change in this refactor is invisible to the mind-form at the prompt-interface level. The mind-form notices new capabilities (background tasks survive, sub-agents persist) but receives the same wake-message shape, the same identity prompt, the same dream protocol. This makes upgrade for the mind-form transparent.
