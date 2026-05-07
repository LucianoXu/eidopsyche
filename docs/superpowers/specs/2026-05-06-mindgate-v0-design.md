# MindGate v0 Design — Human-to-Human Messaging

**Date**: 2026-05-06
**Status**: Approved (pending implementation)
**Scope**: First milestone of Eidopsyche. MindGate only; no MindForge container, no Agent integration.

## Acceptance criterion

Two human users (Alice, Bob), each running their own MindGate deployment on independent hosts, can exchange end-to-end encrypted messages following the EXAMPLE.md Step 0–3 flow:

1. Each runs `mindgate init` then `mindgate daemon` and `mindgate relay`.
2. Each runs `mindgate card` and exchanges the resulting `mindgate://...` URI out-of-band.
3. Each runs `mindgate add-contact <peer-uri>`.
4. Each can `mindgate send <peer-npub> "..."` and observe the message via `mindgate inbox --tail` on the other side within seconds.

The four mind-form steps in EXAMPLE.md (Steps 4–5) are **out of scope** for v0; the full flow is exercised in a later milestone once MindForge exists.

---

## 1. Architecture

### 1.1 Conceptual division

- **MindForge** — a mind-form's lifecycle and self-reflection runtime (container, ontology, agent loop, wake, dream). v0 does not implement it.
- **MindGate** — a network entity's identity and communication surface. Every entity in the network — human or mind-form — has its own MindGate instance. **A human only has a MindGate**; a mind-form has both a MindForge and a MindGate. v0 implements MindGate sufficient to support humans.

The two CLIs (`mindforge`, `mindgate`) operate on different objects (mind-form lifecycle vs. network identity) and are never mutually substitutable. Their only contact point — used in later milestones — is an in-container unix socket between the mindgate daemon and the mindforge wake mechanism.

### 1.2 Process topology

A MindGate deployment consists of three independent processes plus a state directory:

```
┌─ A MindGate deployment (one entity) ────────────────────┐
│                                                          │
│   mindgate (CLI)  ──unix socket──▶  mindgate daemon     │
│                                       │                  │
│                                       │ websocket        │
│                                       ▼                  │
│                            external relay(s) + own relay │
│                                       ▲                  │
│                                       │ websocket        │
│   mindgate relay  ──SQLite (RO)──── (whitelist source)   │
│        │                                                 │
│        └── data/ (khatru events)                         │
│                                                          │
│   state dir: $MINDGATE_HOME (default: ~/.mindgate)       │
└──────────────────────────────────────────────────────────┘
```

| Process | Responsibility | Holds |
|---|---|---|
| `mindgate daemon` | Business core: keystore, contacts, inbox, persistent multi-relay subscriptions, signing, encryption, IPC server | Private key + all entity state |
| `mindgate relay` | Embedded khatru-based Nostr relay; in `paired` mode reads whitelist from daemon's state.db RO | Only Nostr event store |
| `mindgate <verb>` (CLI) | Stateless short-lived clients; talk to daemon over unix socket | Nothing |

### 1.3 Three deployment topologies for the relay

The same `mindgate relay` subcommand binary covers all three:

1. **Co-resident with mind-form**, inside its docker container (later milestone). Supervised by `mindforge-init`. Port exposed to host or LAN.
2. **Co-resident with a human user**, on their laptop/VPS. Run alongside `mindgate daemon` (e.g., as systemd units, or `mindgate relay &` in a tmux pane).
3. **Standalone** as a redundant or public node. `--mode public`, no owner, no per-pubkey whitelist (rate-limit and basic abuse defenses only).

Topology 2 is the default for v0 acceptance. Topologies 1 and 3 are supported by the same binary with different flags but are not part of the v0 acceptance test.

### 1.4 Multi-user / multi-identity

`one daemon = one keypair = one entity` is invariant. Multi-user is achieved by running multiple daemons:

- Different Linux uids → different `$HOME/.mindgate/` → no collision.
- Same uid, multiple personas → set `$MINDGATE_HOME` differently per persona.
- `--state-dir <path>` flag overrides env. Useful for tests that run Alice + Bob in one shell.

The daemon is never multi-tenant; multi-tenancy at the daemon level would blur the "1 pubkey = 1 entity" axiom and is explicitly rejected.

State-dir resolution order:
1. `--state-dir` flag
2. `$MINDGATE_HOME`
3. `$XDG_STATE_HOME/mindgate`
4. `$HOME/.mindgate`

### 1.5 Send-flow walkthrough

Alice sends "hi" to Bob:

```
1. alice% mindgate send npub1bob "hi"
2. CLI -> daemon over unix socket:
       {"id":1, "method":"send", "params":{"to":"npub1bob...", "content":"hi"}}
3. daemon:
   a. Look up Bob in contacts; collect Bob's relay URLs.
   b. Build NIP-17 gift wrap: rumor (kind:14) -> seal (kind:13) -> wrap (kind:1059).
      Wrap event R: addressed to Bob (outer NIP-44 encrypts to Bob; #p=Bob).
      Wrap event S: addressed to Alice herself (outer NIP-44 encrypts to Alice; #p=Alice).
      Both wraps share the same inner seal (signed by Alice) containing the same rumor.
      The two wraps have **different** event ids (different ephemeral wrap keys).
   c. Append a line to outbox/YYYY/MM/DD.jsonl synchronously *before* publish, keyed
      by the recipient-wrap event id (event R's id). This guarantees a record even
      if the daemon crashes mid-publish.
   d. Publish wraps R and S in parallel to:
        own_relays(role=home) ∪ contact_relays(npub=Bob) ∪ own_relays(role=fallback)
   e. Wait until at least one non-fallback relay returns OK for wrap R (timeout 10s total).
4. daemon -> CLI: {"id":1, "result":{"event_id":"<R hex>", "accepted_by":["..."]}}
   The reported event_id is the recipient wrap's id (R) — that is the id Bob will see
   and the natural reference for cross-side correlation.
5. Bob's daemon (already subscribed kind:1059, #p=B_user, on his relay set):
   - Receives wrap event for Bob.
   - In-memory LRU dedupe by event_id.
   - NIP-44 decrypt outer wrap -> seal.
   - Verify seal signature (sender = signer of seal).
   - NIP-44 decrypt seal content -> rumor.
   - Client-side whitelist check: sender in contacts AND tier != blocked.
   - Append to inbox JSONL; update relay_state.last_seen_at.
   - Push {"event":"inbox.message", "data":{...}} on every IPC connection
     subscribed to inbox.tail.
```

---

## 2. Go module layout

Single Go workspace; v0 only adds files under `cmd/mindgate/` and `internal/`. The `cmd/mindforge*` directories are not yet introduced.

```
eidopsyche/
├── go.work                       # workspace
├── go.mod / go.sum               # root module
├── cmd/
│   └── mindgate/
│       ├── main.go               # cobra root, subcommand dispatch
│       ├── init.go               # `mindgate init`
│       ├── daemon.go             # `mindgate daemon`
│       ├── relay.go              # `mindgate relay`
│       ├── client.go             # IPC client helpers reused across subcommands
│       ├── whoami.go
│       ├── card.go               # `mindgate card`, `mindgate scan`
│       ├── contacts.go           # add-contact, contacts, remove-contact
│       ├── relays.go             # relays, relay-add, relay-remove
│       ├── send.go
│       ├── inbox.go
│       └── version.go
├── internal/
│   ├── identity/                 # secp256k1, NIP-19 (npub/nsec), keystore file IO
│   ├── card/                     # mindgate:// URI parse / format
│   ├── nostr/
│   │   ├── event.go              # event types + sign/verify wrappers (over go-nostr)
│   │   ├── client.go             # single-relay websocket client
│   │   ├── pool.go               # multi-relay publish + subscribe + dedupe
│   │   ├── nip44/                # NIP-44 helpers (thin shim over go-nostr)
│   │   └── nip17/                # rumor/seal/giftwrap construction & opening
│   ├── store/                    # SQLite connection, schema migrations
│   ├── contacts/                 # contacts CRUD over store
│   ├── inbox/                    # inbox JSONL writer + tailer + outbox writer
│   ├── ipc/                      # daemon ↔ CLI protocol: server + client
│   ├── relayd/                   # khatru wrapper + paired-whitelist policy
│   └── config/                   # config.toml + state-dir resolution
├── pkg/                          # empty in v0
├── docker/                       # not used in v0
├── docs/
│   └── superpowers/specs/2026-05-06-mindgate-v0-design.md
├── test/
│   └── integration/              # build tag `integration`
├── Makefile                      # build / test / lint / integration
├── SPEC.md / EXAMPLE.md / CLAUDE.md
└── .gitignore
```

### 2.1 Dependency direction

```
cmd/mindgate ─► internal/{ipc,identity,card,config,store,relayd}
[daemon main] ─► internal/{identity, nostr/{client,pool,nip17,nip44},
                            contacts, inbox, store, ipc, config}
internal/relayd ─► internal/store (RO)
internal/contacts, inbox ─► internal/store
internal/nostr/nip17 ─► internal/nostr/nip44, internal/identity
```

No cycles. `store` and `identity` are leaves.

### 2.2 Third-party libraries

| Purpose | Library | Notes |
|---|---|---|
| Nostr protocol primitives (events, NIP-19/44/17, relay client) | `github.com/nbd-wtf/go-nostr` | Most complete Nostr Go library; covers NIP-44 and NIP-17 |
| Embedded relay | `github.com/fiatjaf/khatru` | Same author as go-nostr; type-compatible |
| SQLite (pure Go) | `modernc.org/sqlite` | No CGO; works in distroless and on any Go cross-compile target |
| CLI framework | `github.com/spf13/cobra` | Subcommand tree |
| Config | `github.com/BurntSushi/toml` | |
| Logging | `log/slog` (stdlib) | JSON handler, level-aware |

NIP-46 / WebRTC libraries are not pulled in (out of scope).

---

## 3. Storage

### 3.1 State directory layout

```
$MINDGATE_HOME/
├── key                   # 32-byte secp256k1 private key, hex-encoded, 0600
├── state.db              # SQLite (WAL); relational data
├── state.db-wal
├── state.db-shm
├── state.lock            # daemon PID, flock'd
├── config.toml           # user-editable config
├── sock                  # unix socket; IPC; 0600
├── inbox/
│   └── YYYY/MM/DD.jsonl  # one line per inbound message; UTC date
├── outbox/
│   └── YYYY/MM/DD.jsonl  # self-copy of sent messages
├── log/
│   ├── mindgate-daemon.log
│   ├── mindgate-relay.log
│   └── audit.log
└── relay/                # only present when `mindgate relay` runs
    └── events.db         # khatru's own event store
```

`$MINDGATE_HOME` itself is `0700`.

### 3.2 SQLite schema (state.db, schema_version=1)

```sql
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
-- keys: schema_version, owner_pubkey, created_at, mindgate_version, label

CREATE TABLE contacts (
  pubkey      TEXT PRIMARY KEY,           -- 32-byte hex
  label       TEXT NOT NULL,
  tier        TEXT NOT NULL DEFAULT 'friend'
                  CHECK(tier IN ('master','friend','acquaintance','blocked')),
  notes       TEXT,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);

CREATE TABLE contact_relays (
  pubkey      TEXT NOT NULL,
  relay_url   TEXT NOT NULL,
  priority    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (pubkey, relay_url),
  FOREIGN KEY (pubkey) REFERENCES contacts(pubkey) ON DELETE CASCADE
);
CREATE INDEX idx_contact_relays_url ON contact_relays(relay_url);

CREATE TABLE own_relays (
  relay_url   TEXT PRIMARY KEY,
  role        TEXT NOT NULL CHECK(role IN ('home','fallback')),
  added_at    INTEGER NOT NULL
);

CREATE TABLE relay_state (
  relay_url     TEXT PRIMARY KEY,
  last_seen_at  INTEGER NOT NULL DEFAULT 0
);

CREATE VIEW relay_whitelist AS
  SELECT pubkey FROM contacts WHERE tier != 'blocked'
  UNION
  SELECT value AS pubkey FROM meta WHERE key = 'owner_pubkey';
```

The `inbox` and `outbox` tables explicitly do **not** exist; that data lives in JSONL. See §3.4.

### 3.3 Why SQLite for this and JSONL for that

Conversational history (inbox/outbox) and relational metadata (contacts/relays/whitelist) have different access patterns:

- **Conversational history**: append-only single-writer; consumed sequentially; schema must tolerate forward evolution; benefits from `tail -f`, `jq`, `gzip`. → JSONL.
- **Relational metadata**: small fixed-shape sets, queried by key, support `O(1)` membership tests (the relay's whitelist check on every inbound event), require cascade integrity (delete contact → delete its relays). → SQLite.

This split mirrors what Claude Code and OpenClaw do for session logs. See discussion of the choice in design notes for rationale.

### 3.4 JSONL formats

`inbox/YYYY/MM/DD.jsonl` — one line per received message:

```json
{"v":1,"event_id":"<wrap hex>","inner_id":"<rumor hex>",
 "from":"<sender hex>","kind":14,"content":"hi",
 "rumor_at":1746531200,"received_at":1746531202,
 "relays":["wss://alice.host:22895"]}
```

`outbox/YYYY/MM/DD.jsonl` — one line per sent message:

```json
{"v":1,"event_id":"<recipient-wrap hex>","self_event_id":"<self-wrap hex>",
 "inner_id":"<rumor hex>",
 "to":"<recipient hex>","kind":14,"content":"hi",
 "rumor_at":1746531200,"sent_at":1746531200,
 "accepted_by":["ws://127.0.0.1:22895","wss://bob.host:22895"]}
```

The `event_id` field is the recipient wrap's id — the same id Bob's inbox will record. `self_event_id` records the self-copy wrap so the daemon can recognize and skip the echo when it arrives via subscribe (see §5.3).

The line is written **before** publish, with `accepted_by` initialized to `[]`. If publish succeeds on at least one non-fallback relay, the daemon writes a **second** line (same `event_id`) carrying the populated `accepted_by` and an additional field `"final":true`. JSONL is append-only; downstream readers are responsible for collapsing rows by `event_id` and preferring the `"final":true` row. (This is the simplest crash-safe pattern: no in-place edits, no rename-on-write.) `audit.log` also records the publish outcome for that `event_id`. v0 thus distinguishes "we tried" (outbox line exists, `final` absent) from "it landed" (a `final:true` row exists with non-empty `accepted_by`). Total publish failure leaves only the initial line; CLI displays this state as "send pending / never confirmed".

Append semantics: the daemon `flock`s the day's file, `O_APPEND | O_WRONLY`s, writes the line + LF, `fsync`. Single-writer (the daemon under its instance lock) eliminates contention.

Day rollover is determined by UTC date of `received_at` / `sent_at`.

### 3.5 Read state for inbox

v0 does not track per-message read flags. `mindgate inbox --tail` and `mindgate inbox` are stateless reads driven by the user. If a "last seen" anchor is needed by a UI in future, the natural place to add it is a small `inbox_anchor` SQLite table (one row per CLI session id), not a column in JSONL.

### 3.6 Private-key file `key`

- Content: 64 hex characters (32 bytes), trailing LF.
- Permissions: `0600`. Daemon refuses to start if the file mode is broader.
- Loaded into `[32]byte` and zeroed on shutdown via `crypto/subtle`.
- v0 has no passphrase or OS-keychain integration. The `internal/identity` package leaves a `KeyProvider` interface where these can be plugged in later.

### 3.7 .gitignore

The repository-root `.gitignore` targets paths that would only ever come from runtime state (someone running `mindgate init --state-dir ./X` inside the repo, or a test creating scratch data) plus build artifacts. It deliberately does **not** use bare `**/inbox` style patterns because those would match source-tree directories like `internal/inbox/`.

```
# Build artifacts
/bin/
*.test
*.out
coverage.out

# MindGate state directories accidentally created in-tree
/.mindgate/
/.mindgate-*/

# Integration-test scratch
/test/integration/.tmp/
/.testdata-tmp/

# SQLite WAL/SHM stragglers (rare but possible if a test leaks)
*.db-wal
*.db-shm

# Editor / OS
.DS_Store
.vscode/
.idea/
```

Test fixtures with the `_test_only` suffix under `testdata/` follow Go convention and are explicitly **not** ignored (per CLAUDE.md).

---

## 4. Daemon ↔ CLI IPC protocol

### 4.1 Transport

- Unix stream socket at `$MINDGATE_HOME/sock`, perms `0600`, owner = daemon uid.
- On daemon start, a stale socket file (no listener) is removed and recreated.
- CLI checks for the socket; absence yields `daemon not running`.

### 4.2 Wire format

Newline-delimited JSON, JSON-RPC-2.0–shaped but not strictly RFC-conformant. Each connection is bidirectional and long-lived. Client requests carry an `id`; the server responds with the same `id`. The server may also push events with no `id` and an `event` field.

```jsonc
// Request
{"id": 1, "method": "send", "params": {"to": "npub1bob...", "content": "hi"}}

// Successful response
{"id": 1, "result": {"event_id": "5f8e...", "accepted_by": ["ws://..."]}}

// Error response
{"id": 1, "error": {"code": "CONTACT_NOT_FOUND", "message": "..."}}

// Server-pushed event (subscriptions)
{"event": "inbox.message", "data": {"event_id":"...", "from":"...", "content":"hi", "received_at":...}}
```

A subscription (e.g., `inbox.tail`) lasts as long as the connection. Closing the socket cancels the subscription. There is no explicit unsubscribe.

### 4.3 Method catalog

| Method | Params | Result |
|---|---|---|
| `whoami` | — | `{pubkey, npub, label, home_relays:[{url,status}]}` |
| `card.export` | `{label?}` | `{uri}` |
| `card.parse` | `{uri}` | `{pubkey, npub, relay, label}` |
| `contact.add` | `{npub, relays:[], label, tier?}` | `{ok:true}` |
| `contact.list` | `{tier?}` | `[{npub, label, tier, relays:[]}]` |
| `contact.remove` | `{npub}` | `{ok:true}` |
| `relay.list` | — | `[{url, role, status, last_seen_at}]` |
| `relay.add` | `{url, role}` | `{ok:true}` |
| `relay.remove` | `{url}` | `{ok:true}` |
| `send` | `{to, content}` | `{event_id, accepted_by:[]}` |
| `inbox.list` | `{since?, limit?, from?}` | `[{...}]` (most recent first) |
| `inbox.tail` | `{since?}` | streamed `inbox.message` events |
| `outbox.list` | `{since?, limit?, to?}` | `[{...}]` (most recent first; collapsed by `event_id` with `final` preferred) |
| `version` | — | `{daemon_version, schema_version}` |

### 4.4 Server-pushed events

| Event | Data |
|---|---|
| `inbox.message` | the JSONL line as parsed JSON |
| `relay.status` | `{url, status, last_error?}` (status: `connected`/`reconnecting`/`failed`) |

### 4.5 Error codes

A fixed string set. CLI surfaces these via exit code + human-readable message:

- `INVALID_REQUEST`, `INVALID_PARAMS`, `UNKNOWN_METHOD`
- `NOT_INITIALIZED` (state dir missing or schema older than daemon expects)
- `CONTACT_NOT_FOUND`, `CONTACT_EXISTS`, `INVALID_NPUB`
- `RELAY_UNREACHABLE`, `RELAY_REJECTED`, `NO_RELAYS_REACHABLE`
- `INTERNAL` (CLI shows generic message; daemon logs full stack)

---

## 5. Nostr wire protocol

### 5.1 NIP-17 layering

A "send" produces two gift wraps (one for recipient, one for self), each enclosing the same seal which encloses the same rumor.

```
rumor (kind 14)            unsigned, contains real sender pubkey + content
  └─encrypted to recipient via NIP-44, embedded as content of:
seal (kind 13)             signed by real sender
  └─encrypted to wrap-recipient via NIP-44 from a fresh ephemeral key, embedded in:
gift wrap (kind 1059)      signed by ephemeral key, addressed via #p tag,
                           created_at jittered uniformly in [now-2d, now]
```

The two wraps differ only in the recipient of the *outer* NIP-44 encryption (and therefore the `#p` tag and ephemeral key): one wraps to Bob; the other wraps to Alice. Both contain the same inner seal whose signer is Alice.

### 5.2 Publish strategy

```
target_relays = own_relays(role=home)
              ∪ contact_relays(npub=to)
              ∪ own_relays(role=fallback)
```

- Publish in parallel; each relay returns its own `OK`/`NOTICE`.
- Per-relay timeout: 5 s; total send timeout: 10 s.
- **Success criterion**: at least one non-fallback relay accepts.
- On full failure, return `NO_RELAYS_REACHABLE`. v0 does not retry; the user resends.
- The self-copy and the recipient-copy use the same target set; both must reach at least one accepting relay (in practice the home relay accepts both; the recipient's relay accepts both because the daemon is in their whitelist and it's just two events).

### 5.3 Subscribe strategy

```
subscribed_relays = own_relays(home)
                  ∪ ⋃ contact.relays
                  ∪ config.subscribe.extra_relays
```

For each unique relay URL, the daemon maintains one persistent websocket connection and one `REQ` filter:

```json
["REQ", "<sub-id>",
 {"kinds":[1059], "#p":["<owner pubkey hex>"], "since": <last_seen_at - 48*3600>}]
```

The 48-hour `since` overlap is mandated by NIP-17's `created_at` jitter window; messages from the past 2 days legitimately appear in real time.

On `EVENT` receipt:

1. In-memory LRU dedupe by event id (capacity 10 000). Also drop if the event id was just emitted by this daemon as a self-copy in the current run (kept in a small in-memory set populated at send-time, capacity 1 000, FIFO eviction).
2. NIP-44 decrypt the wrap with owner's private key → seal.
3. Verify the seal signature; the signer is the asserted sender.
4. NIP-44 decrypt the seal's content → rumor.
5. **Self-copy filter**: if `rumor.pubkey == owner_pubkey`, this is the echo of a wrap we sent to ourselves. The outbox already has the record (written at send time, §1.5). Drop here; do not insert into inbox. Still update `relay_state.last_seen_at`.
6. **Client-side whitelist check**: `sender ∈ contacts AND tier != 'blocked'`. Drop otherwise.
7. Append the rumor as a JSONL line to `inbox/YYYY/MM/DD.jsonl` with fsync.
8. Update `relay_state.last_seen_at = max(current, wrap.created_at)` for this relay.
9. Push `inbox.message` to every IPC connection currently in `inbox.tail`.

Note on durability of self-copies: if the daemon dies after publishing a self-copy but before writing the outbox line, the self-copy will eventually arrive via subscribe with `rumor.pubkey == owner_pubkey`. v0 still drops it (per step 5) — the user's only authoritative record of "what I sent" is the outbox JSONL written before publish. Hardening this gap (recover lost outbox entries from subscribe-side self-copies) is future work.

### 5.4 Connection management

- WS disconnect → exponential backoff reconnection (1s, 2s, 4s, … cap 60s, ±20% jitter).
- Whenever contacts change, the daemon recomputes the union of relays; new ones are dialed, removed-and-no-longer-referenced ones are closed.
- Multiple contacts pointing to the same relay URL share a single websocket and a single REQ filter (the filter `kind=1059, #p=self` already matches all messages addressed to the owner regardless of sender).

### 5.5 Paired-relay specifics

- Daemon dials the local paired relay over `127.0.0.1` plain ws. No TLS locally.
- When the daemon publishes a self-copy wrap, it both (a) records the wrap event id in the in-memory self-copy set described in §5.3 step 1, and (b) sees the same wrap echo back via its subscription to the home relay. Step 1's set provides fast-path dedupe; step 5's `rumor.pubkey == owner_pubkey` filter is the slow-path defense if the in-memory set has been evicted (e.g., across daemon restart). Either way the inbox is not double-written.
- `mindgate relay` survives daemon restarts; the daemon survives relay restarts. Relay→daemon is one-way (relay reads daemon's state.db RO); the daemon never depends on the relay being up to function as a client of remote relays.

### 5.6 Out of scope at the protocol layer

- NIP-65 (relay-list metadata publication): replaced by OOB cards in v0.
- NIP-100 (WebRTC signaling), real-time media: v2.
- NIP-46 (remote signing): future thin-client milestone.
- File transfer / large-message chunking: v0 is text only (kind 14).
- Per-contact rate limits: future hardening.

---

## 6. CLI surface

```
mindgate init                 [--state-dir <path>] [--label <str>]
mindgate daemon               [--state-dir <path>] [--config <path>]
mindgate relay                [--state-dir <path>] [--mode paired|public]
                              [--listen <addr>] [--data-dir <path>]
mindgate whoami
mindgate card                 [--qr]
mindgate scan                 <uri>
mindgate add-contact          <npub-or-uri> [--relay <url>...] [--label <str>]
                                            [--tier friend|acquaintance]
mindgate remove-contact       <npub>
mindgate contacts             [--tier <t>]
mindgate relays
mindgate relay-add            <url> --role home|fallback
mindgate relay-remove         <url>
mindgate send                 <npub> [<content> | --stdin]
mindgate inbox                [--since <time>] [--from <npub>] [--limit N] [--tail]
mindgate outbox               [--since <time>] [--to <npub>] [--limit N]
mindgate version
```

### 6.1 Common flags

`--state-dir`, `--socket`, `--json` (machine-readable output), `-q/--quiet`, `-v/--verbose`.

### 6.2 Conventions

- Any `<npub>` argument also accepts: hex pubkey, `npub1...`, or a `mindgate://...` URI (in which case the relay/label are taken from the URI).
- `mindgate add-contact <uri>` is a one-shot: it parses the URI for relay/label/npub.
- `mindgate send` reads `<content>` as a positional or, if absent, from stdin.

---

## 7. Bootstrap flow

### 7.1 `mindgate init`

```
$ mindgate init
✓ created /home/alice/.mindgate (perm 0700)
✓ generated secp256k1 keypair
  → /home/alice/.mindgate/key (0600)
✓ wrote /home/alice/.mindgate/state.db (schema v1)
✓ wrote /home/alice/.mindgate/config.toml (defaults)
  default home relay: ws://127.0.0.1:22895 (paired-mode local relay)
  label:              alice@hostname
your identity:
  npub1alice...
  hex:  79be667e...
next steps:
  1) start daemon:  mindgate daemon
  2) start relay:   mindgate relay
  3) share card:    mindgate card
```

Semantics:

- **Idempotency**: refuses to run if `key` already exists. The user must remove it deliberately.
- **No network calls**: pure local filesystem and key generation.
- **No process spawning**: daemon and relay are independent commands.
- Writes `meta` rows: `schema_version=1`, `owner_pubkey=<hex>`, `created_at=<unix>`, `mindgate_version=<semver>`, `label=<chosen>`.
- Writes default `own_relays` row: the local paired relay URL.

### 7.2 `mindgate daemon`

```
INF mindgate-daemon version=0.0.1
INF state_dir=/home/alice/.mindgate owner=npub1alice...
INF ipc listening on /home/alice/.mindgate/sock
INF relay-pool subscribing kind=1059 to self
WRN relay ws://127.0.0.1:22895 unreachable; retrying
INF relay ws://127.0.0.1:22895 connected (since=...)
INF ready
```

- Acquires `flock` on `state.db` and writes PID to `state.lock`. A second daemon refuses to start.
- On `SIGINT`/`SIGTERM`: stop accepting IPC connections → drain in-flight requests (≤5 s grace) → close subscriptions → close SQLite → exit.
- v0 has no `SIGHUP` reload; configuration changes require restart.

### 7.3 `mindgate relay`

```
INF mindgate-relay mode=paired listen=127.0.0.1:22895
INF whitelist source=/home/alice/.mindgate/state.db (RO)
INF whitelist size=1 (owner only; add contacts to expand)
INF ready
```

- Opens the daemon's `state.db` with `mode=ro`.
- Polls `state.db` mtime every 1 s; refreshes the in-memory `relay_whitelist` set on change. (A more proactive socket-based notification can replace this in a future revision.)
- **Paired-mode reject policy**: an event is accepted iff
  `event.kind == 1059` **and** the `#p` tag references `owner_pubkey`. Other events are rejected. This expresses "this relay only carries gift wraps addressed to its owner".
- **Public-mode reject policy** (out of v0 acceptance, but the code path exists): accept any well-formed signed event subject to a basic per-IP rate limit.

---

## 8. Errors, logging, security hygiene

### 8.1 Error severity model

| Severity | Examples | Behavior |
|---|---|---|
| Configuration / fatal | state dir missing, key permissions wrong, schema mismatch | refuse to start, stderr message, non-zero exit |
| Persistent (fail-fast) | unknown contact, all relays reject | IPC error returned; daemon does not retry |
| Transient (silent retry) | websocket drop, single-relay timeout | daemon retries with backoff; CLI never sees |
| Data integrity (panic) | SQLite corruption, fsync failure | log ERROR + exit; supervisor restarts |

### 8.2 Logging

- `log/slog` JSON handler.
- Daemon: stderr + `log/mindgate-daemon.log`.
- Relay: stderr + `log/mindgate-relay.log`.
- Audit (key load, daemon start/stop, contact add/remove): `log/audit.log`.
- Day-based rotation; bounded size.
- **Never log message plaintext or private keys.** Inbox event log lines record only `event_id`, `from_pubkey`, and `content_len=N`. The plaintext lives in JSONL (under stricter perms) and never in syslog/journald paths.

### 8.3 Security hygiene

- Private key in memory is `[32]byte`; zeroed on shutdown via `crypto/subtle.ConstantTimeCopy` to a sink, then GC.
- All user-visible identifiers default to bech32 (`npub1...`); raw hex requires `--hex`.
- IPC error responses do not embed internal stack traces; full details go to logs.
- The daemon refuses to accept a `key` file with mode broader than `0600` or non-owner ownership.
- Following CLAUDE.md, no real Nostr `nsec…` keys appear in committed code or fixtures. Test keys are generated ephemerally or live under `testdata/*_test_only*`.

---

## 9. Testing strategy

### 9.1 Unit tests (`go test ./...`)

- `internal/identity`: keypair generation; NIP-19 round-trip; keystore load fails on bad permissions; zeroing.
- `internal/nostr/nip17`: rumor → seal → wrap → unwrap → seal-verify → rumor recovery, including self-copy. Failure of seal-signature verification rejects the message.
- `internal/nostr/pool`: against an in-process fake relay (in-memory websocket), publish to N relays with arbitrary success/failure; subscribe/dedupe; `since`-based resume.
- `internal/contacts`, `internal/inbox`: CRUD; concurrency; JSONL append + day rollover.
- `internal/ipc`: protocol round-trips; error paths; cancellation by closing the connection.
- `internal/card`: URI parse + format round-trip; rejects malformed input; preserves label.

### 9.2 Integration tests (`go test -tags=integration ./test/integration/...`)

A single-process loopback fixture spins up two MindGate "deployments" (Alice and Bob) inside the test binary, each with its own state dir, daemon, and embedded khatru relay bound to `127.0.0.1:0` (ephemeral port).

Cases:

1. **Happy path**: alice sends "hi" → bob's `inbox.tail` IPC channel receives it within 5 s, JSONL contains the line.
2. **Resume after offline**: alice keeps her relay running but stops her daemon; bob sends; alice's daemon restarts; alice receives via since-based resume.
3. **Self-copy + dedupe**: alice's outbox has the line; alice's inbox does *not* duplicate her own self-copy (LRU dedupe + the rumor sender check distinguishes "from me" from "to me").
4. **Send to non-contact**: alice tries to send to a stranger → daemon refuses (`CONTACT_NOT_FOUND`) before publish.
5. **Receive from non-contact**: bob (not in alice's contacts) sends to alice; the wrap reaches alice's relay+daemon, but the client-side whitelist drops it; nothing in inbox.
6. **Single-instance lock**: a second daemon on the same state dir fails to start.
7. **Relay paired-mode rejection**: a stranger publishes a kind-1 note to alice's relay; relay rejects.

### 9.3 End-to-end (CI matrix job, optional locally)

`docker-compose` brings up two `mindgate` containers with separate state dirs and bridged networks. A test harness drives `mindgate` CLI inside each and asserts JSONL outcomes. This job mirrors the EXAMPLE.md Step 0–3 flow exactly.

### 9.4 Lint and static checks

- `gofmt -l .` must produce empty output.
- `go vet ./...`
- `staticcheck ./...`
- `govulncheck ./...` (CI nightly, not blocking PRs).

### 9.5 Makefile targets

```
make build        # go build ./...
make test         # go test ./...
make integration  # go test -tags=integration ./...
make lint         # gofmt -l + go vet + staticcheck
make e2e          # docker-compose-based EXAMPLE.md flow (CI)
make ci           # everything above gating
```

---

## 10. Out of scope for v0

| Area | Why deferred |
|---|---|
| MindForge container, mindforge-init, agent loop | Next milestone; v0 only validates human-to-human MindGate. |
| Wake signal IPC to MindForge | Same. |
| Tier differentiation (master / acquaintance differing behavior) | Column exists; v0 treats all non-blocked as friend; blocked = client-side drop. |
| Per-contact rate limit | Deferred hardening. |
| Send retry / store-and-forward | The user resends; queueing is future work. |
| NIP-65 relay-list publication | OOB card already provides relay info. |
| NIP-100 WebRTC | Real-time media is v2. |
| NIP-46 remote signing / thin client | Belongs to project's third component. |
| Multimedia / file attachments | Text only (kind 14). |
| Group / multi-party chat | SPEC marks v2+. |
| OS keychain / passphrase encryption of `key` | File-mode 0600 is sufficient for v0; a `KeyProvider` interface leaves room for it. |
| Web UI | CLI suffices for the acceptance test. |
| Ontology / mind-form private domain | No mind-form exists in v0. |

---

## 11. Acceptance verification (end of milestone)

The milestone is complete when, on a clean checkout:

1. `make ci` passes (build, lint, unit, integration, e2e if Docker is available).
2. Two operators following the README on two independent hosts (or two VMs / containers) reproduce EXAMPLE.md Step 0–3 and successfully exchange messages.
3. `docs/` contains an updated user-facing `INSTALL.md` and `USAGE.md` derived from the bootstrap flow in §7 above. (`SPEC.md` and `EXAMPLE.md` need no edits; they describe the full system, which v0 partially realizes.)
4. `CLAUDE.md` does not need changes; project instructions still apply.

---

## 12. Open questions and explicit non-decisions

- **NIP-44 v2** is the only NIP-44 version targeted. v1 is not supported.
- **Bech32 prefixes**: only `npub` is mandated; `nsec` is generated by `init` for clarity but never displayed by default.
- **Time source**: `time.Now().UTC()` for all internal timestamps; clock skew handling is the 48-hour `since` overlap from NIP-17 jitter, not NTP enforcement.
- **Crash semantics**: a JSONL line is either fully written + fsync'd, or absent. A torn-write at process kill reduces to "the latest event was not yet inserted on this side"; the next subscribe `since`-resume re-fetches it from the relay. Idempotency comes from event-id dedup.
- **Backup**: `tar czf state.tar.gz $MINDGATE_HOME/` is the supported backup. Restoring on the same uid restores identity. Documented in `INSTALL.md`.
