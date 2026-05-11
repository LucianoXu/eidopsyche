# `utils/promptdump` — Markdown Full Coverage of JSON

**Status:** Design • 2026-05-11
**Scope:** Extend `renderMarkdown` so every top-level field in the captured envelope — and every top-level field inside `request` — is reachable from the Markdown output without expanding any `<details>` block. Today's renderer drops `claude_path`, any `request` field outside `{system, tools, messages, model, max_tokens, stream}`, and the non-first lines of tool descriptions (the latter survives only as JSON-escaped text inside the collapsed full-schemas block).
**Parent:** `docs/superpowers/specs/2026-05-11-promptdump-markdown-output-design.md` (PR #66, in review at spec time). This spec lands as a follow-on PR.

## Why

The promise of the Markdown output is "human-readable equivalent of the JSON". Today it's a deliberate subset:

- `claude_path` is in the envelope but not in the Markdown metadata block.
- `request.model` / `request.max_tokens` / `request.stream` render as bullets; any other field Anthropic ships in the request (`temperature`, `anthropic_beta`, `metadata`, `tool_choice`, `top_p`, future additions) is silently absent.
- Tool descriptions in the bullet list show only the first non-empty line. The full description survives in the collapsed "Full tool schemas" `<details>` block, but as a JSON string — so the multi-line content reappears as `\n` escapes, which is exactly what motivated the Markdown form in the first place.

The user wants "lossless" Markdown — anything they could learn from the `.json` should be readable in the `.md` without expanding `<details>` and without mentally decoding JSON escapes.

## Non-goals

- No new flags or output modes.
- No reordering of the existing structural sections (metadata → request → system → tools → first user message).
- No "compact" / "expanded" toggle; if the document grows with 68 tools, that's the cost of full coverage.

## Changes

### 1. `claude_path` in the metadata block

Add one line between `**Claude version:**` and `**Claude args:**`:

```markdown
**Claude path:** `/usr/bin/claude`
```

Trivial; honors the same "render only if non-empty" pattern as the existing metadata lines.

### 2. "Other request fields" subsection

The `## Request` section gains a sub-section that renders every `request` key not already covered by an existing bullet or top-level section. The covered set is exactly:

```
{ "system", "tools", "messages", "model", "max_tokens", "stream" }
```

For each remaining key, in iteration order (alphabetical for determinism — see "Test fixtures must be stable" below):

- **Scalar** (`string`, `number`, `bool`, `null`): rendered as `- **<key>:** <value>` where the value uses backticks for strings and `null`/`true`/`false`/numeric literals as-is.
- **Complex** (`object` or `array`): rendered as `- **<key>:**` followed by a fenced ` ```json ` block of the pretty-printed value (`json.MarshalIndent` with 2-space indent).

The subsection heading **only appears if there is at least one such key** — for a vanilla `claude -p ping` capture where `request` carries only the well-known fields, the document is unchanged.

```markdown
### Other request fields

- **temperature:** `1.0`
- **anthropic_beta:** `prompt-caching-2024-07-31`
- **metadata:**
  ```json
  {
    "user_id": "..."
  }
  ```
```

Note: `raw_body` / `parse_error` (the malformed-body fallback fields at envelope top level, not under `request`) are already handled by the existing fallback path and stay untouched.

### 3. Tools — per-tool subsection

Replace the existing single `<details><summary>Full tool schemas</summary>` block with **one subsection per tool**. The top bullet list stays as a scannable index.

```markdown
## Tools (68)

- **`Read`** — Reads a file from the local filesystem.
- **`Bash`** — Runs a shell command.
- ...

### `Read`

```
Reads a file from the local filesystem.

Usage:
- ...
[full description, with real newlines]
```

<details><summary>input_schema</summary>

```json
{
  "type": "object",
  "properties": {...},
  "required": ["file_path"]
}
```

</details>

### `Bash`
...
```

Tool ordering inside the subsection list follows the ordering of `request.tools` in the captured JSON (not alphabetical) — Claude Code's tool order has semantic meaning (recommended-first), and we don't reorder it elsewhere.

The description fenced block uses `` ``` `` (no language tag) — descriptions are prose, often with Markdown-significant chars (`**`, `` ` ``), and a fenced block bypasses all of that. The `input_schema` fenced block uses `json` for syntax highlighting where the reader supports it.

If a tool has no `input_schema` (theoretically possible), the `<details>` block is omitted.

## Coverage invariant

A test asserts the invariant explicitly: given an envelope built from a fixture body containing extra request fields and tool descriptions with multi-line content, every value that appears in the JSON shape must also appear in the Markdown — either as visible prose, as a bullet, or inside a fenced block. The test enumerates the keys/values it expects and `strings.Contains`-checks each.

Concretely: for every JSON path `P` in the envelope, `strings.Contains(markdown, stringify(value_at(P)))` is true, except for:
- Object-typed values rendered as fenced JSON — they appear as JSON strings, not pretty-printed structure-by-structure
- Numeric values that round through `float64` (test fixture uses integer-valued floats to avoid this)

## Implementation

Within `utils/promptdump/main.go`'s `renderMarkdown`:

1. **Metadata block** — add the `claude_path` line right after `claude_version`. ~2 lines.
2. **Other request fields** — new helper `renderOtherRequestFields(b *strings.Builder, req map[string]any)` that iterates `req`, skips the known set, sorts remaining keys, dispatches scalar vs complex rendering. Called from `renderMarkdown` after the `Stream` bullet. ~30 lines.
3. **Tools restructure** — keep the existing bullet-list emission, then iterate `tools` again and emit per-tool subsections. Drop the existing `<details>Full tool schemas</details>` block. New helper `renderToolDetail(b *strings.Builder, tool map[string]any)`. ~30 lines.

Total: ~60 lines added in `main.go`, ~0 deleted. The function grows but stays under 200 lines; no file split needed.

### Helper signatures

```go
// renderOtherRequestFields appends an "### Other request fields"
// subsection for every key in req not already covered by the
// dedicated sections. Skipped entirely if no such key exists.
func renderOtherRequestFields(b *strings.Builder, req map[string]any)

// renderToolDetail appends a "### `<name>`" subsection for one tool:
// its full description in a fenced block, and the input_schema in a
// collapsible <details> block. Skipped when name and description are
// both empty.
func renderToolDetail(b *strings.Builder, tool map[string]any)
```

### Determinism of "Other request fields" ordering

Map iteration in Go is intentionally randomized, so the helper sorts keys alphabetically before emitting. This makes the rendering reproducible — important for tests, for `diff` between two captures, and for the spec's "test fixtures must be stable" requirement.

## Testing

Five tests touch the renderer; one is new-test-only, three guard the new behaviors, one is an update to an existing test.

1. **`TestRenderMarkdown_ClaudePath` (new)** — fixture with `claude_path: "/usr/bin/claude"`; assert `**Claude path:** `/usr/bin/claude`` in the output.

2. **`TestRenderMarkdown_OtherRequestFields` (new)** — fixture with `request.temperature: 1.0`, `request.anthropic_beta: "prompt-caching-2024-07-31"`, `request.metadata: {"user_id":"u1"}`. Assert: `### Other request fields` heading present; `- **temperature:**` bullet with `1` (JSON round-trips to float64, formatted as integer); `- **anthropic_beta:** `prompt-caching-2024-07-31``; `- **metadata:**` followed by fenced `json` block containing `"user_id": "u1"`.

3. **`TestRenderMarkdown_NoOtherFields` (new)** — fixture with only the well-known request fields. Assert: `### Other request fields` heading **absent**.

4. **`TestRenderMarkdown_PerToolSections` (new)** — fixture with two tools, each with a multi-line description and an `input_schema`. Assert: `### `Read`` and `### `Bash`` subsection headings present; each description's later lines appear (e.g., `"Usage:"`); each tool has a `<details><summary>input_schema</summary>` block; the old `<details><summary>Full tool schemas</summary>` block is **not** present.

5. **`TestRenderMarkdown_HappyPath` (update)** — its existing assertion `strings.Contains(out, "<details><summary>Full tool schemas</summary>")` is now wrong (we removed that block). Replace with `<details><summary>input_schema</summary>` (per-tool form). All other assertions stay.

The smoke test (`TestRunCapture_Smoke`) does not need changes — it only asserts envelope shape, not Markdown content.

## Documentation

- `utils/promptdump/README.md` § Reading the output — replace "full schemas folded into a `<details>` block" with "each tool's `input_schema` folded into its own `<details>` block". The "any leftover request fields appear under Other request fields" gets a one-sentence mention.
- This spec is the canonical record of the change; the parent `2026-05-11-promptdump-markdown-output-design.md` does **not** need updating beyond a possible one-line "extended in <this spec>" forward-reference — the parent's Markdown layout section is now stale in places (no more single full-schemas block, etc.), but rewriting it in place would be churn. Future readers chasing parent → this spec is fine.

## Risks

- **Document length.** A real capture has 68 tools with descriptions averaging ~30 lines each → roughly 2000 extra lines of Markdown vs today. Still trivial in size (~80 KB) and search-friendly in any text editor. The bullet index up top makes navigation manageable; Markdown-renderers' automatic ToC handles the rest.
- **Future request fields with surprising shapes.** If Anthropic adds a field whose value is, say, a giant base64 blob or a 100-element array, "Other request fields" will render it inline and inflate the doc. We accept this — the alternative (allowlisting which complex fields to render) defeats the lossless goal.
