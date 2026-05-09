# NIP-17 Inner-Content Envelope v1

**Date**: 2026-05-07
**Status**: Approved (pending implementation)
**Scope**: Replace the current "raw text in NIP-17 rumor `.content`" wire format with a versioned, structured JSON envelope. v1 carries chat messages and operator-controlled commands. Foundation for future features (peer-RPC, attachments, threading).

## 1. Problem statement

Today, MindGate puts the user's text directly into the NIP-17 rumor (`kind:14`) `.content` field. This works for chat-only social messaging but cannot grow:

- No way to distinguish "social message to mind-form" from "operator command to gate" — the gate cannot offer slash-commands without ambiguity.
- No version field — once peers exist on the wire, future schema changes have no migration anchor.
- No client identification for debugging / interop diagnostics.
- Future features (attachments, threading, peer mind-form RPC) have nowhere to land.

OpenClaw's "BodyForAgent vs CommandBody" split inspired the direction: the receiver should know whether a message is a social/agent-facing chat or a parser-facing control instruction, so the two can be routed to different consumers.

## 2. Design intent

Three commitments shape the rest of the spec:

1. **Structured-only on the wire.** Receivers do not auto-detect or fall back to plain text. A rumor whose `.content` does not parse as a valid v1 envelope is **soft-rejected** (stored for observability, never executed, never delivered to the mind-form). This eliminates parser ambiguity and keeps the receive path single-branched.

2. **Minimal v1, explicit Deferred.** Only the fields required by the v1 use cases ship now. Speculative fields are listed in §10 with the consumer that would justify adding them.

3. **`v` + strict schema = forward compatibility anchor.** Future schema changes bump `v`; v1 readers reject `v != 1` with a distinct soft-reject reason so operators see "your peer is on a newer version."

## 3. Wire format

NIP-17 `kind:14` rumor `.content` MUST be a JSON object matching:

```json
{
  "v": 1,
  "type": "chat" | "command" | "ack",
  "text": "string",
  "command": { "name": "string", "args": object },
  "ref": "string",
  "client": { "name": "string", "ver": "string" }
}
```

Field semantics:

| Field | Required | Notes |
|---|---|---|
| `v` | yes | Schema version. v1 ships `v: 1`. Integer. |
| `type` | yes | Discriminator. v1 enum: `chat`, `command`, `ack`. Unknown values → soft-reject (`schema_violation`). |
| `text` | conditional | Required when `type=chat`. RECOMMENDED when `type=command` (operator audit trail in inbox display, e.g., `"/status"`). Free-form UTF-8 string. |
| `command` | conditional | Required when `type=command`; MUST be absent when `type=chat` or `type=ack`. Object with `name` (non-empty string) and `args` (object; MUST be present, MAY be empty `{}`). |
| `ref` | conditional | Required when `type=ack`; MUST be absent (or empty) for other types. The 64-character lowercase-hex inner rumor id of the message being acknowledged. See `2026-05-09-message-delivery-status-design.md` for the tier-2 delivery acknowledgement design. |
| `client` | optional but recommended | Sender identification. Object with `name` (string, e.g., `"eidos"`) and `ver` (string, e.g., `"0.3.0"`). No capability field in v1. |

The `.content` string is the canonical JSON serialization of this object. Field order is not significant. Whitespace within strings is preserved; whitespace between tokens is not.

### 3.1 Validation rules

A receiver's envelope decoder MUST reject (return error) when any of these hold:

- `.content` does not parse as a JSON object → `ErrNotEnvelope`
- Object lacks `v` or `type` → `ErrNotEnvelope` (treated as "not envelope-shaped" rather than "malformed envelope" — distinguishes random JSON from intentional but broken envelopes)
- `v` is not the integer `1` → `ErrUnsupportedVersion`
- `type` is not in `{chat, command, ack}` → `ErrSchemaViolation`
- `type=chat` and `text` is missing, not a string, or empty → `ErrSchemaViolation`
- `type=chat` and `command` is present → `ErrSchemaViolation`
- `type=command` and `command` is missing, or its `name` is not a non-empty string, or `args` is missing or not an object → `ErrSchemaViolation`
- `type=command` and `command.name` is unknown to the dispatcher → handled at dispatch (§5.4), **not** a decode-time error
- `type=ack` and `ref` is missing or not a 64-character lowercase-hex string → `ErrSchemaViolation`
- `type=ack` and `text` is non-empty, or `command` is present → `ErrSchemaViolation`
- `client` is present but not `{name: string, ver: string}` → `ErrSchemaViolation`

Unknown additional fields at the envelope top level → MUST be ignored (forward-compatibility for additive changes within v1, e.g., a future minor that adds an optional field; readers should still validate).

**Schema evolution.** New non-breaking types may land additively within a major version (`v` unchanged). Old peers running an older v1 that doesn't know the new type will hit the existing `default: return ErrSchemaViolation` branch and soft-reject the message — graceful degradation requires no code change on their side. Only breaking changes — field-semantics shifts on existing types, validation tightening, removed types — require bumping `v`. `type=ack` (added 2026-05-09) is the first additive type; see `2026-05-09-message-delivery-status-design.md`.

## 4. type=chat

A social message. The `text` is the message body and is the unit consumed by the receiving mind-form's wake context (eventually — see §11 for forge integration scope). v1 has no annotation on chat messages: no threading, no attachments, no reactions.

`text` MUST be present and non-empty. (Empty-text chats are rejected as `ErrSchemaViolation`.)

## 5. type=command

### 5.1 Authority

A `type=command` envelope is executed by the receiving daemon **only** when the sender pubkey equals the receiver's own pubkey. All other senders → soft-reject with reason `unauthorized_command`. Authorization check is performed after NIP-17 unwrap, since the inner rumor's `pubkey` is the authoritative sender identity.

This rule is intentionally stricter than the contacts tier system. Commands are operator-control of the operator's own daemon. The use case is: "I'm on my phone via thin client (NIP-46 remote signer), and I want to issue a command to my home daemon." Both endpoints share the same npub — this rule fits naturally.

Other contacts (master, friend, etc.) sending `type=command` — even master tier — are soft-rejected. If we later need delegation (a trusted operator running diagnostics on a friend's daemon), it goes through a separate authorization mechanism, not the tier system.

### 5.2 Command set

v1 ships exactly one command: `status`. Read-only, no side effects, safe to expose first.

```json
{
  "v": 1,
  "type": "command",
  "text": "/status",
  "command": { "name": "status", "args": {} },
  "client": { "name": "eidos", "ver": "0.3.0" }
}
```

### 5.3 `status` semantics

When the daemon receives an authorized `status` command, it constructs a reply envelope of `type=chat` and sends it back via NIP-17 to the sender (which is itself).

Reply text (format is human-readable; not a stable machine-readable contract):

```
eidos-gate v0.3.0  uptime 3d 4h 12m
contacts: 1 master, 4 friend, 0 acquaintance, 0 blocked
relays:   2 configured
forge:    n/a (forge subcommand not yet integrated)
```

v1 reports `relays:` as a configured count (cardinality of `own_relays`).
Live connection state and inbox unread/last-received summaries are
deferred — the latter requires "last read" tracking that does not yet
exist; the former requires exposing subscriber state. Both can be added
without bumping `v` since the reply text is a non-stable contract.

The reply is a normal `type=chat` envelope. Because the sender is self, the reply lands in the operator's own inbox and is trivially distinguishable from peer messages by sender pubkey.

**v1 has no machine-readable structured-data form for command results.** §10 records this as a known limitation and the conditions under which v2 would address it.

### 5.4 Unknown commands

A schema-valid envelope with `type=command` and an unrecognized `command.name` (e.g., `name: "wake-now"` arriving at a v1 daemon) → soft-reject with reason `unknown_command`. The reply is **not** sent (we don't notify on unknown commands; the dashboard / inbox view surfaces these for the operator).

Adding new commands later does NOT bump `v`. New `name` values are additive; v1 readers ignoring an unknown name is correct behavior.

## 6. Soft-reject behavior

For any inbound NIP-17 `kind:14` rumor that fails any validation step, the daemon:

1. Appends to the inbox JSONL with structured rejection metadata (see §7).
2. Surfaces a marker in `eidos gate inbox` output and (eventually) in the dashboard.
3. Does **not** trigger mind-form wake.
4. Does **not** execute commands.
5. Does **not** auto-reply (avoids being misused as a reflection amplifier).
6. Logs one line at INFO level. Implementation SHOULD rate-limit per sender pubkey to prevent log flood (e.g., 1 line per pubkey per minute); the exact rate is an implementation detail, not a spec requirement.

Rejection reasons enumerated:
- `not_envelope` — `.content` not parseable as v1 envelope shape
- `unsupported_version` — `v != 1`
- `schema_violation` — required field missing/wrong type/forbidden combination
- `unauthorized_command` — `type=command` from non-self sender
- `unknown_command` — schema-valid command but `name` not in v1 set

These are stored alongside the message so operators can debug interop issues.

## 7. Storage changes

### 7.1 Inbox JSONL

Existing `Message` struct fields are preserved. `content` continues to hold the raw rumor JSON string (the envelope itself). Two additive fields:

```go
type Message struct {
    // ... existing fields (v, EventID, InnerID, From, Kind, Content, RumorAt, ReceivedAt, Relays) ...
    Malformed       bool   `json:"malformed,omitempty"`
    RejectReason    string `json:"reject_reason,omitempty"` // one of the §6 reasons
}
```

Rule:
- Successfully decoded envelope → `Malformed=false`, `RejectReason=""`, `Content` is the raw envelope JSON.
- Soft-rejected → `Malformed=true`, `RejectReason=<reason>`, `Content` is the raw rumor content as received (whatever bytes were there, including non-JSON).

`Envelope` itself is not stored; it is parsed on read from `Content` when needed.

### 7.2 Outbox JSONL

Symmetrically, `Sent` records carry the envelope JSON in `content`. No `malformed` flag on outbox (we only emit valid envelopes; if encoding fails the send is aborted and never recorded).

### 7.3 Legacy data (pre-v1)

Pre-envelope rows have no `v` field anywhere in `.content` (it was raw text). On read, the inbox CLI / dashboard treats any row whose `Content` does not parse as a v1 envelope and does not have `Malformed=true` set as **legacy plain text**: render `Content` as-is, no envelope semantics. No migration is performed. This affects only inbox rendering; subscriber path operates on fresh events post-upgrade.

## 8. New module

### `internal/envelope/`

```go
package envelope

type Type string
const (
    TypeChat    Type = "chat"
    TypeCommand Type = "command"
)

type Envelope struct {
    V       int      `json:"v"`
    Type    Type     `json:"type"`
    Text    string   `json:"text,omitempty"`
    Command *Command `json:"command,omitempty"`
    Client  *Client  `json:"client,omitempty"`
}

type Command struct {
    Name string         `json:"name"`
    Args map[string]any `json:"args"`
}

type Client struct {
    Name string `json:"name"`
    Ver  string `json:"ver"`
}

// Decode parses a NIP-17 rumor content string into an Envelope.
// Returns sentinel errors for each failure mode (§6).
func Decode(content string) (Envelope, error)

// Encode serializes an Envelope into a NIP-17 rumor content string.
// Returns error if the envelope itself is invalid (e.g., empty text on chat).
func Encode(env Envelope) (string, error)

// Validate checks the envelope's invariants (used by Encode and as a
// preflight in tests). Same error sentinels as Decode.
func Validate(env Envelope) error

// Sentinel errors (matched with errors.Is).
var (
    ErrNotEnvelope         = errors.New("not envelope shape")
    ErrUnsupportedVersion  = errors.New("unsupported envelope version")
    ErrSchemaViolation     = errors.New("envelope schema violation")
)
```

Notes:
- `Args` is `map[string]any` to keep v1 unconstrained on per-command schemas. Each command implementation is responsible for unmarshaling its expected args.
- `Validate` is the authoritative reference for §3.1; both `Decode` and `Encode` route through it.

## 9. Changed modules

### 9.1 `internal/inbox/`

Subscriber (`runSubscriber` / `handleIncoming`) gains an envelope decode step **after** NIP-17 unwrap and **before** dispatch:

```
Unwrap → Decode envelope
  ├─ ok, type=chat:    persist with Malformed=false; mind-form wake path (existing behavior)
  ├─ ok, type=command, sender==self, known name:   execute; reply via SendChat
  ├─ ok, type=command, sender!=self:               persist Malformed=true, RejectReason=unauthorized_command
  ├─ ok, type=command, unknown name:               persist Malformed=true, RejectReason=unknown_command
  └─ decode error:                                 persist Malformed=true, RejectReason=<sentinel>
```

The `broadcastInbox` IPC event is fired in all persisted cases (so dashboards see soft-rejected items live). Mind-form wake is fired only on the chat path.

### 9.2 `cmd/eidos/gate/send.go`

`eidos gate send <npub> <text>` automatically wraps `text` in `{v:1, type:"chat", text, client:{...}}` before publishing. New flag `--command <name>` switches to `{v:1, type:"command", text:"/<name>", command:{name, args:{}}, client:{...}}`.

`--command` is the v1 minimum-viable interface. A more ergonomic `eidos gate cmd <npub> <name> [--arg key=value ...]` subcommand is deferred to a follow-up; no protocol change is required to add it later.

### 9.3 `internal/daemon/`

New command dispatcher. v1 registers exactly one handler: `status`. Dispatcher signature:

```go
type CommandHandler func(ctx context.Context, args map[string]any) (replyText string, err error)

type CommandRegistry struct { /* map[string]CommandHandler */ }
```

Handler returns the reply text. Daemon wraps it in a `type=chat` envelope and sends to the original sender (which is self).

### 9.4 `internal/nostr/` (NIP-17 wrap helpers)

Existing `nostr.Wrap` / `nostr.Unwrap` are unchanged — they operate on rumor objects, not envelope content. Envelope encoding/decoding is layered above.

## 10. Known limitations / v2 candidates

The items below are candidates that **would** coincide with the next version bump because they require breaking changes — new mandatory fields, modified semantics on existing types, or fields whose absence in a v1 peer would corrupt user-visible behavior. Strictly additive types and optional fields can land within v1 directly via the schema-evolution clause in §3.1.

- **Machine-readable command results.** v1 `status` reply is text-only. When the first **machine consumer** appears (peer mind-form RPC, MCP server exposure, programmatic dashboard widget), v2 will introduce either an optional top-level `data: any` field or a `command_result` type with `in_reply_to` correlation. The choice will be made when the consumer's needs are concrete.
- **Attachments.** Image / audio / file references via NIP-94 + blossom-style upload. Requires a separate spec for the upload pipeline.
- **Threading / `in_reply_to`.** Reply correlation for chat. Trigger: when the first UI demands it (likely the dashboard).
- **`tool_call` / `tool_result` types.** Peer mind-form RPC. Trigger: when a concrete agent-to-agent use case lands.
- **Capability negotiation (`client.caps`).** Per-receiver behavior switching. Trigger: more than one client implementation.
- **Reactions, edits, deletes.** Standard chat affordances.
- **Migration of historical inbox data.** v1 leaves pre-envelope rows alone; if a future export tool needs uniform shape, a one-shot migrator can synthesize legacy envelopes.

### Resolved in v1 additively

- **`type=ack`** — tier-2 delivery acknowledgement. See `2026-05-09-message-delivery-status-design.md`. Added without bumping `v` because old peers gracefully soft-reject the new type via the existing `default: ErrSchemaViolation` branch.

## 11. Future direction (recorded for context, not in v1 scope)

The downstream project that motivates much of this design is **`eidos-mcp`** — an MCP (Model Context Protocol) server exposing the gate's IPC surface as tool calls. Mind-forms running inside their docker container would talk to their gate via `eidos-mcp` rather than parsing envelopes themselves; the MCP server handles encoding/decoding, leaving the mind-form to operate at the high-level "send_message", "read_inbox", "list_contacts" tool layer.

Envelope v1 is a precondition for `eidos-mcp` because:
- The MCP server needs a stable wire-protocol contract to bridge.
- Adding `data` (or equivalent) in v2 will be driven by what `eidos-mcp` consumers (real LLM agents) actually need — the user / consumer pull is concrete by then, avoiding speculative design.

`eidos-mcp` is a separate project with its own spec; this section exists only to record the intent shaping v1's deferrals.

## 12. Testing

### 12.1 Unit tests (`internal/envelope/`)

Round-trip:
- chat with `text`, with and without `client`
- command with `status`, empty args
- command with unknown name (Encode succeeds; Decode succeeds; dispatcher rejects)

Reject:
- `.content = "hello"` → `ErrNotEnvelope`
- `{"foo":"bar"}` → `ErrNotEnvelope` (no `v` or `type`)
- `{"v":2,"type":"chat","text":"hi"}` → `ErrUnsupportedVersion`
- `{"v":1,"type":"unknown"}` → `ErrSchemaViolation`
- `{"v":1,"type":"chat"}` (missing text) → `ErrSchemaViolation`
- `{"v":1,"type":"chat","text":"","command":{"name":"x"}}` → `ErrSchemaViolation` (chat with command)
- `{"v":1,"type":"command","text":"x"}` (missing command) → `ErrSchemaViolation`
- `{"v":1,"type":"command","command":{"name":"x"}}` (missing `args`) → `ErrSchemaViolation` (Decode and Encode both require `args` to be present, even when empty)
- `{"v":1,"type":"command","command":{"name":"","args":{}}}` (empty name) → `ErrSchemaViolation`

### 12.2 Integration tests (`test/integration/`)

Two ephemeral daemons A and B in the existing test harness:
- A sends chat envelope → B receives, decodes; inbox row has `Malformed=false`, `Content` is envelope JSON.
- A sends command to itself (loopback) → A's daemon executes `status`, A's inbox receives a `type=chat` reply with formatted output.
- A sends command to B (foreign) → B persists with `Malformed=true`, `RejectReason=unauthorized_command`. No reply.
- Plain-text rumor (legacy peer) → recipient persists with `Malformed=true`, `RejectReason=not_envelope`.
- `v=2` rumor (synthetic future-version peer) → recipient persists with `Malformed=true`, `RejectReason=unsupported_version`.

### 12.3 Backwards-compat read

Unit test for the inbox renderer: a JSONL row without `Malformed` field and whose `Content` is plain text → renders as legacy.

## 13. Migration / rollout

Single coordinated bump:
1. Daemon update: outbound now emits envelopes; inbound enforces envelope-only.
2. CHANGELOG entry under `feat`: "MindGate now uses a structured message envelope; messages from pre-envelope peers will be soft-rejected and visible in `eidos gate inbox` with a malformed marker."
3. Document the cutoff version in README.

Non-coordinated (a v1 daemon talks to a pre-v1 daemon):
- v1 → pre-v1: pre-v1 sees raw envelope JSON as `.content`. It will display the literal JSON to the operator, who sees something is off. Acceptable transitional UX.
- pre-v1 → v1: v1 soft-rejects with `not_envelope`. Operator sees the offending peer in inbox view with a marker.

After all known peers upgrade, malformed rejections drop to zero. Persistent rejections from a specific pubkey indicate either a hostile sender or a stale peer needing notification.

## 14. Out of scope for this spec

- Implementation of any command beyond `status`. New commands ship as separate work items.
- Changes to NIP-17 wrap/unwrap mechanics.
- Changes to the contacts tier model.
- Web dashboard rendering of envelope fields. (Dashboard is a separate spec; envelope v1 is a precondition only insofar as the dashboard must understand the new `Malformed` and `RejectReason` fields when it lands.)
- `eidos-mcp` itself.

## 15. Acceptance criteria

- `internal/envelope/` package exists, exports `Encode`/`Decode`/`Validate` and the three sentinel errors, with unit tests covering §12.1.
- `internal/inbox/Message` gains `Malformed` and `RejectReason` fields; existing JSONL rows without these fields read correctly.
- `internal/inbox` subscriber implements the §9.1 dispatch table; integration tests in §12.2 pass.
- `cmd/eidos/gate/send.go` wraps text into envelopes; `--command status` flag works end-to-end.
- `internal/daemon/` registers and dispatches `status` command; reply lands in sender's inbox.
- `eidos gate inbox` displays soft-rejected rows with their reason (e.g., `[malformed: unsupported_version]` prefix).
- `go test ./...` and `go test -tags=integration ./...` both pass.
- README / USAGE.md updated to describe `--command` flag and to note the wire-format change.
