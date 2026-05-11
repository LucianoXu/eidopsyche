# `promptdump` Markdown Side-Output Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `utils/promptdump` emit a human-readable Markdown rendering alongside (or instead of) the existing JSON envelope, dispatched by `-o`'s file extension. `cat snap.md` should give a readable view of the captured system prompt, tools, and first user message without piping through `jq`.

**Architecture:** Two new pure functions next to the existing capture/spawn code in `utils/promptdump/main.go`: `renderMarkdown(env map[string]any) (string, error)` produces the readable view; `dispatchOutput(jsonBytes []byte, env map[string]any, outPath string) ([]writeJob, error)` decides what to write where based on the file extension. The existing `buildEnvelope` is lightly refactored to expose its intermediate map so the renderer can consume it without re-parsing.

**Tech Stack:** Go 1.25 stdlib only — same as parent module. No new deps.

**Spec:** `docs/superpowers/specs/2026-05-11-promptdump-markdown-output-design.md`

> **Note on `go` invocations:** `utils/promptdump` is intentionally outside the root `go.work`, so every `go build` / `go test` / `go run` below assumes `GOWORK=off`. From inside `utils/promptdump/`: `GOWORK=off go test ./...`. From the repo root: `GOWORK=off go -C utils/promptdump test ./...`.

---

## File Structure

| Path | Purpose | Touched by |
|---|---|---|
| `utils/promptdump/main.go` | New `renderMarkdown`, new `dispatchOutput` + `writeJob`, `buildEnvelope` refactor into `buildEnvelopeMap` + thin wrapper, `main()` rewire. | Tasks 2, 3, 4, 5, 6 |
| `utils/promptdump/main_test.go` | New `TestBuildEnvelopeMap`, `TestRenderMarkdown_HappyPath`, `TestRenderMarkdown_MissingFields`, `TestRenderMarkdown_MarkdownEscaping`, `TestDispatchOutput`. | Tasks 2, 3, 4, 5, 6 |
| `utils/promptdump/README.md` | Extend flag table with new `-o` semantics; add Markdown invocation example; add "Reading the output" section. | Task 7 |
| `utils/promptdump/.gitignore` | Add `*.md` to ignore captured markdown alongside `*.json`. | Task 7 |
| `docs/superpowers/specs/2026-05-11-utils-promptdump-design.md` | Add one-paragraph forward-reference under §Output format pointing at the new spec. | Task 7 |
| `docs/superpowers/specs/2026-05-11-promptdump-markdown-output-design.md` | Already written in main repo (uncommitted at plan time); carried into the feature branch via Task 1. | Task 1 |
| `docs/superpowers/plans/2026-05-11-promptdump-markdown-output.md` | This plan; same — carried via Task 1. | Task 1 |

---

## Task 1: Worktree + carry spec/plan

**Files:**
- Create: `.claude/worktrees/feat-promptdump-md/` (worktree dir)
- Move: `docs/superpowers/specs/2026-05-11-promptdump-markdown-output-design.md`
- Move: `docs/superpowers/plans/2026-05-11-promptdump-markdown-output.md`

- [ ] **Step 1: Create the worktree from current main**

```bash
git -C /data/eidopsyche worktree add -b feat/promptdump-md /data/eidopsyche/.claude/worktrees/feat-promptdump-md main
git -C /data/eidopsyche worktree list
```

Expected: "Preparing worktree (new branch 'feat/promptdump-md')". The new worktree's HEAD should be the merge commit of PR #64 (`c653a0f`).

- [ ] **Step 2: Carry uncommitted spec + plan into the worktree**

```bash
cp /data/eidopsyche/docs/superpowers/specs/2026-05-11-promptdump-markdown-output-design.md \
   /data/eidopsyche/.claude/worktrees/feat-promptdump-md/docs/superpowers/specs/

cp /data/eidopsyche/docs/superpowers/plans/2026-05-11-promptdump-markdown-output.md \
   /data/eidopsyche/.claude/worktrees/feat-promptdump-md/docs/superpowers/plans/

rm /data/eidopsyche/docs/superpowers/specs/2026-05-11-promptdump-markdown-output-design.md
rm /data/eidopsyche/docs/superpowers/plans/2026-05-11-promptdump-markdown-output.md
```

- [ ] **Step 3: Commit spec + plan as the first commit**

```bash
cd /data/eidopsyche/.claude/worktrees/feat-promptdump-md
git add docs/superpowers/specs/2026-05-11-promptdump-markdown-output-design.md \
        docs/superpowers/plans/2026-05-11-promptdump-markdown-output.md
git commit -m "$(cat <<'EOF'
docs(promptdump): spec and plan for markdown side-output

Adds a human-readable Markdown rendering alongside the existing JSON
envelope. Dispatch is driven by -o's file extension; the existing
-o foo.json invocation continues to work unchanged.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Verify `git status` is clean.

**All subsequent tasks run inside `/data/eidopsyche/.claude/worktrees/feat-promptdump-md`.**

---

## Task 2: Refactor `buildEnvelope` into `buildEnvelopeMap` + thin wrapper (TDD)

The renderer needs the parsed envelope map. Today `buildEnvelope` produces only bytes — we need the intermediate map too. This task is a pure refactor (no behavior change) with a test that pins the map shape.

**Files:**
- Modify: `utils/promptdump/main.go`
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Write the failing test**

Append to `main_test.go`:

```go
// TestBuildEnvelopeMap: buildEnvelopeMap returns the same shape that
// buildEnvelope serializes, but as a map[string]any rather than bytes.
// The renderer (added in a later task) will consume the map directly.
func TestBuildEnvelopeMap(t *testing.T) {
	captured := []byte(`{"model":"claude-sonnet-4-7","system":"foo"}`)
	meta := envelopeMeta{
		ClaudeVersion: "2.1.138",
		ClaudePath:    "/usr/bin/claude",
		ClaudeArgs:    []string{"--model", "sonnet"},
		Host:          hostInfo{Platform: "linux", CWD: "/data/eidopsyche"},
		CapturedAt:    time.Date(2026, 5, 11, 17, 23, 45, 0, time.UTC),
	}
	got, err := buildEnvelopeMap(meta, captured)
	if err != nil {
		t.Fatalf("buildEnvelopeMap: %v", err)
	}
	if got["captured_at"] != "2026-05-11T17:23:45Z" {
		t.Errorf("captured_at = %v", got["captured_at"])
	}
	if got["claude_version"] != "2.1.138" {
		t.Errorf("claude_version = %v", got["claude_version"])
	}
	req, ok := got["request"].(map[string]any)
	if !ok {
		t.Fatalf("request not an object: %T", got["request"])
	}
	if req["model"] != "claude-sonnet-4-7" {
		t.Errorf("request.model = %v", req["model"])
	}
}
```

- [ ] **Step 2: Run — expect FAIL (undefined identifier)**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -run TestBuildEnvelopeMap
```

Expected: compile error, `buildEnvelopeMap` undefined.

- [ ] **Step 3: Refactor `buildEnvelope` to expose the intermediate map**

In `main.go`, replace the existing `buildEnvelope` with:

```go
// buildEnvelopeMap builds the wrapper map captured alongside the
// request body. Valid JSON body lands under "request"; malformed
// body falls back to "raw_body" + "parse_error" so output is always
// usable. Used by buildEnvelope (JSON output) and renderMarkdown
// (human-readable output).
func buildEnvelopeMap(meta envelopeMeta, body []byte) (map[string]any, error) {
	out := map[string]any{
		"captured_at":    meta.CapturedAt.UTC().Format(time.RFC3339),
		"claude_version": meta.ClaudeVersion,
		"claude_path":    meta.ClaudePath,
		"claude_args":    meta.ClaudeArgs,
		"host":           meta.Host,
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err == nil {
		out["request"] = parsed
	} else {
		out["raw_body"] = string(body)
		out["parse_error"] = err.Error()
	}
	return out, nil
}

// buildEnvelope serializes meta + body into the final pretty-printed
// JSON envelope. Thin wrapper around buildEnvelopeMap.
func buildEnvelope(meta envelopeMeta, body []byte) ([]byte, error) {
	m, err := buildEnvelopeMap(meta, body)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(m, "", "  ")
}
```

- [ ] **Step 4: Run all tests — expect PASS**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -short -v
```

Expected: TestBuildEnvelopeMap PASS, all existing tests still pass.

- [ ] **Step 5: gofmt + vet**

```bash
gofmt -l /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump vet ./...
```

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md add utils/promptdump/main.go utils/promptdump/main_test.go
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md commit -m "$(cat <<'EOF'
refactor(promptdump): extract buildEnvelopeMap from buildEnvelope

Pure refactor preparing for the Markdown renderer, which consumes the
envelope as map[string]any rather than re-parsing the JSON bytes.
buildEnvelope is now a thin wrapper around buildEnvelopeMap +
json.MarshalIndent. Behavior unchanged; existing tests still pass.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `renderMarkdown` happy path (TDD)

Build the renderer against a small fixture that covers every section: metadata, system segments (with real newlines inside), tools (name + description), one user message.

**Files:**
- Modify: `utils/promptdump/main.go`
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Write the failing test**

Append to `main_test.go`:

```go
// TestRenderMarkdown_HappyPath: render a fixture envelope that
// exercises every section. Assert real newlines are preserved
// (not the two-char escape), section headings exist, and fenced
// blocks balance.
func TestRenderMarkdown_HappyPath(t *testing.T) {
	env := map[string]any{
		"captured_at":    "2026-05-11T17:23:45Z",
		"claude_version": "2.1.138 (Claude Code)",
		"claude_path":    "/usr/bin/claude",
		"claude_args":    []any{"--model", "sonnet", "-p", "ping"},
		"host":           map[string]any{"platform": "linux", "cwd": "/data/eidopsyche"},
		"request": map[string]any{
			"model":      "claude-sonnet-4-6",
			"max_tokens": float64(32000),
			"stream":     true,
			"system": []any{
				map[string]any{
					"type": "text",
					"text": "line1\nline2\nline3",
					"cache_control": map[string]any{"type": "ephemeral"},
				},
				map[string]any{
					"type": "text",
					"text": "second segment",
				},
			},
			"tools": []any{
				map[string]any{
					"name":        "Read",
					"description": "Reads a file from the local filesystem.\nFull description continues...",
				},
				map[string]any{
					"name":        "Bash",
					"description": "Runs a shell command.",
				},
			},
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": "hello\nworld"},
					},
				},
			},
		},
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}

	// Metadata block
	for _, want := range []string{
		"# promptdump capture",
		"2026-05-11T17:23:45Z",
		"2.1.138 (Claude Code)",
		"--model sonnet -p ping",
		"linux",
		"/data/eidopsyche",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in metadata section", want)
		}
	}

	// Request section
	if !strings.Contains(out, "claude-sonnet-4-6") {
		t.Error("model id missing from output")
	}

	// System prompt section: real newlines must survive
	if !strings.Contains(out, "## System prompt (2 segments)") {
		t.Error("system-prompt heading missing")
	}
	if !strings.Contains(out, "line1\nline2\nline3") {
		t.Error("system-segment real newlines not preserved (got JSON-escaped form?)")
	}
	if !strings.Contains(out, `cache_control: {"type":"ephemeral"}`) {
		t.Error("cache_control annotation missing from segment heading")
	}

	// Tools section
	if !strings.Contains(out, "## Tools (2)") {
		t.Error("tools heading missing")
	}
	if !strings.Contains(out, "**`Read`**") || !strings.Contains(out, "Reads a file") {
		t.Error("Read tool not listed in bullet form")
	}
	if !strings.Contains(out, "<details><summary>Full tool schemas</summary>") {
		t.Error("collapsible tool-schemas block missing")
	}

	// User message
	if !strings.Contains(out, "## First user message") {
		t.Error("user-message heading missing")
	}
	if !strings.Contains(out, "hello\nworld") {
		t.Error("user-message real newlines not preserved")
	}

	// Fenced code blocks must balance (count of ``` is even).
	fences := strings.Count(out, "```")
	if fences%2 != 0 {
		t.Errorf("unbalanced fenced blocks: %d ``` markers", fences)
	}
}
```

- [ ] **Step 2: Run — expect FAIL (undefined identifier)**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -run TestRenderMarkdown_HappyPath
```

Expected: compile error, `renderMarkdown` undefined.

- [ ] **Step 3: Implement `renderMarkdown`**

Insert in `main.go` next to `buildEnvelope`:

```go
// renderMarkdown formats a parsed envelope as a human-readable
// Markdown document. Companion to buildEnvelope (which produces the
// canonical JSON). Sections — metadata, request config, system
// prompt, tools, first user message — degrade gracefully when
// fields are absent; missing fields yield empty sections rather
// than errors.
func renderMarkdown(env map[string]any) (string, error) {
	var b strings.Builder

	// Header + metadata
	b.WriteString("# promptdump capture\n\n")
	if v, ok := env["captured_at"].(string); ok {
		fmt.Fprintf(&b, "**Captured at:** %s\n", v)
	}
	if v, ok := env["claude_version"].(string); ok {
		fmt.Fprintf(&b, "**Claude version:** %s\n", v)
	}
	if v, ok := env["claude_args"].([]any); ok {
		fmt.Fprintf(&b, "**Claude args:** `%s`\n", joinArgs(v))
	}
	if h, ok := env["host"].(map[string]any); ok {
		platform, _ := h["platform"].(string)
		cwd, _ := h["cwd"].(string)
		fmt.Fprintf(&b, "**Host:** %s · cwd=`%s`\n", platform, cwd)
	}
	b.WriteString("\n")

	req, _ := env["request"].(map[string]any)
	if req == nil {
		// Malformed-body fallback: emit raw_body verbatim and stop.
		if raw, ok := env["raw_body"].(string); ok {
			b.WriteString("## Raw body (failed to parse as JSON)\n\n```\n")
			b.WriteString(raw)
			b.WriteString("\n```\n")
		}
		return b.String(), nil
	}

	// Request config
	b.WriteString("## Request\n\n")
	if v, ok := req["model"].(string); ok {
		fmt.Fprintf(&b, "- **Model:** `%s`\n", v)
	}
	if v, ok := req["max_tokens"].(float64); ok {
		fmt.Fprintf(&b, "- **Max tokens:** %d\n", int(v))
	}
	if v, ok := req["stream"].(bool); ok {
		fmt.Fprintf(&b, "- **Stream:** %v\n", v)
	}
	b.WriteString("\n")

	// System prompt
	sys, _ := req["system"].([]any)
	fmt.Fprintf(&b, "## System prompt (%d segments)\n\n", len(sys))
	for i, seg := range sys {
		segMap, _ := seg.(map[string]any)
		header := fmt.Sprintf("### Segment %d", i+1)
		if cc, ok := segMap["cache_control"]; ok {
			ccBytes, _ := json.Marshal(cc)
			header += fmt.Sprintf(" — `cache_control: %s`", string(ccBytes))
		}
		b.WriteString(header + "\n\n")
		text, _ := segMap["text"].(string)
		b.WriteString("```\n")
		b.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}

	// Tools
	tools, _ := req["tools"].([]any)
	fmt.Fprintf(&b, "## Tools (%d)\n\n", len(tools))
	for _, t := range tools {
		tm, _ := t.(map[string]any)
		name, _ := tm["name"].(string)
		desc, _ := tm["description"].(string)
		fmt.Fprintf(&b, "- **`%s`** — %s\n", name, firstNonEmptyLine(desc))
	}
	if len(tools) > 0 {
		b.WriteString("\n<details><summary>Full tool schemas</summary>\n\n")
		b.WriteString("```json\n")
		toolsJSON, _ := json.MarshalIndent(tools, "", "  ")
		b.Write(toolsJSON)
		b.WriteString("\n```\n\n</details>\n\n")
	}

	// First user message
	messages, _ := req["messages"].([]any)
	if len(messages) > 0 {
		b.WriteString("## First user message\n\n")
		msg, _ := messages[0].(map[string]any)
		switch content := msg["content"].(type) {
		case string:
			b.WriteString("```\n")
			b.WriteString(content)
			if !strings.HasSuffix(content, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n")
		case []any:
			for _, part := range content {
				pm, _ := part.(map[string]any)
				typ, _ := pm["type"].(string)
				switch typ {
				case "text":
					txt, _ := pm["text"].(string)
					b.WriteString("```\n")
					b.WriteString(txt)
					if !strings.HasSuffix(txt, "\n") {
						b.WriteString("\n")
					}
					b.WriteString("```\n")
				default:
					mediaType, _ := pm["media_type"].(string)
					fmt.Fprintf(&b, "_[%s%s]_\n", typ, formatMediaType(mediaType))
				}
			}
		}
	}

	return b.String(), nil
}

// joinArgs renders a []any of strings as a space-joined argv-like
// line for the metadata block. Non-string entries are %v-formatted.
func joinArgs(args []any) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if s, ok := a.(string); ok {
			parts = append(parts, s)
		} else {
			parts = append(parts, fmt.Sprintf("%v", a))
		}
	}
	return strings.Join(parts, " ")
}

// firstNonEmptyLine returns the first non-empty trimmed line of s.
// Used to summarize tool descriptions in the bullet list.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			return t
		}
	}
	return ""
}

// formatMediaType adds a " (type)" suffix if non-empty; otherwise
// returns empty. Used in user-message rendering for non-text parts.
func formatMediaType(s string) string {
	if s == "" {
		return ""
	}
	return " " + s
}
```

- [ ] **Step 4: Run — expect PASS**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -run TestRenderMarkdown -v
```

Expected: PASS.

- [ ] **Step 5: gofmt + vet + run full suite**

```bash
gofmt -l /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump vet ./...
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -short -v
```

Expected: no output from fmt/vet; all tests pass.

- [ ] **Step 6: Commit**

```bash
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md add utils/promptdump/main.go utils/promptdump/main_test.go
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md commit -m "$(cat <<'EOF'
feat(promptdump): markdown renderer

renderMarkdown(env) produces a human-readable view of the captured
envelope: metadata block, request config, per-segment system prompt
with real newlines preserved in fenced blocks, tool bullet list with
full schemas folded into a <details> block, and first user message.
Pure function; tested against a fixture envelope.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `renderMarkdown` missing-fields path (TDD)

Cover degenerate envelopes: empty tools array, no `system`, no `messages`, malformed body fallback.

**Files:**
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Add the failing test**

Append:

```go
// TestRenderMarkdown_MissingFields: empty/absent fields must not panic
// and must produce sensible (possibly empty) sections.
func TestRenderMarkdown_MissingFields(t *testing.T) {
	env := map[string]any{
		"captured_at":    "2026-05-11T17:23:45Z",
		"claude_version": "2.1.138",
		"request": map[string]any{
			"model": "claude-opus-4-7",
		},
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	for _, want := range []string{
		"# promptdump capture",
		"## System prompt (0 segments)",
		"## Tools (0)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in degenerate-input output", want)
		}
	}
	// No user message means no "## First user message" section.
	if strings.Contains(out, "## First user message") {
		t.Error("user-message section rendered despite no messages")
	}
}

// TestRenderMarkdown_RawBodyFallback: if the envelope contains a raw_body
// (parse failure), the renderer emits a "Raw body" section instead of
// the structured request sections.
func TestRenderMarkdown_RawBodyFallback(t *testing.T) {
	env := map[string]any{
		"captured_at":    "2026-05-11T17:23:45Z",
		"claude_version": "2.1.138",
		"raw_body":       "not json at all",
		"parse_error":    "invalid character 'n'",
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	if !strings.Contains(out, "## Raw body (failed to parse as JSON)") {
		t.Error("raw-body section missing")
	}
	if !strings.Contains(out, "not json at all") {
		t.Error("raw body content missing")
	}
	if strings.Contains(out, "## Request") {
		t.Error("structured request section rendered despite parse failure")
	}
}
```

- [ ] **Step 2: Run — expect PASS already**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -run TestRenderMarkdown -v
```

Expected: both new tests PASS (the Task 3 implementation already handles these cases). If either fails, fix the renderer.

- [ ] **Step 3: Commit**

```bash
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md add utils/promptdump/main_test.go
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md commit -m "$(cat <<'EOF'
test(promptdump): renderMarkdown degenerate inputs

Empty fields don't panic; absent messages don't produce an empty
"First user message" section; raw-body fallback renders a Raw body
section instead of the structured request layout.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Markdown-escaping regression test (TDD)

Tool descriptions occasionally contain Markdown-significant characters (`**`, backticks, `#`). Inside fenced blocks no escaping is needed; in the bullet list, descriptions may render oddly. We accept that as long as the output is **structurally** safe — fences still balance, headings still parse — and the content is intact.

**Files:**
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Add the failing test**

Append:

```go
// TestRenderMarkdown_MarkdownInDescription: a tool description
// containing Markdown-special characters must not break the
// document structure. Real Claude Code tools have ** and ` in their
// docs.
func TestRenderMarkdown_MarkdownInDescription(t *testing.T) {
	env := map[string]any{
		"request": map[string]any{
			"tools": []any{
				map[string]any{
					"name":        "Edit",
					"description": "Performs **exact** string replacements in `files`.\n\nUsage: ...",
				},
			},
		},
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	// The tool name must still appear under the bullet list.
	if !strings.Contains(out, "**`Edit`**") {
		t.Error("tool name bullet missing")
	}
	// The description's first line is included verbatim — we accept
	// that bold/code rendering will happen; the test only guards
	// content presence, not formatting fidelity.
	if !strings.Contains(out, "Performs **exact** string replacements in `files`.") {
		t.Error("tool description content missing")
	}
	// Fenced blocks still balance.
	if strings.Count(out, "```")%2 != 0 {
		t.Errorf("unbalanced fenced blocks: description's backticks broke structure")
	}
}
```

- [ ] **Step 2: Run — expect PASS already**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -run TestRenderMarkdown_MarkdownInDescription -v
```

Expected: PASS. If the fence-balance assertion fails, the renderer is concatenating a description with unbalanced backticks inside a fenced block — investigate (likely the full-tool-schemas JSON block).

- [ ] **Step 3: Commit**

```bash
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md add utils/promptdump/main_test.go
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md commit -m "$(cat <<'EOF'
test(promptdump): markdown-special chars in tool descriptions

Asserts that ** and backticks in real-world tool descriptions don't
break the document structure (fenced blocks still balance, headings
still scan), even though we accept that the bullet-list rendering
will pick up bold/code formatting.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: `dispatchOutput` + main rewire (TDD)

The dispatcher decides what to write where based on `-o`'s extension. Factor it out as a pure function so the table of cases is testable without filesystem side effects, then wire `main()` to call it.

**Files:**
- Modify: `utils/promptdump/main.go`
- Modify: `utils/promptdump/main_test.go`

- [ ] **Step 1: Write the failing test**

Append:

```go
// TestDispatchOutput: covers the -o extension dispatch table.
// jsonBytes and env are dummy values; we only assert which paths
// and content kinds the dispatcher produces.
func TestDispatchOutput(t *testing.T) {
	jsonBytes := []byte(`{"captured_at":"x"}`)
	env := map[string]any{
		"captured_at": "x",
		"request":     map[string]any{},
	}

	tests := []struct {
		name      string
		outPath   string
		wantPaths []string
		wantKinds []string // "json" or "md"
	}{
		{"stdout", "", []string{""}, []string{"json"}},
		{"json only", "snap.json", []string{"snap.json"}, []string{"json"}},
		{"md only", "snap.md", []string{"snap.md"}, []string{"md"}},
		{"basename → both", "snap", []string{"snap.json", "snap.md"}, []string{"json", "md"}},
		{"other ext → both", "snap.txt", []string{"snap.txt.json", "snap.txt.md"}, []string{"json", "md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs, err := dispatchOutput(jsonBytes, env, tt.outPath)
			if err != nil {
				t.Fatalf("dispatchOutput: %v", err)
			}
			if len(jobs) != len(tt.wantPaths) {
				t.Fatalf("got %d jobs, want %d: %+v", len(jobs), len(tt.wantPaths), jobs)
			}
			for i, j := range jobs {
				if j.path != tt.wantPaths[i] {
					t.Errorf("job[%d].path = %q, want %q", i, j.path, tt.wantPaths[i])
				}
				switch tt.wantKinds[i] {
				case "json":
					if !bytes.Equal(j.content, jsonBytes) {
						t.Errorf("job[%d] content not JSON bytes", i)
					}
				case "md":
					if !bytes.Contains(j.content, []byte("# promptdump capture")) {
						t.Errorf("job[%d] content not Markdown", i)
					}
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -run TestDispatchOutput
```

Expected: compile error, `dispatchOutput` / `writeJob` undefined.

- [ ] **Step 3: Implement `dispatchOutput` and `writeJob`**

Insert in `main.go` next to `renderMarkdown`:

```go
// writeJob is one filesystem write produced by dispatchOutput.
// An empty path means "write to stdout"; main() interprets it.
type writeJob struct {
	path    string
	content []byte
}

// dispatchOutput decides what files to write based on outPath's
// extension. Pure (no filesystem side effects); main() iterates the
// returned jobs.
//
// Rules:
//   outPath == ""        → [{stdout, jsonBytes}]
//   ext == ".json"       → [{outPath, jsonBytes}]
//   ext == ".md"         → [{outPath, markdown(env)}]
//   anything else        → [{outPath+".json", jsonBytes}, {outPath+".md", markdown(env)}]
func dispatchOutput(jsonBytes []byte, env map[string]any, outPath string) ([]writeJob, error) {
	if outPath == "" {
		return []writeJob{{path: "", content: jsonBytes}}, nil
	}
	switch filepath.Ext(outPath) {
	case ".json":
		return []writeJob{{path: outPath, content: jsonBytes}}, nil
	case ".md":
		md, err := renderMarkdown(env)
		if err != nil {
			return nil, err
		}
		return []writeJob{{path: outPath, content: []byte(md)}}, nil
	default:
		md, err := renderMarkdown(env)
		if err != nil {
			return nil, err
		}
		return []writeJob{
			{path: outPath + ".json", content: jsonBytes},
			{path: outPath + ".md", content: []byte(md)},
		}, nil
	}
}
```

Add `path/filepath` to the import list.

- [ ] **Step 4: Run dispatch test — expect PASS**

```bash
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -run TestDispatchOutput -v
```

Expected: all 5 subtests PASS.

- [ ] **Step 5: Rewire `runCapture` to also build the envelope map, then `main()` to consume `dispatchOutput`**

In `main.go`, modify `runCapture` to return both the JSON bytes and the envelope map. Replace its final return with:

```go
	cwd, _ := os.Getwd()
	meta := envelopeMeta{
		CapturedAt:    time.Now().UTC(),
		ClaudeVersion: claudeVer,
		ClaudePath:    claudePath,
		ClaudeArgs:    args,
		Host:          hostInfo{Platform: runtime.GOOS, CWD: cwd},
	}
	envMap, err := buildEnvelopeMap(meta, srv.captured)
	if err != nil {
		return nil, nil, err
	}
	jsonBytes, err := json.MarshalIndent(envMap, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return jsonBytes, envMap, nil
}
```

Update the `runCapture` signature to:

```go
func runCapture(ctx context.Context, opts runOpts) ([]byte, map[string]any, error) {
```

And update `TestRunCapture_Smoke` to consume the new return values:

```go
	jsonBytes, _, err := runCapture(ctx, runOpts{...})
	if err != nil {
		t.Fatalf("runCapture: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(jsonBytes, &envelope); err != nil {
		t.Fatalf("envelope not valid JSON: %v\n%s", err, jsonBytes)
	}
```

Now replace `main()` with:

```go
func main() {
	opts, outPath, err := parseFlags(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	jsonBytes, envMap, err := runCapture(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: %v\n", err)
		os.Exit(1)
	}

	jobs, err := dispatchOutput(jsonBytes, envMap, outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promptdump: %v\n", err)
		os.Exit(1)
	}

	var wrote []string
	for _, j := range jobs {
		if j.path == "" {
			os.Stdout.Write(j.content)
			os.Stdout.Write([]byte("\n"))
			continue
		}
		if err := os.WriteFile(j.path, j.content, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "promptdump: write %s: %v\n", j.path, err)
			os.Exit(1)
		}
		wrote = append(wrote, j.path)
	}
	if len(wrote) > 0 {
		fmt.Fprintf(os.Stderr, "promptdump: wrote %s\n", strings.Join(wrote, ", "))
	}
}
```

- [ ] **Step 6: Run full suite (incl. smoke) — expect PASS**

```bash
gofmt -l /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump vet ./...
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -short -v
PROMPTDUMP_SMOKE=1 GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -run TestRunCapture_Smoke -v -timeout 60s
```

Expected: fmt/vet clean; all unit tests pass; smoke passes.

- [ ] **Step 7: Manual smoke — both files produced**

```bash
cd /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump
GOWORK=off go run . -o /tmp/smoke
ls -la /tmp/smoke.json /tmp/smoke.md
head -30 /tmp/smoke.md
```

Expected: both files exist; `/tmp/smoke.md` starts with `# promptdump capture`, has real newlines in its system-prompt fenced blocks.

```bash
GOWORK=off go run . -o /tmp/smoke.json    # JSON only
test -f /tmp/smoke-jsononly.md && echo "WRONG: md created"
GOWORK=off go run . -o /tmp/smoke.md      # md only
head -10 /tmp/smoke.md
```

Expected: respective single-file behaviors.

- [ ] **Step 8: Commit**

```bash
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md add utils/promptdump/main.go utils/promptdump/main_test.go
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md commit -m "$(cat <<'EOF'
feat(promptdump): extension-driven output dispatch

dispatchOutput decides what to write based on -o's extension:
  .json   → JSON only (existing behavior, preserved)
  .md     → Markdown only
  other / no ext → both <path>.json and <path>.md
Empty -o still streams JSON to stdout. Pure function, table-driven
test covers every case; main() rewired to iterate the returned
writeJob list.

runCapture now returns (jsonBytes, envelopeMap, error) so the
markdown renderer can consume the map directly without re-parsing.
TestRunCapture_Smoke updated for the new signature.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Docs + .gitignore

**Files:**
- Modify: `utils/promptdump/README.md`
- Modify: `utils/promptdump/.gitignore`
- Modify: `docs/superpowers/specs/2026-05-11-utils-promptdump-design.md`

- [ ] **Step 1: Update `.gitignore`**

Read `utils/promptdump/.gitignore`; ensure it contains `*.md`. After edit it should read:

```
# Compiled binary
/promptdump

# Captures
*.json
*.md
```

- [ ] **Step 2: Update `utils/promptdump/README.md`**

Find the Flags table and replace the `-o` row with:

```
| `-o <path>` | _stdout_ | Write envelope to this file. Extension-driven: `.json` → JSON only; `.md` → Markdown only; anything else → both `<path>.json` and `<path>.md`. |
```

In the **Useful invocations** section, add after the existing examples:

````markdown
```sh
# Capture both forms at once — JSON for jq, Markdown for reading.
GOWORK=off go run . -o /tmp/snap          # writes /tmp/snap.json + /tmp/snap.md

# Just the readable view (no JSON file).
GOWORK=off go run . -o /tmp/snap.md
```
````

In the **Reading the output** section, prepend:

```markdown
For a quick visual read, capture with a basename or `.md` extension and open the Markdown file. Sections cover metadata, request config, each system-prompt segment (in fenced blocks with real newlines), the tool catalogue (name + lede; full schemas folded into a `<details>` block), and the first user message.

For programmatic slicing, keep the JSON form and use `jq`:
```

(The existing jq recipes follow.)

- [ ] **Step 3: Update the parent spec**

In `docs/superpowers/specs/2026-05-11-utils-promptdump-design.md`, find the `## Output format` heading. After the existing prose+JSON example, add:

```markdown
**Markdown side-output.** Since 2026-05-11, `-o` is extension-driven and can also produce a human-readable Markdown rendering alongside the JSON. See `docs/superpowers/specs/2026-05-11-promptdump-markdown-output-design.md` for details. The JSON form is unchanged.
```

- [ ] **Step 4: Commit**

```bash
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md add utils/promptdump/README.md utils/promptdump/.gitignore docs/superpowers/specs/2026-05-11-utils-promptdump-design.md
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md commit -m "$(cat <<'EOF'
docs(promptdump): document markdown side-output

README: update -o flag description to cover the new extension-driven
dispatch; add a "both files at once" invocation example; lead the
"Reading the output" section with a note about the markdown form.

.gitignore: add *.md so captured .md files aren't accidentally
tracked, mirroring the existing *.json rule.

Parent spec: add a forward-reference to the new sub-spec under
§Output format so readers landing there are aware of the markdown
form.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Final verify, codex review, push, PR

- [ ] **Step 1: Run full suite end-to-end**

```bash
gofmt -l /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump vet ./...
GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -v -timeout 60s
PROMPTDUMP_SMOKE=1 GOWORK=off go -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md/utils/promptdump test -run TestRunCapture_Smoke -v -timeout 60s
```

Expected: everything green.

- [ ] **Step 2: Verify eidos workspace still untouched**

```bash
cd /data/eidopsyche/.claude/worktrees/feat-promptdump-md
go build ./cmd/eidos
go test ./... -short -count=1 -timeout 5m | tail -10
go list ./... | grep -E 'utils|promptdump' || echo "(utils/ correctly outside workspace)"
```

Expected: eidos builds, all unit tests pass, utils/ does not appear.

- [ ] **Step 3: Codex review**

```bash
cd /data/eidopsyche/.claude/worktrees/feat-promptdump-md
codex exec review --base main 2>&1 | tail -50
```

Address any actionable findings inline before pushing. Append any review-driven fixes as a `fix(promptdump): address codex review feedback` commit.

- [ ] **Step 4: Push and open PR**

```bash
git -C /data/eidopsyche/.claude/worktrees/feat-promptdump-md push -u origin feat/promptdump-md

gh pr create --title "feat(promptdump): markdown side-output via -o extension" --body "$(cat <<'EOF'
## Summary
- `utils/promptdump`'s `-o` flag is now extension-driven:
  - `-o foo.json` → JSON only (current behavior, preserved)
  - `-o foo.md` → Markdown only
  - `-o foo` (no extension) → both `foo.json` and `foo.md`
  - omitted → JSON to stdout (current behavior, preserved)
- New `renderMarkdown` formats the captured envelope as a human-readable Markdown document: metadata, request config, per-segment system prompt in fenced blocks (real newlines preserved), tool bullet list with full schemas folded into `<details>`, and first user message.
- Pure-function design: `renderMarkdown` and `dispatchOutput` are testable without filesystem or claude spawn.

## Spec
- `docs/superpowers/specs/2026-05-11-promptdump-markdown-output-design.md`

## Test plan
- [x] Unit tests: `TestBuildEnvelopeMap`, `TestRenderMarkdown_HappyPath`, `TestRenderMarkdown_MissingFields`, `TestRenderMarkdown_RawBodyFallback`, `TestRenderMarkdown_MarkdownInDescription`, `TestDispatchOutput` (5 subtests).
- [x] Smoke test (`PROMPTDUMP_SMOKE=1 go test -run TestRunCapture_Smoke`) passes — JSON path unchanged.
- [x] Manual smoke: `-o /tmp/smoke` produces both `/tmp/smoke.json` and `/tmp/smoke.md`; head of `.md` shows `# promptdump capture` and real newlines in system-prompt fenced blocks.
- [x] Eidos workspace unaffected: `go build ./cmd/eidos` + `go test ./... -short` pass; `utils/promptdump` absent from `go list ./...`.
- [x] Codex review clean.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 5: Watch CI**

```bash
gh pr checks --watch
```

Expected: both checks (lint/test, cross-compile) pass.

- [ ] **Step 6: Address Copilot review (if any)**

After the bot review arrives, list inline comments:

```bash
gh api repos/LucianoXu/eidopsyche/pulls/$(gh pr view --json number -q .number)/comments --paginate -q '.[] | "=== \(.path):\(.line // .original_line // "?") ===\n\(.body)\n"'
```

Triage each comment, fix actionable ones, commit as `fix(promptdump): address copilot review feedback`, push.

- [ ] **Step 7: Print PR URL and hand back**

```bash
gh pr view --json url -q .url
```

---

## Self-review notes

**Spec coverage:**
- CLI semantics table (4 main rows + `.txt` fallback) → Task 6 `TestDispatchOutput` (5 cases including `.txt`). ✓
- Markdown layout (metadata, request, system segments w/ cache_control, tools bullet+details, first user message) → Task 3 happy-path test asserts each section. ✓
- `buildEnvelope` refactor into `buildEnvelopeMap` → Task 2. ✓
- `renderMarkdown(env map[string]any) (string, error)` → Task 3. ✓
- Missing-fields/raw-body fallback → Task 4. ✓
- Markdown-special-chars in tool descriptions → Task 5. ✓
- README + .gitignore + parent spec forward-ref → Task 7. ✓
- No integration test changes beyond updating smoke for the new signature — exactly what spec promised. ✓

**Placeholder scan:** No "TBD", no "implement later", no "similar to Task N". Each test step includes the full test code. Each implementation step shows the actual Go code.

**Type consistency:**
- `buildEnvelopeMap(meta envelopeMeta, body []byte) (map[string]any, error)` — Task 2 defined, Task 6 uses. ✓
- `renderMarkdown(env map[string]any) (string, error)` — Task 3 defined, Tasks 4/5/6 use. ✓
- `dispatchOutput(jsonBytes []byte, env map[string]any, outPath string) ([]writeJob, error)` — Task 6 defined, used by `main`. ✓
- `runCapture` new signature `(ctx, opts) → ([]byte, map[string]any, error)` — Task 6 defines, smoke test updated in same task. ✓
