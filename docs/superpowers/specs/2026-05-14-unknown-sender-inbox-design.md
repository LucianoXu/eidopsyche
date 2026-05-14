# Unknown-sender inbox visibility — design

**Status:** draft
**Author:** Claude (with Yingte)
**Date:** 2026-05-14
**Scope:** `internal/daemon/`, `internal/inbox/`, `cmd/eidos/gate/inbox.go`, `internal/dashboard/`, `docs/USAGE.md`, `docs/specs/SPEC.md`
**Related specs:** `2026-05-09-mindforge-v0-design.md` (one-container/one-volume baseline; defines master / mindform / first-breath semantics), `2026-05-11-unified-state-interface-design.md` (IPC method table — new `sender` filter param)

## §1 Goal

A NIP-17 chat from an unknown sender (a pubkey that is not in this gate's contacts) currently disappears: the daemon decodes the envelope, observes that the sender has no `contacts` row, and `return`s without persisting, waking, or surfacing the message anywhere. The only trace is a `dropped non-contact / blocked` line at `slog.Debug` level. Replace that hard drop with **persist + segregate**:

- All non-blocked chats are appended to inbox storage, exactly as a contact's chat would be.
- The inbox list / tail surfaces distinguish **known** (sender has a non-blocked contact row) from **unknown** (no contact row). `eidos gate inbox` defaults to known-only; `--all` and `--unknown` reveal pending rows.
- Wake and ack emission stay gated on the known-sender path. Unknown chats never wake a mindform; they never get a delivery-ack returned to the sender.
- Promoting a stranger via `eidos gate add-contact` retroactively surfaces their prior chats in the default inbox view (no migration step — the contact lookup happens at list time).

This restores first-breath visibility without weakening the wake gate or leaking presence signals.

## §2 Why now — the test 004 finding

deploy-test 004 (`deploy-test/004-mindform-introduction/`) drives Bob (mbp) summoning Alice (selene) with Bob's card as master. After "💠 Ritual complete." the wizard has shown Alice's first-breath message ("Hello, Bob. You were right…") and the user expects to find it in Bob's inbox on mbp. They don't:

```
$ ssh yingtexu@mbp 'eidos gate inbox --limit 50'
2026-05-14 14:04:04  Alice  Bob，办好了。已兑换邀请把 Charlie 加为联系人…
```

Only the post-task confirmation (sent from Alice **after** the operator manually ran `eidos gate add-contact alice` on mbp in Step 6) survived. Alice's own outbox shows the first-breath wrap was published at 12:02:00 with status `✓` (relay accepted, no ack returned). The relay delivered it; Bob's daemon decoded it; `internal/daemon/daemon.go:557-565` then dropped it:

```go
if rumor.PubKey != d.Key.PublicHex {
    c, err := d.Repo.Get(ctx, rumor.PubKey)
    if err != nil || c.Tier == contacts.TierBlocked {
        d.Log.Debug("dropped non-contact / blocked", "from", rumor.PubKey, …)
        return
    }
    senderContact = c
}
```

The wizard's `ResponseWait` saw "Hello, Bob…" because it read `chest/first-message.md` directly from Alice's container volume — not from Bob's inbox. This created the illusion of delivery. **What actually happened** is that the master's gate silently filtered out the most important message of the mindform's life.

The same trap fires for any cold-call: a friend handed Bob's npub at a party can write him, the wrap reaches his relay, his daemon decrypts it, and it vanishes. The hard drop optimises for spam suppression at the cost of every legitimate first-touch.

## §3 Scope, non-goals, and what stays

**In scope (this spec):**

- Replace the hard drop at `daemon.go:557-565` with persist-then-filter.
- Add a `sender` enum param to the existing `inbox.list` and `inbox.tail` IPC methods: `"known"` (default), `"unknown"`, `"all"`.
- Add `--sender {known|unknown|all}` (default `known`) to `eidos gate inbox` (and the existing `--tail` mode honours it).
- Dashboard: existing inbox panel keeps showing known rows by default; add a small "Pending (n)" count chip that flips the listing to unknown.
- Tests across `internal/daemon/` dispatch, `internal/inbox/` list, CLI rendering, and a deploy-test 004 baseline rerun.

**Non-goals:**

- **No envelope schema changes.** Per `feedback_protocol_yagni`, do not add a `birth-greeting` envelope type, a `master_hint` field, or any wire-level marker. The fix is purely receiver-side.
- **No auto-promotion of summoned mindforms to master's contacts.** That is a separate, tighter fix (Bob's host gate, at `forge.create` time, adds the new mindform's npub as a TierFriend contact) and is tracked as a follow-up in §9. It is complementary to this spec — both should ship, but they are independent.
- **No retroactive wake.** Promoting a stranger via `add-contact` does not replay queued unknown chats as wakes. The operator who reads an unknown-pending row decides whether to add the contact and, if so, whether to also `eidos forge wake <name>` manually. KISS over magic.
- **No spam-rate-limiter today.** Inbox is unbounded JSONL today; we accept the same write-amplification cost for unknown senders as for known. If unknown spam becomes a real pressure (Eidopsyche has zero production deployments today), a `--cap-unknown-per-day` config knob can be added without revisiting this design.
- **No per-tier storage.** Pending-vs-known is computed at list time from the contacts table, not stamped into the inbox row. This means the same row's classification flips when the operator promotes the sender — desired behaviour, and one fewer schema migration.

**What stays the same:**

- `TierBlocked` is **still hard-dropped at dispatch time.** Blocked means blocked. Persistence is reserved for "unknown" (no contact row at all).
- Wake gating: only known, non-blocked senders submit a wake to `wakeDir`. Mindforms cannot be cold-summoned by strangers.
- Ack emission: only known, non-blocked senders receive an ack envelope. Strangers do not get a confirmation that their message was received — this also keeps a daemon from confirming the npub is live to spammers.
- The `Malformed` / `RejectReason` soft-reject path on the envelope decoder is unchanged. Unknown sender is **not** a malformed envelope; do not conflate the two.
- Self-chats (`rumor.PubKey == d.Key.PublicHex`) still bypass the contact filter entirely.

## §4 Design

### §4.1 Dispatch path (`internal/daemon/daemon.go:539-589`)

The behaviour change is a single block. Current:

```go
case envelope.TypeChat:
    var senderContact *contacts.Contact
    if rumor.PubKey != d.Key.PublicHex {
        c, err := d.Repo.Get(ctx, rumor.PubKey)
        if err != nil || c.Tier == contacts.TierBlocked {
            d.Log.Debug("dropped non-contact / blocked", "from", rumor.PubKey, …)
            return
        }
        senderContact = c
    }
    msg := inbox.Message{ … }
    if err := d.Box.AppendInbox(msg); err != nil { … }
    if d.wakeDir != "" { submitWake(d.wakeDir, msg) }
    d.broadcastInbox(ctx, msg)
    if senderContact != nil {
        d.emitAck(ctx, rumor.PubKey, senderContact.Relays, rumor.ID)
    }
```

Proposed:

```go
case envelope.TypeChat:
    var senderContact *contacts.Contact
    if rumor.PubKey != d.Key.PublicHex {
        c, err := d.Repo.Get(ctx, rumor.PubKey)
        switch {
        case err == nil && c.Tier == contacts.TierBlocked:
            // Blocked tier is hard-drop, as today: no persistence,
            // no wake, no ack, no broadcast.
            d.Log.Debug("dropped blocked sender", "from", rumor.PubKey, …)
            return
        case err == nil:
            senderContact = c
        default:
            // Unknown sender: persist + broadcast, but do not wake
            // and do not ack.  senderContact stays nil; downstream
            // branches use the same nil-check they already use for
            // self-chats to skip the ack.
            d.Log.Debug("inbox: unknown sender (persisted, no wake/ack)",
                "from", rumor.PubKey)
        }
    }
    msg := inbox.Message{ … }
    if err := d.Box.AppendInbox(msg); err != nil { … }
    if d.wakeDir != "" && senderContact != nil {
        // Wake only on known senders.  Self-chats (senderContact == nil
        // with rumor.PubKey == self) intentionally do not wake either —
        // matches today's pre-spec behaviour for self-chats.
        if err := submitWake(d.wakeDir, msg); err != nil { … }
    }
    d.broadcastInbox(ctx, msg)
    if senderContact != nil {
        d.emitAck(ctx, rumor.PubKey, senderContact.Relays, rumor.ID)
    }
```

Three interface-level invariants are preserved:

- **No wake from a stranger.** A mind-form's wake stream is bounded to "trusted" peers — defined as `(rumor.PubKey == d.Key.PublicHex) || senderContact != nil`. Self stays trusted (today's self-chats still wake; the container daemon uses self-chats for self-reflection bumps); known non-blocked contacts stay trusted; unknown senders are excluded. The wake guard reads `if d.wakeDir != "" && trustedSender`.
- **No ack to a stranger.** The existing `if senderContact != nil` around `emitAck` already does the right thing under the new flow — it now also serves as the suppression for unknown senders. Self never received an ack (would be weird; we skip the ack to self today via the same nil-check), and that stays.
- **No pollution of "known" view from a past-blocked sender.** A contact that was promoted to known and later moved to TierBlocked still has historical inbox rows. The list-time classifier treats them as Pending (see §4.3) so the default `eidos gate inbox` view never shows them again — matching operator expectation that "block" means "stop seeing this".

The dispatcher's local debug-log changes: today there is one `dropped non-contact / blocked` line covering both cases; under the new flow, blocked stays at `Debug` (operator-curious) and unknown drops to `Debug` with a different message so log parsers can distinguish.

### §4.2 Storage schema — minimal transit-only addition

The on-disk JSONL format is unchanged. The Pending-vs-Known classification is derived per-row at list time from the contacts repo (the same lookup `annotateInboxLabels` already does for `Label`). The list / tail responses gain a transit-only `Pending bool` annotation on `inbox.Message`:

```go
// types.go
type Message struct {
    …
    // Pending is true when the From pubkey is not in the contacts
    // repo at list time. Transit-only: persisted rows do not carry
    // this field; ListInbox computes it from the contacts lookup.
    Pending bool `json:"pending,omitempty"`
}
```

Renderers (CLI, dashboard) tag pending rows distinctively (see §5). The flag is set inside the existing `annotateInboxLabels` (renamed to `annotateInboxRows`) so we get one pass over the rows with one shared cache.

**Pending predicate** (single source of truth, lives in `internal/contacts`):

```go
// IsPending reports whether a sender pubkey is "pending" for inbox classification.
// True when there is no contact row, or when the contact is TierBlocked.
// Self (caller's own pubkey) is the responsibility of the caller — pass selfHex.
func IsPending(ctx context.Context, r *Repo, pubkey, selfHex string) bool {
    if pubkey == selfHex { return false }
    c, err := r.Get(ctx, pubkey)
    if err != nil { return true }
    return c.Tier == TierBlocked
}
```

`annotateInboxRows` invokes `IsPending` per row using the same cache it already builds for `Label`. The list-time filter (§4.3) shares this predicate.

### §4.3 IPC contract — `inbox.list` and `inbox.tail`

Both methods gain a `sender` enum field. Defaults preserve "known-only" so existing callers (dashboard, CLI scripts) see no behavioural shift unless they opt in.

```jsonc
// inbox.list params
{
  "since": <unix int>,   // existing
  "from":  "<hex>",      // existing
  "limit": 50,           // existing
  "sender": "known"      // NEW; one of "known" | "unknown" | "all";
                         // default "known".  Mutually exclusive with `from`
                         // (a specific pubkey filter already disambiguates).
}
```

`inbox.tail` mirrors the same field on its subscribe request.

Validation in `inboxList` (`methods_messaging.go:175-205`): the `sender` enum is parsed; "known" excludes rows where `IsPending(From) == true`; "unknown" inverts; "all" skips the filter. The `from` and `sender` params are mutually exclusive — when `from` is set, the sender enum is ignored (a specific-pubkey query is the most specific possible filter).

The filter is pushed down into `internal/inbox`'s streaming scanner via a new optional `Keep func(Message) bool` parameter on `ListInbox` so the `limit` semantic remains "return up to N matching rows, scanning newest-first" rather than "return N rows then filter, possibly returning fewer than N matches." `Keep == nil` keeps every row (today's behaviour). The daemon method constructs `Keep` from the parsed `sender` enum and a per-call contact-lookup cache.

### §4.4 CLI — `eidos gate inbox`

Surface mirrors the IPC param:

```
$ eidos gate inbox --help
…
Flags:
  --since <duration>    only show messages newer than this (e.g. 1h, 24h)
  --from  <npub|label>  only show messages from this peer
  --limit <int>         max rows (default 20)
  --sender {known|unknown|all}
                        filter by contact-graph tier (default known)
  --tail                stream new inbound messages live
```

Default rendering for unknown rows tags them inline:

```
2026-05-14 12:02:00  (unknown) npub10uz92ast…  Hello, Bob.  You were right…
```

— the `(unknown)` literal sits where the label would, and the short-hex of `From` follows so the operator can copy-paste it into `eidos gate add-contact`. The dashboard inbox panel takes the same treatment (italic-grey row + a "Promote to contact" button).

### §4.5 Dashboard

Existing inbox panel: unchanged for known rows. Add a small "Pending (n)" chip in the panel header that, when clicked, swaps the listing to `sender=unknown`. The pending count is recomputed at each `inbox.list` / `inbox.tail` push. A single row in the pending list has a "Promote to contact" button that opens the existing `contact.add` modal pre-filled with the sender's hex.

### §4.6 Promotion semantics

`gate add-contact <npub|mindgate://...>` (no API change) inserts a `contacts` row; subsequent `inbox.list` calls reclassify rows from that pubkey as known. No replay, no wake, no notification to the sender — the operator promoted them on their own terms. The deploy-test 004 success criterion remains: after Step 6 (`eidos gate add-contact alice`), Bob's `eidos gate inbox` shows two rows from Alice — the first-breath at 12:02:00 (previously pending) and the post-task confirmation at 12:04:04.

### §4.7 What happens at daemon startup

The daemon's startup dedupe-rebuild (`Store.EventIDs`) already loads every persisted inbox row. No change needed: rows previously dropped at dispatch time were never persisted, so there is nothing to backfill; rows persisted under the new policy will already be in `EventIDs` so a re-delivery from the relay does not double-append.

## §5 CLI / dashboard rendering rules

| Surface | Default view | Unknown view | Row marker |
|---|---|---|---|
| `eidos gate inbox` | known only | `--sender unknown` | `(unknown)` literal in label column + short-hex from `From` |
| `eidos gate inbox --tail` | known only | `--sender unknown` | as above |
| Dashboard inbox panel | known only | "Pending (n)" chip | grey row + "Promote" button |
| Dashboard SSE | both pushed | client-side filter | `pending: true` field on JSON event |

Wake context (for mindform receivers): unchanged — mindforms only see wake-triggered messages, and unknown senders never trigger wake. So the agent-loop's view of "messages I should respond to" is identical to today.

## §6 Test plan

**Unit (new):**

- `daemon.dispatchEnvelope`: unknown sender, kind=14 chat → `Box.AppendInbox` called once; `submitWake` not called; `emitAck` not called; `broadcastInbox` called once.
- `daemon.dispatchEnvelope`: blocked sender → none of the above called (parity with today).
- `daemon.dispatchEnvelope`: known sender → unchanged behaviour (regression guard).
- `inbox.ListInbox` filter param: `sender=known` excludes rows whose `From` is not in the supplied stub contacts map; `sender=unknown` inverts; `sender=all` returns both.
- `annotateInboxTiers`: rows from a known pubkey get `Pending=false`; rows from an unknown pubkey get `Pending=true`; self-rows (pubkey == self) get `Pending=false`.

**Unit (regression):**

- `daemon.dispatchEnvelope`: TierBlocked still produces no inbox append (do not silently downgrade blocked into pending).

**Integration (new):**

- New file `internal/daemon/dispatch_unknown_test.go`: two pre-built keypairs, sender unknown to receiver; assert receiver's inbox has one row with the right content, `senderHex` matches, and an out-of-band contact-add reclassifies the row on the next list call.

**deploy-test 004 baseline rerun:**

- Repeat the full Step 2-8 with the daemon change deployed. Success criterion §1.3 of `script.md` is upgraded: Bob's inbox after Step 5 (before Step 6's manual add-contact) shows exactly one row from Alice — the first-breath — under `eidos gate inbox --sender unknown`. After Step 6, it appears under the default `eidos gate inbox` view as well. After Step 7, both Alice rows are present in default view; Charlie sees the greeting on msi.

## §7 Migration notes

Pre-1.0 — no migration. The daemon binary upgrade is the migration: post-upgrade, every newly-received unknown chat is persisted; pre-upgrade dropped chats are gone (the relay no longer holds them). This is acceptable given that the project has no production deployments and the only documented victim of the old policy is test 004's first-breath message (which gets re-tested under the new policy).

## §8 Performance and abuse considerations

**Storage:** inbox is JSONL append-only, one file per UTC day. An unknown-spam wave that sends 10⁴ messages would write ~5 MB extra per day (~500 bytes per row). At today's scale (a handful of mindforms, no production users) this is irrelevant. The follow-up §9 lists a `unknown_per_day_cap` config knob as the durable answer when somebody complains.

**Lookup cost:** `annotateInboxTiers` reuses `annotateInboxLabels`' per-call cache and adds no new DB calls. Page-load time for `eidos gate inbox --limit 50` is unchanged.

**Presence leak:** strangers receive **no** ack and **no** read receipt, identical to today. The only signal a stranger can observe is "the relay accepted my wrap" — they cannot distinguish "delivered to operator's inbox-pending" from "decoded but dropped because the receiver runs an older daemon" from "receiver is offline and the relay queued it." This is by design.

**Wake DoS:** Mind-forms never wake on unknown chats. A stranger cannot drain a mindform's API budget by sending it cold messages.

## §9 Follow-ups

Tracked separately, not part of this spec:

- **Auto-promote summoned mindforms to master contacts.** When `eidos forge create` runs on the same host as the master gate, the create flow can call `gate.contact.add` with the new mindform's npub at TierFriend before the container boots — this closes the timing gap that prompted this spec without requiring `--sender unknown`. Independent of this spec; both should ship.
- **`unknown_per_day_cap` config key.** A daemon-level circuit breaker for the spam edge case. Defer until pressure exists.
- **Wake-on-promote.** When the operator runs `gate add-contact`, optionally re-submit any queued pending wakes from the past N minutes. Strict opt-in (flag on `add-contact`), since drive-by mindform wake is exactly the spam vector this spec carefully avoids.

## §10 Rejected alternatives

**(A) Add a `birth-greeting` envelope type.** Mark the mindform's first message with a distinguishing envelope `type` so the master daemon can bypass the contact filter when `(sender ∈ known-summoned-mindforms) && (type == birth-greeting)`. Rejected because it (1) special-cases the symptom rather than the root cause — cold-calls from non-mindforms remain dropped — and (2) requires both an envelope schema bump and a daemon-side index of "mindforms summoned by me," violating the YAGNI feedback memory.

**(B) Wizard-side reverse-contact add.** The wizard, after sealing Alice on selene, reaches into Bob's gate on mbp via SSH or a cross-host IPC and inserts Alice as a contact before starting her container. Rejected because (1) the wizard does not have Bob's IPC socket address or credentials in the general case (Bob is on mbp; the wizard runs on selene), and (2) it doesn't fix cold-calls from non-mindforms.

**(C) Repurpose the soft-reject path.** Persist unknown-sender rows with `Malformed=true, RejectReason="unknown_sender"`. Rejected because the soft-reject path has a specific semantic — "the envelope decoder declined to dispatch this rumor" — and overloading it with "the social graph declined to dispatch this rumor" would confuse both operators (who currently see Malformed rows as "something is wire-broken upstream") and the dashboard's malformed-event indicator.

**(D) Keep the hard drop and rely on operator vigilance.** Document the gotcha in USAGE.md, add a `gate trace --dropped` debug subcommand, and continue dropping by default. Rejected: the symptom we observed in test 004 was that the operator had no way to know a message had been dropped. Debug logs are not a user surface.

## §11 Open questions

- **Should `eidos gate contacts` list pending senders too?** A pending sender is in a separate-but-related namespace; surfacing them under `contacts --pending` could be useful, but it duplicates the inbox view's signal. Lean toward "no, keep them in inbox-only," but reconsider if dashboard usage shows operators using `gate contacts` as their first-look surface.
- **Should the dashboard's "Promote to contact" button default to TierFriend or TierAcquaintance?** TierFriend matches `contacts.Repo.Add`'s default. Probably correct, but TierAcquaintance is the cautious choice for cold strangers. Resolve during implementation, with a one-line user-facing label adjustment if we pick Acquaintance.
- **`inbox.list since=…` interaction.** Does `--sender unknown` honour `--since`? Yes — same row filter pipeline; the unknown filter is just the last predicate. No design issue, but call it out in the test plan to make sure we have coverage.
