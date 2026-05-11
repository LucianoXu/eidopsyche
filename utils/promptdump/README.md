# promptdump

Capture Claude Code's Anthropic Messages API request body — the verbatim
default system prompt, tool definitions, and initial user message — so
you can study and borrow from it when designing mind-form prompts.

## How it works

`promptdump` starts a tiny HTTP server on a random localhost port, then
spawns `claude -p <prompt>` with these env overrides:

```
ANTHROPIC_BASE_URL=http://127.0.0.1:<random>
ANTHROPIC_API_KEY=sk-dummy-promptdump
```

Claude's SDK posts to our local server instead of `api.anthropic.com`.
We capture the first POST to `/v1/messages`, return a minimal valid
SSE stream so claude exits cleanly, and write a JSON envelope to stdout
(or `-o <file>`).

The captured body is verbatim — we add no interpretation layer. Use
`jq` to slice it.

## Build / run

This utility is an **independent Go module**, intentionally outside the
eidopsyche workspace (`go.work`). All `go` invocations therefore need
`GOWORK=off` so Go doesn't try to apply the parent workspace.

```sh
# From inside utils/promptdump:
cd utils/promptdump
GOWORK=off go build .          # produces ./promptdump (gitignored)
GOWORK=off go run .            # default: Opus + no extra flags
GOWORK=off go run . -o snap.json

# From repo root (no cd):
GOWORK=off go -C utils/promptdump run .
GOWORK=off go -C utils/promptdump test ./...
```

If you forget `GOWORK=off` you'll see "main module (...) does not
contain package ..." — that's the signal to add it.

## Useful invocations

```sh
cd utils/promptdump

# Capture under the eidopsyche mindform defaults (sonnet + medium effort).
GOWORK=off go run . -- --model sonnet --effort medium

# Compare with identity injection.
GOWORK=off go run . -o /tmp/with-identity.json -- \
    --model sonnet --append-system-prompt "$(cat identity.md)"

# Compare normal vs --bare mode (what --bare strips).
GOWORK=off go run . -o /tmp/normal.json
GOWORK=off go run . -o /tmp/bare.json -- --bare
diff <(jq -S . /tmp/normal.json) <(jq -S . /tmp/bare.json) | head -200
```

## Reading the output

```sh
# The default system prompt segments (cache-control'd).
jq '.request.system' snap.json

# Just the prose, no metadata.
jq -r '.request.system[].text' snap.json

# Tool catalogue.
jq '.request.tools | map(.name)' snap.json

# First user message (includes additionalContext / session hooks).
jq '.request.messages[0]' snap.json
```

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `-p <text>` | `ping` | Prompt passed to claude as `-p <prompt>`. |
| `-o <path>` | _stdout_ | Write envelope JSON to this file instead. |
| `-v` | off | Log proxy traffic and claude stderr. |
| `--keep-going` | off | Do not SIGTERM claude after capture; let it finish on its own. |
| `--` | — | Anything after this is passed verbatim to `claude`. |

## Output shape

```json
{
  "captured_at": "2026-05-11T17:23:45Z",
  "claude_version": "2.1.138",
  "claude_path": "/usr/local/bin/claude",
  "claude_args": ["--model", "sonnet", "-p", "ping"],
  "host": { "platform": "linux", "cwd": "/data/eidopsyche" },
  "request": {
    "model": "claude-sonnet-4-7",
    "max_tokens": 32000,
    "stream": true,
    "system": [ /* cache-control segmented system prompt */ ],
    "tools": [ /* tool definitions */ ],
    "messages": [ /* first user message */ ]
  }
}
```

If the captured body fails to parse as JSON, the envelope contains
`raw_body` (a string) and `parse_error` instead of `request`.

## Privacy caveat

The captured request body contains **your local context**: cwd, git
status, the contents of any `CLAUDE.md` in scope, environment hints,
your installed plugins. Don't commit raw captures to a public repo
without redacting them. For a "clean" snapshot, run from a neutral
working dir, e.g. `cd /tmp && GOWORK=off go run /path/to/utils/promptdump`.

The `.gitignore` in this directory excludes `*.json` so captures
written here with `-o` are not accidentally tracked.

## Limitations

- Linux / macOS only. Windows process and signal handling untested.
- Captures only the first `/v1/messages` POST. Multi-turn dynamics are
  out of scope — the first request is the informative one.
- Not a release artifact. This binary is not built by CI and not shipped
  with eidos; it's a dev-only tool that lives in `utils/`.

## Testing

```sh
cd utils/promptdump

# Unit tests only (fast; skips the smoke that spawns real claude).
GOWORK=off go test -short ./...

# Including smoke test (requires `claude` on PATH; ~3s).
GOWORK=off go test ./...
```
