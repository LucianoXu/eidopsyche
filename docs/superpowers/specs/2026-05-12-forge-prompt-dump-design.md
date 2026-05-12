# `eidos forge prompt-dump` — per-mindform request envelope capture

**Status:** Design • 2026-05-12
**Scope:** New `forge` subcommand pair (host wrapper + in-container worker) that captures the verbatim `/v1/messages` request a given mind-form's `claude` would send right now — its default system prompt, tool catalogue, injected `identity.md`, ontology-resolved `CLAUDE.md`, model, and first-message ambient context. Plus an extraction of the existing `utils/promptdump` core into a shared `internal/promptcapture` package.

## Why

`eidos forge watch <name>` already streams the mind-form's **response** side: the stream-json transcript ndjson the agent-loop captures (assistant thinking, tool calls, tool results). We have no equivalent view of the **request** side — what each turn actually sends to Anthropic. The system prompt, the tool input_schema catalogue, the cwd / git / plugin context block, and the rolling effect of `identity.md` on the final envelope are all invisible from outside the container.

That gap matters in two situations:

1. **Designing the 纲领 / `identity.md`.** Authors want to see how a wording change in `identity.md` actually composes with Claude Code's much larger default preamble, and what cache breakpoints the SDK chooses around it.
2. **Investigating prefab drift.** When two mindforms summoned from different prefabs behave differently, the operator needs to see the resolved envelope to know whether the divergence is in identity prose, in `CLAUDE.md`, in the model setting, or in something claude is injecting on its own.

The existing `utils/promptdump` dev tool answers a *related* question — "what does Claude Code on my host send for an arbitrary prompt?" — but not the mind-form-specific one: which `identity.md`, which `CLAUDE.md`, which model, which in-container `claude` version.

## Non-goals

- Not a live tail. This is a one-shot snapshot of what a *fresh session* for the mind-form would look like right now. It does **not** capture the running agent-loop's in-flight conversation history. A live-tail proxy is a separately scoped future feature.
- Not an interface to the running agent-loop. The capture spawns its own `claude` process with a new session UUID and exits. It does not touch session state, dream state, transcripts, or the agent lock.
- Not a re-implementation. The proxy + envelope-rendering logic comes from `utils/promptdump`. The change here is mostly relocation + a new pair of cobra commands; no fundamental new mechanism.
- No Windows support (matches the existing `utils/promptdump` Linux/macOS-only constraint).

## Source decision: fresh session, in-container

Three sources were considered:

| Source | Verdict |
|---|---|
| **S1 — fresh `claude` in-container, `--session-id <new-uuid>`** (chosen) | Faithful to identity / config / ontology / tool catalogue / model / container claude version. No live conversation history. Zero impact on the running agent-loop. |
| S2 — `claude --resume <live-session-uuid>` in-container | Adds real conversation history at the cost of racing the agent-loop's own claude on the same session jsonl. Rejected: corruption risk is unacceptable for a debug tool. |
| S3 — reconstruct from session jsonl + identity | Conversation history is real, but the default system prompt and tool catalogue are injected by claude at request-build time and are not present in the jsonl. Strictly worse than S1 for the design-time use case this tool serves. |

S1 maps cleanly onto today's `utils/promptdump` mechanism, just with identity / config / model sourced from the mind-form rather than the operator's defaults.

## Host vs in-container: in-container

Two implementations were considered:

- **In-container subcommand (chosen).** Proxy + claude both run on `127.0.0.1` inside the container. `identity.md` and `config.toml` are read by normal Go (`os.ReadFile` + `internal/config.Load`). `--append-system-prompt` is passed via `exec.Cmd`, no shell quoting, no argv-size hazard. Platform-uniform because everything is inside the container.
- **Host-only orchestrator (rejected).** Host runs the proxy; `docker exec`s `claude` inside the container with env overrides. Requires the container to reach the host port, which means a docker-bridge-gateway probe with a `host.docker.internal` fallback — usable but introduces a real platform-variance surface (Linux docker0 vs macOS Docker Desktop vs Windows). Also pushes `--append-system-prompt` content through an argv literal, which works today but creates a fragility ceiling if `identity.md` grows.

The cost of the in-container choice is one extra subcommand registration. The cost of the host-only choice is permanent platform-bridging logic in a debug tool. We pay the smaller cost.

## Directory & module layout

```
internal/promptcapture/              # NEW — promoted core
├── doc.go
├── proxy.go                         # listen on 127.0.0.1:0, capture first /v1/messages, return stub SSE
├── proxy_test.go
├── envelope.go                      # JSON envelope + Markdown rendering (lossless)
├── envelope_test.go
├── spawn.go                         # build claude argv from CaptureOpts; env injection
├── spawn_test.go
├── capture.go                       # Run(ctx, opts) → Envelope: high-level "do the dance"
└── capture_test.go                  # smoke test against real claude, gated like today's promptdump

cmd/eidos/forge/
├── prompt_dump.go                   # NEW — host-side wrapper: docker exec passthrough; -o dual-write
├── prompt_dump_test.go              # fake forgectl.Client (existing pattern from status_test.go)
├── prompt_dump_incontainer.go       # NEW — in-container worker: reads identity.md + config.toml,
│                                    #        calls promptcapture.Run, writes envelope JSON to stdout
├── prompt_dump_incontainer_test.go  # injects a stub claude bin, asserts captured_from fields
└── incontainer.go                   # MODIFIED — register newPromptDumpHostCmd / newPromptDumpInContainerCmd

utils/promptdump/                    # KEPT — thin wrapper over internal/promptcapture
├── main.go                          # shrinks to: parse flags → promptcapture.Run → write envelope
├── go.mod                           # still outside go.work; require parent module via replace directive
└── README.md                        # explain: dev shim only; for a mindform's real envelope use forge prompt-dump

docs/superpowers/specs/
└── 2026-05-12-forge-prompt-dump-design.md   # this file
```

### Why `internal/` and not `pkg/`

`pkg/` is reserved for *stable public interfaces* per the top-level `CLAUDE.md`. The prompt-capture core is an internal mechanism whose surface we expect to iterate on as Claude Code's request shape evolves. `internal/promptcapture` keeps the freedom to refactor without an external contract.

### `utils/promptdump`'s ongoing role

It stays useful for inspecting envelopes that *do not* belong to a mind-form: probing what an arbitrary `claude --append-system-prompt $(cat X.md)` would emit, exploring `--bare` vs identity-loaded deltas before authoring a new prefab's `identity.md`, and so on. As a thin wrapper around `internal/promptcapture` it can remain outside `go.work` (per `utils/CLAUDE.md`) using a `replace github.com/LucianoXu/eidopsyche => ../..` directive in its `go.mod` — the same shape other Go monorepos use when keeping a satellite module out of the parent workspace. If the `replace` approach turns out to be awkward (e.g. CI considerations), the fallback is to keep `utils/promptdump` as a tiny re-implementation that calls into the public-API portion of the core; the LOC cost is small.

## CLI surface

### Host-side wrapper

```
eidos forge prompt-dump <name> [flags]

  <name>           mind-form name (must resolve to a running container)
  -o <path>        extension-driven dual output (mirrors utils/promptdump):
                     .json  → JSON only
                     .md    → Markdown only
                     other  → both <path>.json + <path>.md
                   default: write JSON to stdout
  -p, --prompt <text>   stub prompt to send claude (default: "ping")
  --bare           omit --append-system-prompt; show pure-claude envelope baseline
  -v, --verbose    surface proxy + claude stderr to host stderr
```

Registered in `registerHost()` in `cmd/eidos/forge/incontainer.go`. Implementation:

```go
func newPromptDumpHostCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "prompt-dump <name>",
        Short: "Capture the /v1/messages request envelope a mind-form would send right now",
        Long:  /* see Long-help section below */,
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            // 1. validate <name> + container running (forgectl.ContainerInspectState)
            // 2. build in-container argv:
            //      ["eidos","forge","prompt-dump",
            //       "--mindform-name", name,
            //       "--prompt", opts.Prompt,
            //       optional "--bare", optional "-v"]
            //    (no <name> positional, no -o — those are host-side concerns)
            // 3. c.ContainerExec(ctx, cont, argv); stderr → host stderr (if -v)
            // 4. -o nil  → stream stdout (the JSON envelope) verbatim to host stdout
            //    -o set  → unmarshal envelope, call internal/promptcapture/envelope.WriteOutputs(env, path)
            //              which mirrors today's utils/promptdump extension-driven dual-write
        },
    }
}
```

### In-container worker

```
eidos forge prompt-dump [flags]

  -p, --prompt <text>      stub prompt (default: "ping")
  --bare                   omit --append-system-prompt
  --mindform-name <name>   recorded in captured_from.mindform; host wrapper sets it
  -v                       log proxy traffic + claude stderr to its own stderr
```

Registered in `registerInContainer()`. No `<name>` positional (it *is* the mind-form), no `-o` (host wrapper owns output framing). `--mindform-name` is the host's way of stamping the operator-side name onto the envelope without forcing the host to re-serialize the JSON. Implementation:

```go
func newPromptDumpInContainerCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "prompt-dump",
        Short: "(in-container) Capture this mind-form's /v1/messages envelope",
        RunE: func(cmd *cobra.Command, args []string) error {
            // 1. read /eidos/ontology/self/identity.md (best-effort)
            // 2. config.Load("/eidos/gate/config.toml") → cfg.MindForm.Model
            // 3. envelope, err := promptcapture.Run(ctx, promptcapture.Opts{
            //        ClaudeBin:      "claude",
            //        Cwd:            "/eidos/ontology",
            //        IdentityPrompt: identityBytes,   // empty if --bare or missing
            //        Model:          cfg.MindForm.Model,
            //        Prompt:         opts.Prompt,
            //        Verbose:        opts.Verbose,
            //        Timeout:        10 * time.Second,
            //    })
            // 4. enrich envelope.CapturedFrom { Mindform, OntologyRoot, Model, IdentityPath, Bare }
            // 5. json.NewEncoder(os.Stdout).Encode(envelope)
        },
    }
}
```

The in-container subcommand is registered alongside the existing in-container introspection commands (`runtime-state`, `agent-state`, `transcript-list`, `transcript-tail`). It is operator-facing, not part of the mindform's self-reflection surface; that's the same role those neighbouring commands already play.

## Data flow

```
1. host: eidos forge prompt-dump alice -o snap
2. host: forgectl.ContainerInspectState → "running" (else clean error)
3. host: c.ContainerExec(ctx, "eidos-alice",
           ["eidos","forge","prompt-dump",
            "--mindform-name","alice",
            "--prompt","ping"])
4. in-container:
   4a. os.ReadFile("/eidos/ontology/self/identity.md") → identity
   4b. config.Load("/eidos/gate/config.toml")      → cfg
   4c. promptcapture.Run(ctx, Opts{
         ClaudeBin:"claude", Cwd:"/eidos/ontology",
         IdentityPrompt: identity, Model: cfg.MindForm.Model,
         Prompt: "ping",
       }):
       i.   start http.Server on 127.0.0.1:0, capture first POST /v1/messages
       ii.  exec.Cmd "claude" with argv: --append-system-prompt <identity>
            [--model <model>] --dangerously-skip-permissions -p "ping",
            env += ANTHROPIC_BASE_URL=http://127.0.0.1:<port>,
                   ANTHROPIC_API_KEY=sk-dummy-promptdump,
                   CLAUDE_DIR=/eidos/ontology/.claude   (matches agent-loop)
       iii. proxy receives body, writes minimal valid SSE response,
            signals captured-chan, returns 200
       iv.  claude exits clean (single-shot -p with stub stream)
       v.   envelope = { metadata + captured_from + claude_args
                         + request (parsed JSON) or raw_body+parse_error }
   4d. envelope.captured_from = { Mindform: "alice" (from --mindform-name),
                                  OntologyRoot, Model, IdentityPath, Bare }
   4e. json.NewEncoder(os.Stdout).Encode(envelope); exit 0
5. host:
   5a. -o nil → stream stdout verbatim to host stdout (the envelope JSON
                is already complete; no host-side re-serialization)
   5b. -o set → json.Decode the streamed envelope, then
                promptcapture.envelope.WriteOutputs(envelope, "snap")
                writes snap.json + snap.md on the host filesystem
```

## Envelope shape

Inherits today's `utils/promptdump` JSON, plus a new `captured_from` subobject documenting the mind-form context:

```json
{
  "captured_at": "2026-05-12T14:23:45Z",
  "captured_from": {
    "mindform": "alice",
    "ontology_root": "/eidos/ontology",
    "model": "sonnet",
    "identity_path": "self/identity.md",
    "bare": false
  },
  "claude_version": "2.1.138",
  "claude_path": "/usr/local/bin/claude",
  "claude_args": ["--append-system-prompt","...","--model","sonnet","-p","ping","--dangerously-skip-permissions"],
  "host": { "platform": "linux", "cwd": "/eidos/ontology" },
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

When the captured body fails to parse as JSON, `request` is replaced by `raw_body` + `parse_error`, exactly as today.

Markdown rendering: reuse the existing renderer (already lossless wrt every field) with one addition at the top — a "Captured from" section so a reader of a `.md` snapshot immediately sees which mindform, model, and identity it belongs to.

## Edge cases

1. **Container not running** — host wrapper returns `mind-form %q not running (state: %s)` before exec, matching `forge status`'s pattern.
2. **`identity.md` missing in-container** — proceed with empty `IdentityPrompt`; the captured envelope is still useful (shows the pure-claude baseline). Stderr line: `prompt-dump: identity not found at <path>; proceeding bare`. `captured_from.bare` stays `false` (the user didn't ask for `--bare`); `captured_from.identity_path` is `""`.
3. **`config.toml` model unset** — omit `--model` from claude argv; let claude pick its default. `captured_from.model` is `""`; `claude_args` records what was actually used.
4. **`claude` binary missing inside the container** — `promptcapture.Run` returns a clear `claude: executable not found in $PATH` error; host wrapper exits non-zero with the same message. This shouldn't happen with the official image (`docker/mindform/Dockerfile` installs claude) and is a config-drift signal worth surfacing clearly rather than masking.
5. **Capture timeout** — default 10 s. If `claude` posts nothing in that window (auth failure, claude regression, container misconfig), return an envelope with `parse_error: "capture timeout: claude posted no /v1/messages within 10s"` and exit non-zero. The existing `utils/promptdump` 5-s default is too tight for a cold-start `claude` inside a sleeping mindform.
6. **Concurrent invocations against the same mindform** — fine. Each spawns its own `claude` with its own random session UUID and its own random proxy port. No interaction with the running agent-loop; no agent lock needed.
7. **`--bare`** — sets `IdentityPrompt = ""` regardless of whether `identity.md` exists. Useful for seeing the deltas claude itself injects vs what `identity.md` layers on top. `captured_from.bare = true`.
8. **`--mindform-name` not provided** — when the in-container worker is invoked directly (e.g. `docker exec eidos-alice eidos forge prompt-dump` by an operator debugging), the flag is absent. `captured_from.mindform` becomes `""`. The host wrapper always passes it, so under normal use the field is populated.

## Tests

| Test | Pins down |
|---|---|
| `internal/promptcapture/proxy_test.go` (ported from `utils/promptdump`) | Captures first `/v1/messages`; ignores later POSTs; returns minimal-valid SSE that the Anthropic SDK accepts. |
| `internal/promptcapture/envelope_test.go` (ported) | JSON round-trip; Markdown renderer covers every top-level field and every `request` field without expanding `<details>`. |
| `internal/promptcapture/capture_test.go` (ported smoke) | End-to-end against real `claude`; gated by `testing.Short()` like today. |
| `cmd/eidos/forge/prompt_dump_test.go` | Host wrapper: container-not-running → clear error; container-running → invokes `ContainerExec` with expected argv; `-o snap` writes both `snap.json` and `snap.md` (uses a fake `forgectl.Client`, existing pattern). |
| `cmd/eidos/forge/prompt_dump_incontainer_test.go` | In-container worker: stub `claudeBin` parameter accepts a fake binary that posts a canned `/v1/messages` and exits; verifies `captured_from.{model,identity_path,bare}` values; verifies `identity.md`-missing and `model`-unset edge cases. |
| Integration (`-tags=integration`) | Spin a fake `claude` (reuse `internal/agentloop/testfake/`) inside a real mindform container; run the full host→exec→in-container→envelope→host flow; assert envelope's `captured_from.mindform` matches the container name. |

The existing `utils/promptdump` smoke test is the model; we lift it into `internal/promptcapture` so both surfaces are covered by one path.

## Doc updates

- `README.md` — add `eidos forge prompt-dump <name>` to the inspection commands listed in the quick-start / observability section.
- `utils/promptdump/README.md` — rewrite the opening paragraph: this is the dev-only host shim; for capturing a real mindform's envelope use `eidos forge prompt-dump`. Keep the flag table and jq examples.
- `utils/CLAUDE.md` — note that the core has moved to `internal/promptcapture`; `utils/promptdump` is now a thin wrapper, `GOWORK=off` invariant unchanged. Document the `replace` directive (or fallback) chosen.
- `CLAUDE.md` (project root) — extend the inspection-tool family description to call out `forge watch` (response side) vs `forge prompt-dump` (request side, snapshot).

## Long-help text (host-side `--help`)

```
eidos forge prompt-dump <name>

Capture the /v1/messages request envelope this mind-form's claude would send
right now: the default system prompt, tool catalogue, the layered identity.md,
the ontology's CLAUDE.md, the configured model, and the first-message
ambient context block.

This is a one-shot snapshot of a fresh session — it does not affect the
running agent-loop and does not include the rolling conversation history of
in-flight turns. For that, use `forge watch` (the response side) and read
runtime-state for the current session UUID and turn count.

The capture runs inside the mind-form's container, using the mind-form's own
identity.md, config.toml, ontology CLAUDE.md, and claude binary. The result
is captured by a small in-container loopback proxy and returned to the host.

Examples:
  eidos forge prompt-dump alice                # JSON envelope to stdout
  eidos forge prompt-dump alice -o snap        # writes snap.json + snap.md
  eidos forge prompt-dump alice -o snap.md     # Markdown only
  eidos forge prompt-dump alice --bare         # skip --append-system-prompt
```

## Out of scope (named, not promised)

- **Live tail of every outgoing request.** Different operational model: needs an always-on proxy sidecar in each running mindform. Defer; revisit if multiple operators ask for it.
- **Snapshot of a prefab before summoning.** "What would mindform X look like if I summoned it from prefab Y." Useful for prefab authors. Would reuse `internal/promptcapture` against a temporary container or a host-side claude with an ontology bind-mount. Defer.
- **`forge status`-from-inside-container symmetry.** Surfaced during this design but separate concern; tracked as a follow-up to bring `forge status` under the same IPC method table as `state.get`, per the "operator/mindform symmetry" principle in the project `CLAUDE.md`.
