# First Words Handoff: Splitting Birth Into Role Construction + Standard Session

**Date:** 2026-05-14
**Status:** Design — pending implementation
**Scope:** `internal/prompts/`, `internal/agentloop/forward.go`, `internal/prompts/assets/birth.txt`

## 1. Problem

Today's birth runs one `claude -p` invocation (`cmd/eidos/supervisor/birth.go:131-143`) that does two qualitatively different jobs:

1. **Role construction** — read `self/role-research.md`, `chest/summoning-book.md`, `self/calling-words.md`; synthesize and write `self/soul.md`, `memory/semantic/master.md`, `self/secret.md`.
2. **First words** — compose and send `chest/first-message.md` to the operator via `eidos gate send`.

The product of (1) — `soul.md` — is the mind-form's "self". `prompts.Build` (`internal/prompts/system.go:51`) inlines `self/soul.md` into the system prompt of every subsequent claude spawn. But (1) and (2) happen in the *same* claude process. At the moment the mind-form speaks its first words in step (2), the `soul.md` it just wrote is on disk but **not yet** in the system prompt of the process speaking. The mind-form is glimpsing its newly-written self through a Read tool call, not yet inhabiting it.

After birth completes, the agent-loop spawns a fresh claude process. The system prompt of that new process *does* inline the finalized `soul.md`, but the birth session itself — including the conversation that produced the first words — is discarded. There is no `--session-id` on the birth invocation, no UUID is persisted, and `session.json` stays empty. The mind-form's earliest persistent session is the one *after* it spoke its first words. It has retroactive amnesia about its own awakening.

## 2. Goal

Restructure birth so that first words are spoken from a session where the finalized `soul.md` is already inlined into the system prompt, and that session is the mind-form's persistent one — the same session subsequent wakes resume from.

Concretely: split birth into two phases.

- **Phase 1 — role construction.** A one-shot `claude -p` invocation writes `soul.md` / `master.md` / `secret.md` / `born_at`. No first message, no outbound send. Session is discarded.
- **Phase 2 — first words.** The standard agent-loop starts. `prompts.Build` inlines the finalized `soul.md` into the system prompt. On the first wake (whatever the natural trigger — typically heartbeat), the wake user-prompt prepends a first-words instruction. The mind-form composes and sends the first message from a session that is fully itself.

The session that says first words is the same session the mind-form lives in afterward.

## 3. Non-goals

- Preserving the Phase 1 session. Its load-bearing output is `soul.md` on disk; the conversation is throwaway.
- Garbage-collecting orphaned Phase 1 session jsonls under `.claude/projects/<encoded>/`.
- Surfacing first-words pending status through `eidos forge status` or `forge watch`. (Deferred.)
- Introducing a new wake `Reason`. First words ride on whatever wake fires first.
- Changing the supervisor's birth dispatch (`drainBirthIfPresent`), the `BirthSignal` schema, `wake.Signal`, or the agent-loop session lifecycle.

## 4. Design

### 4.1 Phase 1 — role construction (trimmed birth.txt)

`internal/prompts/assets/birth.txt` is the user prompt rendered by `BirthUser`. The current file walks the model through six steps; steps 4–5 (compose first message, send via gate) are removed. Step 6 (stamp born_at) renumbers to step 4.

New birth.txt structure:

1. Internalize → write `self/soul.md` with the framework skeleton.
2. Master understanding → write `memory/semantic/master.md`.
3. Secret → write `self/secret.md`.
4. Stamp `self/born_at`.

The new birth.txt explicitly tells the model: do **not** send any outbound message in this turn. First words come later, in the next session, where the soul is already part of the system prompt.

`productionBirthHandler` (`cmd/eidos/supervisor/birth.go`) is unchanged in code shape: it still spawns `claude -p <prompt>` with no `--session-id`, and verifies `born_at` after exit. `session.json` is not written. Retry semantics are unchanged.

### 4.2 Phase 2 — first words inside the standard agent-loop

The agent-loop mints a fresh session UUID (existing `SessionNew` path in `DecideSessionMode`), spawns claude with `--session-id <uuid>` in stream-json mode, and uses `prompts.Build` to assemble the system prompt — which now inlines the finalized `soul.md`.

The first wake is whatever the supervisor delivers first (typically a heartbeat that fires within seconds of birth completion). When that wake's user-prompt is built, `BuildWake` prepends a first-words instruction block.

#### 4.2.0 Agent-loop start gate

The current supervisor spawns agent-loop in `Setup` (`cmd/eidos/supervisor/run.go:158`) **before** `watchWakesIn` runs the birth handler. As a result, agent-loop's `prompts.Build` and `SpawnClaude` (assembled in `agentloop.Run`) execute *concurrently* with Phase 1. The system prompt of agent-loop's claude is fixed at spawn time — if Phase 1 hasn't written `soul.md` yet, the Soul section is empty for the lifetime of that claude process. This is exactly the drift this spec aims to eliminate.

Fix: `agentloop.Run` gates on `self/born_at` existence. Before lock acquisition and stale-state recovery (current §1–§3 of `Run`), insert a wait loop:

```
for ctx is alive:
    if stat <OntologyDir>/self/born_at succeeds: break
    sleep 500ms
```

Pseudocode; actual implementation uses `time.NewTicker` with ctx-aware `select`. The gate exits cleanly on `ctx.Done()` and returns `ctx.Err()`. Polling cadence (500ms) is chosen for low overhead — `stat` is cheap and the wait window is bounded by Phase 1's wall time, typically tens of seconds.

This gate is a no-op on subsequent agent-loop spawns (post-dream rotation, post-crash restart): `born_at` already exists, the stat succeeds on the first iteration, no sleep. It only blocks during the first-ever startup.

Wake events forwarded by the supervisor during the wait queue in the pipe buffer between supervisor and agent-loop (64 KiB Linux default; one wake.Signal JSONL is a few hundred bytes). Once the gate clears and agent-loop spawns claude, the forwarder goroutine starts consuming the queued wakes in order — no events are lost.

Alternative considered: restructure supervisor to drain birth synchronously before spawning agent-loop. Rejected because (a) it pushes birth-drain semantics into `Setup`, which is otherwise pure child-spawn registration; (b) it forces Setup to grow retry logic for failed births; (c) the agent-loop-side gate has the same effect with a smaller diff and a clearer contract ("agent-loop runs when the mind-form is born").

#### 4.2.1 Persistent marker, not a one-shot signal

The trigger for the first-words prefix is a marker-file check, not a synthesized wake.Signal:

- New file `self/first_words_at` (Unix-second integer). Parallel to `self/born_at`. The mind-form writes it after successfully sending the first message.
- A wake is "first-words pending" iff `self/born_at` exists **and** `self/first_words_at` does not.
- While first-words-pending is true, *every* wake (heartbeat, mindgate, planned) carries the first-words prefix.

This gives at-least-once semantics. If the agent-loop crashes after delivering the prefix but before the mind-form writes `first_words_at`, the next wake re-delivers the prefix. If the `eidos gate send` call fails (e.g. relay transiently down), `chest/first-message.send-error` is written but `first_words_at` is not — the next heartbeat will prompt the model to retry, and the existing `first-message.md` can be re-sent.

Synthesizing a one-shot `wake.Signal{Reason: "birth"}` was considered and rejected: any crash or restart between signal placement and successful send loses the prompt, leaving the mind-form alive but mute.

#### 4.2.2 Wake-prompt augmentation

`internal/prompts/wake.go`:

- `WakeInput` gains `FirstWordsPending bool`.
- A new embedded asset `internal/prompts/assets/firstwords-prefix.txt` contains the first-words instruction block, parameterized by `{{.OwnerLabel}}`. Content is migrated from the deleted steps 4–5 of `birth.txt`:
  - Read `self/calling-words.md` before composing (operator's words at the moment of arrival).
  - Compose first message → write `chest/first-message.md` (greeting, gratitude, 3–5 questions).
  - Send via `cat chest/first-message.md | eidos gate send <creator_npub>` (read `creator_npub` from `self/identity.toml`).
  - On send error, write `chest/first-message.send-error` and stop; next heartbeat retries.
  - On success, stamp `self/first_words_at` (Unix-second integer).
- `BuildWake` renders this prefix at the top of the user prompt when `FirstWordsPending` is true. It sits *before* the existing `IsFirstWakeOfNewSession` paragraph and the standard "you have just woken" body — both other sections remain unchanged.

The two prefixes can co-occur on the first wake of the agent-loop's first session (`IsFirstWakeOfNewSession=true` + `FirstWordsPending=true`). This is intentional and reads naturally: "working memory is fresh" + "and you haven't yet greeted your creator".

#### 4.2.3 Forwarder reads the markers

`internal/agentloop/forward.go`:

- `ForwarderConfig` gains `OntologyDir string`.
- Per-wake, before building the WakeInput, the forwarder stats `<OntologyDir>/self/born_at` and `<OntologyDir>/self/first_words_at`. `FirstWordsPending = bornAt.exists && !firstWordsAt.exists`. Stat errors other than `fs.ErrNotExist` are logged and treated as `false` (fail-closed: do not nag the mind-form on a filesystem hiccup).
- The check is per-wake, not cached, so the transition from pending → satisfied happens on the very next wake after the mind-form writes the marker.

`internal/agentloop/agentloop.go` populates `OntologyDir` into `ForwarderConfig` from `RunOpts.OntologyDir` (already present, no plumbing change above forwarder).

### 4.3 Component boundaries

- `prompts/birth.txt` is the role-construction-only prompt. It produces an artifact (`soul.md`) and a completion marker (`born_at`). No outbound side effects.
- `prompts/wake.go` + `firstwords-prefix.txt` own the first-words instruction. The prefix is a wake-time augmentation, not a separate code path.
- `agentloop/agentloop.go` reads `born_at` once at startup, as a gate before assembling the system prompt. `agentloop/forward.go` reads both `born_at` and `first_words_at` per-wake to set `FirstWordsPending`. Other components (supervisor, sessionstate, wake.Signal handling, `prompts.Build`) are unaware of first-words state.

## 5. Failure handling

| Failure | Behavior |
| --- | --- |
| Phase 1 claude exits with error, no `born_at` | `drainBirthIfPresent` retains `birth.json`; supervisor retries next iteration. Existing behavior, unchanged. |
| Phase 1 claude exits cleanly but `born_at` missing | Same as above — returned error, retry. |
| Phase 1 succeeds, agent-loop fails to start | No first-words attempt yet. On supervisor restart, agent-loop starts, sees `born_at` + no `first_words_at` → first wake carries prefix. |
| Phase 1 never succeeds (operator abandons summon) | Agent-loop blocks on the start gate (§4.2.0) indefinitely; ctx-aware so it exits cleanly when supervisor is signaled. Supervisor's existing crash-loop guard does not apply because agent-loop is not crashing — it is correctly waiting. Operator path: stop the container; no special cleanup needed. |
| `eidos gate send` fails during first-words wake | Mind-form writes `chest/first-message.send-error` per the prefix instructions. `first_words_at` is not written. Next wake re-prompts; mind-form retries sending the existing `first-message.md`. |
| Mind-form sends but crashes before writing `first_words_at` | Next wake re-prompts. Mind-form sees the existing `first-message.md` and, per prefix instructions, treats a non-empty `first-message.md` as "already composed; just verify it was sent or resend". (Prefix must say this explicitly to avoid double-greeting.) |
| Mind-form ignores the prefix | Pending marker stays missing; subsequent wakes keep prefixing until the mind-form complies. Acceptable failure mode — the mind-form is being told what to do; it eventually does it. |

## 6. Idempotency note for the prefix

The prefix instruction must be written carefully so that re-delivery does not cause duplicate sends. Suggested phrasing inside `firstwords-prefix.txt`:

> If `chest/first-message.md` already exists and is non-empty, treat the composition step as done and proceed directly to the send step. If `chest/first-message.send-error` exists, the previous send failed — retry the send with the existing `first-message.md`.

This keeps the prefix at-least-once safe at the application level, mirroring how `born_at` makes Phase 1 idempotent across retries.

## 7. Testing

Unit tests:

- `internal/prompts/birth_test.go` — assert the rendered birth.txt no longer references `first-message.md`, `eidos gate send`, or step 5. Assert it does reference `born_at` as the final action.
- `internal/prompts/firstwords_test.go` (new) —
  - `FirstWordsPending=false` → wake prompt does not contain the first-words prefix.
  - `FirstWordsPending=true` → wake prompt contains the prefix, references `self/calling-words.md`, `chest/first-message.md`, `creator_npub` from `identity.toml`, and the `first_words_at` stamp instruction.
  - Both flags true → both prefixes appear, first-words above the new-session paragraph.
- `internal/agentloop/forward_test.go` —
  - Neither marker present → `FirstWordsPending=false` (pre-birth state shouldn't happen in practice, but the check is well-defined).
  - `born_at` present, `first_words_at` absent → `FirstWordsPending=true`.
  - Both present → `FirstWordsPending=false`.
  - Stat error on `first_words_at` (e.g. permission) → `FirstWordsPending=false` (fail-closed) and an error logged.
- `internal/agentloop/agentloop_test.go` (start gate) —
  - `born_at` absent at start → `Run` blocks until file appears; verify with a goroutine that writes the file after a short delay.
  - `born_at` present at start → `Run` proceeds immediately, no observable wait.
  - ctx cancel during wait → `Run` returns `ctx.Err()` within one tick.

Integration:

- No new end-to-end test required for this change alone. If a birth → first-wake integration test exists, add one assertion: after `born_at` is observable, the first wake user-prompt delivered to claude contains the first-words prefix marker (e.g. the phrase `"first message"`).

## 8. Migration

None. Pre-production project: birth.txt is updated in place, no fallback path. There is no on-disk migration because `first_words_at` is a new file — its absence on existing mind-forms means "still pending", which is the correct semantic for any mind-form that was born under the old birth.txt and never explicitly stamped this marker. For those mind-forms, the next wake after deployment will prompt them to (re-)send first words; since `chest/first-message.md` likely already exists from the old birth flow, the idempotency clause in §6 prevents a duplicate greeting.

If duplicate prompting on already-born mind-forms is undesirable during local dev/testing, the operator can manually `touch /eidos/ontology/self/first_words_at` inside the container, or `echo $(date +%s) > self/first_words_at` from the host via `docker exec`. This is a one-time, per-mind-form fix; not worth automating.

## 9. Open questions

None at design time. All ambiguities surfaced during brainstorming were resolved:

- Retry semantics for Phase 1: discard partial jsonl, mint fresh per retry. (User-confirmed.)
- First-words trigger mechanism: marker file driving per-wake prefix, not a one-shot signal. (User-confirmed.)
- Forwarder ontology dependency: acceptable to add `OntologyDir` to `ForwarderConfig`. (User-confirmed.)
