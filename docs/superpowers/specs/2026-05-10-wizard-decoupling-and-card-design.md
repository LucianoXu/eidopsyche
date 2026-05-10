# Wizard decoupling + identity card design

**Date**: 2026-05-10
**Status**: Draft (pending user review)
**Scope**: Two coupled changes that together let `eidos` be deployed in three modes — mindgate-only, mindform-only, both — and let identities be moved between machines or lent to a hosted mind-form.

## 1. Problem statement

The First Contact wizard currently **bundles** the operator's mindgate identity creation with mind-form summoning into one inseparable ritual:

- Phase 1 always asks for a label and home relay and writes a fresh keypair via `identity.Bootstrap`.
- Phases 2–3 always summon a mind-form bound to the just-created keypair.

This forecloses three legitimate deployments:

1. **mindgate-only** — operator wants only to talk to other people / mind-forms; doesn't want to summon their own yet.
2. **mindform-only** — operator wants to host a mind-form for someone else (the master is *another* person, not the host operator), or wants no local identity at all on this host.
3. **identity migration** — operator already has an `nsec` from a previous machine and wants to bring it here, not generate a new one.

Separately, eidos has no canonical way for one entity (human or mind-form) to **introduce itself** to another in one self-contained, human-readable, parseable artifact. Today an introduction is an ad-hoc paste of an `npub` plus a relay URL plus a label, and parsers must guess the format.

This spec resolves both: a TOML **identity card** for self-introduction, and a wizard **restructured along the mindgate / mindform layer split** so each can be installed independently.

## 2. Design intent

1. **Cards are public, nsec is private — and the file system reflects that.** A card is a "show this to the world" artifact. An `nsec` is "never share this" material. They are **separate inputs** with separate handling. No file format ever optionally carries `nsec`.
2. **Two layers of eidos, two stages of wizard.** MindGate (identity, communication) and MindForm (a summoned mind-form) are conceptually independent (CLAUDE.md § Project Structure). The wizard mirrors that split: an identity stage and a mindform stage, each independently skippable.
3. **One predicate for "is the host initialized".** `firstcontact.IsIdentityInitialized` (renamed from `IsSubsequentRun`) is the single source of truth. Both `cmd/eidos/main.go`'s auto-dispatch and the wizard's own subsequent-mode check call it; they cannot drift.
4. **Flags win, prompts fill in the rest.** When `eidos summon --master-card <path>` is given, the wizard does not interactively re-ask the master source. The flag is the answer.
5. **YAGNI on card v1.** No signature, no NIP-65 list, no bio, no entity-kind tag. The pubkey *is* the identity; richer metadata belongs to its own Nostr event types or a future card v2.

## 3. Identity card

### 3.1 Format

TOML. Filename convention: `<label>.eidos-card.toml` — the `.eidos-card.toml` suffix is the parse-format binder; the `<label>` prefix lets a directory of cards coexist (`~/eidos-cards/alice.eidos-card.toml`, `~/eidos-cards/forge-team.eidos-card.toml`).

### 3.2 Schema (v1)

```toml
# eidopsyche identity card · v1
schema_version = 1

label       = "Alice"
pubkey_hex  = "5e9dabf301a0...0000000000000000000000000000000000000000"
npub        = "npub1tn6dt7eqq..."
home_relay  = "wss://relay.damus.io"

created_at  = "2026-05-10T12:34:56Z"
```

| Field | Required | Validation |
|---|---|---|
| `schema_version` | yes | integer; v1 parser accepts `1`, refuses anything else |
| `label` | yes | non-empty string |
| `pubkey_hex` | yes | 64 lowercase hex chars |
| `npub` | yes | NIP-19 decodes to **bytes-equal** `pubkey_hex`; otherwise refuse the card |
| `home_relay` | yes | parses as `ws://` or `wss://` URL with non-empty host |
| `created_at` | yes | RFC3339 UTC |

**Why hex AND npub redundantly**: hex is the source-of-truth (Nostr libs use it internally); npub is what humans paste from clipboard / read in UIs. Including both means the human-side ergonomics work and the machine-side parser doesn't have to convert. Decoder cross-checks them on load — mismatch = forged or corrupt card.

### 3.3 Out of scope for v1

- **Signature**. Same security model as a paper business card: pubkey is the proof of identity; label/relay are advisory. Want to verify "is this Alice"? Send a NIP-17 to that pubkey, see who replies. `schema_version` reserves room for v2 with a Schnorr signature over the canonical encoding.
- **Multiple relays / NIP-65 list**. `home_relay` is mindgate's "must be reachable somewhere" guarantee; richer relay lists are NIP-65 events, not card content.
- **Bio / avatar / entity-kind (human|mindform)**. Nostr kind 0 metadata covers profile-level fields; the card stays minimal.

### 3.4 `internal/card/` package

New package, single file plus tests:

```go
package card

type Card struct {
    SchemaVersion int       `toml:"schema_version"`
    Label         string    `toml:"label"`
    PubkeyHex     string    `toml:"pubkey_hex"`
    Npub          string    `toml:"npub"`
    HomeRelay     string    `toml:"home_relay"`
    CreatedAt     time.Time `toml:"created_at"`
}

func Encode(w io.Writer, c Card) error
func Decode(r io.Reader)        (Card, error)
func Read(path string)          (Card, error)
func Write(path string, c Card) error
func (c Card) Validate() error  // all v1 rules
func Sample() Card              // for help-text examples
```

`Decode` MUST call `Validate` before returning the Card; callers never see an unvalidated Card.

### 3.5 CLI surface

- `eidos gate card export [--out <path>] [--label <override>]`
  - Reads the local gate state (key + state.db meta + own_relays.home), writes a card to `--out` or stdout.
  - Default `--out`: `~/eidos-cards/<label>.eidos-card.toml` (mkdir -p).
  - `--label <override>` lets the operator export under a different display name without changing local state.
- `eidos gate card show <path>`
  - Loads, validates, pretty-prints. Used for manual verification (especially before importing someone else's card as a master or contact).

Wizard input is via the **`--master-card <path>`** flag on `eidos summon` (and on the bare-`eidos` auto-dispatched wizard, via the Phase 2 "use a card" branch which prompts for a path interactively when the flag is absent).

## 4. Wizard restructure

### 4.1 New phase tree

```
Phase 0 — open                              first run only
  · logo
  · language picker
  · project intro                           (§ 5)

Phase 1 — identity                          when host has no local identity
  · 新建    → label + home_relay → identity.Bootstrap
  · 导入    → nsec (paste / --key-file)
              [+ optional --card / interactive card path:
                 label + home_relay default to card values]
              → identity.BootstrapWithExistingKey
  · 跳过    → no state written; mindform stage will require a card

Phase 2 — mindform choice                   always; option set context-dependent
  · 退出                                    (only when entry was bare `eidos`)
  · 用本地身份召唤                           (only when host has a local identity)
  · 用名片召唤                               → ask --master-card path if not yet provided

Phase 3 — character                          (current Phase 2; renamed)
Phase 4 — seal & response                    (current Phase 3; renamed)
```

### 4.2 Phase 2 option matrix

| Entry | Local identity? | Phase 2 options |
|---|---|---|
| bare `eidos` | yes | 退出 / 用本地身份召唤 / 用名片召唤 |
| bare `eidos` | no (Phase 1 = 跳过) | 退出 / 用名片召唤 |
| `eidos summon` | yes | 用本地身份召唤 / 用名片召唤 |
| `eidos summon` | no | (skip prompt; require a card path) |
| any + `--master-card <path>` flag | any | (skip Phase 2 entirely; flag wins, no warning) |

### 4.3 Entry-point semantics

- **bare `eidos`**: dispatched by `cmd/eidos/main.go` only when the host is *not* identity-initialized; offers the full menu (identity stage with three branches; mindform stage with the option matrix above including 退出).
- **`eidos summon`**: always proceeds to summon a mind-form; Phase 2's 退出 option is suppressed. Phase 1 runs only if no local identity.
- **`eidos gate init`**: equivalent to "wizard with Phase 2 forced to 退出"; for users who want a local identity but no mind-form. (Replaces the existing standalone `gate init` UX with a path through the same wizard infrastructure, so identity-stage code lives in one place.) When the host is already identity-initialized, `gate init` prints "this host already has a gate identity at `<stateDir>` (label: `<label>`)" and exits 0 — preserving today's "already initialized" exit behavior, just with a clearer message.

### 4.4 Resolved decisions

1. **`eidos summon --master-card <path>` with a local identity present**: card wins, no warning. The flag is the user's explicit answer.
2. **Mindform's `home_relay`**: always shares the master's. Local-master → `s.HomeRelay` from local gate state. Card-master → `card.HomeRelay`. (A future "place this mind-form on a different relay than its master" flow is out of scope; users can edit the in-container `gate/config.toml` after creation.)
3. **`eidos summon` with existing identity**: skips the Phase 2 退出/召唤 question, but still asks 用本地身份召唤 vs 用名片召唤 — master source remains a per-summon decision (use case: "I have my own gate identity, but I'm hosting a mind-form for a friend on this machine and the friend should be its master").

### 4.5 Subsequent-mode predicate rename

`firstcontact.IsSubsequentRun` → `firstcontact.IsIdentityInitialized`. Semantics unchanged: returns true iff `<stateDir>/key` and `<stateDir>/state.db` both exist. Both `cmd/eidos/main.go::shouldDispatchToWizard` and the wizard's own first-run vs. subsequent-mode branch consult this single predicate so the two layers cannot drift.

The "欢迎回来, <label>" line moves from its current position (top of subsequent-mode flow) to the entry of Phase 2 — the natural place to acknowledge a returning operator now that Phase 1 is conditionally skipped.

### 4.6 Package layout changes

| Action | File |
|---|---|
| New | `internal/card/card.go`, `internal/card/card_test.go` |
| Modify (extend) | `internal/firstcontact/phase0_open.go` (project intro) |
| Rename + rewrite | `internal/firstcontact/phase1_self.go` → `phase1_identity.go` (three branches) |
| New | `internal/firstcontact/phase2_choose.go` (mindform choice + master source) |
| Rename | `internal/firstcontact/phase2_book.go` → `phase3_book.go` |
| Rename | `internal/firstcontact/phase3_seal.go` → `phase4_seal.go` |
| Modify | `internal/firstcontact/run.go` (drive new phase tree; thread `entryMode` enum) |
| Modify | `internal/firstcontact/strings.go` (new keys for intro, three identity branches, master-source choice) |
| Modify | `cmd/eidos/main.go` (consult the renamed predicate; unchanged otherwise) |
| Modify | `cmd/eidos/summon/cmd.go` (pass `entryMode = summon`; thread `--master-card` and `--key-file` flags) |
| Modify | `cmd/eidos/gate/init.go` (delegate to the wizard with `entryMode = gateInit`) |
| New | `cmd/eidos/gate/card.go` (subcommands: `export`, `show`) |

Existing `Phase0`/`Phase1`/`Phase2`/`Phase3` exported function names are renamed to match the new phase tree; tests update accordingly. No external consumers (this is `internal/`).

### 4.7 Run-mode passing

`firstcontact.Deps` gains an `EntryMode` field:

```go
type EntryMode int

const (
    EntryBareEidos EntryMode = iota
    EntrySummon
    EntryGateInit
)
```

Phase 2 reads `EntryMode` to decide which options to offer. `--master-card` and `--key-file` come in via separate `Deps.MasterCardPath` and `Deps.OperatorKeyPath` strings (empty = not provided).

Wiring at the call sites:

- `cmd/eidos/main.go` auto-dispatch: passes `EntryMode = EntryBareEidos`.
- `cmd/eidos/summon.Run`: passes `EntryMode = EntrySummon`; reads `--master-card` / `--key-file` from cobra flags.
- `cmd/eidos/gate/init`: passes `EntryMode = EntryGateInit`.

Both `summon` and `gate init` retain their cobra commands; only their `RunE` bodies become thin wrappers that compose `firstcontact.Deps` and call `firstcontact.Run`.

## 5. Project intro

### 5.1 Placement

Phase 0, after logo + language picker, before Phase 1's identity-branch choice. Skipped on subsequent runs (where Phase 0 is skipped wholesale per § 4.5).

### 5.2 Copy

**zh**:

```
Eidopsyche 是一个数字生命的社交网络框架。它有两层：

  · MindGate  — 你和别人、和心智体之间的通信
  · MindForm  — 一个由你召唤的心智体，它有自己的内在生活

接下来 wizard 会问你两件事：
  1. 要不要一个本地身份？可以新建、可以导入、可以跳过
  2. 要不要现在召唤一个心智体？

  · 只想跟别人说话：第一题选"新建"，第二题选"退出"
  · 只想给朋友跑一个心智体：第一题选"跳过"，第二题给朋友的名片
  · 两者都要：默认路径
```

**en**:

```
Eidopsyche is a social-network framework for digital lives. It has two layers:

  · MindGate  — communication between you and other people / mind-forms
  · MindForm  — a mind-form you summon, with its own inner life

The wizard will ask you two things:
  1. Do you want a local identity? You can create one, import one, or skip
  2. Do you want to summon a mind-form right now?

  · Just want to talk to others: pick "create" then "exit"
  · Just want to host a mind-form for a friend: pick "skip" then give their card
  · Both: the default path
```

### 5.3 Tone

Plain and human (consistent with the wizard-LLM tone fix in v0.10.2). No ritual register — that vocabulary is reserved for Phase 3 onward, when the user is actually writing the summoning book. The intro is a project orientation, not a ceremony opening.

## 6. Migration & breaking changes

- **Wizard state files** (`~/.eidos/gate/key`, `state.db`, `config.toml`) are **format-compatible**; an existing operator initialized under v0.10.x will be detected by `IsIdentityInitialized` and routed to Phase 2 directly. No re-bootstrap required.
- **`gate init`** behaviour changes: it now goes through the wizard rather than its current standalone form. Output and resulting state are unchanged; the user-visible difference is the project-intro and identity-branch UX.
- **No data migration** for already-summoned mind-forms. They are unaffected — this spec touches only the host-side wizard and CLI.
- **`firstcontact.IsSubsequentRun`** is renamed; this is `internal/`, no external impact. Tests update with the rename.

## 7. Testing

- `internal/card/card_test.go`:
  - round-trip `Encode → Decode` preserves all fields
  - `Validate` rejects: missing fields, malformed `pubkey_hex`, npub/hex mismatch, non-ws relay, unknown `schema_version`
  - `Sample` produces a card that round-trips and validates
- `internal/firstcontact`:
  - phase 1 三分支 unit tests (新建 / 导入 / 跳过) with fake renderer + identity stub
  - phase 2 option matrix (4 entry × identity-presence cells) drives the right prompts
  - `--master-card` flag bypasses Phase 2 prompt
  - `--master-card` + local identity uses card (resolved decision § 4.4.1)
  - `IsIdentityInitialized` covers (key+db absent / both present / one missing) × all entry modes
- `cmd/eidos/gate`:
  - `card export` writes a file that decodes back to the same Card via `card.Read`
  - `card show <bad-card>` exits non-zero with the validation error

## 8. Out of scope (deferred)

- Card v2 with Schnorr signatures (room reserved via `schema_version`).
- "Place this mind-form on a different relay than its master" workflow.
- Mindform-side `eidos card export` (a mind-form exporting its own card from inside its container — possible later via the same `internal/card` package, but the host-side surfaces ship first).
- Importing a card via `eidos gate add-contact --from-card <path>` — natural follow-up; not blocking on this spec.
- Encrypted nsec backup file format. The user's stated need is "import an existing nsec into a fresh host"; pasting nsec or pointing `--key-file` at a chmod-0600 file is sufficient. Encrypted backup is a separate, larger design (passphrase, KDF, integrity, recovery).

## 9. Documentation updates

- `docs/specs/FirstContact.md` — section 一 (前置硬性要求) and section 二 (阶段顺序) need rewriting against the new four-phase tree; section 六 (Subsequent Runs) updates to reflect the new identity-stage skip + Phase 2 still-asking master source.
- `README.md` — quick-start section gets a one-line note that `eidos` first-run offers three deployment modes (mindgate / mindform / both).
- `CHANGELOG.md` — auto-generated by GoReleaser from Conventional Commits on the next tagged release.
