# Mind-form status taxonomy & headless reasoning observer

> **Superseded in part by [`2026-05-12-forge-status-always-on-alignment-design.md`](2026-05-12-forge-status-always-on-alignment-design.md).** The phase taxonomy described here (`sleeping`/`awake[reason]`/`awake+dreaming`) and the `RuntimeState` v1 schema were a fit for the per-wake-spawn `agent-runner` runtime that v0.14.0 (PR #71) replaced with the always-on `agent-loop`. The current phase values are `offline | starting | auth-required | dreaming | thinking | idle` and `RuntimeState` is at v2; see the linked spec for the live shape. The `forge watch` renderer design in §6 is still current.

- **Date**: 2026-05-10
- **Touches**: `cmd/eidos/forge/{status,list,status_detail,watch,transcript_tail,transcript_list,runtime_state}.go` (new + modified), `cmd/eidos/supervisor/agent_runner.go`, `internal/forgectl`, `internal/wake` (read-only), `docker/entrypoint.sh` (transcripts dir setup), `internal/config` (new fields), `README.md`, `EXAMPLE.md`
- **Sequencing**: three PRs — PR-A (status taxonomy), PR-B1 (capture + storage), PR-B2 (`forge watch` renderer). See §8.

## 1. Problem statement

Two related visibility gaps are blocking effective operation and debugging of mind-forms today:

1. **Status is too coarse.** `eidos forge status` and `eidos forge list` only surface the docker container state (`running` / `exited` / `created`). The operator cannot tell, while the container is running, whether the agent is actively processing a wake, idling between wakes, or dreaming. Several already-on-disk signals carry the answer (`wake/active.json`, `dreamstate.CurrentlyDreaming`, the wake reason), but they are not exposed to the host UI.
2. **Headless reasoning is invisible.** `cmd/eidos/supervisor/agent_runner.go` invokes `claude -p <msg>` (plain print mode), so `docker logs` only sees the agent's final assistant text — `thinking` blocks, `tool_use` invocations, `tool_result` payloads, model id, MCP server inventory, and cost summary are lost. When a wake misbehaves (no reply, wrong action, hang) the operator has no structured trace to inspect.

This spec ships both fixes, in three small PRs that share a single design.

## 2. Design intent

- **Phase, not state.** Status display gets a new "phase" field derived from existing on-disk signals: `offline | sleeping | awake[reason] | awake+dreaming`, plus an orthogonal `auth_required` modifier.
- **Capture the structured stream once, render it many ways.** `agent-runner` switches claude into `--output-format stream-json` and persists the NDJSON event stream to a per-wake file in the container volume. Both this PR's CLI renderer and any future surface (dashboard, MCP) consume the same file.
- **Separation of duty:** in-container code does file I/O (data); host code does presentation. Transcripts live in the volume, owned by the mind-form's UID; a hidden in-container subcommand streams them out, and the host's `forge watch` is the human renderer.
- **Lifecycle log vs. reasoning log are different concerns.** After the change, `docker logs` becomes a clean lifecycle/audit log (wake started / tool used / completed with cost / failed); `forge watch` is the deep reasoning view. Both have their place.
- **Single Call Path is respected, but the gate daemon is not the through-line for forge surfaces.** Like `forge status` and `forge logs` today, the new commands stay on the host-direct + `docker exec` pattern. If a future Dashboard panel needs the same data, the in-container subcommands get promoted to gate-daemon IPC methods at that point — not preemptively.
- **YAGNI ruthlessly.** No replay/time-travel, no cross-mindform aggregation, no transcript editing/annotation, no Bubble Tea TUI, no Dashboard panel in this milestone. Each is named in §9 with the trigger that would unblock it.
- **Graceful degradation.** Old claude versions (pre stream-json or with different event names) fall back to the existing `-p` text path: no transcript file, but no broken mindform. Old mindform images without `runtime-state` / `transcript-tail` fall back to legacy display in `forge status` (same pattern as current `status-detail`).

## 3. Status taxonomy (PR-A)

### 3.1 Phase machine

Pure derivation from existing on-disk signals — no new persistent state.

```
                    docker.state == "running" ?
                          /              \
                        no                yes
                        │                  │
                     offline   /eidos/run/wake/active.json exists ?
                                     /              \
                                   no               yes
                                   │                  │
                              sleeping         awake[reason]
                                                       │
                              /eidos/run/dream-state.json
                                .CurrentlyDreaming == true ?
                                                       │
                                              awake+dreaming
```

Modifier (independent of phase, set when present): `auth_required` — derived from `internal/authstate.Read()`, same as today's `status-detail` line.

The wake reason inside `awake[reason]` is one of the existing `wake.Reason` values: `mindgate | heartbeat | planned | manual | birth`.

### 3.2 In-container subcommand: `eidos forge runtime-state`

New hidden subcommand under `cmd/eidos/forge/`, alongside the existing `whoami` and `status-detail`. Outputs JSON only (no text format) for clean machine consumption:

```json
{
  "v": 1,
  "phase": "awake",
  "wake_reason": "mindgate",
  "active_wake_id": "1715000000-mindgate",
  "dreaming": false,
  "auth_required": false,
  "since_phase_change_seconds": 42,
  "container_started_at": 1715000000
}
```

Field rules:

| Field | Type | When set |
|---|---|---|
| `v` | int | Always `1` |
| `phase` | string | `sleeping` / `awake` / `awake+dreaming` (never `offline` — host-side derivation) |
| `wake_reason` | string | Only when `phase` starts with `awake`; copied from `active.json` |
| `active_wake_id` | string | Only when `phase` starts with `awake`; copied from `active.json` |
| `dreaming` | bool | `dreamstate.CurrentlyDreaming` |
| `auth_required` | bool | `authstate.Read() != nil` |
| `since_phase_change_seconds` | int | only set for `awake[+dreaming]`: seconds since `active.json` mtime, for "stuck wake" detection. Omitted in `sleeping`. |
| `container_started_at` | int | unix seconds; from `/proc/1/stat` start_time, fallback `0` if unreadable |

Reasoning:
- Pure JSON output — `forge status` parses it and renders.
- Not folded into `status-detail`: `status-detail` is human-formatted text and feeds directly into `forge status` text rendering. `runtime-state` is structured data, consumed by both `forge status` and `forge list`.

### 3.3 Host-side rendering

**`forge status <name>`** (`cmd/eidos/forge/status.go`):
- After the existing `state: <docker-state>` line, replace it with `phase: <phase> [(<reason>)]` derived from `runtime-state` JSON when the container is running.
- When container is not running: `phase: offline`.
- Existing `whoami` and `status-detail` invocations continue unchanged (they handle `auth_required`, plans, dreams text formatting).
- If `runtime-state` returns non-zero (older image without the subcommand), fall back to the legacy `state: <docker-state>` line — no breakage.

**`forge list`** (`cmd/eidos/forge/list.go`):
- Second column changes from raw docker state to phase string. Format: `<name>  <phase>` where phase is one of `offline | sleeping | awake | awake+dreaming` (the wake reason is omitted in `list` to keep the column narrow; one-line summary).
- Same fallback to docker state when `runtime-state` is unavailable.

### 3.4 Test plan (PR-A)

Unit tests in `cmd/eidos/forge/`:
- `runtime_state_test.go` — table test: given a tmpdir with various combinations of `wake/active.json` / `dream-state.json` / `authstate` files, assert the JSON output matches expectations.
- `status_test.go` — extend existing tests with phase rendering: mock `forgectl.Client.ContainerExec` to return canned `runtime-state` JSON, assert `forge status` output.
- `list_test.go` — mock to return canned phase, assert column format.
- Fallback tests: `runtime-state` exec returns exit 1 (older image), assert legacy `state:` line still appears.

## 4. Capture layer — agent-runner stream-json (PR-B1)

### 4.1 Claude invocation change

`buildClaudeArgs` (`cmd/eidos/supervisor/agent_runner.go:247`) appends three flags:

```go
args = append(args,
    "--output-format", "stream-json",
    "--verbose",                  // required for stream-json + -p combo
    "--include-partial-messages", // enables content_block_delta events
)
```

Per Claude Code docs: `claude -p "<msg>" --output-format stream-json --verbose --include-partial-messages` is the documented headless streaming form.

### 4.2 stdout routing

Currently agent-runner sets `c.Stdout = os.Stdout`, sending claude's plain text to the supervisor → docker logs. After the change:

```
claude --output-format stream-json
       │
       ▼ (NDJSON lines)
agent-runner reads claude.Stdout (pipe)
       │
       ├──► /eidos/run/transcripts/wake-<id>.ndjson   (raw passthrough)
       │
       └──► parser → lifecycle log lines on stdout to docker logs (stderr stays for real errors):
              "agent-runner: wake-id=abc reason=mindgate model=<model> tools=Read,Bash,..."
              "agent-runner: wake-id=abc tool=Read input={path:/eidos/inbox/...}"
              "agent-runner: wake-id=abc text=<truncated 80 char preview>"
              "agent-runner: wake-id=abc completed cost_usd=0.012 dur_ms=42017 ok"
              "agent-runner: wake-id=abc failed exit=1 reason=<short>"
```

Implementation:
- `cmd.StdoutPipe()` returns the read end of claude's stdout
- A goroutine reads it line-by-line (large buffer — see §10.2), tees each raw line to the ndjson file, and forwards parsed lifecycle events to `log.Printf` (which goes to docker logs via supervisor's redirect).
- claude's stderr stays wired to `os.Stderr` so error output still surfaces to docker logs unchanged.

The lifecycle parser is intentionally minimal — it only categorizes by top-level event type, picks out a few headline fields (model id, tool name, cost), and emits one log line per. The full semantic interpretation belongs to `forge watch`.

### 4.3 Fallback to plain `-p` mode

Stream-json may fail on:
- Older claude CLI versions that don't support `--include-partial-messages` or have different event schema
- Non-stream-json claude builds (custom forks, stub binaries used in tests)

Detection:
- agent-runner runs `claude --version` once at process start. Parse `Major.Minor.Patch`.
- If parse succeeds AND version ≥ documented minimum (set in spec table; tentative `2.1.0` based on context7's available tags — confirm at implementation), use stream-json path.
- If version parse fails or below minimum, fall back to plain `-p msg` (current behavior). agent-runner emits a single warning lifecycle line: `"agent-runner: stream-json unsupported (claude version=X.Y.Z); transcripts disabled"`.
- This means `forge watch` will display "no transcript available for this wake (capture disabled)" when reading a wake from the fallback period.

The version probe is cached for the lifetime of the agent-runner process (one wake), so the cost is one extra `claude --version` exec per wake.

### 4.4 Test plan (PR-B1, capture portion)

`cmd/eidos/supervisor/agent_runner_test.go`:
- New test `Test_streamJSONTeeing`: stub claude binary (shell script) that emits a canned stream-json fixture. Run agent-runner. Assert (a) the ndjson file contains the exact bytes claude emitted, (b) docker stdout contains the expected lifecycle log lines, (c) exit code propagates, (d) auth_required marker still triggers correctly when the stub emits the auth-error pattern.
- `Test_versionFallback`: stub claude that prints version `1.0.0` (below minimum). Assert it's invoked with the legacy `-p` arg form (no stream-json flag) and no transcript file is written.
- Tests for the lifecycle parser as a pure function, table-driven on event types.

Real Claude Code is not invoked in CI — the stub binary covers the contract.

## 5. Storage layer (PR-B1)

### 5.1 Layout

All under the per-mindform docker volume, under `/eidos/run/transcripts/`:

```
transcripts/
├── index.json                  # metadata, owned by agent-runner
├── current  →  wake-<id>.ndjson   # symlink, present only during a wake
└── wake-<id>.ndjson            # one file per wake, append-only NDJSON
```

The directory is created with mode `0700` and `chown 1000:1000` by `init-volume` (`cmd/eidos/forge/init_volume.go`) at volume bootstrap, alongside the existing `/eidos/run/wake/` setup.

### 5.2 `index.json` schema

```json
{
  "v": 1,
  "wakes": [
    {
      "id": "1715000000-mindgate",
      "reason": "mindgate",
      "started_at": 1715000000,
      "ended_at": 1715000042,
      "ok": true,
      "exit_code": 0,
      "cost_usd": 0.012,
      "tool_use_count": 5,
      "thinking_blocks": 3,
      "size_bytes": 12345
    }
  ]
}
```

- Sorted newest-first.
- `cost_usd` may be `null` when claude returns no cost (max plan / unsupported model). Renderer displays `-` for null/zero.
- `ok` is `false` when claude exited non-zero, OR when agent-runner crashed mid-wake (recovered lazily — see §5.5).
- `size_bytes` for budgeting rotation (§5.4).

### 5.3 Wake lifecycle (writer = agent-runner, sole owner)

Concurrency is already guaranteed by `agentLockPath` (`/eidos/run/agent.lock`, `agent_runner.go:38`) — only one agent-runner runs at a time, so the writer is single-threaded. No additional lock on transcripts/ is required.

Sequence per wake:

1. **Bootstrap**:
   - `os.MkdirAll(transcriptsDir, 0o700)` (idempotent).
   - Crash-recovery scan (§5.5).
2. **Open file**: `os.OpenFile(wakeFile, O_CREATE|O_EXCL|O_WRONLY, 0o600)`. On `EEXIST` (extremely unlikely — wake IDs are `<unix>-<reason>` and same-second same-reason collisions don't occur due to the wake-dir flock and coalescing) suffix `.dup-<pid>` and proceed.
3. **Atomic symlink swap**: `os.Symlink("wake-<id>.ndjson", current.tmp)` then `os.Rename(current.tmp, current)`. Atomic on the same directory; survives a partial agent-runner crash before claude starts.
4. **Stream tee**: tee claude.Stdout to wakeFile; in parallel run the lifecycle parser.
5. **On claude exit**:
   - Close wakeFile (flush).
   - Parse the just-written file to compute `tool_use_count`, `thinking_blocks`, `cost_usd` (final `result` event).
   - Append a row to `index.json` (read-modify-write via temp file + rename for atomicity).
   - Run rotation pass (§5.4).
   - Remove `current` symlink.

`current` is updated under the agent-lock, so concurrent readers (in-container `transcript-tail --wake current`) only ever see a consistent state: either pointing at the active wake, or absent.

### 5.4 Rotation

Two limits, applied after each wake's index update:

- `mindform.transcripts_max_count` (default 50)
- `mindform.transcripts_max_bytes` (default `100MiB`)

Algorithm:
1. While `len(wakes) > max_count`: drop oldest entry + `os.Remove` its ndjson file.
2. While `sum(size_bytes) > max_bytes`: same.

Configuration plumbed through `internal/config` as new optional keys in `[mindform]`. Reading fallback: missing keys → use defaults. No migration needed — additive.

### 5.5 Crash recovery (lazy)

On agent-runner startup, before bootstrapping a new wake:
1. Read `index.json` (empty list if absent).
2. List `wake-*.ndjson` files in transcripts/.
3. For any ndjson file NOT in index: synthesize an entry `{ok: false, exit_code: -1, ended_at: file.mtime, started_at: derived_from_filename or mtime, ...}` and add to index. Best-effort cost/tool counts (re-parse the orphan file).
4. If `current` symlink exists and points to a file already in `index.json`: stale link, `os.Remove(current)`.
5. If `current` points to a missing file: also `os.Remove`.

This is one cold scan per agent-runner invocation — typically <10 files, <1ms.

### 5.6 In-container readers

Two new hidden subcommands under `cmd/eidos/forge/`:

**`eidos forge transcript-list [--limit N] [--json]`**
- Default output is human-readable table (used for direct `docker exec` debugging).
- `--json` emits raw `index.json` content (used by `forge watch --list` host renderer).
- `--limit` truncates to the N most recent.

**`eidos forge transcript-tail --wake <id|current> [--follow]`**
- `--wake current`: readlink the symlink. If absent, exit 0 immediately (renderer treats as "no active wake").
- `--wake <id>`: open `wake-<id>.ndjson` directly. Exit 1 if not found.
- `--follow`: poll for file growth at 100ms intervals, write any new bytes to stdout. Exit when the wake is no longer current AND the file's last modification was >2s ago (clean-shutdown signal). Otherwise (`--no-follow`) cat the file and exit.
- Output is raw NDJSON bytes — no parsing, no decoration.

Polling rationale: inotify on docker volume bind mounts is brittle across docker versions and not reliable across overlayfs. 100ms poll keeps complexity minimal.

### 5.7 Test plan (PR-B1, storage portion)

- `cmd/eidos/forge/transcript_list_test.go`, `transcript_tail_test.go` — file fixtures + table-driven tests for both human and JSON output, follow loop, missing-file handling.
- `cmd/eidos/supervisor/agent_runner_test.go` — extend with: rotation triggered (write 51st wake → assert oldest removed), bytes rotation triggered, crash-recovery (drop a synthetic orphan file in tmpdir + run agent-runner → assert index.json gets a synthesized entry).

## 6. Renderer — `eidos forge watch <name>` (PR-B2)

### 6.1 Command surface

```
eidos forge watch <name>                    # default: follow current wake; if none, dump latest then wait
eidos forge watch <name> --wake <id>        # render specific past wake; default --no-follow
eidos forge watch <name> --list [--limit N] # list recent wakes
eidos forge watch <name> --thinking         # expand thinking blocks (default: collapsed summary)
eidos forge watch <name> --no-follow        # dump and exit (no waiting for new content)
eidos forge watch <name> --raw              # passthrough NDJSON (no decoration)
```

`--raw` is the unix-pipeline escape hatch for jq users.

### 6.2 Default behavior

```
forge watch <name>      # no flags
```

1. host calls `forge transcript-tail --wake current --follow` over `docker exec`.
2. If exit 0 with empty output (no current wake): host calls `forge transcript-list --json --limit 1`, dumps the latest wake, then loops back to step 1 with a 1s sleep between attempts. This yields the natural "show me the latest, then keep watching" behavior.
3. SIGINT (Ctrl-C): the `docker exec` is killed; `forge watch` exits cleanly.

### 6.3 Rendering

Style: lipgloss (already a dependency via `internal/firstcontact`). Color groups:

- `system` event → muted gray header line: `━━━ wake <id> (reason · started HH:MM:SS) ━━━` plus a model/tools/MCP summary line.
- `assistant.text` block → boxed in a solid border, white text.
- `assistant.thinking` block → dim, italic, boxed; **collapsed by default** to `╭─ thinking ───╮ (12 lines, --thinking to expand)`. With `--thinking`, full text rendered.
- `assistant.tool_use` block → blue accent box: `╭─ tool: <name> ──╮` followed by truncated input preview (one line with full input on `--verbose`, deferred).
- `user` block (tool_result) → green accent box: `╭─ result ──────╮` followed by content (truncated to 30 lines by default; `--full-results` deferred).
- `result` event → muted footer: `━━━ done · cost $X.XXX · Ns · M tools · <ok|failed> ━━━`.
- `stream_event` partials (when `--include-partial-messages`) → text deltas appended to the current open block in real time. Tool input deltas accumulate but only fully render at `content_block_stop`.

NO_COLOR / non-tty stdout: lipgloss auto-degrades. Verified by tests that run with `TERM=dumb`.

### 6.4 `--list` rendering

Columns: `ID  REASON  STARTED  DURATION  COST  STATUS`. Crashed wakes show `crashed` in red. ID is the truncated wake ID (first 8 chars suffice for human selection).

```
ID         REASON      STARTED              DUR    COST      STATUS
abc123ef   mindgate    2026-05-10 14:32:01  42s    $0.012    ok
def456ab   heartbeat   2026-05-10 12:00:00  18s    $0.005    ok
9876fedc   planned     2026-05-10 08:00:00  -      -         crashed
```

User invokes `forge watch <name> --wake <id-prefix>` with the prefix; host expands to full ID by querying `transcript-list --json` and matching prefix.

### 6.5 Test plan (PR-B2)

- `cmd/eidos/forge/watch_test.go` — golden tests: feed canned NDJSON fixtures (under `testdata/transcripts/`) through the renderer with and without `--thinking`, assert byte-for-byte stdout match.
- Fixture set: one nominal wake (system + 2 assistant turns + 1 tool round-trip + result), one with `--include-partial-messages` deltas, one with no `result` event (crashed mid-wake), one with no thinking blocks.
- `--list` rendering test with a canned `index.json`.
- Mock `docker exec` at the `forgectl.Client` interface — same approach as existing `forge status` tests.

## 7. Configuration additions

`internal/config` gains two optional keys under `[mindform]`:

```toml
[mindform]
transcripts_max_count = 50         # default
transcripts_max_bytes = "100MB"    # default; supports K/M/G suffixes
```

Both are read by agent-runner during rotation (§5.4). Missing keys → defaults. No migration / no breaking change to existing configs.

## 8. PR sequencing

| # | PR | Touches | Estimated size | Depends on |
|---|---|---|---|---|
| 1 | **PR-A** | `forge runtime-state` subcommand + `forge status` rendering + `forge list` column | small (~300 LOC) | none |
| 2 | **PR-B1** | `agent-runner` stream-json + transcripts/ + in-container readers + new `internal/transcript` shared package (event constants + minimal AST) + config + entrypoint chown | medium (~700 LOC) | none — can develop in parallel with PR-A |
| 3 | **PR-B2** | `forge watch` host renderer + golden tests + README + EXAMPLE | medium (~600 LOC) | PR-B1 (data shape) |

Recommended merge order: A → B1 → B2 (sequential), but B1 can be drafted in parallel with A's review.

Each PR follows the CLAUDE.md commit pipeline: code → codex review → docs update → push → CI watch → fix Copilot comments.

## 9. Out of scope

Explicitly deferred. Each item is named with the trigger that would unblock it.

- **Dashboard panel for transcripts.** Trigger: PR-B2 has merged AND at least one real-world use case asks for browser rendering. Implementation would promote `transcript-list/tail` to gate-daemon `methodTable` and add an SSE-fed tab to the dashboard.
- **Daemon IPC method `transcript.{list,tail}`.** Trigger: dashboard or future MCP server consumes the same data — at that point promote, per Single Call Path principle.
- **Time-travel / replay (re-run a wake from its captured prompt).** Independent feature; out of scope.
- **TUI split-pane viewer (Bubble Tea).** Decided against — user explicitly chose CLI streaming for v0.
- **Cross-mindform aggregation (`forge watch --all`).** No real consumer; YAGNI.
- **Editing / annotating past transcripts.** UX research first.
- **Structured stderr parsing.** Current stderr handling is sufficient.
- **Transcript export (`forge transcript export <id> > file.json`).** Easy to add later; not needed for the immediate debug workflow because `forge watch <name> --wake <id> --raw > file.ndjson` already works.

## 10. Risks & open questions

### 10.1 Claude version compatibility

Stream-json's event schema is documented but the doc tracks the latest claude version. Older versions may emit different field names or omit `--include-partial-messages`. Mitigation:
- Version probe at agent-runner start (§4.3).
- Fallback to plain `-p` mode if version is below minimum or unparseable.
- Spec leaves the minimum version as `TBD` — to be pinned by the implementing PR after validating the actual schema against `claude --version` on the maintainer's dev box. Default placeholder: `2.1.0`.

### 10.2 Large NDJSON lines

A `tool_result` from reading a 1MB file becomes a single ~1MB JSON line. `bufio.Scanner` defaults to a 64KB limit. Mitigation:
- Use `bufio.Reader.ReadString('\n')` (no upper limit aside from memory) in both the agent-runner tee and the host renderer parser.
- Or `bufio.Scanner.Buffer()` with an explicit 16MB cap. Pick whichever is cleaner at implementation time; both work.

### 10.3 Cost field

`result.total_cost_usd` may be `null` or `0` for max-plan / non-billable users. Spec rule: render as `-` in `--list` and the footer; do not treat zero as error. `index.json` stores `null` (JSON null) when absent.

### 10.4 Wake ID uniqueness

Current format is `<unix-seconds>-<reason>` (`forge/wake_internal.go:23`). Same-second same-reason collisions are theoretically possible but practically impossible thanks to the wake-dir flock + coalescing rules in `wake.Submit`. Mitigation in case of collision: agent-runner's `O_CREATE|O_EXCL` open detects EEXIST and suffixes `.dup-<pid>` (§5.3). Spec accepts this is a rare-edge corner.

### 10.5 Image / host version skew

Old image, new host: PR-A's `forge status` falls back gracefully. PR-B's `forge watch` displays "transcripts not available — image too old" if `transcript-tail` is not a known subcommand (detected via non-zero exit + a sentinel stderr substring).

New image, old host: NDJSON passthrough means the host parser sees events it doesn't render. Spec rule: unknown event types are silently passed through to stderr-as-JSON (visible only with `--raw` or `-v`). Forward compatibility is maintained.

### 10.6 Transcripts disk usage

100MB default × 50 wakes is a soft cap per mindform. On constrained hosts this could bite. Mitigation: configurable (§7), and the rotation pass runs on every wake. If a user sets aggressive caps the most likely failure mode is "the wake I was looking at got rotated" — acceptable.

### 10.7 Parser drift between agent-runner and `forge watch`

The lifecycle parser in agent-runner (§4.2) and the renderer in `forge watch` (§6.3) both decode NDJSON. They must stay aligned on event type names. Mitigation: extract a shared `internal/transcript` package with the event type constants and minimal AST. Both consumers depend on it. Spec creates this package as part of PR-B1.

## 11. Documentation updates

Per CLAUDE.md commit pipeline ("Documentation: After any code implementation, check and update the documentations"):

- **README.md**: status section updated with new phase taxonomy. New "Debugging a mind-form" subsection mentioning `forge watch`.
- **EXAMPLE.md**: in the existing walkthrough, replace the "look at docker logs to see the agent reply" step with `forge watch <name>`.
- **`forge logs` help text**: add a note that for the agent's reasoning chain, use `forge watch`.
- **Changelog**: PR-A adds a `feat(forge): show mindform phase` entry; PR-B1 adds `feat(forge): capture stream-json transcripts`; PR-B2 adds `feat(forge): forge watch — render mindform reasoning chain`.

## 12. Migration & backward compatibility

- New transcripts directory: created at next `forge create` (init-volume) automatically. Existing volumes get the directory created lazily by agent-runner on first wake post-upgrade.
- New config keys: all optional; missing → defaults. No config migration required.
- `forge logs` semantic shift: docker logs now contains lifecycle lines instead of raw claude text. README explicitly notes this; users wanting the old "see what claude said" UX should run `forge watch`.
- Old image / new host and vice versa: graceful fallbacks documented in §10.5.

## 13. Acceptance criteria

A milestone is shipping-ready when, on a fresh host:

1. `eidos forge status <name>` shows `phase: awake (mindgate)` while a mindgate-triggered wake is in flight, transitions to `phase: sleeping` on completion, and `phase: offline` after `forge stop`.
2. `eidos forge list` shows the same phase column.
3. `eidos forge watch <name>` shows, for an in-progress wake, the system header → live thinking (`--thinking`) / assistant text / tool calls / tool results, then the done footer.
4. `eidos forge watch <name> --list` shows the last N wakes with reason, time, duration, cost, status.
5. `eidos forge watch <name> --wake <id>` re-renders any past wake from the index.
6. After 51 wakes (or >100MB), the oldest is rotated out — `--list` shows only the surviving entries.
7. `docker logs <container>` shows clean per-wake lifecycle lines, no NDJSON noise.
8. With `claude --version` below the supported minimum, agent-runner emits the warning lifecycle line and falls back to plain `-p` mode without crashing.
