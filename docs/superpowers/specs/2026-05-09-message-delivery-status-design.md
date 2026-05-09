# Message delivery status — two tiers (closes #21)

- **Date**: 2026-05-09
- **Issue**: [#21 — gate: document the two scopes of message status — relay acceptance vs. recipient feedback](https://github.com/LucianoXu/eidopsyche/issues/21)
- **Touches**: `SPEC.md`, `internal/envelope`, `internal/inbox`, `internal/daemon`, `internal/dashboard`, `cmd/eidos/gate`, `docs/superpowers/specs/2026-05-07-envelope-v1-design.md`

## 1. Problem statement

Users naturally ask "did my message get through?" That question conflates two distinct facts:

1. **Relay acceptance** — N relays returned `OK` on publish. *Local truth*. Already captured in `internal/inbox/types.go` as `Sent.AcceptedBy` / `Sent.Final` and emitted by `internal/daemon/methods.go` after `Pool.Publish`. Not yet surfaced in either CLI or dashboard.
2. **Recipient feedback** — the peer's gate decoded our message and acknowledged it. Not yet committed to as a wire-protocol promise.

Without an explicit decision, UI work risks blurring the two — e.g., a green checkmark that means "accepted by relay" but reads as "delivered to peer". Nostr provides no native delivery ACK; tier-2 must be defined on top of NIP-17.

Issue #21 originally asked only for documentation. This spec expands scope: tier-2 has a concrete consumer now (the dashboard's `sent` / `delivered` indicator), so we both **document the model in SPEC.md and ship the implementation** in the same release.

## 2. Design intent

- **Two tiers, never one number.** UI surfaces them as two distinct visual states (`✓` and `✓✓`), and the SPEC subsection names them so the distinction is recoverable.
- **Tier 1 is local truth, tier 2 is a wire-protocol commitment.** Tier 1 reuses fields already on `Sent`. Tier 2 introduces a new envelope `type=ack` and a new daemon control-plane path on both sender and receiver.
- **Daemon-level "delivered", not "read".** The ack proves the receiver's gate unwrapped, validated, and persisted. It does *not* prove a mind-form or human consumed the message. "Read receipts" are out of scope.
- **Additive, not breaking.** envelope-v1 stays at `v: 1`; ack is a new type within v1. Old peers degrade gracefully: they soft-reject inbound ack as `schema_violation` and never emit one, so their senders see `✓` indefinitely — acceptable.
- **YAGNI on richer status.** A single ack state ("delivered") only. No `received` / `persisted` / `failed` gradations, no per-relay per-tier breakdown in the compact UI, no opt-out per contact tier — all gated behind real consumers.
- **No protocol commitment beyond what UI consumes.** `ack.ref` is the only payload field. No timestamp (the wrap event's `created_at` carries that), no status enum, no client metadata. Future fields require a real consumer.

## 3. Wire format — `type=ack`

### 3.1 Schema amendment to envelope-v1

Add to `internal/envelope/envelope.go`:

```go
const (
    TypeChat    Type = "chat"
    TypeCommand Type = "command"
    TypeAck     Type = "ack"   // new
)

type Envelope struct {
    V       int      `json:"v"`
    Type    Type     `json:"type"`
    Text    string   `json:"text,omitempty"`
    Command *Command `json:"command,omitempty"`
    Ref     string   `json:"ref,omitempty"`   // new — required for type=ack
    Client  *Client  `json:"client,omitempty"`
}
```

`SchemaVersion` stays `1`. The envelope-v1 spec (`docs/superpowers/specs/2026-05-07-envelope-v1-design.md`) is amended to clarify the additive-types extension contract: new non-breaking types may land within a major version; only field changes to existing types or breaking validation changes bump `v`.

Wire example:

```json
{"v":1,"type":"ack","ref":"7f3c1...e9b2"}
```

### 3.2 Validation rules

For `type=ack`:
- `ref` required, must be exactly 64 lowercase hex characters (matches Nostr event/rumor id shape).
- `text` forbidden, `command` forbidden.
- `client` optional (same as other types).

`Validate` returns `ErrSchemaViolation` for any violation. `Decode` of an ack from an old client (which doesn't know `TypeAck`) hits the existing `default: return ErrSchemaViolation` branch — no code change needed on old peers.

### 3.3 Soft-reject behavior on old peers

Old peers receiving an ack persist a row to `inbox.jsonl` with `Malformed=true, RejectReason="schema_violation"`. They do not display it as a chat. This is the existing v1 soft-reject path — no new code on old peers is required for graceful degradation. Cost: a small amount of malformed-row noise on old peers' inboxes during the rollout window. Documented in the migration section.

## 4. Receiver behavior — emitting ack

### 4.1 Trigger conditions

In `internal/daemon` on every successfully-decoded inbound NIP-17 event, after the existing inbox-persist path completes, the receiver emits an ack envelope **only when all** of:

1. The event was successfully unwrapped (we have a rumor).
2. The decoded envelope satisfies `Validate` and `envelope.Type ∈ {chat, command}`.
3. The inbox row was persisted with `Malformed=false` (i.e., not soft-rejected).
4. The sender (rumor `pubkey`) is whitelisted (any tier other than blocklist).
5. The sender is not self (suppresses self-copy ack-loop; `rumor.PubKey != d.Key.PublicHex`).

If any condition fails, no ack is emitted and the inbound row is left as-is.

### 4.2 Emit path

The receiver builds:

```go
ackEnv := envelope.Envelope{V: 1, Type: envelope.TypeAck, Ref: rumorID}
content, _ := envelope.Encode(ackEnv)
wrap, _, _ := nostr.Wrap(d.Key.PrivateHex, recipientPubkey, content)
d.Pool.Publish(ctx, urls, wrap)
```

The `urls` set is the union of:
- the receiver's `own_relays`,
- the recipient's `Relays` from the contacts table (the original sender's published inbox relays),
- `d.Cfg.Publish.FallbackRelays`.

This mirrors the existing `send` publish path. The ack is best-effort: no retry, no self-copy of the ack, no `Sent` row written for the ack on the receiver's side. If the publish fails on every relay, we log and move on — the sender's tier-2 status simply stays `✓`.

### 4.3 Suppressed cases

- **`type=ack` inbound** — never emit an ack-of-ack. Recursion stop. The receive-side ack handler (§5) consumes inbound acks; no further action.
- **Malformed / soft-rejected inbound** — no ack. We have nothing to commit to.
- **Self-copy** — sender's own loopback wrap arrives with `rumor.PubKey == d.Key.PublicHex`; skip.
- **Blocklisted sender** — already filtered out before persist; never reaches the ack-emit step.
- **Acquaintance / friend / master** — all ack equally. Per-tier opt-out is YAGNI; whitelist already gates engagement.

## 5. Sender behavior — recording ack

### 5.1 `Sent` extension

`internal/inbox/types.go`:

```go
type Sent struct {
    V           int      `json:"v"`
    EventID     string   `json:"event_id"`
    SelfEventID string   `json:"self_event_id"`
    InnerID     string   `json:"inner_id"`
    To          string   `json:"to"`
    Kind        int      `json:"kind"`
    Content     string   `json:"content"`
    RumorAt     int64    `json:"rumor_at"`
    SentAt      int64    `json:"sent_at"`
    AcceptedBy  []string `json:"accepted_by"`
    Final       bool     `json:"final,omitempty"`
    AckedAt     int64    `json:"acked_at,omitempty"`     // new
    AckEventID  string   `json:"ack_event_id,omitempty"` // new
}
```

`AckedAt` is the unix second the ack envelope's wrap event was received. `AckEventID` is the wrap event id of the ack — kept for audit / debugging. Both omitted-when-zero so old rows round-trip cleanly.

### 5.2 Inbound ack dispatch

In the daemon's inbound NIP-17 handler, after envelope decode, branch on `envelope.Type`:

```go
case envelope.TypeAck:
    s.handleInboundAck(rumor.PubKey, wrapEvent.ID, env.Ref)
    return  // do NOT write to inbox.jsonl, do NOT trigger wake
```

`handleInboundAck`:

1. Reject if `env.Ref` is not 64-hex (already enforced by `Validate`, but defensive).
2. Reject if `rumor.PubKey` is not in the whitelist (defense-in-depth).
3. Look up the matching `Sent` by `InnerID == env.Ref`. Use `Box.ListOutbox(nil, "", 0)` and search; the outbox is already daily-bucketed so this is bounded. If not found, debug-log and drop.
4. Verify `Sent.To == rumor.PubKey` — the ack must come from the original recipient. Otherwise drop with a warning (someone is acking a message we didn't send them).
5. If `Sent.AckedAt != 0`, drop (first-ack-wins, idempotent).
6. Append a delta `Sent` row keyed by the same `EventID` with all `omitempty`-zero fields zero **except** `EventID`, `AckedAt`, `AckEventID`. The `ListOutbox` collapse merges fields rather than replaces rows (see §5.3).

The ack envelope itself is never written to `inbox.jsonl` and never triggers a wake.

### 5.3 `ListOutbox` field-merge fix

The current collapse logic (`internal/inbox/outbox.go:43-51`):

```go
prev, seen := collapsed[o.EventID]
if !seen {
    order = append(order, o.EventID)
    collapsed[o.EventID] = o
    continue
}
if o.Final || !prev.Final {
    collapsed[o.EventID] = o
}
```

is row-replace with a "Final-row stickiness" rule. An ack delta (Final=false) arriving after a publish-finalization row (Final=true) would be discarded; conversely a Final=true delta arriving after an ack-only delta would erase the ack fields.

Fix: keep the existing row-replace logic exactly as-is, and overlay ack fields via a small parallel map populated during the same scan, applied after the collapse loop. This isolates the ack-merge concern from the existing Final-stickiness rule:

```go
collapsed := make(map[string]Sent)
order := make([]string, 0)
acks := make(map[string]Sent)  // EventID → row carrying ack info; first-ack-wins

for ... scanning rows in chronological order ... {
    var o Sent
    if err := json.Unmarshal(sc.Bytes(), &o); err != nil { ... }

    // Existing Final-stickiness collapse — UNCHANGED
    prev, seen := collapsed[o.EventID]
    if !seen {
        order = append(order, o.EventID)
        collapsed[o.EventID] = o
    } else if o.Final || !prev.Final {
        collapsed[o.EventID] = o
    }

    // Ack overlay: first-ack-wins
    if o.AckedAt != 0 {
        if _, taken := acks[o.EventID]; !taken {
            acks[o.EventID] = o
        }
    }
}

// Apply ack overlay
for eid, a := range acks {
    s := collapsed[eid]
    s.AckedAt = a.AckedAt
    s.AckEventID = a.AckEventID
    collapsed[eid] = s
}
```

This keeps Final-stickiness for the publish-finalization fields (`AcceptedBy`, `Final`, etc.) and gives ack fields their own first-write-wins discipline, independent of row ordering and Final state.

### 5.4 IPC / unified call path

`outbox.list` (already an IPC method per the unified-call-path design) returns the extended `Sent` shape with `acked_at` / `ack_event_id`. Both CLI and dashboard consume the same JSON. No new method is required for tier-2 — tier-2 is read state on `Sent`, not a new operation.

### 5.5 SSE push for live status

The dashboard's existing SSE channel (`internal/dashboard/sse.go`) emits an `outbox.update` event when a `Sent` row is mutated (publish finalization already does this). The ack-receive path piggybacks: after appending the ack delta row, it pushes the same `outbox.update` event so the dashboard flips `✓` → `✓✓` without page reload.

## 6. Surfacing — CLI + dashboard

### 6.1 CLI

`eidos gate outbox` gains a status column rendered as one of:

| Render | Condition |
|---|---|
| `✓`  | `Sent.AcceptedBy` non-empty, `Sent.AckedAt == 0` |
| `✓✓` | `Sent.AckedAt != 0` |

Output format:

```
2026-05-09 15:04:05  → npub1bob...d34f  ✓    Hello there
2026-05-09 14:30:00  → npub1ali...1234  ✓✓   What's up?
```

A row never has zero `AcceptedBy` because `send` already returns `ErrNoRelaysReachable` when no relay accepted, so `Sent` rows always have at least `✓`. No `✗` state in the compact view. Per-relay detail is deferred to a future `--verbose` / detail subcommand.

### 6.2 Dashboard

Each outbound message bubble in the chat view carries a small muted status indicator (`✓` / `✓✓`) under or trailing the bubble text. SSE drives live transition from `✓` to `✓✓` when the ack arrives.

UTF-8 check marks render fine in modern terminals and browsers; no ASCII fallback is shipped. Color is not required (renderers may use muted gray for `✓` and the existing accent color for `✓✓`).

## 7. SPEC.md amendment

A new bullet inside `## MindGate` → `### 设计选择`, between the existing `异步与实时分离` and `以点对点（1:1）为基础` bullets:

```markdown
- **投递状态分层**。MindGate 区分两层投递语义，UI 不混淆。
  - **Tier 1 — relay 接受**。本地事实：N 个 relay 在发布时返回 OK，记录于 `Sent.AcceptedBy`。CLI/dashboard 渲染为 `✓`。无协议承诺。
  - **Tier 2 — 对端回执**。envelope-v1 中的 `type=ack` 消息：对端 daemon 在成功解开 gift wrap、校验 envelope、持久化到 inbox 后，向原发件方发出 `{v:1, type:"ack", ref:<inner_id>}`。命中后 `Sent.AckedAt` / `Sent.AckEventID` 写入，UI 渲染为 `✓✓`。
  - **不替代项**。NIP-65 / kind:10050 "对方有订阅 inbox" 不等价于"已投递"，不应作为 tier 2 的代理出现在状态界面。
  - **优雅降级**。未升级的旧 daemon 收到 ack 会按 envelope 软拒绝（`schema_violation`），发件方的 `✓✓` 不会出现，仅显示 `✓`。
  - 设计 trace: [#21](https://github.com/LucianoXu/eidopsyche/issues/21)。
```

## 8. envelope-v1 spec amendment

`docs/superpowers/specs/2026-05-07-envelope-v1-design.md` gets three updates:

1. §3 Wire format: add `Ref string` to the `Envelope` struct definition (matching §3.1 of this spec) so the v1 wire surface in that doc reflects the now-three-type schema.
2. §3.1 Validation rules: add the `type=ack` case (`ref` required, hex 64; `text` and `command` forbidden). Add a one-line "Schema evolution" note stating that new non-breaking types may land additively within a major version (`v` unchanged); only breaking changes — field-semantics shifts on existing types, validation tightening, removed types — bump `v`.
3. §10 Known limitations / v2 candidates: reframe the section preamble to "candidates that *would* coincide with the next version bump because they require breaking changes" rather than implying every new type forces v2. Add a "Resolved in v1 additively" subsection cross-linking this spec for `type=ack`.

## 9. Testing

### 9.1 Unit (`internal/envelope/`)

Add to existing round-trip / reject tables:

Round-trip:
- ack with valid 64-hex ref, no `client`
- ack with valid ref and optional `client`

Reject:
- `{"v":1,"type":"ack"}` (missing ref) → `ErrSchemaViolation`
- `{"v":1,"type":"ack","ref":"short"}` → `ErrSchemaViolation`
- `{"v":1,"type":"ack","ref":"<64 with uppercase>"}` → `ErrSchemaViolation`
- `{"v":1,"type":"ack","ref":"<valid>","text":"x"}` → `ErrSchemaViolation` (forbidden field)
- `{"v":1,"type":"ack","ref":"<valid>","command":{...}}` → `ErrSchemaViolation`

### 9.2 Unit (`internal/daemon/`)

Receiver gating (using injected fakes for Pool, contacts):
- whitelisted-sender chat → ack publish invoked with correct ref
- whitelisted-sender command → ack publish invoked
- self-copy (rumor.PubKey == self) → no ack publish
- blocklisted sender → no ack publish (also no inbox row, but that's existing)
- malformed envelope → no ack publish
- ack-of-ack (inbound type=ack) → no second ack publish

Sender ack-receive:
- inbound ack with matching Sent → outbox delta appended, no inbox row, no wake
- inbound ack with unknown ref → drop, no row written
- inbound ack from non-recipient (To != rumor.PubKey) → drop with warning
- duplicate ack (Sent already has AckedAt) → drop, no second delta row

### 9.3 Unit (`internal/inbox/`)

`ListOutbox` collapse:
- pre-ack Final=true row + later ack-only delta → merged row has both AcceptedBy and AckedAt
- ack-only delta arrives before Final=true row → merged row still has both
- two ack deltas with different AckEventIDs → first-ack-wins (no overwrite)

### 9.4 Integration (`-tags=integration`)

Two-daemon harness:
- A → B chat → A's outbox flips `✓` → `✓✓` within ack-publish window
- A → B command → same flip; command reply still arrives as a separate chat row in A's inbox
- A → B with B's daemon stopped → A shows `✓` only; B starts, processes inbox, emits ack; A shows `✓✓` (provided A is online to receive)
- A → B from a non-whitelisted A (B does not have A in contacts) → message soft-rejected on B; A never gets ack
- A sends, A is offline when B's ack publishes → A's relays buffer (or fallback drop-box) → A reconnects and processes ack → outbox flips

## 10. Migration / rollout

- Single coordinated daemon update in the next release.
- CHANGELOG entry under `feat`: "tier-2 delivery acknowledgement: outbox / dashboard show `✓` (relay accepted) and `✓✓` (peer confirmed receipt)."
- Old peers receiving acks see malformed inbox rows with `RejectReason="schema_violation"`. We do not filter these out specifically — they are visible only via `eidos gate inbox --include-malformed` or equivalent diagnostic, not in the default inbox view (existing soft-reject behavior).
- No data migration of pre-existing `Sent` rows. They simply have `AckedAt == 0` and render as `✓` forever — correct, since no ack was ever solicited or received.

## 11. Out of scope

- Read receipts ("mind-form / human consumed the message"). Different signal, different consumer; not asked for.
- Per-contact-tier opt-out for ack emission (privacy). YAGNI — whitelist already gates engagement.
- ack retry / delivery guarantee for the ack envelope itself. Best-effort by design; recursion stop.
- Per-relay tier-2 detail in compact CLI / dashboard view. Deferred behind a `--verbose` / detail-subcommand consumer.
- `nack` / negative acknowledgement (e.g., "I received but rejected"). YAGNI; a missing ack already conveys this and a malicious peer could lie either way.
- Tier-2 timeout / "delivery failed" UI state. Sender's UI just shows `✓` until ack arrives; no automatic flip to a third "failed" state.
