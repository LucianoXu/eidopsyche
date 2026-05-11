# `promptdump` Markdown Full Coverage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `utils/promptdump`'s `renderMarkdown` so every top-level field in the captured envelope and every top-level field inside `request` is reachable from the `.md` output without expanding `<details>` blocks. Three additions: `claude_path` line in metadata, "Other request fields" subsection, per-tool subsections replacing the single "Full tool schemas" `<details>` block.

**Architecture:** Three small extensions to the existing `renderMarkdown` function in `utils/promptdump/main.go`, plus two new helpers (`renderOtherRequestFields`, `renderToolDetail`). No structural file changes; no new dependencies. Each change is TDD-driven by a focused new test.

**Tech Stack:** Go 1.25 stdlib only — same as parent module.

**Spec:** `docs/superpowers/specs/2026-05-11-promptdump-md-full-coverage-design.md`
**Parent feature:** PR #66 (`feat/promptdump-md`), which lands the initial `renderMarkdown` + extension-driven `-o`. This plan assumes #66 is merged into `main` before T1 runs; T1 verifies that explicitly.

> **Note on `go` invocations:** `utils/promptdump` is intentionally outside the root `go.work`, so every `go build` / `go test` / `go run` below assumes `GOWORK=off`. From inside `utils/promptdump/`: `GOWORK=off go test ./...`. From the repo root: `GOWORK=off go -C utils/promptdump test ./...`.

---

## File Structure

| Path | Purpose | Touched by |
|---|---|---|
| `utils/promptdump/main.go` | Add `claude_path` to metadata block; add `renderOtherRequestFields` helper + call site; replace single `<details>` schemas block with per-tool subsections via new `renderToolDetail` helper. | Tasks 2, 3, 4 |
| `utils/promptdump/main_test.go` | New `TestRenderMarkdown_ClaudePath`, `TestRenderMarkdown_OtherRequestFields`, `TestRenderMarkdown_NoOtherFields`, `TestRenderMarkdown_PerToolSections`; existing `TestRenderMarkdown_HappyPath` updated to assert the per-tool `<details>` form rather than the now-removed combined block. | Tasks 2, 3, 4 |
| `utils/promptdump/README.md` | One-paragraph update to "Reading the output" reflecting per-tool schema folding + "Other request fields" coverage. | Task 5 |
| `docs/superpowers/specs/2026-05-11-promptdump-md-full-coverage-design.md` | Already written in main repo working tree (uncommitted at plan time); carried into the feature branch via T1. | Task 1 |
| `docs/superpowers/plans/2026-05-11-promptdump-md-full-coverage.md` | This plan; same — carried via T1. | Task 1 |

---

## Task 1: Verify base + set up worktree + carry spec/plan

**Files:**
- Create: `.claude/worktrees/feat-promptdump-md-full/` (worktree dir)
- Move: `docs/superpowers/specs/2026-05-11-promptdump-md-full-coverage-design.md` (already in main repo working tree, uncommitted)
- Move: `docs/superpowers/plans/2026-05-11-promptdump-md-full-coverage.md` (this file, same)

- [ ] **Step 1: Verify PR #66 is merged into main**

```bash
gh pr view 66 --json state,mergedAt
git -C /data/eidopsyche fetch origin
git -C /data/eidopsyche log --oneline origin/main | head -5 | grep -q "promptdump-md\|markdown side-output"
```

Expected: PR #66 state is `MERGED`, and the merge commit appears in `origin/main` log. If not merged yet, **stop and prompt the user** — they may want to merge #66 first to keep the diff clean. Alternative path: branch off `feat/promptdump-md` and target that branch in the new PR (proper PR stack); only choose if the user explicitly asks.

- [ ] **Step 2: Create the worktree from current origin/main**

```bash
git -C /data/eidopsyche fetch origin
git -C /data/eidopsyche worktree add -b feat/promptdump-md-full /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full origin/main
git -C /data/eidopsyche worktree list
```

Expected: "Preparing worktree (new branch 'feat/promptdump-md-full')". The new worktree's HEAD must be the latest origin/main — confirm with `git -C <worktree> log --oneline -1` that the message references the PR #66 merge.

- [ ] **Step 3: Carry uncommitted spec + plan into the worktree**

```bash
cp /data/eidopsyche/docs/superpowers/specs/2026-05-11-promptdump-md-full-coverage-design.md \
   /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/docs/superpowers/specs/

cp /data/eidopsyche/docs/superpowers/plans/2026-05-11-promptdump-md-full-coverage.md \
   /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/docs/superpowers/plans/

rm /data/eidopsyche/docs/superpowers/specs/2026-05-11-promptdump-md-full-coverage-design.md
rm /data/eidopsyche/docs/superpowers/plans/2026-05-11-promptdump-md-full-coverage.md
```

- [ ] **Step 4: Commit spec + plan as the first commit**

```bash
cd /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full
git add docs/superpowers/specs/2026-05-11-promptdump-md-full-coverage-design.md \
        docs/superpowers/plans/2026-05-11-promptdump-md-full-coverage.md
git commit -m "$(cat <<'EOF'
docs(promptdump): spec and plan for full markdown coverage

Extension of the markdown side-output (PR #66) so every JSON top-level
field and every request top-level field is reachable in the .md
output without expanding <details> blocks. Adds claude_path metadata,
"Other request fields" subsection, and per-tool subsections.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Verify `git status` is clean.

**All subsequent tasks run inside `/data/eidopsyche/.claude/worktrees/feat-promptdump-md-full`.**

---

## Task 2: `claude_path` in metadata (TDD)

**Files:**
- Modify: `utils/promptdump/main.go`
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Add the failing test**

Append to `main_test.go`:

```go
// TestRenderMarkdown_ClaudePath: claude_path must appear in the
// metadata block. Regression guard for the original Markdown
// renderer, which omitted this field entirely.
func TestRenderMarkdown_ClaudePath(t *testing.T) {
	env := map[string]any{
		"claude_path":    "/usr/bin/claude",
		"claude_version": "2.1.139",
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	if !strings.Contains(out, "**Claude path:** `/usr/bin/claude`") {
		t.Errorf("missing claude_path line in metadata:\n%s", out)
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump test -run TestRenderMarkdown_ClaudePath -v
```

Expected: `--- FAIL: TestRenderMarkdown_ClaudePath` with "missing claude_path line in metadata".

- [ ] **Step 3: Add the metadata line**

In `main.go`, find the metadata block at the top of `renderMarkdown` and insert the `claude_path` line between `claude_version` and `claude_args`:

```go
	if v, ok := env["claude_version"].(string); ok {
		fmt.Fprintf(&b, "**Claude version:** %s\n", v)
	}
	if v, ok := env["claude_path"].(string); ok && v != "" {
		fmt.Fprintf(&b, "**Claude path:** `%s`\n", v)
	}
	if v, ok := env["claude_args"].([]any); ok {
		fmt.Fprintf(&b, "**Claude args:** `%s`\n", joinArgs(v))
	}
```

The `&& v != ""` guard means an envelope without `claude_path` (e.g., older captures) doesn't emit an empty line.

- [ ] **Step 4: Run — expect PASS**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump test -short -v
```

Expected: all existing tests still pass, and `TestRenderMarkdown_ClaudePath` passes.

- [ ] **Step 5: gofmt + vet**

```bash
gofmt -l /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump vet ./...
```

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full add utils/promptdump/main.go utils/promptdump/main_test.go
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full commit -m "$(cat <<'EOF'
feat(promptdump): render claude_path in markdown metadata

claude_path was captured in the JSON envelope but silently omitted
from the Markdown rendering. Adds a metadata line between Claude
version and Claude args; empty/absent values are skipped so old
envelopes still render cleanly.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: "Other request fields" subsection (TDD)

Three tests cover the behavior: happy path with both scalar and complex values, deterministic alphabetical key ordering, and the skip-if-empty rule.

**Files:**
- Modify: `utils/promptdump/main.go`
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `main_test.go`:

```go
// TestRenderMarkdown_OtherRequestFields: keys in request not covered
// by the dedicated sections must surface under "Other request fields".
// Scalars render as bullets; complex values render as fenced json.
// Keys are sorted alphabetically for deterministic output.
func TestRenderMarkdown_OtherRequestFields(t *testing.T) {
	env := map[string]any{
		"request": map[string]any{
			"model":          "claude-opus-4-7",
			"temperature":    1.0,
			"anthropic_beta": "prompt-caching-2024-07-31",
			"metadata": map[string]any{
				"user_id": "u1",
			},
		},
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	if !strings.Contains(out, "### Other request fields") {
		t.Error("missing Other request fields heading")
	}
	if !strings.Contains(out, "- **anthropic_beta:** `prompt-caching-2024-07-31`") {
		t.Error("anthropic_beta bullet missing or malformed")
	}
	if !strings.Contains(out, "- **temperature:** 1") {
		t.Error("temperature bullet missing or malformed (expected integer-shaped 1)")
	}
	if !strings.Contains(out, "- **metadata:**") {
		t.Error("metadata bullet header missing")
	}
	if !strings.Contains(out, `"user_id": "u1"`) {
		t.Error("metadata fenced JSON body missing")
	}
	// Alphabetical ordering: anthropic_beta < metadata < temperature.
	ab := strings.Index(out, "anthropic_beta")
	md := strings.Index(out, "metadata:")
	tp := strings.Index(out, "temperature")
	if !(ab < md && md < tp) {
		t.Errorf("keys not alphabetically ordered: ab=%d md=%d tp=%d", ab, md, tp)
	}
}

// TestRenderMarkdown_NoOtherFields: a request with only the well-known
// fields must NOT emit an "Other request fields" heading.
func TestRenderMarkdown_NoOtherFields(t *testing.T) {
	env := map[string]any{
		"request": map[string]any{
			"model":      "claude-opus-4-7",
			"max_tokens": float64(32000),
			"stream":     true,
			"system":     []any{},
			"tools":      []any{},
			"messages":   []any{},
		},
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	if strings.Contains(out, "### Other request fields") {
		t.Error("Other request fields heading rendered despite no extra keys")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump test -run "TestRenderMarkdown_OtherRequestFields|TestRenderMarkdown_NoOtherFields" -v
```

Expected: `TestRenderMarkdown_OtherRequestFields` fails (heading missing); `TestRenderMarkdown_NoOtherFields` may pass (heading naturally absent), but that's incidental.

- [ ] **Step 3: Implement `renderOtherRequestFields`**

Add to `main.go` immediately after the existing `firstNonEmptyLine` helper:

```go
// renderOtherRequestFields appends an "### Other request fields"
// subsection enumerating every key in req that is not already
// covered by a dedicated section (model/max_tokens/stream as bullets;
// system/tools/messages as their own sections). Skipped entirely if
// no such key exists. Keys are sorted alphabetically for deterministic
// output.
//
// Scalar values render as `- **key:** value` (strings backticked).
// Complex values (objects, arrays) render as `- **key:**` followed
// by a fenced `json` block.
func renderOtherRequestFields(b *strings.Builder, req map[string]any) {
	covered := map[string]struct{}{
		"system": {}, "tools": {}, "messages": {},
		"model": {}, "max_tokens": {}, "stream": {},
	}
	var keys []string
	for k := range req {
		if _, ok := covered[k]; ok {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)

	b.WriteString("### Other request fields\n\n")
	for _, k := range keys {
		v := req[k]
		switch vv := v.(type) {
		case string:
			fmt.Fprintf(b, "- **%s:** `%s`\n", k, vv)
		case bool, float64, nil:
			fmt.Fprintf(b, "- **%s:** %v\n", k, vv)
		default:
			fmt.Fprintf(b, "- **%s:**\n  ```json\n", k)
			j, _ := json.MarshalIndent(vv, "  ", "  ")
			b.Write(j)
			b.WriteString("\n  ```\n")
		}
	}
	b.WriteString("\n")
}
```

Add `"sort"` to the import list.

- [ ] **Step 4: Wire `renderOtherRequestFields` into `renderMarkdown`**

In `main.go`, find the section in `renderMarkdown` that emits the `Model / Max tokens / Stream` bullets:

```go
	if v, ok := req["stream"].(bool); ok {
		fmt.Fprintf(&b, "- **Stream:** %v\n", v)
	}
	b.WriteString("\n")
```

Insert a call right after the `b.WriteString("\n")`:

```go
	renderOtherRequestFields(&b, req)
```

- [ ] **Step 5: Run — expect PASS**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump test -run "TestRenderMarkdown_OtherRequestFields|TestRenderMarkdown_NoOtherFields" -v
```

Expected: both tests pass.

- [ ] **Step 6: Run full short suite to confirm no regressions**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump test -short
```

Expected: PASS.

- [ ] **Step 7: gofmt + vet**

```bash
gofmt -l /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump vet ./...
```

- [ ] **Step 8: Commit**

```bash
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full add utils/promptdump/main.go utils/promptdump/main_test.go
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full commit -m "$(cat <<'EOF'
feat(promptdump): render request keys outside the well-known set

renderOtherRequestFields walks request and emits any key not already
covered by Model/Max tokens/Stream/System/Tools/Messages. Scalars
render as bullets; complex values land in a fenced json block. Keys
are alphabetically sorted for deterministic diff. The subsection
heading appears only when there's at least one extra key, so vanilla
captures stay unchanged.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Per-tool subsections (TDD)

The single combined `<details><summary>Full tool schemas</summary>` block is replaced by per-tool subsections (one `### `<name>`` heading per tool, with full description in a fenced block and `input_schema` in its own per-tool `<details>` block). The top bullet index stays.

**Files:**
- Modify: `utils/promptdump/main.go`
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Add the failing test**

Append to `main_test.go`:

```go
// TestRenderMarkdown_PerToolSections: each tool gets a "### `<name>`"
// subsection with the full multi-line description in a fenced block
// and the input_schema in a per-tool <details> block. The old single
// "Full tool schemas" combined block must be gone.
func TestRenderMarkdown_PerToolSections(t *testing.T) {
	env := map[string]any{
		"request": map[string]any{
			"tools": []any{
				map[string]any{
					"name":        "Read",
					"description": "Reads a file from the local filesystem.\n\nUsage:\n- absolute path required\n- max 2000 lines per call",
					"input_schema": map[string]any{
						"type":     "object",
						"required": []any{"file_path"},
					},
				},
				map[string]any{
					"name":        "Bash",
					"description": "Runs a shell command.\nReturns stdout/stderr/exit_code.",
					"input_schema": map[string]any{
						"type":     "object",
						"required": []any{"command"},
					},
				},
			},
		},
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}

	if strings.Contains(out, "<details><summary>Full tool schemas</summary>") {
		t.Error("old combined Full tool schemas <details> block still present; should be removed")
	}
	for _, want := range []string{
		"### `Read`",
		"### `Bash`",
		// Full multi-line description must be present, including
		// non-first lines (real newlines preserved).
		"Usage:\n- absolute path required\n- max 2000 lines per call",
		"Returns stdout/stderr/exit_code.",
		// Per-tool <details><summary>input_schema</summary> blocks
		"<details><summary>input_schema</summary>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in rendered output", want)
		}
	}
	// Each tool should have one <details> block, so count = 2.
	if got := strings.Count(out, "<details><summary>input_schema</summary>"); got != 2 {
		t.Errorf("expected 2 per-tool <details> blocks, got %d", got)
	}
	// Fenced blocks still balance.
	if strings.Count(out, "```")%2 != 0 {
		t.Errorf("unbalanced fenced blocks")
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump test -run TestRenderMarkdown_PerToolSections -v
```

Expected: multiple `missing %q` failures; the old combined-block assertion may also fail depending on implementation.

- [ ] **Step 3: Replace the combined `<details>` block with per-tool subsections**

In `main.go`, find the current tools rendering block. It currently looks like:

```go
	if len(tools) > 0 {
		b.WriteString("\n<details><summary>Full tool schemas</summary>\n\n")
		b.WriteString("```json\n")
		toolsJSON, _ := json.MarshalIndent(tools, "", "  ")
		b.Write(toolsJSON)
		b.WriteString("\n```\n\n</details>\n\n")
	}
```

Replace it with a per-tool loop that calls a new helper:

```go
	for _, t := range tools {
		tm, _ := t.(map[string]any)
		renderToolDetail(&b, tm)
	}
```

- [ ] **Step 4: Add `renderToolDetail` helper**

Insert near `renderOtherRequestFields` (right after it, before `runOpts`):

```go
// renderToolDetail appends a "### `<name>`" subsection for one tool:
// its full description in a fenced block, and the input_schema in a
// collapsible <details> block. Skipped when both name and description
// are empty. The input_schema <details> block is omitted when the
// tool has no schema.
func renderToolDetail(b *strings.Builder, tool map[string]any) {
	name, _ := tool["name"].(string)
	desc, _ := tool["description"].(string)
	if name == "" && desc == "" {
		return
	}
	fmt.Fprintf(b, "### `%s`\n\n", name)
	if desc != "" {
		b.WriteString("```\n")
		b.WriteString(desc)
		if !strings.HasSuffix(desc, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}
	if schema, ok := tool["input_schema"]; ok {
		b.WriteString("<details><summary>input_schema</summary>\n\n")
		b.WriteString("```json\n")
		schemaJSON, _ := json.MarshalIndent(schema, "", "  ")
		b.Write(schemaJSON)
		b.WriteString("\n```\n\n</details>\n\n")
	}
}
```

- [ ] **Step 5: Update `TestRenderMarkdown_HappyPath` to match the new layout**

In `main_test.go`, find the existing assertion in `TestRenderMarkdown_HappyPath`:

```go
	if !strings.Contains(out, "<details><summary>Full tool schemas</summary>") {
		t.Error("collapsible tool-schemas block missing")
	}
```

Replace with:

```go
	if !strings.Contains(out, "<details><summary>input_schema</summary>") {
		t.Error("per-tool input_schema <details> block missing")
	}
```

The fixture already has two tools with descriptions, so the existing per-tool subsections will be exercised; no further fixture changes are needed.

- [ ] **Step 6: Run — expect PASS**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump test -run TestRenderMarkdown -v
```

Expected: all `TestRenderMarkdown_*` tests pass, including the updated `HappyPath` and new `PerToolSections`.

- [ ] **Step 7: Run full suite, smoke + manual sanity-check**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump test -short
PROMPTDUMP_SMOKE=1 GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump test -run TestRunCapture_Smoke -timeout 60s

# Manual: capture a real prompt and check the new shape.
cd /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump
GOWORK=off go run . -o /tmp/smoke
grep -c '^### `' /tmp/smoke.md          # → 68 (or whatever current tool count is)
grep -c 'input_schema' /tmp/smoke.md    # → at least 2× tool count
test -z "$(grep -c 'Full tool schemas' /tmp/smoke.md)" || ! grep -q 'Full tool schemas' /tmp/smoke.md
echo "manual smoke OK"
rm -f /tmp/smoke.json /tmp/smoke.md
```

Expected: short tests pass; smoke passes; the manual checks show >0 per-tool subsections, multiple `input_schema` occurrences (one per tool plus possibly the test fixture), and no surviving "Full tool schemas" string.

- [ ] **Step 8: gofmt + vet**

```bash
gofmt -l /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump vet ./...
```

- [ ] **Step 9: Commit**

```bash
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full add utils/promptdump/main.go utils/promptdump/main_test.go
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full commit -m "$(cat <<'EOF'
feat(promptdump): per-tool subsections in markdown

Replaces the single "Full tool schemas" collapsible block with one
"### `<name>`" subsection per tool: full multi-line description in a
fenced block (real newlines preserved), input_schema folded into its
own per-tool <details>. The top bullet index stays for scannability.

Effect: every tool's description is now readable in the rendered
Markdown without expanding <details> and without manually decoding
JSON escapes — closing one of the gaps that motivated the markdown
output in the first place.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: README update

**Files:**
- Modify: `utils/promptdump/README.md`

- [ ] **Step 1: Update "Reading the output" section**

Read the current `## Reading the output` section. Replace its first paragraph:

```markdown
For a quick visual read, capture with a basename or `.md` extension and
open the Markdown file. Sections cover metadata, request config, each
system-prompt segment (in fenced blocks with real newlines), the tool
catalogue (name + lede; full schemas folded into a `<details>` block),
and the first user message.
```

with:

```markdown
For a quick visual read, capture with a basename or `.md` extension and
open the Markdown file. The rendering is **lossless** with respect to the
JSON envelope — every top-level field and every `request` field is
reachable without expanding `<details>`. Sections cover metadata
(including `claude_path` and any non-standard `request` fields under
"Other request fields"), per-segment system prompt in fenced blocks with
real newlines, a per-tool subsection for each tool with full description
in a fenced block and `input_schema` folded into a per-tool `<details>`,
and the first user message.
```

- [ ] **Step 2: Commit**

```bash
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full add utils/promptdump/README.md
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full commit -m "$(cat <<'EOF'
docs(promptdump): README reflects full markdown coverage

Update "Reading the output" to call out that the Markdown form is now
lossless with respect to the JSON envelope: claude_path, Other request
fields, and per-tool subsections (with each tool's input_schema in its
own collapsible) are all visible without expanding <details>.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Final verify + codex + push + PR + Copilot fixes

- [ ] **Step 1: Run full suite end-to-end**

```bash
gofmt -l /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump vet ./...
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump test -v -timeout 60s
PROMPTDUMP_SMOKE=1 GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full/utils/promptdump test -run TestRunCapture_Smoke -v -timeout 60s
```

Expected: everything green.

- [ ] **Step 2: Verify eidos workspace still untouched**

```bash
cd /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full
go build ./cmd/eidos
go test ./... -short -count=1 -timeout 5m | tail -5
go list ./... | grep -E 'utils|promptdump' || echo "(utils/ correctly outside workspace)"
```

Expected: eidos builds, all unit tests pass, utils/ does not appear in the workspace package list.

- [ ] **Step 3: Codex review**

```bash
cd /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full
codex exec review --base main 2>&1 | tail -50
```

Triage every actionable finding. For each finding inside this PR's diff, fix and re-test before pushing. For findings clearly outside this PR's diff (e.g., codex flags an unrelated file from main), note them but don't block the push.

- [ ] **Step 4: Push and open PR**

```bash
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md-full push -u origin feat/promptdump-md-full

gh pr create --title "feat(promptdump): full markdown coverage of JSON envelope" --body "$(cat <<'EOF'
## Summary
- Markdown output is now **lossless** with respect to the JSON envelope. Every top-level field of the envelope and every top-level field inside ` + "`request`" + ` is reachable without expanding ` + "`<details>`" + `.
- Three additions to ` + "`renderMarkdown`" + `:
  1. ` + "`claude_path`" + ` line in the metadata block.
  2. ` + "`### Other request fields`" + ` subsection enumerating any ` + "`request`" + ` key outside ` + "`{system, tools, messages, model, max_tokens, stream}`" + `. Scalars as bullets, complex values as fenced JSON, alphabetical ordering. Subsection only appears when there's at least one such key.
  3. Per-tool ` + "`### " + "`<name>`" + "`" + ` subsections replacing the single ` + "`<details>Full tool schemas</details>`" + ` block. Each tool's full multi-line description renders in a fenced block (real newlines preserved); ` + "`input_schema`" + ` folds into a per-tool ` + "`<details>`" + `.

## Spec
- ` + "`docs/superpowers/specs/2026-05-11-promptdump-md-full-coverage-design.md`" + `

## Test plan
- [x] New unit tests: ` + "`TestRenderMarkdown_ClaudePath`" + `, ` + "`TestRenderMarkdown_OtherRequestFields`" + ` (alphabetical ordering + scalar/complex rendering), ` + "`TestRenderMarkdown_NoOtherFields`" + ` (skip-if-empty), ` + "`TestRenderMarkdown_PerToolSections`" + ` (per-tool subsections, ` + "`input_schema`" + ` count, fence balance, absence of old combined block).
- [x] ` + "`TestRenderMarkdown_HappyPath`" + ` updated to assert the per-tool ` + "`<details>`" + ` form instead of the now-removed combined block.
- [x] Smoke test (` + "`PROMPTDUMP_SMOKE=1 go test -run TestRunCapture_Smoke`" + `) passes against real claude.
- [x] Manual smoke: ` + "`go run . -o /tmp/smoke`" + ` against a default capture shows N per-tool subsections, no surviving "Full tool schemas" string, and ` + "`grep input_schema`" + ` matches at least 2× tool count.
- [x] Eidos workspace unaffected: ` + "`go build ./cmd/eidos`" + ` + ` + "`go test ./... -short`" + ` pass; ` + "`utils/promptdump`" + ` absent from ` + "`go list ./...`" + `.
- [x] Codex review clean for this PR's diff.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 5: Watch CI**

```bash
gh pr checks --watch
```

Expected: both checks pass.

- [ ] **Step 6: Address Copilot review (if any)**

After Copilot's review arrives, list inline comments:

```bash
gh api repos/LucianoXu/eidopsyche/pulls/$(gh pr view --json number -q .number)/comments --paginate -q '.[] | "=== \(.path):\(.line // .original_line // "?") ===\n\(.body)\n"'
```

Triage each comment. Fix actionable ones, commit as `fix(promptdump): address copilot review feedback on PR #<N>`, push, and re-watch CI.

- [ ] **Step 7: Print PR URL and hand back**

```bash
gh pr view --json url -q .url
```

---

## Self-review notes

**Spec coverage:**
- §1 `claude_path` in metadata → Task 2. ✓
- §2 "Other request fields" subsection (alphabetical, scalar vs complex rendering, skip-if-empty) → Task 3, with three assertions in `TestRenderMarkdown_OtherRequestFields` (heading, scalar bullet, complex fenced JSON, alphabetical ordering) plus a dedicated `TestRenderMarkdown_NoOtherFields`. ✓
- §3 Per-tool subsections + remove combined `<details>` block → Task 4, including the explicit assertion that the old block is gone (`strings.Contains(out, "<details><summary>Full tool schemas</summary>")` must be false). ✓
- Coverage invariant → exercised in T4's manual smoke step (real capture). ✓
- Test list (5 tests, one update) → Tasks 2, 3, 4 cover all five. ✓
- README touch → Task 5. ✓

**Placeholder scan:**
- No "TBD" / "implement later" / "similar to Task N" patterns.
- Every TDD step shows complete test or implementation code.
- Every commit message is fully drafted.

**Type consistency:**
- `renderOtherRequestFields(b *strings.Builder, req map[string]any)` — defined in T3, called from `renderMarkdown` in T3. ✓
- `renderToolDetail(b *strings.Builder, tool map[string]any)` — defined in T4, called from `renderMarkdown` in T4. ✓
- Existing `joinArgs`, `firstNonEmptyLine`, `formatMediaType` are untouched.
- `renderMarkdown` signature unchanged.
- `buildEnvelopeMap` / `buildEnvelope` / `dispatchOutput` / `runCapture` all untouched.

**Test interactions:**
- `TestRenderMarkdown_HappyPath`'s fixture has two tools (Read, Bash) — after T4, those exercise the per-tool subsection code path. The fixture's `cache_control` segment 1 also continues to exercise the existing system-prompt code path, which T4 does not touch.
- `TestRenderMarkdown_MarkdownInDescription` continues to assert structural safety (fence balance) under the new per-tool layout — that test's fixture has one tool with backticks in its description, and the per-tool fenced block neutralizes them. No update needed.
