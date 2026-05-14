# Mind-form Tool Allowlist — Design

**Date:** 2026-05-14
**Status:** Approved (brainstorming)
**Scope:** `internal/claudeexec/` (new file), `internal/agentloop/spawn.go`, `cmd/eidos/supervisor/birth.go`, `internal/promptcapture/capture.go`, `docs/specs/SPEC.md`

## Goal

Enforce the SPEC's "only these tools are exposed to a mind-form" rule
on every claude subprocess the framework spawns. Today SPEC.md
§ Claude Code 调用设计 (lines 199-210) lists an explicit
whitelist, but none of the three claude spawn sites pass `--tools`,
so claude falls back to `default` and loads every built-in tool it
ships with. An empirical prompt-dump against the `alice` mind-form
(Claude Code 2.1.138) confirms 26 tools are exposed — including
operator-UX tools (`EnterPlanMode`, `EnterWorktree`), host-only tools
(`PushNotification`, `RemoteTrigger`), session-scoped duplicates of
the eidos scheduler (`CronCreate/CronDelete/CronList`), and the
no-applicable-surface `AskUserQuestion` / `NotebookEdit`.

## Non-Goals

- **MCP tool gating.** The `--tools` flag controls Claude Code's
  built-in tools only. MCP-server-provided tools (e.g. a future
  `eidos-mcp`) are wired through a separate registration channel
  (`mcpServers` in `settings.json` / `--mcp-config`) and are not
  in scope here. If we later want to restrict them, it lands as a
  separate design.
- **first-contact research call.** `internal/firstcontact/claude.go`
  invokes `claude -p ...` in one-shot text-mode to enrich a freshly-
  summoned mind-form's character profile. It is non-interactive
  (`--output-format json`, no tool loop), so the tool list does not
  influence its output. Out of scope.
- **Bumping `CLAUDE_CODE_VERSION`** from 2.1.138 → 2.1.141. The SPEC
  table is being rewritten against the 2.1.141 tool naming (the
  current target version on Anthropic's side), but the Dockerfile
  bump is a separate, independent PR — to keep the diff for this
  change reviewable and to avoid coupling a behaviour change (tool
  surface) with an image bump.
- **Permissions-mode tightening.** Mind-form claude runs with
  `--dangerously-skip-permissions`. That stays. We are reducing the
  tool *catalogue*, not adding permission gates.

## Current state (as built)

Three call sites spawn claude inside the mind-form container, and
none of them sets `--tools`:

- `internal/agentloop/spawn.go:128` — `buildClaudeArgs(SpawnOpts)`,
  the long-lived agent-loop subprocess.
- `cmd/eidos/supervisor/birth.go:131` — the one-shot `claude -p`
  birth-event invocation that writes the first journal entry.
- `internal/promptcapture/capture.go:137` — `buildClaudeArgs(Opts)`,
  the proxy-backed prompt-dump capture used by
  `eidos forge prompt-dump <name>`.

All three reach `claude` without `--tools`, so claude treats the
flag as absent → `default` → every built-in is loaded.

A fourth call site exists but is intentionally untouched:

- `internal/firstcontact/claude.go:40` — `args(prompt string)`,
  the first-contact research probe. It is `-p ... --output-format
  json`, no tool loop runs, so this design does not modify it.

## Design

### Source of truth

New file `internal/claudeexec/tools.go`:

```go
// Package claudeexec — tools.go: framework-level allowlist of the
// Claude Code built-in tools the mind-form is permitted to invoke.
//
// SPEC.md §"Claude Code 调用设计" is the prose authority; this slice
// is its executable form. Edit them together.
//
// Target Claude Code version: 2.1.141 (built-in tool naming).
package claudeexec

import "strings"

// Allowed is the canonical list of Claude Code built-in tool names
// the mind-form's claude subprocess is permitted to load. Order is
// stable for diffability of rendered argv.
var Allowed = []string{
    // file ops
    "Read", "Write", "Edit", "Glob", "Grep",
    // execution
    "Bash",
    // sub-agents and pacing
    "Agent", "ScheduleWakeup", "Monitor",
    // background task management
    "TaskOutput", "TaskStop",
    // turn-local todo list
    "TodoWrite",
    // long-term method invocation
    "Skill",
    // outward exploration
    "WebFetch", "WebSearch",
}

// ToolsArg returns the comma-separated value to pass to claude's
// --tools flag. Stable order, no spaces, ASCII-safe.
func ToolsArg() string {
    return strings.Join(Allowed, ",")
}
```

That's the entire surface. 15 tools. Single point of edit.

### Call-site changes

Each of the three spawn sites grows exactly one pair of
`append`-ed args:

```go
args = append(args, "--tools", claudeexec.ToolsArg())
```

- `internal/agentloop/spawn.go` `buildClaudeArgs` — append before
  the trailing `-p ""` placeholder, after `--include-partial-messages`.
- `cmd/eidos/supervisor/birth.go` — append before `args = append(args,
  "-p", userPrompt)`.
- `internal/promptcapture/capture.go` `buildClaudeArgs` — append
  before `args = append(args, "-p", opts.Prompt)`.

No new types, no config plumbing, no flag surface. The constant
is framework policy, not user-tunable: operators cannot loosen
the catalogue from `config.toml` or the CLI, mirroring how
`--dangerously-skip-permissions` is wired in unconditionally.

### SPEC.md update

`docs/specs/SPEC.md` lines 199-210 are rewritten so the prose
matches the slice. Concretely:

- Drop `LSP` (not a Claude Code built-in; it's a harness/plugin
  extension in some CC distributions, not the mind-form's CC).
- Drop `TaskCreate, TaskGet, TaskList, TaskUpdate` (those were the
  pre-2.1.x harness names; 2.1.x consolidated them into
  `TodoWrite`).
- Add `TodoWrite` under "turn 内待办".
- Add `WebSearch` under "外界探索" alongside `WebFetch`.
- Bracket the table with a sentence: "Tool names target Claude Code
  2.1.141. When bumping `docker/mindform/Dockerfile`'s
  `CLAUDE_CODE_VERSION`, re-validate against the upstream
  release notes and update `internal/claudeexec.Allowed` together
  with this table."

The dropped tools (`AskUserQuestion`, `Cron*`, `EnterPlanMode`/
`ExitPlanMode`, `EnterWorktree`/`ExitWorktree`, `NotebookEdit`,
`PushNotification`, `RemoteTrigger`) were never in SPEC's table;
they were simply leaking through because no `--tools` flag was
passed. SPEC.md does not need to enumerate them.

### Data flow

```
                       ┌─────────────────────────┐
                       │ internal/claudeexec     │
                       │   var Allowed []string  │
                       │   func ToolsArg() string│
                       └────────────┬────────────┘
                                    │ ToolsArg()
            ┌───────────────────────┼────────────────────────┐
            ▼                       ▼                        ▼
  agentloop/spawn.go      supervisor/birth.go     promptcapture/capture.go
  buildClaudeArgs         (inline args)           buildClaudeArgs
  "--tools" + arg         "--tools" + arg         "--tools" + arg
            │                       │                        │
            └───────────────────────┴────────────────────────┘
                                    ▼
                          /usr/local/bin/claude
                          (inside mind-form container)
```

### Error handling

- **Tool name unknown to claude.** If a `--tools` entry isn't a
  recognized built-in name in the running Claude Code version,
  claude logs a warning to stderr and proceeds with the recognized
  subset. We accept this — it surfaces as a normal stderr line in
  `agentloop`'s stderr capture and shows up in `eidos forge watch`
  metadata, but does not crash the mind-form. Re-validation is a
  manual step at Dockerfile version bump time (per the SPEC.md
  bracket sentence above).
- **Empty allowlist.** Guarded by code: `ToolsArg` returns the
  non-empty constant; an empty list would render `--tools ""` which
  in claude semantics disables all tools. We do not expose a knob
  that lets this happen. (A unit test asserts `len(Allowed) > 0`.)

### Testing

Unit:

- `internal/claudeexec/tools_test.go`:
  - `TestAllowed_NonEmpty` — sanity guard against the empty-list trap.
  - `TestToolsArg_StableOrder` — golden string assertion so a
    code-review diff catches accidental reordering or comma-spacing
    drift.
  - `TestAllowed_NoDuplicates` — guards against
    silently-double-listed names.
- `internal/agentloop/spawn_test.go` (extend
  `TestBuildClaudeArgs`) — assert the argv contains `--tools`
  immediately followed by `claudeexec.ToolsArg()`, positioned where
  the spec says.
- `internal/promptcapture/capture_test.go` (analogous extension).
- `cmd/eidos/supervisor/birth_test.go` if it exists, else this
  particular argv is covered by integration only.

Integration (build tag `integration`):

- New test `cmd/eidos/forge/prompt_dump_tool_allowlist_test.go`
  (or extend the existing `prompt_dump_incontainer_test.go`): spin
  up the prefab `_test_fixture` mind-form, run `prompt-dump`, parse
  the captured envelope, extract the `tools` array claude advertises
  to the model, and assert it equals `claudeexec.Allowed` modulo
  whatever set-difference noise the running claude version surfaces
  (warning lines). This is the end-to-end check that the wire-level
  request sent by claude to `/v1/messages` actually carries only the
  approved tools.

## Open questions

None expected at implementation time. The 15-tool list is closed
per this brainstorm; further additions/removals require a SPEC.md
edit and a follow-up design entry.

## Migration / rollout

Pre-production project — `internal/claudeexec.Allowed` lands and
takes effect on next mind-form spawn or restart. No feature flag,
no phased rollout, no migration code. Existing running mind-forms
pick up the tighter set on their next claude rotation (dream-end →
`--session-id` reroll) or on `eidos forge restart <name>`.
