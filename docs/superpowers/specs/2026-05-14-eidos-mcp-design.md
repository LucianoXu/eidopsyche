# eidos-mcp — Design

**Date:** 2026-05-14
**Status:** Approved (brainstorming)
**Scope:** new `cmd/eidos/mcp/`, new `internal/mcp/`, new `docker/mindform/mcp.json`,
edits to `internal/agentloop/spawn.go`, `internal/prompts/assets/system1-instructions.txt`,
`docs/USAGE.md`, `go.mod`/`go.sum`.

## Goal

Give the mind-form a typed, discoverable tool surface for operating
on its own MindForge framework state — sending MindGate messages,
managing contacts, reading inbox/outbox, adjusting config, querying
state. Today the only way for the mind-form to reach the daemon is
to `Bash`-shell out to `eidos gate ...` strings; the system prompt
itself acknowledges this gap:

> "(To be added: tools for sending messages through MindGate, querying
> contacts, adjusting your own scheduling. For now use the host CLI via
> Bash where needed.)" — `internal/prompts/assets/system1-instructions.txt:38`

`eidos-mcp` closes that gap by wrapping the daemon's unified IPC
method table as MCP tools the mind-form's Claude Code session sees
natively in its tool catalogue.

The MCP server is also offered (same binary, same subcommand) to the
operator's host Claude Code session, in service of the
[Operator/mindform symmetry][unified-call-path] principle: every
adjustment and every read in the same state schema is reachable
through the same surface regardless of caller context.

[unified-call-path]: ./2026-05-09-unified-call-path-design.md

## Non-Goals

- **MCP for clients other than Claude Code.** The transport is stdio
  and follows the MCP spec; in theory any MCP client works. We do not
  promise compatibility testing against any client other than Claude
  Code in this iteration.
- **Push notifications for state.changed.** MCP's server→client
  notification channel is unused in v1. The mind-form is already
  woken on the events that matter (HeartBeat, inbound MindGate
  message); tool-result is the natural ack for self-induced changes.
  Future hook noted under "Future work".
- **Streaming subscriptions (`inbox.tail` analogue).** Tool calls
  are request/response. Streaming consumers stay on the existing
  agent-loop wake mechanism.
- **Per-tool capability gating beyond daemon Contexts.** The MCP
  server inherits the daemon's UID-trust + Context bitmask. There is
  no per-tool ACL layer at the MCP boundary; whoever can dial the
  socket can call any registered tool.
- **Wrapping host-only orchestration verbs** (`forge.upgrade`,
  `forge.workspace.*`, `daemon.exec-replace`, `lifecycle.run`). These
  remain CLI-only. See "Tool surface" for the curation rationale.
- **Replacing `Bash` for the mind-form.** Bash is in the
  [tool allowlist][tool-allowlist] and stays. `eidos-mcp` is the
  preferred path for state operations; Bash remains the escape hatch
  for everything else (file ops, `git`, custom scripts).

[tool-allowlist]: ./2026-05-14-mindform-tool-allowlist-design.md

## Current state (as built)

- Daemon IPC method table at `internal/daemon/methods.go:22-73`
  registers ~32 methods across 7 domains. `internal/daemon/handler.go`
  dispatches by name; `internal/ipc/protocol.go` defines the wire
  envelope.
- All surfaces converge on this table today: the CLI
  (`cmd/eidos/gate/*`) calls via `ipc.Client.Call` over the unix
  socket, and the dashboard adapter (`internal/daemon/dashboard_adapter.go`)
  calls via in-process `daemon.Call` against the same handler set.
- The mind-form's Claude session has no MCP servers configured today.
  `internal/agentloop/spawn.go:128` builds the claude argv with no
  `--mcp-config` flag. The mind-form interacts with the daemon only
  by shelling out to `eidos gate ...` via the `Bash` built-in.
- Each mind-form container runs its own gate daemon — supervisor
  (PID-1, `cmd/eidos/supervisor/run.go:92`) spawns `eidos gate` as a
  child. The socket lives at `/eidos/gate/sock`.
- The project has no Go MCP dependencies. `internal/transcript/events.go:4`
  parses an `MCPServers` field from claude's `system-init` event but
  no server-side MCP code exists.

## Design

### Library

Adopt the official Anthropic Go SDK:

```
github.com/modelcontextprotocol/go-sdk    v1.6.0+  (Apache 2.0 / MIT)
```

Rationale:

- License-compatible with the project's Apache 2.0 (the SDK is
  Apache-2.0 for new contributions and MIT for pre-existing code;
  neither imposes copyleft).
- Stable v1; protocol revs land in the SDK without forcing surface
  changes on us.
- `mcp.StdioTransport{}` + `mcp.AddTool` is a ~one-line stdio server
  plus one line per tool registration.
- Schema is generated from Go struct tags (`json:"..."
  jsonschema:"..."`), matching idiom — no separate schema language.

Hand-rolling is rejected: ~300-500 LOC just for the protocol
plumbing (initialize / tools/list / tools/call / capability
negotiation / framing) before any tool logic, and forfeiture of free
upgrades as the spec revs.

`mark3labs/mcp-go` (community SDK, more popular by stars but
non-official) is rejected on tie-break: when an official SDK exists
and is healthy, we use it.

### Topology

Stdio. The MCP server is a short-lived subprocess of the Claude
Code session that spawned it; lifetime is bounded by the claude
session.

Inside a mind-form container:

```
supervisor (PID 1)
├─ eidos gate (long-lived daemon, owns /eidos/gate/sock)
└─ eidos agentloop
    └─ claude
        └─ eidos mcp    ← spawned by claude as MCP subprocess
            └─ dials /eidos/gate/sock via ipc.Client
```

On the operator's host machine (symmetric path):

```
user shell
└─ claude  (Claude Code, started by the operator)
    └─ eidos mcp
        └─ dials ~/.config/eidos/sock (or $EIDOS_GATE_HOME/sock)
```

Trade-offs evaluated and recorded for posterity:

- **HTTP transport** would add no capability the design needs
  (single client per session, single container per claude, no
  cross-process sharing). Adopting HTTP would mean an extra
  long-lived server, an extra port, and an auth surface inside the
  container without producing a benefit. Auto-upgrade-style features
  do not need HTTP — the hard problem there is cross-container
  signaling, which transport does not address.
- **Per-wake process restart cost** (Go cold start ~20ms) is
  negligible against any meaningful agent action.
- The decision is reversible: tool handlers are pure
  parameter-packing over `ipc.Client.Call`; if HTTP ever becomes
  necessary, the I/O loop is the only thing that changes.

### Symmetry — same binary, both contexts

`eidos mcp` is a generic subcommand. It does not know whether it is
running on a host or inside a container. It resolves its IPC socket
the same way `eidos gate <subcommand>` resolves it today
(`internal/config.Config.SocketPath()`, env `EIDOS_GATE_HOME`).
Whatever daemon picks up the socket sets the operating Context;
the MCP server is content to be a thin adapter.

Tool catalogue is a **static full set** regardless of the daemon's
Context. Two methods in the catalogue (`eidos_agent_state`,
`eidos_lifecycle_status`) only make sense in `ContainerCtx`. When the
operator's host claude calls them, the daemon responds with
`CONTEXT_MISMATCH`; the MCP server passes the error through verbatim.
The error message is informative enough (it includes the verb hint
from `internal/daemon/mutate.go:86-99`) that the LLM can recover.

Rejected alternative: dynamic catalogue (probe `service.status` at
MCP startup, register only context-appropriate tools). Rejected
because (a) it forces daemon-availability at MCP startup, coupling
process lifetimes; (b) the UX win is marginal — same daemon-side
error path either way; (c) static catalogue is testable as a
fixed-shape unit.

### Tool surface — the 21

Curated 1:1 with the daemon method table, with deliberate omissions.

| Domain | MCP tool | Wraps IPC method | Effective context |
|---|---|---|---|
| Identity | `eidos_set_label` | `set-label` | Both |
|  | `eidos_service_status` | `service.status` | Both |
|  | `eidos_agent_state` | `agent.state` | Container |
| Contacts | `eidos_contact_add` | `contact.add` | Both |
|  | `eidos_contact_add_from_card` | `contact.add-from-card` | Both |
|  | `eidos_contact_remove` | `contact.remove` | Both |
|  | `eidos_contact_set_label` | `contact.set-label` | Both |
|  | `eidos_contact_set_tier` | `contact.set-tier` | Both |
| Cards | `eidos_card_parse` | `card.parse` | Both |
|  | `eidos_card_scan` | `card.scan` | Both |
| Messaging | `eidos_send` | `send` | Both |
|  | `eidos_inbox_list` | `inbox.list` | Both |
|  | `eidos_outbox_list` | `outbox.list` | Both |
| Invites | `eidos_invite_create` | `invite.create` | Both |
|  | `eidos_invite_list` | `invite.list` | Both |
|  | `eidos_invite_revoke` | `invite.revoke` | Both |
| Relays | `eidos_relay_add` | `relay.add` | Both |
|  | `eidos_relay_remove` | `relay.remove` | Both |
| Config | `eidos_config_set` | `config.set` | Both (specific keys may be `HostCtx`/`ContainerCtx` per `internal/config/keys.go`; daemon rejects mismatch) |
| State | `eidos_state_get` | `state.get` | Both |
| Lifecycle | `eidos_lifecycle_status` | `lifecycle.status` | Container |

**Deliberately not exposed:**

| Excluded | Reason |
|---|---|
| `forge.upgrade` | Host-only orchestration verb; mind-form can't upgrade its own container from inside. |
| `forge.workspace.add` / `.list` / `.remove` | Host-side container-config plumbing; not in mind-form's mental model. |
| `daemon.exec-replace` | Admin-grade self-binary swap; not an agent-tool concern. |
| `lifecycle.run` | Accepts arbitrary CLI argv — equivalent to `Bash` + the host CLI, which the mind-form already has. Avoids two paths for the same capability. |
| `inbox.tail` | Streaming subscription; mind-form already wakes on inbound messages. Re-poll via `eidos_inbox_list` if needed. |
| `relay.list` / `contact.list` (legacy) | Subsumed by `eidos_state_get` on `relays` / `contacts`. |
| State subtree wrappers (`eidos_get_contacts`, `eidos_get_config`, …) | Single `eidos_state_get(path)` covers the entire read schema. Avoid catalogue bloat. |

The principle is: **a method enters the MCP catalogue when it is a
write/action verb appropriate for the agent OR a filtered list that
`state.get` cannot express**. Pure reads route through
`eidos_state_get`. Future IPC methods that the mind-form should
reach get an MCP wrapper as part of the same PR — the conscious
case-by-case admission is a feature, not a maintenance burden.

### Schema source of truth — independent

Each MCP tool defines its own `Input` and `Output` Go structs in
`internal/mcp/tools_*.go`. The handler converts `Input` to the
daemon's `Params` value and calls via `ipc.Client.Call`. Two reasons
to keep them independent:

- **Agent-facing wording.** Tool descriptions and field
  `jsonschema:"..."` tags are written for an LLM reader, not for
  CLI parity. We may rename `to_hex` → `to`, hide advanced fields
  (`relays` on `eidos_send`), add ergonomic aliases ("npub or
  contact label"), and so on, without disturbing the daemon's wire
  schema.
- **Two-layer evolution.** The daemon's IPC schema serves CLI +
  dashboard + future MCP; tying them to the LLM-tuned MCP surface
  would force every CLI-facing change to pass an LLM-UX review.

Sketch:

```go
// internal/mcp/tools_messaging.go
type SendInput struct {
    To   string `json:"to" jsonschema:"npub or contact label of recipient"`
    Body string `json:"body" jsonschema:"message text"`
}
type SendOutput struct {
    EventID    string   `json:"event_id"`
    AcceptedBy []string `json:"accepted_by"`
}

func (s *Server) eidosSend(ctx context.Context, _ *mcp.CallToolRequest, in SendInput) (*mcp.CallToolResult, SendOutput, error) {
    var out SendOutput
    err := s.client.Call(ctx, "send", map[string]any{
        "to":   in.To,
        "body": in.Body,
    }, &out)
    return nil, out, err
}
```

The conversion is by hand. The 21 tool count makes the maintenance
load tolerable; we are not generating code.

### Error mapping

The IPC client returns `*ipc.Error{Code, Message}` for daemon-side
errors and `error` for transport-side. The MCP handler renders both
as a non-nil `error` return from the typed handler, which the SDK
converts to `CallToolResult{IsError: true, Content: [{Text: ...}]}`
per SDK convention (verified during implementation against the SDK
version pinned in `go.mod`). The text format we adopt:

```
[<CODE>] <message>
```

Examples the model will see:

- `[CONTACT_NOT_FOUND] no contact with label "alice"`
- `[CONTEXT_MISMATCH] lifecycle.status requires container context`
- `[INVALID_PARAMS] body must be non-empty`
- `[IPC_DOWN] daemon unreachable at /eidos/gate/sock` (transport)

Including the code prefix lets the LLM pattern-match recovery
strategies (e.g. on `CONTACT_NOT_FOUND`, run `eidos_contact_add`
first). The daemon already has a stable code vocabulary in
`internal/ipc/protocol.go:22-32` and per-method handlers; we
reuse it.

### Code layout

```
cmd/eidos/mcp/
  cmd.go                # cobra entry; resolves socket path, builds Server, runs stdio.
internal/mcp/
  server.go             # SDK setup, tool registration, signal handling, lifecycle.
  client.go             # thin wrapper over internal/ipc.Client with sensible
                        # call timeout default; centralises error translation.
  tools_identity.go     # eidos_set_label, eidos_service_status, eidos_agent_state
  tools_contacts.go     # eidos_contact_* and eidos_card_*
  tools_messaging.go    # eidos_send, eidos_inbox_list, eidos_outbox_list
  tools_invites.go      # eidos_invite_*
  tools_relays.go       # eidos_relay_*
  tools_config.go       # eidos_config_set
  tools_state.go        # eidos_state_get
  tools_lifecycle.go    # eidos_lifecycle_status
  tools_test.go         # table-driven: input → fake ipc → expected output / error.
docker/mindform/
  mcp.json              # baked into image; COPYed to /etc/eidos/mcp.json.
```

Edits:

```
cmd/eidos/main.go                              # register `mcp` subcommand
internal/agentloop/spawn.go                    # +--mcp-config /etc/eidos/mcp.json
internal/prompts/assets/system1-instructions.txt   # replace placeholder line
docker/mindform/Dockerfile                     # COPY docker/mindform/mcp.json → /etc/eidos/mcp.json
docs/USAGE.md                                  # host-operator mcp.json snippet
go.mod, go.sum                                 # add modelcontextprotocol/go-sdk
```

### .mcp.json placement

**Container.** A static file baked into the mind-form image:

```json
{
  "mcpServers": {
    "eidos": { "command": "eidos", "args": ["mcp"] }
  }
}
```

Stored at `docker/mindform/mcp.json` in the repo, copied to
`/etc/eidos/mcp.json` in the image via `Dockerfile`'s final stage.

`internal/agentloop/spawn.go:128` `buildClaudeArgs` appends
`--mcp-config /etc/eidos/mcp.json` to the claude argv. Path is fixed
because the image owns it; no config plumbing.

The other two claude-spawning sites — `cmd/eidos/supervisor/birth.go`
(birth-event one-shot, no tool loop runs against eidos-mcp; we
deliberately do NOT wire MCP into birth) and
`internal/promptcapture/capture.go` (prompt-dump capture — we DO
want the dump to reflect the runtime tool surface, so wire it in
there) — see "Spawn integration" below for the per-site decision.

**Host (operator).** Operator-supplied. `docs/USAGE.md` documents
the snippet for `~/.claude.json`, project-local `.mcp.json`, or
`claude --mcp-config <path>`. Same JSON, same binary.

### Spawn integration — three call sites, three answers

| Spawn site | Add `--mcp-config`? | Why |
|---|---|---|
| `internal/agentloop/spawn.go:128` `buildClaudeArgs` | **Yes** | The long-lived agent-loop is the primary consumer; the mind-form's daily tool surface. |
| `internal/promptcapture/capture.go:137` `buildClaudeArgs` | **Yes** | Prompt-dump must reflect the runtime tool surface or it is misleading. The MCP tool list shows up in the captured system-init envelope's `tools` array. |
| `cmd/eidos/supervisor/birth.go:131` (one-shot birth `claude -p ...`) | **No** | Birth is `-p`-mode JSON output, no tool loop runs; loading MCP servers adds startup cost with no consumer. (Mirrors the same call-site's `--tools` decision in the tool-allowlist design.) |
| `internal/firstcontact/claude.go:40` (first-contact research probe) | **No** | Same reasoning: `-p`-mode, no tool loop. |

### System prompt update

`internal/prompts/assets/system1-instructions.txt:38` (the
"To be added: ..." placeholder line) is replaced with a paragraph
that introduces the surface and sets habits. Draft text — final
wording is part of the implementation PR:

> You have a set of MCP tools, all prefixed `eidos_`, that operate on
> your MindForge framework state directly. Use them in preference to
> `Bash` + `eidos gate ...`: they reach the same daemon underneath,
> but the typed surface is friendlier for tool calls.
>
> - **Messaging:** `eidos_send` to send a MindGate message;
>   `eidos_inbox_list` and `eidos_outbox_list` to inspect message
>   history.
> - **Contacts:** `eidos_contact_add` / `_add_from_card`, `_remove`,
>   `_set_label`, `_set_tier`. `eidos_card_scan` parses a
>   `mindgate://...` URI and tells you whether the npub is already a
>   contact.
> - **Invites:** `eidos_invite_create` / `_list` / `_revoke`.
> - **Relays:** `eidos_relay_add` / `_remove`.
> - **Self:** `eidos_set_label` to change your advertised label;
>   `eidos_config_set` to adjust a registered config key;
>   `eidos_agent_state` to see your own claude-busy flag.
> - **State reads:** `eidos_state_get <path>` is the unified read
>   tool — `state.get contacts`, `state.get config.heartbeat.interval`,
>   `state.get inbox.recent`, and so on. Use it instead of asking the
>   filesystem for what the daemon already knows.
>
> Write operations are validated against the daemon's Context / key
> registry. If you try something that belongs to the host side, you
> will get a clear `[CONTEXT_MISMATCH]` error with a verb hint —
> propagate it to the operator if needed, do not try to work around
> it.

The exact wording will be tightened during implementation in the
same PR that registers the tools, so prompt and tool catalogue stay
in lock-step.

## Data flow

```
                ┌───────────────────────────────┐
                │ internal/mcp                  │
                │   server.go (SDK setup)       │
                │   client.go (ipc wrapper)     │
                │   tools_*.go (21 handlers)    │
                └───────────────┬───────────────┘
                                │ mcp.AddTool
                                ▼
                ┌───────────────────────────────┐
                │ modelcontextprotocol/go-sdk   │
                │   stdio transport             │
                │   JSON-RPC 2.0                │
                └───────────────┬───────────────┘
                                │ stdin/stdout
                                ▼
                            claude CLI
                       (parent: agentloop /
                        promptcapture / host)
                                │
                                ▼  (decisions)
                        LLM tool calls
                                │
                                ▼
                        internal/mcp handlers
                                │ ipc.Client.Call
                                ▼
                /eidos/gate/sock  (in container)
            or  $EIDOS_GATE_HOME/sock  (on host)
                                ▼
                        eidos gate (daemon)
                       methodTable dispatcher
                                │
                                ▼
                        internal/daemon/methods_*.go
                                │
                                ▼
                        state.changed events,
                        Mutate hooks, persisted state
```

## Error handling

- **Daemon down.** `ipc.Dial` fails → MCP handler returns
  `[IPC_DOWN] daemon unreachable at <socket>`. Claude receives this
  per-call; the MCP server itself does not exit. (We do not pre-dial
  at server startup — first tool call is the first connect.)
- **Daemon mid-call disconnect.** `ipc.Client.Call` returns a
  transport error. Same shape; tool error.
- **Per-call timeout.** Default 10s on `ipc.Client.Call`,
  overridable per tool in `internal/mcp/client.go`. State reads stay
  at default; `eidos_send` may need a longer ceiling because relay
  publish has tail latency. Per-tool overrides are declared in the
  tool file.
- **Daemon returns typed error.** `[CODE] message` text, model can
  pattern-match.
- **stdin/stdout EOF / SIGTERM / SIGINT.** SDK handles transport
  shutdown. We add a context-cancellation in `cmd/eidos/mcp/cmd.go`
  so in-flight IPC calls return promptly. No persistent state to
  flush.
- **Daemon's `state.changed` events.** Discarded for now. The MCP
  server does not subscribe; nothing is propagated to claude. See
  "Future work".

## Testing

**Unit:**

- `internal/mcp/tools_test.go` — table-driven per tool: given
  `Input`, the handler calls the right IPC method with the right
  params, and translates the result. The fake `ipc.Client` records
  `(method, params)` and returns canned `(result, err)`.
- Schema golden tests: `mcp.Tool` JSON schema for each handler is
  asserted against a golden file. Catches accidental tag drift or
  required-field changes.
- Error mapping: `[CODE] message` text format covered for at least
  one of each: typed daemon error, transport error, context
  mismatch.

**Integration (build tag `integration`):**

- `internal/mcp/integration_test.go` (new): spin up a real
  `eidos gate` daemon on a temp socket, run `eidos mcp` against it
  via stdio (a tiny in-test MCP client), call `eidos_set_label` +
  `eidos_state_get identity`, assert the label round-trips and the
  daemon emits the expected `state.changed:identity.label` event.
- `cmd/eidos/forge/prompt_dump_mcp_test.go` (new, integration tag):
  prompt-dump the `_test_fixture` mind-form, assert the captured
  envelope's `mcp_servers` array contains `eidos` and the `tools`
  array exposed to the model includes every `eidos_*` we expect.

**Manual:**

- `eidos forge create test && eidos forge start test &&
   eidos forge prompt-dump test` — inspect the captured tool
  catalogue.
- `eidos forge watch test` while issuing a manual wake — confirm
  the mind-form picks an `eidos_*` tool over a `Bash` shellout
  when both could solve a task (UX outcome, not a correctness check).

## Future work

- **`state.changed` push.** Subscribe in eidos-mcp to relevant
  daemon events and surface them as MCP resource updates or
  notifications. Useful only if we discover an actual UX gap; the
  agent-loop wakes already cover the high-value events.
- **Mind-form-driven host orchestration.** A mind-form asking its
  host to `forge.upgrade` itself, change workspace mounts, etc.
  This needs a cross-container signaling channel (Nostr admin DM
  to operator, or a dedicated host↔container IPC), plus an explicit
  capability/consent model. Out of scope here; this design assumes
  host-only verbs stay host-only.
- **Per-tool ACL.** If we ever ship a "limited" mind-form mode
  (e.g. a delegated sub-agent that can read but not write), the
  enforcement point would be MCP-side: a filter over which tools
  are registered for that session's MCP server instance. The MCP
  server already has the right shape to host this — drop unwanted
  tools at registration time.
- **OperatorTUI / CLI completions.** `eidos mcp` could grow a
  `--list-tools` flag that prints the tool catalogue for
  documentation; nice-to-have, not on the critical path.

## Migration / rollout

Pre-production project — no migration. The change ships and takes
effect on next mind-form image build + next `eidos forge start`.
Existing running mind-forms pick up the new tool surface on their
next claude rotation (dream-end → `--session-id` reroll) or on
`eidos forge restart <name>`. The host-operator path requires the
operator to add the snippet to their own `.mcp.json` — documented
but voluntary.
