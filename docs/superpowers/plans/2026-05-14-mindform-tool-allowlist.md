# Mind-form Tool Allowlist Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pass `--tools <comma-list>` to every claude subprocess the framework spawns so the mind-form's claude only loads the 15 built-in tools approved by `docs/specs/SPEC.md`, instead of the current full 26-tool default.

**Architecture:** New canonical constant `internal/claudeexec.Allowed []string` plus helper `ToolsArg() string` — single source of truth. Three claude spawn sites (`internal/agentloop/spawn.go`, `cmd/eidos/supervisor/birth.go`, `internal/promptcapture/capture.go`) each append one `--tools <ToolsArg()>` pair to their argv. SPEC.md table rewritten to match. Birth.go's inline argv is extracted into a small `buildBirthArgs` helper so it becomes unit-testable.

**Tech Stack:** Go 1.25, standard library only. Tests use the existing `internal/agentloop/spawn_test.go` and `internal/promptcapture/capture_test.go` patterns plus the existing `prompt_dump_incontainer_test.go` integration shape.

**Spec:** `docs/superpowers/specs/2026-05-14-mindform-tool-allowlist-design.md`

---

## File map

- **Create:**
  - `internal/claudeexec/tools.go` — `Allowed` slice + `ToolsArg()` helper.
  - `internal/claudeexec/tools_test.go` — unit tests for the constant.
- **Modify:**
  - `internal/agentloop/spawn.go` — append `--tools` in `buildClaudeArgs`.
  - `internal/agentloop/spawn_test.go` — extend `TestBuildClaudeArgs_StreamJSONContract` to assert tool arg.
  - `cmd/eidos/supervisor/birth.go` — extract `buildBirthArgs` helper, include `--tools`.
  - `cmd/eidos/supervisor/birth_test.go` — add `TestBuildBirthArgs` covering the tool arg and existing semantics.
  - `internal/promptcapture/capture.go` — append `--tools` in `buildClaudeArgs`.
  - `internal/promptcapture/capture_test.go` — add `TestBuildClaudeArgs` unit covering tool arg.
  - `cmd/eidos/forge/prompt_dump_incontainer_test.go` — extend the existing in-container test to assert envelope's `claude_args` carries `--tools` followed by `ToolsArg()`.
  - `docs/specs/SPEC.md` — rewrite the tools table at lines 199-210.

---

## Task 1: Canonical allowlist constant (TDD)

**Files:**
- Create: `internal/claudeexec/tools.go`
- Test:   `internal/claudeexec/tools_test.go`

This task introduces the single source of truth before any spawn site
imports it.

- [ ] **Step 1.1: Write the failing tests**

Create `internal/claudeexec/tools_test.go`:

```go
package claudeexec

import (
	"strings"
	"testing"
)

func TestAllowed_NonEmpty(t *testing.T) {
	if len(Allowed) == 0 {
		t.Fatal("claudeexec.Allowed must not be empty (would render --tools \"\" → disable all tools)")
	}
}

func TestAllowed_NoDuplicates(t *testing.T) {
	seen := make(map[string]struct{}, len(Allowed))
	for _, name := range Allowed {
		if _, dup := seen[name]; dup {
			t.Errorf("duplicate tool name %q in Allowed", name)
		}
		seen[name] = struct{}{}
	}
}

func TestToolsArg_GoldenString(t *testing.T) {
	// Golden value mirrors the SPEC.md §"Claude Code 调用设计" table.
	// If you change either side, change them together — the diff for
	// this line is the review gate.
	const want = "Read,Write,Edit,Glob,Grep,Bash,Agent,ScheduleWakeup,Monitor,TaskOutput,TaskStop,TodoWrite,Skill,WebFetch,WebSearch"
	if got := ToolsArg(); got != want {
		t.Errorf("ToolsArg mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestToolsArg_NoWhitespace(t *testing.T) {
	if strings.ContainsAny(ToolsArg(), " \t\n") {
		t.Errorf("ToolsArg must be whitespace-free, got %q", ToolsArg())
	}
}
```

- [ ] **Step 1.2: Run tests to verify they fail**

Run: `go test ./internal/claudeexec/...`
Expected: FAIL — `undefined: Allowed`, `undefined: ToolsArg`.

- [ ] **Step 1.3: Create the implementation**

Create `internal/claudeexec/tools.go`:

```go
// Package claudeexec — tools.go: framework-level allowlist of the
// Claude Code built-in tools the mind-form is permitted to invoke.
//
// docs/specs/SPEC.md §"Claude Code 调用设计" is the prose authority;
// this slice is its executable form. Edit them together.
//
// Target Claude Code version: 2.1.141. When bumping
// docker/mindform/Dockerfile's CLAUDE_CODE_VERSION, re-validate
// against the upstream release notes — tools added/removed/renamed
// upstream must be reflected here and in SPEC.md.
package claudeexec

import "strings"

// Allowed is the canonical list of Claude Code built-in tool names
// the mind-form's claude subprocess is permitted to load. Order is
// stable for diffability of rendered argv and golden tests.
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
// --tools flag. Stable order, no spaces.
func ToolsArg() string {
	return strings.Join(Allowed, ",")
}
```

- [ ] **Step 1.4: Run tests to verify they pass**

Run: `go test ./internal/claudeexec/...`
Expected: PASS — all four tests green.

- [ ] **Step 1.5: gofmt + vet**

Run: `gofmt -l internal/claudeexec/ && go vet ./internal/claudeexec/...`
Expected: no output from gofmt, no errors from vet.

- [ ] **Step 1.6: Commit**

```bash
git add internal/claudeexec/tools.go internal/claudeexec/tools_test.go
git commit -m "feat(claudeexec): canonical Allowed tools list for mind-form claude

Introduce internal/claudeexec/tools.go with the 15-tool whitelist
mirroring docs/specs/SPEC.md §\"Claude Code 调用设计\". Helper
ToolsArg() returns the comma-separated value spawn sites pass to
claude's --tools flag.

No spawn site consumes it yet — wired up in follow-up commits."
```

---

## Task 2: Wire `--tools` into the agent-loop spawn (TDD)

**Files:**
- Modify: `internal/agentloop/spawn.go:128-155` (`buildClaudeArgs`)
- Modify: `internal/agentloop/spawn_test.go:88-152` (`TestBuildClaudeArgs_StreamJSONContract`)

- [ ] **Step 2.1: Extend the existing tests to assert `--tools`**

Open `internal/agentloop/spawn_test.go`. Inside the `cases` slice in
`TestBuildClaudeArgs_StreamJSONContract` (line 89), add a new case
to every existing test case's `require` list by appending two new
entries `"--tools"` and `claudeexec.ToolsArg()` to the first case's
`require` slice, and add this dedicated case at the end:

```go
		{
			name: "tools whitelist is passed",
			opts: SpawnOpts{Mode: SessionNew, SessionUUID: "u", SystemPrompt: "x"},
			require: []string{
				"--tools", claudeexec.ToolsArg(),
			},
		},
```

Then add the import at the top of the file (the existing block):

```go
import (
	// ... existing imports ...
	"github.com/LucianoXu/eidopsyche/internal/claudeexec"
)
```

If `claudeexec` is already imported in the file, skip the import edit.

- [ ] **Step 2.2: Run the test to verify it fails**

Run: `go test ./internal/agentloop/ -run TestBuildClaudeArgs_StreamJSONContract -v`
Expected: FAIL on `tools whitelist is passed` — argv does not contain `--tools`.

- [ ] **Step 2.3: Update `buildClaudeArgs` in `spawn.go`**

Open `internal/agentloop/spawn.go`. Locate `buildClaudeArgs` (line 128).
Add an import for `claudeexec` to the existing import block:

```go
	"github.com/LucianoXu/eidopsyche/internal/claudeexec"
```

Replace the existing tail of `buildClaudeArgs` (lines 146-154) with:

```go
	args = append(args,
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--tools", claudeexec.ToolsArg(),
		"-p", "",
	)
	args = append(args, opts.ExtraArgs...)
	return args
```

The diff is two lines: a new `"--tools", claudeexec.ToolsArg(),`
between `--include-partial-messages` and `-p ""`.

- [ ] **Step 2.4: Run the tests to verify they pass**

Run: `go test ./internal/agentloop/ -run TestBuildClaudeArgs_StreamJSONContract -v`
Expected: PASS on all sub-tests.

- [ ] **Step 2.5: Run the full agent-loop package tests**

Run: `go test ./internal/agentloop/...`
Expected: PASS. (If `TestSpawnedClaude_*` regress, it means the stub
claude binary doesn't tolerate the new flag — investigate then.)

- [ ] **Step 2.6: gofmt + vet**

Run: `gofmt -l internal/agentloop/ && go vet ./internal/agentloop/...`
Expected: no output, no errors.

- [ ] **Step 2.7: Commit**

```bash
git add internal/agentloop/spawn.go internal/agentloop/spawn_test.go
git commit -m "feat(agentloop): pass --tools whitelist to long-lived claude

Tighten the agent-loop spawn so claude loads only the 15 tools in
internal/claudeexec.Allowed instead of the 2.1.x default 26."
```

---

## Task 3: Wire `--tools` into supervisor birth (TDD with refactor)

**Files:**
- Modify: `cmd/eidos/supervisor/birth.go:131-141` (extract helper)
- Modify: `cmd/eidos/supervisor/birth_test.go` (append a new test)

This task extracts the inline argv block into a `buildBirthArgs`
helper so the tool-list invariant is unit-testable.

- [ ] **Step 3.1: Write the failing test**

Append to `cmd/eidos/supervisor/birth_test.go` (end of file):

```go
func TestBuildBirthArgs(t *testing.T) {
	got := buildBirthArgs(birthArgsInput{
		SystemPrompt: "sys",
		Model:        "claude-opus-4-7",
		Effort:       "high",
		UserPrompt:   "boot",
	})
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"--system-prompt", "sys",
		"--dangerously-skip-permissions",
		"--model", "claude-opus-4-7",
		"--effort", "high",
		"--tools", claudeexec.ToolsArg(),
		"-p", "boot",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing required arg %q in: %s", want, joined)
		}
	}
}

func TestBuildBirthArgs_OmitsModelAndEffortWhenEmpty(t *testing.T) {
	got := buildBirthArgs(birthArgsInput{
		SystemPrompt: "sys",
		UserPrompt:   "boot",
	})
	joined := strings.Join(got, " ")
	for _, no := range []string{"--model", "--effort"} {
		if strings.Contains(joined, no) {
			t.Errorf("unexpected arg %q in: %s", no, joined)
		}
	}
	// --tools is independent of model/effort and must always be present.
	if !strings.Contains(joined, "--tools") {
		t.Errorf("--tools missing from: %s", joined)
	}
}
```

Add to the imports block at the top of `birth_test.go`:

```go
import (
	// ... existing ...
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/claudeexec"
)
```

If `strings` is already imported, keep that line as-is.

- [ ] **Step 3.2: Run the test to verify it fails**

Run: `go test ./cmd/eidos/supervisor/ -run TestBuildBirthArgs -v`
Expected: FAIL — `undefined: buildBirthArgs`, `undefined: birthArgsInput`.

- [ ] **Step 3.3: Extract the helper in `birth.go`**

Open `cmd/eidos/supervisor/birth.go`. Add `claudeexec` to the import
block:

```go
	"github.com/LucianoXu/eidopsyche/internal/claudeexec"
```

Above the function that currently contains the inline args block
(the one with `args := []string{...}` near line 131), add a new
helper at file scope:

```go
// birthArgsInput is the parameter packet for buildBirthArgs. It
// exists so the argv assembly is independently unit-testable.
type birthArgsInput struct {
	SystemPrompt string
	Model        string
	Effort       string
	UserPrompt   string
}

// buildBirthArgs assembles the argv for the one-shot birth claude.
// The --tools value is sourced from internal/claudeexec so all
// mind-form claude subprocesses share the same allowlist.
func buildBirthArgs(in birthArgsInput) []string {
	args := []string{
		"--system-prompt", in.SystemPrompt,
		"--dangerously-skip-permissions",
	}
	if in.Model != "" {
		args = append(args, "--model", in.Model)
	}
	if in.Effort != "" {
		args = append(args, "--effort", in.Effort)
	}
	args = append(args, "--tools", claudeexec.ToolsArg())
	args = append(args, "-p", in.UserPrompt)
	return args
}
```

Then replace the inline block (the existing `args := []string{ ... }`
through `args = append(args, "-p", userPrompt)`) with:

```go
	args := buildBirthArgs(birthArgsInput{
		SystemPrompt: systemPrompt,
		Model:        facts.Model,
		Effort:       facts.Effort,
		UserPrompt:   userPrompt,
	})
```

- [ ] **Step 3.4: Run the new test to verify it passes**

Run: `go test ./cmd/eidos/supervisor/ -run TestBuildBirthArgs -v`
Expected: PASS on both subcases.

- [ ] **Step 3.5: Run the full supervisor tests**

Run: `go test ./cmd/eidos/supervisor/...`
Expected: PASS. The birth-handler tests use mocks, so the refactor
should not affect them — but the run confirms.

- [ ] **Step 3.6: gofmt + vet**

Run: `gofmt -l cmd/eidos/supervisor/ && go vet ./cmd/eidos/supervisor/...`
Expected: no output, no errors.

- [ ] **Step 3.7: Commit**

```bash
git add cmd/eidos/supervisor/birth.go cmd/eidos/supervisor/birth_test.go
git commit -m "feat(supervisor): pass --tools whitelist to birth claude

Extract the inline argv assembly into buildBirthArgs so the
--tools invariant from internal/claudeexec is unit-testable. The
birth claude is now subject to the same 15-tool whitelist as the
long-lived agent loop."
```

---

## Task 4: Wire `--tools` into prompt-dump capture (TDD)

**Files:**
- Modify: `internal/promptcapture/capture.go:136-152` (`buildClaudeArgs`)
- Modify: `internal/promptcapture/capture_test.go` (add unit test)

- [ ] **Step 4.1: Write the failing test**

Append to `internal/promptcapture/capture_test.go`:

```go
func TestBuildClaudeArgs_IncludesToolsWhitelist(t *testing.T) {
	args := buildClaudeArgs(Opts{
		SystemPrompt: "sys",
		Model:        "claude-opus-4-7",
		Effort:       "high",
		Prompt:       "ping",
	})
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--system-prompt", "sys",
		"--model", "claude-opus-4-7",
		"--effort", "high",
		"--dangerously-skip-permissions",
		"--tools", claudeexec.ToolsArg(),
		"-p", "ping",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing required arg %q in: %s", want, joined)
		}
	}
}

func TestBuildClaudeArgs_BareOmitsSystemPrompt(t *testing.T) {
	args := buildClaudeArgs(Opts{Prompt: "ping"})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--system-prompt") {
		t.Errorf("--system-prompt leaked in bare mode: %s", joined)
	}
	if !strings.Contains(joined, "--tools") {
		t.Errorf("--tools must be present even in bare mode: %s", joined)
	}
}
```

Add to the imports at the top of `capture_test.go`:

```go
import (
	// ... existing ...
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/claudeexec"
)
```

If `strings` is already imported, leave the existing line.

- [ ] **Step 4.2: Run the test to verify it fails**

Run: `go test ./internal/promptcapture/ -run TestBuildClaudeArgs -v`
Expected: FAIL — `--tools` substring not in argv.

- [ ] **Step 4.3: Update `buildClaudeArgs` in `capture.go`**

Open `internal/promptcapture/capture.go`. Add `claudeexec` to the
import block:

```go
	"github.com/LucianoXu/eidopsyche/internal/claudeexec"
```

Replace `buildClaudeArgs` (lines 137-152) with:

```go
// buildClaudeArgs constructs the argv slice for the spawned process.
func buildClaudeArgs(opts Opts) []string {
	var args []string
	if opts.SystemPrompt != "" {
		args = append(args, "--system-prompt", opts.SystemPrompt)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}
	args = append(args, "--dangerously-skip-permissions")
	args = append(args, "--tools", claudeexec.ToolsArg())
	args = append(args, opts.ExtraArgs...)
	args = append(args, "-p", opts.Prompt)
	return args
}
```

The diff is one new line: `args = append(args, "--tools", claudeexec.ToolsArg())`
positioned after `--dangerously-skip-permissions`.

- [ ] **Step 4.4: Run the tests to verify they pass**

Run: `go test ./internal/promptcapture/ -run TestBuildClaudeArgs -v`
Expected: PASS on both subcases.

- [ ] **Step 4.5: Run the full promptcapture tests**

Run: `go test ./internal/promptcapture/...`
Expected: PASS. The stub-claude–driven tests don't care about
the extra flag.

- [ ] **Step 4.6: gofmt + vet**

Run: `gofmt -l internal/promptcapture/ && go vet ./internal/promptcapture/...`
Expected: no output, no errors.

- [ ] **Step 4.7: Commit**

```bash
git add internal/promptcapture/capture.go internal/promptcapture/capture_test.go
git commit -m "feat(promptcapture): pass --tools whitelist to dumped claude

prompt-dump now snapshots the same tool catalogue the running
agent-loop sees, instead of the full 2.1.x default."
```

---

## Task 5: Integration check — envelope reflects the whitelist

**Files:**
- Modify: `cmd/eidos/forge/prompt_dump_incontainer_test.go` (extend
  `TestRunPromptDumpInContainer_PopulatesCapturedFrom`)

The envelope's `meta.claude_args` already captures the argv we passed
to claude (`capture.go:129`). We assert it carries `--tools` followed
by `ToolsArg()` so the wire-level argv invariant is exercised
end-to-end through the same code path the operator hits.

- [ ] **Step 5.1: Extend the existing in-container test**

Open `cmd/eidos/forge/prompt_dump_incontainer_test.go`.
`TestRunPromptDumpInContainer_PopulatesCapturedFrom` already
decodes the envelope into `var env map[string]any` and reads
`env["captured_from"]`. The envelope JSON also has a top-level
`claude_args` array (see `internal/promptcapture/envelope.go:54`).
We assert that array carries `--tools` immediately followed by
`claudeexec.ToolsArg()`.

After the existing `env["identity_path"]` block (the last assertion
inside the test, around the end of the test body), append:

```go
	args, _ := env["claude_args"].([]any)
	var sawTools bool
	for i, a := range args {
		if s, _ := a.(string); s == "--tools" && i+1 < len(args) {
			next, _ := args[i+1].(string)
			if next == claudeexec.ToolsArg() {
				sawTools = true
				break
			}
		}
	}
	if !sawTools {
		t.Errorf("claude_args must contain --tools %s, got %v",
			claudeexec.ToolsArg(), args)
	}
```

Add to the existing import block at the top of the file:

```go
	"github.com/LucianoXu/eidopsyche/internal/claudeexec"
```

`encoding/json`, `bytes`, and the rest are already imported.

- [ ] **Step 5.2: Run the test to verify it passes**

Run: `go test ./cmd/eidos/forge/ -run TestRunPromptDumpInContainer -v`
Expected: PASS — the argv recorded in the envelope now carries
`--tools <ToolsArg()>` since Task 4 added it.

- [ ] **Step 5.3: gofmt + vet**

Run: `gofmt -l cmd/eidos/forge/ && go vet ./cmd/eidos/forge/...`
Expected: no output, no errors.

- [ ] **Step 5.4: Commit**

```bash
git add cmd/eidos/forge/prompt_dump_incontainer_test.go
git commit -m "test(forge): assert prompt-dump envelope records --tools whitelist

End-to-end pin so a regression in promptcapture's argv assembly
fails this integration test, not just the unit test."
```

---

## Task 6: SPEC.md — align prose with code

**Files:**
- Modify: `docs/specs/SPEC.md:199-211` (tool table + surrounding sentence)

- [ ] **Step 6.1: Open the file and locate the block**

Run: `sed -n '195,215p' docs/specs/SPEC.md`
Expected: shows the table currently listing `Read, Write, Edit,
Glob, Grep`, `Bash`, `Agent, ScheduleWakeup, Monitor`, `TaskOutput,
TaskStop`, `TaskCreate, TaskGet, TaskList, TaskUpdate`, `Skill`,
`WebFetch`, `LSP`.

- [ ] **Step 6.2: Rewrite the table**

Replace lines 199-210 (the bullet introducing the table and the
table body) with:

```markdown
- 工具方面，我们只保留下列工具，通过 `<tools>` 传入（Claude Code 2.1.141 命名口径，与 `internal/claudeexec.Allowed` 保持一致）：

  | 类别 | 工具 |
  |---|---|
  | 文件操作 | `Read`, `Write`, `Edit`, `Glob`, `Grep` |
  | 执行 | `Bash` |
  | 子代理与节奏 | `Agent`, `ScheduleWakeup`, `Monitor` |
  | 后台任务管理 | `TaskOutput`, `TaskStop` |
  | turn 内待办 | `TodoWrite` |
  | 长期方法 | `Skill` |
  | 外界探索 | `WebFetch`, `WebSearch` |

  当 `docker/mindform/Dockerfile` 的 `CLAUDE_CODE_VERSION` 升级时，需对照 upstream release notes 校验本表，并与 `internal/claudeexec.Allowed` 同步更新。
```

- [ ] **Step 6.3: Verify the diff**

Run: `git diff docs/specs/SPEC.md`
Expected: shows `LSP` removed; `TaskCreate, TaskGet, TaskList, TaskUpdate` row removed; `TodoWrite` row added under "turn 内待办"; `WebSearch` added next to `WebFetch`; trailing version-coupling sentence added.

- [ ] **Step 6.4: Commit**

```bash
git add docs/specs/SPEC.md
git commit -m "docs(spec): align mind-form tool whitelist with claude 2.1.141

- Drop LSP (not a built-in tool in current Claude Code).
- Drop TaskCreate/TaskGet/TaskList/TaskUpdate (consolidated upstream into TodoWrite).
- Add TodoWrite, WebSearch.
- Cross-reference internal/claudeexec.Allowed as the executable form."
```

---

## Task 7: Cross-package verification

**Files:** none modified — purely verification.

- [ ] **Step 7.1: Run gofmt across the project**

Run: `gofmt -l .`
Expected: no output.

- [ ] **Step 7.2: Run go vet across the workspace**

Run: `go vet ./...`
Expected: no errors.

- [ ] **Step 7.3: Run the full unit-test suite**

Run: `go test ./...`
Expected: PASS. Watch for any test in `internal/agentloop`,
`internal/promptcapture`, `cmd/eidos/supervisor`, or
`cmd/eidos/forge` that was implicitly relying on the absence of
`--tools` (none expected, but the full run confirms).

- [ ] **Step 7.4: Re-dump alice (manual smoke)**

```bash
go build -o bin/eidos ./cmd/eidos
# rebuild the mindform image so /usr/local/bin/eidos inside the
# container matches; the in-container prompt-dump uses the
# container's eidos binary.
make image IMAGE_TAG=dev
./bin/eidos forge restart alice
./bin/eidos forge prompt-dump alice -o /tmp/alice-after
```

Expected: `/tmp/alice-after.md` shows a `## Tools` section listing
exactly the 15 tools from `internal/claudeexec.Allowed` (no
`EnterPlanMode`, no `Cron*`, no `RemoteTrigger`, no `NotebookEdit`,
no `WebSearch`-vs-`WebFetch` confusion, etc.).

If the dumped tool list still contains 26 tools, the running
container is using a stale `eidos` binary inside — re-run
`make image IMAGE_TAG=dev` and `eidos forge upgrade alice` (or
restart from a freshly built image) before re-dumping.

- [ ] **Step 7.5: No commit (verification-only task)**

If any verification step fails, return to the relevant earlier task,
fix, and re-run from Step 7.1.

---

## Out of scope (do not include in this PR)

- Bumping `docker/mindform/Dockerfile`'s `CLAUDE_CODE_VERSION` from
  `2.1.138` to `2.1.141`. Tracked separately. The allowlist still
  applies under 2.1.138 — claude logs a warning on unknown names
  (e.g. if a name was added in 2.1.141 only) but proceeds with the
  recognized subset.
- Restricting MCP server tools (`eidos-mcp` or others). `--tools`
  governs built-ins only.
- Adding `--tools` to `internal/firstcontact/claude.go`. That call
  is `-p ... --output-format json` non-interactive; no tool loop
  runs, so the catalogue does not influence output.
