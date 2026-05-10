# Persistent wake context across wakes (with dream as session boundary)

- **Date**: 2026-05-10
- **Touches**: `cmd/eidos/supervisor/agent_runner.go`, `cmd/eidos/forge/{dream,runtime_state,status,transcript_list,watch,watch_render}.go`, `internal/transcript/{store,events}.go`, `internal/sessionstate/` (new package), `SPEC.md`, `EXAMPLE.md`
- **Sequencing**: single PR. Small enough to land together; splitting would force the watch surface to ship without a session model to point at.

## 1. Problem statement

Today every wake invokes `claude -p <wake_msg>` (one-shot print mode) with `--append-system-prompt $(self/identity.md)`. No `--resume` / `--continue` flag is passed. Each wake is therefore a brand-new Claude conversation: the model has no working memory of what it thought, decided, or was halfway through during the previous wake. Continuity is entirely outsourced to the on-disk substrate (`memory/`, `journal/`, `essence/`, `inbox/`, `CLAUDE.md`), which the mind-form must re-read with tool calls every wake.

This is the opposite of what the design intends. The mind-form should experience an **uninterrupted train of thought** between dreams; a dream is the explicit moment of memory consolidation that resets working context. Today both the train of thought and the dream-as-reset are missing — every wake is "born again."

## 2. Design intent

- **Dream-bounded sessions.** A *session* is the span of wakes between two dreams. Within a session, every wake resumes the prior Claude conversation (same `session.jsonl`). The first wake after `dream-end` starts a new session.
- **Session-as-truth lives in two places.** A small JSON file (`/eidos/run/session.json`) records the active session UUID and bookkeeping; Claude Code's own `<CLAUDE_DIR>/projects/<encoded-cwd>/<UUID>.jsonl` holds the conversation. Both live inside the persistent ontology volume and survive container restart, are cleaned by `forge purge`, and travel with `forge ontology export/import`.
- **Re-build the system prompt every wake; let `claude` carry the conversation.** Claude Code does not persist the system prompt into `session.jsonl` — it reconstructs it from CWD/CLAUDE.md/dynamic sections plus any `--append-system-prompt` content on every invocation. This means we keep passing identity.md every wake (same code path as today); identity.md edits propagate on the very next wake. No hash-compare optimization is needed.
- **No automatic context cap.** The mind-form (and operator) own dream cadence. If a session grows past Claude's context window, the wake fails — at which point the operator notices and the mind-form (or operator) should dream. Code does not enforce a soft / hard cap.
- **Silent fall-back, never destructive.** If `--resume` fails because the session file is missing or corrupt, log a warning and mint a new session. The on-disk substrate is intact; the mind-form rebuilds working memory by reading it.
- **YAGNI.** No cross-session metrics, no manual session reset command, no `--continue` shortcut, no fork branches, no archive/export of session jsonl files separate from the ontology export, no auto-dream when session grows large. Each named in §11 with the trigger that would unblock it.

## 3. Session lifecycle

### 3.1 State diagram

```
   ┌────────────────────────────────────┐
   │ /eidos/run/session.json absent     │  fresh container, post-purge,
   └────────────────────────────────────┘  or post-fallback
                    │ wake fires
                    ▼
   ┌────────────────────────────────────┐
   │ agent-runner: sessionstate.Mint()  │  generate UUID, persist
   │ → claude --session-id <UUID>       │  Claude creates the .jsonl
   │ → wake-message has FIRST_WAKE      │  prefix in the user turn
   │   prefix                           │
   └────────────────────────────────────┘
                    │ wake completes (claude exit 0)
                    ▼
   ┌────────────────────────────────────┐
   │ session.json present + UUID stable │
   │ wakes_in_session += 1              │
   └────────────────────────────────────┘
        │ wake fires           ▲
        ▼                      │
   ┌────────────────────────────────────┐
   │ agent-runner: read session.json    │
   │ → claude --resume <UUID>           │
   │ → wake-message: today's snapshot   │  no first-wake prefix
   └────────────────────────────────────┘
                    │ wake invokes `eidos forge dream end`
                    ▼
   ┌────────────────────────────────────┐
   │ dreamEndEcho:                      │
   │   1. dreamstate.End()              │
   │   2. sessionstate.Clear()          │  next wake → top of diagram
   └────────────────────────────────────┘
```

### 3.2 Boundary rule

The next wake starts a fresh session iff **either** of these is true at wake-start:

1. `session.json` is absent or unreadable.
2. `dream.LastDreamFinishedAt > session.SessionStartedAt`.

(2) is a tie-breaker for the case where `dreamstate.End` succeeded but `sessionstate.Clear` failed — the next wake notices the dream is newer than the session and resets anyway. Without this, a partial dream-end could leave a stale session resumed indefinitely.

Cases that do **not** trigger a fresh session:

- `dream-begin` without `dream-end` (incomplete dream): the next wake resumes; the mind-form sees its own half-finished dream in the conversation history and decides whether to retry, abandon, or compensate. Only a *committed* dream resets working memory.
- Multiple `dream-end` calls within a single wake: each calls `Clear`; the result is the same — next wake fresh.

## 4. Data model

### 4.1 New file: `/eidos/run/session.json`

```json
{
  "v": 1,
  "session_id": "7d2f6f2e-1b2a-4c3d-9e8f-aabbccddeeff",
  "session_started_at": 1746920400,
  "wakes_in_session": 47
}
```

Owned by agent-runner (single writer, serialised by the existing `/eidos/run/agent.lock`). Cleared by `eidos forge dream end` (also single-writer in its own scope). Atomic writes via tmp + rename, plus a per-file flock at `session.json.lock` to defend against parallel `forge dream` invocations (rare but possible if an operator runs `eidos forge exec` outside a wake).

### 4.2 New package: `internal/sessionstate/`

Modelled on `internal/dreamstate/`. Public API:

```go
const SchemaVersion = 1

type State struct {
    V                int    `json:"v"`
    SessionID        string `json:"session_id"`
    SessionStartedAt int64  `json:"session_started_at"`
    WakesInSession   int    `json:"wakes_in_session"`
}

// Read returns the state at path. Missing file → zero State + nil err.
// Corrupt file → zero State + non-nil err (caller decides fallback).
func Read(path string) (State, error)

// Mint generates a new UUID, persists a fresh State{SessionID, SessionStartedAt: now.Unix()}, returns it.
func Mint(path string, now time.Time) (State, error)

// IncrementWake atomically bumps WakesInSession by 1. No-op if session.json absent.
func IncrementWake(path string) error

// Clear removes session.json. Idempotent.
func Clear(path string) error
```

Locking: copy `dreamstate_lock_unix.go` / `dreamstate_lock_windows.go` verbatim into the new package. No cross-package import.

### 4.3 Schema bump: `internal/transcript/Entry`

```go
type Entry struct {
    ID             string   `json:"id"`
    SessionID      string   `json:"session_id,omitempty"`  // NEW
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

Old entries (no `session_id` field) read as empty string; renderers display `-` in the SESSION column. Index file schema stays at `v: 1` since the field is purely additive — no migration needed.

### 4.4 Extended runtime-state JSON

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

    // NEW: omitted when session.json is absent.
    SessionID        string `json:"session_id,omitempty"`
    SessionStartedAt int64  `json:"session_started_at,omitempty"`
    WakesInSession   int    `json:"wakes_in_session,omitempty"`
}
```

`computeRuntimeState` reads `session.json` via `sessionstate.Read` and fills these fields when present. No new hidden command; this is purely additive on the existing one.

## 5. Agent-runner changes

### 5.1 `runAgent` flow (modified)

```
1. Acquire /eidos/run/agent.lock                              [unchanged]
2. Read wake.Signal, dreamstate, config                       [unchanged]
3. Read session.json
   - sess, err := sessionstate.Read(sessionStatePath)
   - corrupt or missing                                       → mode = NEW
   - sess.SessionID == ""                                     → mode = NEW
   - dream.LastDreamFinishedAt > sess.SessionStartedAt        → mode = NEW
   - otherwise                                                → mode = RESUME(sess.SessionID)
4. If mode == NEW:
     newSess, _ := sessionstate.Mint(sessionStatePath, time.Now())
     sessionUUID = newSess.SessionID
     isFirstWake = true
     dreamCountAtBoundary, lastDreamFinishedAt = ds.DreamCount, ds.LastDreamFinishedAt
   Else:
     sessionUUID = sess.SessionID
     isFirstWake = false
5. identity := os.ReadFile("self/identity.md")                [unchanged]
6. msg := buildWakeMessage(in, isFirstWake, dreamCountAtBoundary, lastDreamFinishedAt)
7. args := buildClaudeArgs(identity, msg, gateConfigPath, streamJSON, sessionMode{
       Kind: NEW or RESUME,
       UUID: sessionUUID,
   })
8. If mode == RESUME: pre-flight check os.Stat(<CLAUDE_DIR>/projects/<encoded-cwd>/<UUID>.jsonl)
   where <CLAUDE_DIR> = <ontology>/.claude (the value already exported in env by
   runWithTranscript) and <encoded-cwd> is the ontology dir absolute path with
   every non-alphanumeric char replaced by `-` (Claude Code's documented scheme;
   e.g. `/eidos/ontology` → `-eidos-ontology`).
   - if missing: log warning, sessionstate.Clear, retry from step 4 as NEW (one-shot)
9. Run claude with stream-json piping into transcripts/wake-<id>.ndjson  [unchanged]
10. If claude exited non-zero AND stderr contains a session-not-found marker
    AND mode was RESUME and we have not yet retried:
      log warning, sessionstate.Clear, restart from step 4 as NEW (one-shot)
11. transcript.Finalize(entry{SessionID: sessionUUID, ...})    extended
12. sessionstate.IncrementWake(sessionStatePath)               new
13. Release agent.lock                                         [unchanged]
```

The pre-flight `os.Stat` (step 8) catches the common case (file gone, e.g. ontology import lost the jsonl). The stderr match (step 10) is a belt-and-braces catch for the rare case where the file exists but is unparseable. Stderr matcher patterns: `session not found`, `could not find session`, `no such session`. Match is case-insensitive substring; tolerant on additions.

Retry budget: **at most one** NEW retry per wake. A second failure is a real failure (auth, bad model id, network) and falls through to the existing `handleClaudeExit` path.

### 5.2 `buildClaudeArgs` signature change

```go
type SessionMode struct {
    Kind SessionKind  // SessionNew | SessionResume
    UUID string
}

func buildClaudeArgs(
    identity, msg, configPath string,
    streamJSON bool,
    sess SessionMode,
) []string
```

Emits:
- `SessionNew`  → `--session-id <UUID>` appended after `--dangerously-skip-permissions`.
- `SessionResume` → `--resume <UUID>` appended in the same slot.

`--append-system-prompt`, `--model`, `--output-format stream-json --verbose --include-partial-messages`, `-p <msg>` are unchanged.

### 5.3 `buildWakeMessage` signature change

```go
type wakePromptInput struct {
    // ... existing fields ...

    // NEW: when set, prepend the FIRST_WAKE prefix.
    IsFirstWakeOfNewSession bool
    DreamCount              int    // last completed dream's index
    LastDreamFinishedAt     int64  // unix sec
}
```

When `IsFirstWakeOfNewSession`:

```
This is the first wake of a new session (your prior working memory was
consolidated in dream #<N> at <RFC3339>; on-disk memory/journal/essence
are intact, refer to them as needed).

You have just woken. Reason: <...>. Inbox has <N> unread message(s). ...
```

When `LastDreamFinishedAt == 0` (very first wake ever, no prior dream): use the variant `(no prior dream — this is the mind-form's first session; on-disk substrate is intact)`.

## 6. Dream coupling

`cmd/eidos/forge/dream.go` `dreamEndEcho` adds one step after `dreamstate.End` returns successfully:

```go
if err := sessionstate.Clear(sessionStatePath); err != nil {
    log.Printf("dream end: sessionstate.Clear failed (%v); next wake will reset via fallback", err)
}
```

`Clear` failure does not affect the dream-end command's user-visible result. The fallback rule (§3.2 case 2) recovers next wake.

`dreamstate.Begin` is unchanged. A `dream begin` without a matching `end` does not touch session state — the next wake resumes the existing session and the mind-form sees its half-finished dream.

## 7. Surface changes

### 7.1 `forge status` output

Add one line, immediately after `phase:` and `since:`, when `RuntimeState.SessionID != ""`:

```
session: 7d2f6f2e (age 3h12m, 47 wakes)
```

The short prefix is the **first 8 hex chars** of the UUID (consistent with the wake-id column width). Age is `time.Since(time.Unix(SessionStartedAt, 0)).Truncate(time.Second)`. Wake count is `WakesInSession`.

When session is absent (offline / sleeping with no past activity), the line is omitted.

### 7.2 `forge watch --list` SESSION column

Updated header and row format in `cmd/eidos/forge/{transcript_list.go,watch_render.go}`:

```
ID         SESSION    REASON      STARTED              DUR    COST      STATUS
abc12345   7d2f6f2e   heartbeat   2026-05-10 14:30:00  8s     $0.0102   ok
abc12344   7d2f6f2e   heartbeat   2026-05-10 14:29:00  12s    $0.0098   ok
def56789   1a3b8821   heartbeat   2026-05-10 14:28:00  45s    $0.0521   ok
def56788   1a3b8821   inbox       2026-05-10 14:27:00  90s    $0.0334   ok
```

Width is **8 chars**. Old entries with empty `SessionID` render as `-` (dash, left-aligned).

### 7.3 Per-wake header in stream renderer

`renderSystem` (in `watch_render.go`) currently emits:
```
━━━ wake started · model=claude-sonnet-4-6 ━━━
```

Extended to, when index lookup yields a `SessionID` and we know the wake's ordinal in its session:
```
━━━ wake abc12345 (47 of session 7d2f6f2e) · model=claude-sonnet-4-6 ━━━
```

To compute the ordinal: the renderer counts how many entries in the index share the same `SessionID` and have `StartedAt <= this wake's StartedAt`. The active wake (still streaming, not yet `Finalized` into the index) is `(index_count_for_session) + 1`. This is a per-wake-render lookup against the already-loaded index — no new IPC.

When the wake has no `SessionID` (legacy entry, brand-new session whose first wake is also the active one and the index hasn't been reloaded since, or transcript-list call failed), fall back to today's format. No hard error.

### 7.4 Session boundary in follow mode

`runWatchTail`'s follow loop already polls for new wakes. Track the previous wake's `SessionID`; when the next wake's `SessionID` differs, render before its header:

```
━━━ done · cost $0.0098 · 12s · 14 turns · ok ━━━

═══════════════════════════════════════════════════
   New session 1a3b8821
═══════════════════════════════════════════════════

━━━ wake def56789 (1 of session 1a3b8821) · model=claude-sonnet-4-6 ━━━
```

The boundary line is **bare** — only the new session's short UUID. No dream note (avoid surfacing memory snippets through what is otherwise a debug log).

When the previous wake's `SessionID` is empty (legacy) **or** there is no previous wake (the watch session just started), no boundary line is rendered — boundaries are only meaningful between two known sessions. Subsequent transitions render normally.

## 8. Error handling matrix

| Scenario | Behavior | Operator visibility |
|----------|----------|---------------------|
| `session.json` absent (first wake, post-purge, post-fallback) | `mode = NEW`; mint UUID | Normal wake; first-wake prefix in user turn |
| `session.json` corrupt / unmarshal error | log warning, `mode = NEW`, mint | docker logs: `agent-runner: session.json corrupt (%v); minting new` |
| Pre-flight stat: `<CLAUDE_DIR>/projects/<encoded>/<UUID>.jsonl` missing | `sessionstate.Clear`, retry as NEW once | docker logs: `agent-runner: session jsonl for %s missing; resetting` |
| Claude exits non-zero with `session not found` stderr (RESUME path, no prior retry) | `sessionstate.Clear`, retry as NEW once | docker logs: `agent-runner: claude reports session %s not found; resetting and retrying` |
| Second consecutive failure on the NEW retry | Falls through to existing `handleClaudeExit` (auth-gate, exit code) | Same as today's wake failure |
| `dreamstate.End` succeeds, `sessionstate.Clear` fails | `dreamEndEcho` returns success; warning logged | Operator-invisible; next wake's fallback rule resets session |
| Container crashes mid-wake | Existing `transcript.Recover` handles orphan ndjson; `session.json` and `session.jsonl` survive (mounted volume); next wake resumes from claude's last flush | Wake re-runs; minor context gap possible, no reset |
| Two concurrent `forge dream end` invocations | Serialised by `dreamstate.lock` and (separately) `sessionstate.lock`; idempotent | — |
| `forge ontology import` from a backup that has session.json but not the matching jsonl | Pre-flight stat catches it on next wake → fallback | Single warning line in docker logs |
| operator edits `self/identity.md` mid-session | Next wake's `--append-system-prompt` carries new content; session jsonl history preserves old responses (cannot be retroactively edited — Claude's design) | No special handling; behaves identically to today |

## 9. Testing

### 9.1 Unit tests

**`internal/sessionstate/sessionstate_test.go`** — new
- `Read` of missing file → zero State, nil err
- `Read` of corrupt file → zero State, non-nil err
- `Mint` writes a State with valid UUIDv4 and `SessionStartedAt = now.Unix()`
- `IncrementWake` is monotonic; no-op when file absent
- `Clear` removes the file; idempotent
- `Mint` / `IncrementWake` / `Clear` serialise under flock when called from parallel goroutines

**`cmd/eidos/supervisor/agent_runner_test.go`** — extended (uses existing `claudeBin` stub harness)
- Wake with no `session.json` → stub receives `--session-id <uuid>`; `session.json` lands; wake-message contains `"first wake of a new session"` prefix
- Wake with `session.json` and `dream.LastDreamFinishedAt < session.SessionStartedAt` → stub receives `--resume <uuid>`; no prefix
- After dream-end, next wake → stub receives a *new* `--session-id`; prefix present; old `session.json` gone
- `session.json` exists but jsonl path absent (test creates fake CLAUDE_DIR layout) → log warning, retry as NEW, stub receives `--session-id` on retry
- Stub configured to exit with `"session not found"` on stderr → same behavior
- `identity.md` content changes between wakes → stub still receives latest content via `--append-system-prompt`
- Retry budget enforced: a second failure does NOT loop again

**`internal/transcript/store_test.go`** — extended
- `Entry` with `SessionID` round-trips through index.json
- Reading a v1 index.json without `session_id` fields → all entries have empty `SessionID`, no error

**`cmd/eidos/forge/runtime_state_test.go`** — extended
- `session.json` present → fields populated in JSON output
- `session.json` absent → fields omitted (`omitempty` honored)

**`cmd/eidos/forge/transcript_list_test.go`** / **`watch_render_test.go`** — extended
- SESSION column appears with 8-char prefix; legacy empty IDs render as `-`
- Transition between wakes with different `SessionID` triggers boundary line; same `SessionID` does not
- First-ever wake with `SessionID` (previous was empty) does NOT render a boundary

### 9.2 Integration tests (`-tags=integration`, requires Docker)

In a fresh `forge create test && forge start test`:

- After two heartbeats: `session.json` present in `/eidos/run/`, same UUID across both; `<ontology>/.claude/projects/-eidos-ontology/<uuid>.jsonl` exists with both turns
- Trigger dream-end via the mind-form (the integration harness provides a way to send a synthetic wake message): `session.json` disappears
- Next heartbeat: new UUID in `session.json`
- `eidos forge status test` output contains a `session: ` line
- `eidos forge watch test --list` table contains a SESSION column

### 9.3 Manual verification (pre-release)

- `forge watch <name>` left running through one or more dream cycles. Confirm: per-wake header has session ordinal; boundary line renders cleanly between sessions; no extra noise on the first session after upgrade.

## 10. Migration & compatibility

- **Existing mind-forms upgrading to this release**: first wake after upgrade sees no `session.json` → mints new → from then on, normal flow. No migration script required. Their pre-existing wakes in `index.json` carry `SessionID = ""` and render as `-` in the SESSION column; this is a permanent visual artifact for those entries (acceptable — they are historical).
- **Old transcripts on disk**: `transcript.Recover` already tolerates missing fields; the new optional `SessionID` field changes nothing.
- **Older claude versions** (pre stream-json, the existing fallback): unchanged. The `runWithoutTranscript` path keeps the legacy plain-text behavior. We still pass `--session-id` / `--resume`, since both flags exist in the supported floor (`claudeMinVersion` = 2.1.0). If it turns out a future floor is needed for `--session-id`, bump `claudeMinVersion` and document the failure mode.
- **Schema versions**: `session.json.v = 1`; transcript index stays at `v: 1` (additive field).

## 11. Out of scope (named for discoverability)

| Excluded | Trigger that would unblock it |
|----------|-------------------------------|
| Auto-dream when session grows past N tokens / N wakes | A real mind-form running unattended for >24h hits context limit consistently |
| `eidos forge session reset` operator command | Operator needs to force a reset without dreaming (rare; should fall out of dream cadence instead) |
| `forge watch --session <id>` to dump a whole session | More than one operator asks for it |
| `forge watch --session current --list` filtered listing | Same trigger |
| Aggregate session metrics (token sums, cost roll-up) in `forge status` | `forge status` becomes the canonical dashboard surface |
| Cross-session `forge transcripts compare` / replay | A regression debugging task can't be done without it |
| Separate session export/import (independent of ontology export) | Sharing a session jsonl across mind-forms (an entirely different design) |
| Fork sessions (`--fork-session`) for what-if branching | An explicit creative-divergence feature is requested |
| Hash-compare optimization on identity.md re-injection | Profiling shows it matters (it almost certainly doesn't — Claude reconstructs the prompt every call regardless) |
| Session boundary line in `--list` (sub-header between wakes) | Operators report missing the column-only signal |
| Dream note in session boundary line | Operator review surfaces a use case where it's worth the log noise / privacy tradeoff |

## 12. Open questions

None at draft time. All mechanism choices were made during brainstorming (see `claude` chat 2026-05-10).
