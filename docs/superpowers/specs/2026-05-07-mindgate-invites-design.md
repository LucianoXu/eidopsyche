# MindGate Invites Design

**Date**: 2026-05-07
**Status**: Approved (pending implementation)
**Scope**: One-step contact establishment via signed invite tokens, replacing the current four-step symmetric OOB exchange.

## 1. Problem statement

EXAMPLE.md Step 0–3 requires four manual operations: each side prints a card, OOB-exchanges it, and `add-contact`s the other. Friction comes mostly from the symmetry — neither side's `add-contact` accomplishes the other side's add. We want a one-step affordance: Alice creates an invite, hands it to Bob, Bob runs one command, both ends end up with each other.

## 2. Consent model

Issuing an invite **is** consent to add the redeemer.

- When Alice runs `mindgate invite create`, she pre-authorizes Alice's daemon to add whoever redeems the resulting token.
- When Bob runs `mindgate redeem <token>`, the token's signature proves Alice authored it; Bob's daemon adds Alice to contacts on the strength of the OOB channel that delivered the token.
- Bob's redemption event then carries Bob's identity to Alice's daemon, which adds Bob automatically — no further confirmation prompt.

This collapses two confirmations into one. The trust assumption is identical to the prior model: the OOB channel is trusted by both parties for one round of identity exchange. Invites do not relax it; they only remove the second OOB hop.

## 3. Invite token format

```
mindgate-invite://<base64url(payload)>
```

`base64url` is RFC 4648 base64 with URL-safe alphabet and no padding. The decoded payload is canonical JSON (sorted keys for stable signature input):

```json
{
  "v": 1,
  "issuer_npub":          "npub1alice...",
  "issuer_relay":         "ws://alice.host:22895",
  "issuer_label_hint":    "Alice (work)",
  "redeemer_label_hint":  "friend",
  "id":                   "<32-byte hex random>",
  "expires_at":           1746531200,
  "max_uses":             1,
  "sig":                  "<64-hex schnorr sig>"
}
```

Field semantics:

| Field | Required | Notes |
|---|---|---|
| `v` | yes | Protocol version. v0 ships v1 only; future bumps require explicit fallback paths. |
| `issuer_npub` | yes | Bech32. Redeemer derives hex pubkey via NIP-19. |
| `issuer_relay` | yes | The relay where Bob publishes redemption AND where Bob will subscribe after redemption. |
| `issuer_label_hint` | yes | What label the redeemer pre-fills when adding Alice. Editable client-side. |
| `redeemer_label_hint` | optional | What label Alice's daemon will give to the new contact. Empty → fall back to `"invitee-<short-id>"`. Editable later via daemon command. |
| `id` | yes | 32-byte hex; unique on issuer's side. Forms primary key in `invites` table. |
| `expires_at` | yes | Unix seconds; `0` = never expires. |
| `max_uses` | yes | `1` = single-use; `>1` = up to N uses; `0` = unlimited. |
| `sig` | yes | Schnorr signature by `issuer_npub`'s key over the SHA-256 of the canonical JSON of the payload **with `sig` field omitted**. Same scheme as Nostr event sigs. |

**Signature input** (deterministic):
1. Build the payload object without `sig`.
2. Marshal as canonical JSON: keys sorted lexically, no insignificant whitespace, UTF-8.
3. SHA-256 the result.
4. Schnorr-sign over the digest with the issuer's secp256k1 key.
5. Hex-encode the 64-byte signature into the `sig` field.

A redeemer's verifier reverses steps 1–4.

## 4. Redemption protocol (over Nostr)

Redemption uses NIP-17 gift wrap for privacy and authentication, with a custom **inner** rumor kind so messages and redemptions don't collide.

```
Outer  (kind 1059, ephemeral key)            ← standard NIP-17 gift wrap, encrypted to issuer
  └── Seal (kind 13, signed by redeemer)     ← proves redeemer identity to issuer
       └── Rumor (kind 25001)                ← invite-redemption payload
             pubkey:  redeemer hex
             content: <JSON, see below>
             tags:    [["p", issuer hex]]
```

The outer wrap is `kind:1059` so **no relay rule changes are needed** — the existing paired-mode policy already accepts kind:1059 with `#p=owner`. Routing happens at the daemon: after `nostr.Unwrap` returns the rumor, the daemon dispatches by `rumor.Kind`:
- `14` → existing inbox path
- `25001` → new invite-redemption path
- other → drop with warning log

Rumor `content` JSON:

```json
{
  "v": 1,
  "invite_id":           "<32-byte hex; matches token.id>",
  "redeemer_relay":      "ws://bob.host:22895",
  "redeemer_label_hint": "Bob (Hangzhou)"
}
```

`redeemer_label_hint` is what the redeemer suggests Alice call them. Alice's daemon may prefer `redeemer_label_hint` from the token if non-empty, falling back to this; ultimately the label is editable via `remove-contact` + `add-contact` or a future `relabel` command.

## 5. Storage schema (issuer side)

Schema version bumps to **2**. New tables (added to `internal/store/schema.go` as `schemaV2`):

```sql
CREATE TABLE IF NOT EXISTS invites (
  id              TEXT PRIMARY KEY,        -- 32B hex
  created_at      INTEGER NOT NULL,
  expires_at      INTEGER NOT NULL,        -- 0 = never
  max_uses        INTEGER NOT NULL,        -- 0 = unlimited
  uses            INTEGER NOT NULL DEFAULT 0,
  issuer_label    TEXT NOT NULL,
  redeemer_label  TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'active'
                  CHECK(status IN ('active','expired','revoked'))
);

CREATE TABLE IF NOT EXISTS invite_redemptions (
  invite_id       TEXT NOT NULL,
  redeemer_pk     TEXT NOT NULL,
  redeemed_at     INTEGER NOT NULL,
  PRIMARY KEY (invite_id, redeemer_pk),
  FOREIGN KEY (invite_id) REFERENCES invites(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_invites_status ON invites(status, expires_at);
```

Redeemer side has no new tables — Bob just `add`s Alice as a contact via the existing path.

The `Migrate` function in `internal/store` runs `schemaV1` first (idempotent on `IF NOT EXISTS`), then `schemaV2`, then writes/updates `meta.schema_version`. A pre-existing v1 DB transparently picks up the new tables on next daemon start.

## 6. CLI surface

```
mindgate invite create [flags]
   --single-use              shorthand for --max-uses 1   (default if neither given)
   --max-uses N              N >= 1 finite uses
   --unlimited               no use limit (max_uses = 0)
   --expires DURATION        e.g. 7d, 24h, 30m;  default 7d
   --no-expiry               don't expire (expires_at = 0)
   --issuer-label STR        what redeemer pre-fills as Alice's label  (default: own meta.label)
   --redeemer-label STR      what Alice will label the redeemer        (default: empty → daemon picks fallback)

mindgate invite list                # active + expired/revoked summary
mindgate invite revoke <id-prefix>  # marks status='revoked'

mindgate redeem <invite-uri-or-token>
```

`invite create` prints the URI on stdout (so it can be piped into `qrencode` etc.) and a short status summary on stderr.

## 7. Daemon IPC methods

| Method | Params | Result |
|---|---|---|
| `invite.create` | `{single_use?, max_uses?, expires_seconds?, issuer_label?, redeemer_label?}` | `{id, uri, expires_at, max_uses}` |
| `invite.list` | `{status?}` | `[{id, created_at, expires_at, max_uses, uses, status, issuer_label, redeemer_label}]` |
| `invite.revoke` | `{id_prefix}` | `{ok, full_id}` (errors on ambiguous prefix) |
| `invite.redeem` | `{token_or_uri}` | `{issuer_npub, issuer_relay, accepted_by:[]}` (publishing accept_by per relay) |

New IPC error codes:
- `INVITE_INVALID_TOKEN` — base64 / JSON / signature failed
- `INVITE_EXPIRED` — issuer side OR redeemer side detects expiry
- `INVITE_EXHAUSTED` — issuer side: `uses == max_uses` and not unlimited
- `INVITE_REVOKED` — issuer side: status = `revoked`
- `INVITE_ALREADY_REDEEMED` — issuer side: same `(invite_id, redeemer_pk)` row exists; treat as success but idempotent (no contact double-add)
- `INVITE_PREFIX_AMBIGUOUS` — `invite.revoke` matched >1 row

A new server-pushed event surface:
- `contact.added` — `{npub, pubkey, label, source}` where `source` is `"manual"`, `"invite"`, etc. Issued whenever a contact is added (including the existing manual `contact.add` path), so `mindgate inbox --tail` can show "✓ Bob accepted your invite".

## 8. Daemon flow

### 8.1 `invite.create`

1. Parse flags into `{max_uses, expires_at, issuer_label, redeemer_label}`.
2. Generate `id = 32 random bytes hex`.
3. Build canonical JSON payload (no `sig`), SHA-256, sign with own private key.
4. Append `sig` field; base64url-encode the full JSON.
5. Insert row into `invites` table with `status='active'`, `uses=0`.
6. Return URI `mindgate-invite://<base64url>` + metadata.

### 8.2 `invite.redeem`

1. Strip `mindgate-invite://` prefix; base64url-decode; parse JSON.
2. Reconstruct canonical-JSON-without-sig and verify Schnorr signature against decoded `issuer_npub`. Fail → `INVITE_INVALID_TOKEN`.
3. Check `expires_at` against current time. Fail → `INVITE_EXPIRED`.
4. Decode `issuer_npub` → hex; assemble `contacts.Contact{pubkey, label=issuer_label_hint, relays=[issuer_relay], tier=friend}`. Add via existing `contacts.Add`. If `ErrExists`, that's OK — re-redemption flow.
5. Build inner rumor (kind 25001) with content `{v:1, invite_id, redeemer_relay, redeemer_label_hint}`.
6. Build NIP-17 gift wrap addressed to issuer.
7. Build a self-copy gift wrap (consistent with existing send flow) and record its event id in `selfWrapIDs`.
8. Publish both wraps to: `issuer_relay ∪ own_relays(home) ∪ fallbacks`. The issuer's relay is the only mandatory target — fail there → `NO_RELAYS_REACHABLE`.
9. Kick the subscriber so the new issuer relay enters the subscription set immediately. (Same mechanism as `contact.add` already auto-kicks.)
10. Return `{issuer_npub, issuer_relay, accepted_by}`.

### 8.3 Issuer-side incoming kind:25001 rumor

After NIP-17 unwrap (existing path), `handleIncoming` checks `rumor.Kind`:

```
if rumor.Kind == 25001:
    handleInviteRedemption(rumor)
    return  // do NOT write to inbox JSONL
```

`handleInviteRedemption(rumor)`:

1. Parse `rumor.Content` as `{invite_id, redeemer_relay, redeemer_label_hint}`. Malformed → log warn, drop.
2. Look up `invites WHERE id = ?`. Not found → log warn, drop.
3. Status check:
   - `revoked` → log info, drop.
   - `expires_at != 0 AND expires_at < now` → mark status='expired', drop.
   - `max_uses != 0 AND uses >= max_uses` → mark status='expired', drop.
4. Idempotency: `INSERT INTO invite_redemptions(invite_id, redeemer_pk, redeemed_at)`. If primary-key violation, idempotent re-redeem; do not increment `uses` and do not re-add contact. Return.
5. Determine label: prefer the invite's stored `redeemer_label`; fall back to the rumor's `redeemer_label_hint`; final fallback `"invitee-<id[:8]>"`.
6. Add `contacts.Contact{pubkey=rumor.PubKey, label, relays=[redeemer_relay], tier=friend}`. If `ErrExists` (e.g. issuer already has the redeemer from another path), keep going — the redemption is still recorded.
7. `UPDATE invites SET uses = uses + 1`. If now `>= max_uses` and `max_uses != 0`, also `SET status='expired'`.
8. Refresh subscriber so the new redeemer's relay is added. (Same kick mechanism.)
9. Push `contact.added` event with `{npub, label, source:"invite", invite_id}` to all `inbox.tail` connections.

## 9. Failure & abuse modes

| Scenario | Behavior |
|---|---|
| Token tampered (signature mismatch) | Redeemer rejects with `INVITE_INVALID_TOKEN` before publishing anything. |
| Expired (redeemer-side check) | Rejected before publish; saves issuer's bandwidth. |
| Issuer offline at redemption time | Redemption event lands on issuer's relay (assuming it's separately running) and is consumed when daemon comes back. |
| Issuer's relay also offline | Redeem returns `NO_RELAYS_REACHABLE`; redeemer's local contact add stands; `mindgate redeem <token>` retried later is idempotent. |
| Replay of redemption event | Outer wrap event id deduped via existing in-memory LRU; inner `(invite_id, redeemer_pk)` enforces idempotency at DB level. |
| Same redeemer redeems twice | Redeemer's local `contact.add` is idempotent (already exists); issuer's `invite_redemptions` PK collision skips state changes. |
| Single-use already redeemed by attacker | Legitimate redeemer's redemption hits PK collision (if attacker spoofed redeemer key — impossible without their secret) OR invite-exhausted check (`uses >= max_uses`) and is rejected. |
| Token leaked to third party | By design; user is expected to share via secure OOB. Reusable invites assume the holder is trusted by issuer to hand out. |
| Spam against issuer's relay | Outer wraps still need valid signatures. Per-IP rate limit on relay (already in design §4 acceptable for v0; can extend to per-pubkey). |
| Issuer's daemon crashes between wrap arrival and DB write | Wrap is durable on relay; on restart, `since=last_seen-48h` resume re-fetches; idempotent processing. |
| Clock skew between parties | `expires_at` uses Unix seconds; both parties expected within ~minutes. NIP-17 created_at jitter (≤2 days) tolerated by existing subscribe overlap. |

## 10. Out of scope

- Cross-relay invite revocation gossip (revoking on Alice's side is local-only; she trusts that her relay won't accept new redemptions because the daemon checks before adding contact).
- Time-based throttle on `invite create` (anti-token-spam). User-side problem, not protocol.
- Multi-issuer co-signed invites ("Alice introduces Bob to Carol where all three end up connected").
- Mind-form invites (a mind-form issuing invites for itself). Same protocol works; out of v0 since no mind-form yet.
- QR-code rendering. Output is plain URI; user pipes to `qrencode` if desired.
- NIP-46 remote signing of invites (i.e. issuing from a thin client).
- Recording an audit log of who redeemed what (only `invite_redemptions` rows are kept).

## 11. Test plan

Unit:
- `internal/invite` (new package) — token serialize/deserialize round-trip, canonical-JSON + signature verify, expiration / max_uses checks.
- `internal/store` migration test — fresh DB → schema_version=2; v1 DB → applies schemaV2 idempotently → schema_version=2.
- `internal/contacts` — no changes needed.

Integration:
- `TestInviteOneTimeFlow`: Alice issues single-use invite; Bob redeems. Both ends contain each other in `contacts`. Bob's `inbox.tail` shows `contact.added` event from issuer side. Second `redeem` of same token by Bob — idempotent, no double-add, no error.
- `TestInviteReusable`: Alice issues max_uses=2; Bob and Carol both redeem; Alice has both as contacts; third redemption (Dave) fails with `INVITE_EXHAUSTED`-equivalent (silent drop on issuer side; redeemer sees publish-OK but no `contact.added` push).
- `TestInviteExpiry`: Alice issues with 1s expiry; wait 2s; Bob redeems → `INVITE_EXPIRED` client-side.
- `TestInviteRevoke`: Alice issues; revokes; Bob redeems → publish OK but issuer drops; Bob's local has Alice but Alice doesn't have Bob.

## 12. Acceptance

- All unit + integration tests pass.
- A two-host live deployment can do: Alice `invite create` → DM token to Bob → Bob `redeem` → `mindgate inbox` shows the `contact.added` event on Alice's side and Bob's `mindgate contacts` shows Alice. Total user actions: 1 + OOB + 1 = 2 commands and one OOB exchange.
- USAGE.md updated with a new section "Inviting a contact (one-step)".
