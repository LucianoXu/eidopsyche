# `utils/promptdump` — Markdown Side-Output

**Status:** Design • 2026-05-11
**Scope:** Add a human-readable Markdown rendering to `utils/promptdump` so captured snapshots can be read directly (in an editor or `cat`), without piping through `jq` to decode JSON's `\n` escapes inside long strings.
**Parent:** `docs/superpowers/specs/2026-05-11-utils-promptdump-design.md` (already merged via PR #64). This spec is a small addition; it does not supersede.

## Why

JSON encodes every newline inside a string as the literal two-character sequence `\n`. The captured system prompt is ~27 KB and contains many real newlines, so viewing `snap.json` in a text editor yields one giant unreadable line per system segment. The standard workaround — `jq -r '.request.system[].text' snap.json` — works but requires the user to remember which jq incantation goes with which subfield. For a utility whose explicit purpose is **"study and borrow from Claude Code's default system prompt"**, that's a paper cut every single use.

We keep JSON as the canonical, programmatically-parseable form. We add Markdown alongside it as the human-readable form.

## Non-goals

- Not a `--format <fmt>` flag. Extension-driven is enough; one less thing to remember.
- No HTML / PDF / TUI rendering. If you want pretty in a browser, `cat snap.md` into your editor's Markdown preview.
- No re-render-from-saved-JSON subcommand. If you want to re-render a saved capture, `jq` it or re-run promptdump.
- No multi-message rendering. promptdump captures the **first** `/v1/messages` POST only — there is exactly one user message.

## CLI semantics

`-o` is now extension-driven. The existing `-o foo.json` invocation **continues to work unchanged.**

| `-o` argument | Behavior |
|---|---|
| _(absent)_ | JSON to stdout (current default) |
| `-o snap.json` | JSON only to `snap.json` (current behavior, preserved) |
| `-o snap.md` | Markdown only to `snap.md` |
| `-o snap` (no extension) | **Both** `snap.json` and `snap.md` |

Extension matching is case-sensitive (`.json` / `.md`) — claude doesn't run on Windows and we don't support it. Any other extension (e.g. `-o snap.txt`) is treated as "no recognized extension" and produces both `snap.txt.json` and `snap.txt.md`; the alternative — erroring out — is rejected because it forces users to learn another rule before they make their first mistake. The double-extension result is mildly ugly but unambiguous and discoverable from the success message (`promptdump: wrote snap.txt.json, snap.txt.md`).

## Markdown layout

```markdown
# promptdump capture

**Captured at:** 2026-05-11T17:30:28Z
**Claude version:** 2.1.138 (Claude Code)
**Claude args:** `--model sonnet --effort medium -p ping`
**Host:** linux · cwd=`/data/eidopsyche`

## Request

- **Model:** `claude-sonnet-4-6`
- **Max tokens:** 32000
- **Stream:** true

## System prompt (3 segments)

### Segment 1 — `cache_control: {"type":"ephemeral"}`

```
You are an interactive agent that helps users with software
engineering tasks. ...
```

### Segment 2

```
...
```

## Tools (68)

- **`TaskCreate`** — Use this tool to create a structured task list…
- **`Read`** — Reads a file from the local filesystem…
- … (one bullet per tool: name + first non-empty line of description)

<details><summary>Full tool schemas</summary>

` ` ` json
[ ... entire request.tools array, pretty-printed ... ]
` ` `

</details>

## First user message

```
[text content with real line breaks]
```
```

### Layout choices, with reasons

- **Metadata block** (top, ~6 lines) reproduces the JSON envelope's wrapper so the reader can identify the capture without going back to the `.json`. Keeps the document self-contained.
- **System segments** in fenced ` ``` ` blocks. Real newlines preserved → copy-pasteable. `cache_control` shown in the segment heading because that's how Claude Code segments its prompt for caching, and a future reader studying the structure will want to know which segments are cached vs. ephemeral.
- **Tools as a scannable bullet list** — name in code + first description line. Full schemas folded into a `<details>` block so the document stays readable in a 27-KB-typical case but no information is lost; readers who want the full schema expand the block.
- **First user message** verbatim in a fenced block. We don't render `image_url` parts as actual images (Markdown supports it, but promptdump's capture doesn't include image payloads worth rendering); they appear as `[image]` placeholders with their `media_type` annotated.

## Implementation

One new function and a small `main()` change.

```go
// renderMarkdown formats a parsed envelope as a human-readable
// Markdown document. The envelope is the same map[string]any that
// buildEnvelope produces; renderMarkdown is its sibling for the
// human-reading path.
func renderMarkdown(env map[string]any) (string, error)
```

`main()` inspects `filepath.Ext(outPath)`:
- `".json"` → write JSON to `outPath` (current code path)
- `".md"` → marshal envelope back to a map, call `renderMarkdown`, write to `outPath`
- anything else (including no extension) → write both `outPath + ".json"` and `outPath + ".md"`

The double-write path needs the parsed envelope, so we refactor lightly: `runCapture` already builds the envelope JSON. We pull `buildEnvelope` apart into:
1. `buildEnvelopeMap(meta, body) (map[string]any, error)` — produces the parsed wrapper.
2. The current `buildEnvelope(meta, body) ([]byte, error)` becomes a thin caller of `buildEnvelopeMap` + `json.MarshalIndent`.

`renderMarkdown` consumes the map directly — no parse-then-re-parse.

### Why a Go renderer and not a shell wrapper

A 50-line Go function is more reliable than a 50-line bash/jq script that reaches into the envelope's nested structure, handles missing fields, escapes Markdown specials, and stays in sync with envelope evolution. The Go version is also testable as a pure function.

## Testing

`main_test.go` gains:

1. **`TestRenderMarkdown_HappyPath`** — feed a small fixture envelope (model, two system segments with real `\n` inside, three fake tools, one user message). Assert:
   - The output contains `# promptdump capture`.
   - The system-prompt body contains real `\n` (not the two-char `\\n`) — i.e. `strings.Contains(out, "line1\nline2")`.
   - Each tool name appears under `## Tools (3)`.
   - Fenced code blocks balance (count of ` ``` ` is even).
2. **`TestRenderMarkdown_MissingFields`** — empty `tools`, missing `system`, missing `messages`. The function must return non-empty output without panic; sections for absent fields are either omitted or labeled `(none)`.
3. **`TestOutputDispatch`** — table-driven, exercises the `-o` extension logic without calling `runCapture`:
   - `""` → stdout flag set, no files written
   - `"snap.json"` → only `snap.json` written
   - `"snap.md"` → only `snap.md` written
   - `"snap"` → both `snap.json` and `snap.md` written
   - `"snap.txt"` → both `snap.txt.json` and `snap.txt.md` written
   The test factors the dispatch logic into a `dispatchOutput(env map[string]any, outPath string) (writes []writeJob, err error)` helper so it's testable without filesystem side effects.

No new integration test — `TestRunCapture_Smoke` already exercises the JSON path end-to-end and the Markdown rendering is pure-function, exhaustively covered by the unit tests above.

## Documentation

- `utils/promptdump/README.md`: extend the **Flags** table with the new `-o` semantics, add a Markdown-output example to **Useful invocations**, and add a section "Reading the output" that points to `snap.md` for visual reading and the existing `jq` recipes for programmatic slicing.
- `docs/superpowers/specs/2026-05-11-utils-promptdump-design.md` (the parent spec): add a one-paragraph forward-reference to this spec under §Output format.
- `utils/promptdump/.gitignore` already excludes `*.json`; extend with `*.md`.

## Risks

- **Markdown escaping in tool descriptions.** Tool descriptions sometimes contain Markdown-significant characters (`*`, `_`, `` ` ``, `#`). Inside fenced code blocks we don't need to escape. Outside (the bullet list of tool ledes), we wrap the description in inline code only if it contains a backtick conflict; otherwise we render it as prose. Worst case the formatting looks ugly — never wrong-content. Test fixture includes a tool description with `**bold-looking**` text to assert it doesn't accidentally render as bold.
- **Large captures.** 27 KB system prompt × ~3 segments + 68 tool descriptions = the rendered Markdown is ~40 KB. Still trivial; no streaming needed.
