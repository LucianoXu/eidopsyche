# `utils/promptdump` — Claude Code System-Prompt Capture Utility

**Status:** Design • 2026-05-11
**Scope:** New top-level `utils/` directory housing dev-only Go utilities. First inhabitant: `promptdump`, a small binary that captures the structured Anthropic Messages API request body that Claude Code sends — i.e. the system prompt, tool definitions, model, and initial user message — under arbitrary `claude` flag combinations.

## Why

We want to study and borrow from Claude Code's default system prompt when designing mind-form prompts. The natural-sounding path — `claude --debug-file …` then grep the log — does **not** work: `--debug-file` records only API-request metadata (`[API REQUEST] /v1/messages x-client-request-id=…`), not the request body. The system prompt never appears in the debug log.

The reliable way to obtain the verbatim system prompt is to intercept Claude Code's HTTP call to `api.anthropic.com` and dump the request body. This is feasible and clean because the Anthropic SDK honors the `ANTHROPIC_BASE_URL` env var.

Probe verified 2026-05-11: setting `ANTHROPIC_BASE_URL=http://127.0.0.1:18992` + `ANTHROPIC_API_KEY=sk-dummy-probe` and running `claude -p "ping"` causes Claude Code to POST `/v1/messages?beta=true` (body ~190 KB) to the local listener. The request body is the standard Anthropic Messages API JSON: `{"model":..., "messages":[...], "system":[...], "tools":[...], ...}`.

## Non-goals

- Not a runtime feature of `eidos`. Lives outside `cmd/eidos/...`, outside `go.work`, ships with no release artifact.
- Not a Claude Code reverse-engineering tool. We capture what the SDK transmits — nothing more, nothing less. We do **not** read the compiled `claude` bundle.
- Not a long-running proxy. One invocation = one capture. No multi-turn, no archival.
- No Windows support (Linux/macOS only; Windows process/signal handling not worth the edge cases for a dev utility).

## Directory & module layout

```
utils/                                # NEW top-level dir, dev-only utilities
├── README.md                         # one-paragraph: what lives here, why outside go.work
└── promptdump/
    ├── main.go                       # ~300 LOC, single file
    ├── main_test.go                  # capture-server unit tests, no real claude
    ├── go.mod                        # standalone module
    └── README.md                     # how to run, example output, flag passthrough examples
```

- **Independent Go module**, not added to `go.work`. Rationale: this binary depends only on `net/http`, `os/exec`, `encoding/json`, and stdlib. Keeping it out of the workspace means `go build ./cmd/eidos` and the `eidos` import graph stay untouched, and CI's existing `go test ./...` (which is workspace-scoped) is unaffected — `utils/` is invoked explicitly when wanted.
- **Pre-existing project structure note (CLAUDE.md):** the top-level layout list in CLAUDE.md does not mention `utils/`. The implementation PR will add a one-line entry to that list and a one-paragraph subsection describing the `utils/` policy: dev-only, not built or tested by the main workspace, not shipped, no version contract.

## Architecture

```
   ┌────────────┐                      env:
   │ promptdump │   spawn    ───────►  ANTHROPIC_BASE_URL=http://127.0.0.1:<random>
   │   (Go)     │   claude             ANTHROPIC_API_KEY=sk-dummy-promptdump
   └─────┬──────┘                      (plus passthrough flags after `--`)
         │
         │  http.Server on            ┌─────────────────┐
         │  127.0.0.1:<random>  ◄─────│ claude (child)  │
         │                            └─────────────────┘
         │
         │  routes:
         │    POST /v1/messages*  → capture body, return minimal SSE 200,
         │                          signal main goroutine
         │    *                   → return 404 / minimal stub
         │
         ▼
   pretty-print captured JSON (with metadata wrapper)
   to stdout (or -o <file>)
```

### Capture protocol

The server matches **path prefix** `/v1/messages` (query string ignored — claude sends `?beta=true`).

On the first matching POST:
1. Read the entire request body (Content-Length is honored; cap at 16 MB).
2. JSON-unmarshal it to confirm it parses; on failure, surface the raw bytes via `-o` anyway and emit a warning.
3. Respond `200 OK` with `Content-Type: text/event-stream` and a minimal SSE body that satisfies Claude Code's streaming parser. The minimal sequence is:

   ```
   event: message_start
   data: {"type":"message_start","message":{"id":"msg_promptdump","type":"message","role":"assistant","model":"capture","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}

   event: content_block_start
   data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

   event: content_block_stop
   data: {"type":"content_block_stop","index":0}

   event: message_delta
   data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}

   event: message_stop
   data: {"type":"message_stop"}
   ```

   This is the **non-negotiable** alternative to the 401 short-circuit, because the SDK retries on 401 (probe observed 4 identical retries before timeout). A successful 200 + valid stop terminates claude cleanly on the first POST.

4. Signal the main goroutine that capture is complete. Main goroutine waits up to a short grace period (default 3s) for claude to exit on its own; if claude hasn't exited, SIGTERM the child.

### Non-`/v1/messages` requests

Claude Code makes ancillary HTTP calls during startup — model probes, MCP registry fetches, telemetry. The server returns small mock responses for known shapes and `404` for everything else. The catalogue (and whether each path is fatal if mocked badly) is discovered empirically during implementation; the test plan calls out adding a regression case for any path that turns out to block the first `/v1/messages` POST.

### Timeouts

- **Hard wall-clock**: 15 s from `claude` spawn to exit. If exceeded, SIGTERM child and exit non-zero with a clear error.
- **No-POST timeout**: 8 s from spawn. If the server hasn't seen `/v1/messages` by then, SIGTERM child, exit non-zero, dump child stderr to help diagnose.

## CLI

```
promptdump [-o <path>] [-p <prompt>] [-v] [--keep-going] [-- claude-flag ...]
```

| Flag | Default | Behavior |
|---|---|---|
| `-o, --output <path>` | _stdout_ | Write the captured JSON to this file instead of stdout. Parent dir must exist. Existing file is overwritten. |
| `-p, --prompt <text>` | `"ping"` | The prompt argument passed to `claude` after the passthrough flags. Use any non-empty string — its only purpose is to make claude produce one POST. |
| `-v, --verbose` | off | Log every HTTP path the proxy receives, plus claude's stderr (otherwise discarded). |
| `--keep-going` | off | Do not SIGTERM the child after capture; let it exit on its own. Useful for debugging claude behavior under custom flags. |
| `--` | — | Everything after is appended to the `claude` argv before `-p <prompt>`. |

### Examples

```sh
# Default capture (Opus, no effort flag, default settings).
go run ./utils/promptdump

# What the prompt looks like under the chosen mindform defaults.
go run ./utils/promptdump -- --model sonnet --effort medium

# What it looks like with eidos's identity injection.
go run ./utils/promptdump -- --model sonnet --effort medium \
    --append-system-prompt "$(cat /tmp/test-identity.md)"

# What --bare mode strips (compare with a normal run).
go run ./utils/promptdump -o /tmp/normal.json
go run ./utils/promptdump -o /tmp/bare.json -- --bare
diff <(jq -S . /tmp/normal.json) <(jq -S . /tmp/bare.json) | head -200
```

## Output format

The captured request body is wrapped in a top-level envelope that adds reproducibility metadata. The Anthropic request body itself lives unchanged under `request`.

```json
{
  "captured_at": "2026-05-11T17:23:45Z",
  "claude_version": "2.1.138",
  "claude_path": "/home/yingte/.local/share/claude/bin/claude",
  "claude_args": ["--model", "sonnet", "--effort", "medium", "-p", "ping"],
  "host": {
    "platform": "linux",
    "cwd": "/data/eidopsyche"
  },
  "request": {
    "model": "claude-sonnet-4-7",
    "max_tokens": 32000,
    "stream": true,
    "system": [ /* the system prompt, typically a cache_control-segmented array */ ],
    "tools": [ /* full tool definitions */ ],
    "messages": [ /* first-turn user message including additionalContext */ ],
    "metadata": { /* SDK-attached metadata */ }
  }
}
```

The envelope and the embedded `request` object are both pretty-printed with 2-space indent and stable key ordering (`encoding/json` default + `json.Indent`).

`claude_version` is captured by running `claude --version` once before the spawn; on failure it is the literal string `"unknown"`.

**Markdown side-output.** Since 2026-05-11, `-o` is extension-driven and can also produce a human-readable Markdown rendering alongside the JSON. See `docs/superpowers/specs/2026-05-11-promptdump-markdown-output-design.md` for details. The JSON form is unchanged.

## Configuration knobs (no flags exposed yet — YAGNI)

These are deliberately **not** exposed and would be added only on demand:
- Custom listen port — auto-randomize is fine.
- Custom dummy API key value — irrelevant since proxy doesn't validate.
- Streaming vs non-streaming — claude always streams to its SDK; we mirror that.
- Multi-turn capture — first POST is the informative one; later turns add only delta content.
- Snapshot archival, diff tooling, gallery — out of scope (`-o` + git stash is enough).

## Testing

`main_test.go` covers:

1. **Capture server happy path** — Hand-craft a POST to `/v1/messages?beta=true` with a known JSON body, assert: (a) the server responds 200 + valid SSE, (b) the wrapped output JSON has the envelope shape, (c) `request.system` equals the input's `system`.
2. **Non-`/v1/messages` requests don't trigger capture** — POST to `/v1/models`, then a real one to `/v1/messages`; only the latter should appear in the output.
3. **Body too large** — POST a body > 16 MB; expect non-zero exit and an explicit "body exceeds 16 MB" error.
4. **No-POST timeout** — Don't POST anything; expect the timeout error path.
5. **Malformed JSON body** — POST `not json`; output still written (raw bytes under `"raw_body": "…"`) with a warning logged.

**Opt-in smoke test against the real `claude` binary** (`TestRunCapture_Smoke`), gated on `PROMPTDUMP_SMOKE=1`, `-short` absence, and `claude` being on `PATH`. It does **not** assert the contents of `request.system` (those change every claude release and a fixture-based assertion would be a perpetual maintenance burden) — it only asserts the envelope is well-formed JSON and contains a non-empty `request.system` / `request.model`, which guards the capture layer itself against regressions while staying tolerant to upstream prompt drift. CI does not enable the smoke test; the dev-loop expectation is "run it locally after upgrading claude."

## Implementation order (for the plan)

1. Skeleton: `utils/` dir, `utils/README.md`, `utils/promptdump/{main.go,go.mod,README.md}`. Empty `main.go` that prints "hello" — verify standalone module builds.
2. Capture-server alone: listens, captures one POST, prints body. No claude spawn yet.
3. Spawn claude with env overrides; wire capture-complete signal → child termination.
4. Envelope wrapper + `-o` / `-p` / `-v` / `--keep-going` flags.
5. Timeout + error paths.
6. `main_test.go` covering the five cases above.
7. CLAUDE.md update: add `utils/` to the project structure list + one-paragraph policy.

Each step lands as a focused commit on a `feat/utils-promptdump` branch; the PR description links back to this spec.

## Risks & open questions

- **Claude Code may add request validation that breaks the dummy API key path.** Mitigation: today it works; if it stops working we'll need to add `apiKeyHelper` shim support via `--settings`. Defer.
- **The captured `system` field shape may change** (today it is an array of cache-control segments; could become a string in some scenarios). Output is verbatim, so a shape change shows up faithfully — no parser to break.
- **The captured request includes the user's local CLAUDE.md and session context.** That's fine for personal use, but operators sharing snapshots should know not to commit them blindly. Mention this in `utils/promptdump/README.md`.
