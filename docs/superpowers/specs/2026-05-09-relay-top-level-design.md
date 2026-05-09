# Relay top-level subcommand and state decoupling

**Date**: 2026-05-09
**Status**: Approved (pending implementation)
**Target**: dev (the next release after v0.5.0)
**Scope**: Lift relay execution out of the `gate` subcommand tree into a top-level `eidos relay`. Give the relay its own config file and state directory. Add sqlite-backed event persistence. Remove the publisher whitelist. Split service-unit installation so relay-only hosts never invoke `eidos gate ...`.

This builds on `2026-05-07-mindgate-relay-decoupling-design.md` (which made the embedded relay opt-in alongside three home-relay topologies) and finishes the architectural decomposition that doc paved the way for. It also lands inside the `[relay.tls]` / `[relay.auth]` config blocks introduced in `2026-05-08-mindgate-auth-tls-relay-health-design.md` — those blocks move with the rest of the relay config to the new file, unchanged in semantics.

## 1. Problem statement

Today's `eidos gate relay` runs the embedded relay as a subcommand of `gate`. It reads from gate's `~/.config/eidos/state.db` for two pieces of state — `owner_pubkey` and the `relay_whitelist` table — and from gate's `[relay]` config section. This forces three coupling points that are unnecessary now that the SPEC (`:137-138`) treats relay as infrastructure unrelated to identity:

1. **A relay-only host must run `eidos gate init` first** to produce the `state.db` row the relay reads on startup. There is no clean "I run only the relay process on this VPS" deployment.
2. **The publisher whitelist is the main runtime tie**: gate writes rows when contacts are added; relay polls the same sqlite file every second. This is the only piece of code that requires gate and relay to share filesystem state, and SPEC `:145` already names the relay-side whitelist optional defense-in-depth — the source of truth is the gate's client-side filter.
3. **Service-unit installation is gate-flavored**: `eidos-gate-relay` is installed by `eidos gate service install`; a relay-only operator who never runs the gate has no UX path to install the relay's systemd / launchd unit.

Two further problems compound the above:

4. **The current relay does not persist events.** `internal/relayd/relayd.go:51-95` calls `khatru.NewRelay()` and never appends `StoreEvent` / `QueryEvents` handlers. Events are forwarded to currently-connected subscribers and discarded. SPEC `:57` ("容器停止期间，发往该心智体的消息在 Nostr relay 上排队") assumes persistence; today this only works if the user happens to be subscribing to a third-party relay that does persist.
5. **`eidos gate relay` is a misnamed CLI surface.** The relay is *not* part of MindGate's identity / messaging layer; it's infrastructure. Users running a community / shared relay should not see "gate" in their command line.

## 2. Design intent

Five commitments shape the rest:

1. **Relay is infrastructure, not an entity.** It does not have a network identity, does not need a keypair, and does not appear as a participant in the social graph. Optional NIP-11 admin `pubkey` is administrative metadata only. The CLI surface (`eidos relay …`) reflects this — no "gate" prefix, no "identity" plumbing.
2. **Three deployment roles are first-class.** Gate-only (uses external relays); gate + co-located paired relay (personal-inbox operator); relay-only (community / shared-infrastructure operator). The 2026-05-07 doc already made this true at the topology level; this doc finishes it at the CLI / config / service level.
3. **State is decoupled.** Relay reads only from its own config file at `~/.config/eidos/relay/config.toml` and its own event store at `~/.config/eidos/relay/events.db`. It does not open gate's `state.db`. Owner pubkey for paired mode is an explicit config field; there is no `db.GetMeta` lookup.
4. **The publisher whitelist is removed.** Per SPEC `:145`, the source of truth is the gate's client-side filter. Relay-side filtering is keep-the-mode-rules (paired = kind:1059 + addressed-to-owner) only. This eliminates the last runtime coupling between gate and relay.
5. **Persistence is in this iteration.** Without it, the relay-only role makes no sense (operator runs a relay that forgets every event the moment a subscriber disconnects). We use `github.com/fiatjaf/eventstore/sqlite3` as a drop-in for `khatru` handlers — minimal new code, retention is unlimited (no TTL) for now.

Pre-1.0 rules (per `CLAUDE.md`) allow this to ship as a breaking minor release with a documented migration recipe in CHANGELOG. No backward-compat alias for `eidos gate relay`.

## 3. Deployment topology

Three roles, each runnable on a distinct host or co-located:

| Role | Processes on host | Config dirs present | Reachability requirement |
|---|---|---|---|
| **Gate-only** | `eidos-gate-daemon` | `~/.config/eidos/{config.toml, state.db}` | None (uses external relays) |
| **Gate + paired relay** | `eidos-gate-daemon` + `eidos-relay` | both above + `~/.config/eidos/relay/{config.toml, events.db}` | Relay process must be reachable by senders (clearnet, LAN, Tor, …) |
| **Relay-only** | `eidos-relay` | `~/.config/eidos/relay/{config.toml, events.db}` only | Same as above |

A relay-only host never invokes `eidos gate …` and never produces a `state.db`. The two processes communicate, when co-located, only by URL (the gate publishes to / subscribes from the relay's `ws://` or `wss://` endpoint exactly as it would for any external relay).

**Reachability is a deployment fact, not a protocol concern.** A relay that no other gate can dial is not useful — the SPEC's "self-hosted local relay" role assumes the host has public IP, port forwarding, a tunnel, or a private-network arrangement that all participants share.

## 4. CLI surface

### 4.1 Added — `eidos relay` subcommand tree

```
eidos relay init      --mode {public|paired} --listen <host:port> [--owner npub1…]
eidos relay start
eidos relay status
eidos relay config get <key>
eidos relay config set <key> <value>
eidos relay service {install|start|stop|status|uninstall} [--system | --user]
```

| Command | Behavior |
|---|---|
| `init` | Creates `~/.config/eidos/relay/`, writes `config.toml`, opens `events.db` (creates schema). `--mode` is required. `--owner` is required iff `--mode paired`; rejected for `--mode public`. Refuses if the dir already contains a `config.toml` (operator can `--force`). |
| `start` | Runs the relay process in the foreground. Loads `config.toml`, opens `events.db`, wires `eventstore/sqlite3` into `khatru`, calls `Server.ListenAndServe`. Replaces today's `eidos gate relay`. |
| `status` | Prints listen addr, mode, owner pubkey (paired only), event count from `events.db`, uptime if running under a service manager. |
| `config get/set` | Read / mutate `relay/config.toml`. Validation mirrors gate's existing `eidos gate config` (typed keys: bool, host:port, npub). |
| `service ...` | Manages the new `eidos-relay` unit. `install` writes the unit file pointing at `eidos relay start`; `--system` and `--user` flags match `eidos gate service`'s scopes. |

### 4.2 Removed

- `eidos gate relay` (the run command) — gone in the same release. Users follow the migration recipe in CHANGELOG.
- `[relay]` section of gate's `config.toml` — moved entirely to relay's own config; gate config schema drops it.
- `cfg.RelayEnabled()` and all branch points in `cmd/eidos/gate/{start,stop,status,purge,init}.go` that checked it. The relay's existence is now a separate-process concern, not a gate config concern.
- `--with-local-relay` and `--listen` flags on `eidos gate init` (introduced by the 2026-05-07 doc). Local-relay setup is now `eidos relay init`, run separately.
- `relay_whitelist` view in `internal/store/schema.go` (derived from `contacts` + `owner_pubkey` meta).

### 4.3 Unchanged

- `eidos gate relay-add`, `eidos gate relay-list`, `eidos gate relay-remove` — these manage *which relay URLs the gate publishes to / subscribes from* via the `own_relays` table. Orthogonal to running a relay.
- `eidos-gate-daemon` unit name and behavior. Gate `service install` only ever installs this unit now.
- `eidos gate service install` — installs only the daemon unit; never touches relay.

## 5. Config and state layout

```
~/.config/eidos/
├── config.toml            # gate config (no [relay] section)
├── state.db               # gate state (relay_whitelist view dropped; owner_pubkey meta retained for gate's own use)
└── relay/
    ├── config.toml        # all [relay] / [relay.tls] / [relay.auth] sections
    └── events.db          # sqlite event store via fiatjaf/eventstore/sqlite3
```

A relay-only host has only the `relay/` subtree under `~/.config/eidos/`.

`relay/config.toml` skeleton:

```toml
log_level = "info"

[relay]
mode         = "paired"            # "paired" or "public"
listen       = "0.0.0.0:7777"
owner_pubkey = "npub1…"            # required iff mode = "paired"; rejected for "public"

[relay.tls]
cert_file = ""                     # both empty → ws://; both set → wss://
key_file  = ""

[relay.auth]
required    = true                 # NIP-42 AUTH for kind:1059 reads
service_url = ""                   # optional; overrides khatru's auto-derived URL
```

`Defaults()` in the new `internal/relaycfg` (or extension of `internal/config`) returns: `Mode = "paired"`, `Listen = "0.0.0.0:7777"`, `Auth.Required = true`, `TLS = {empty}`. `OwnerPubkey` has no default — `init` requires `--owner` when paired.

**No backward-read of gate's config.** Even on a co-located host, the relay process never opens `~/.config/eidos/config.toml` or `state.db`.

## 6. Code organization

### 6.1 New

| Path | Purpose |
|---|---|
| `cmd/eidos/relay/root.go` | `Command()` returns the `cobra.Command` for `eidos relay`; registered in `cmd/eidos/main.go` alongside forge / gate / supervisor. |
| `cmd/eidos/relay/init.go` | `eidos relay init`. Validates flags; writes config.toml; creates events.db (open + close to ensure schema). |
| `cmd/eidos/relay/start.go` | `eidos relay start`. Loads config, opens eventstore, calls `relayd.New`, `ListenAndServe`. Signal handling identical to today's `eidos gate relay`. |
| `cmd/eidos/relay/status.go` | `eidos relay status`. Prints config + event count + service-manager state. |
| `cmd/eidos/relay/config.go` | `eidos relay config get/set`. Mirrors `cmd/eidos/gate/config.go` shape for the new key set. |
| `cmd/eidos/relay/service.go` | `eidos relay service install/start/stop/status/uninstall`. Calls into `internal/service` with the new `RelayUnitName`. |
| `internal/relayd/store.go` | Wires `fiatjaf/eventstore/sqlite3` into `khatru`. New helper `OpenEventStore(path string) (*sqlite3.SQLite3Backend, error)`. |
| `internal/relaycfg/` (new package) | Relay config struct, `Load(dir string)`, `Save`, `Defaults()`. Kept distinct from `internal/config` so a relay-only build path does not import gate config types. |

### 6.2 Removed

- `cmd/eidos/gate/relay.go` (the run command).
- `internal/relayd/whitelist.go` (whitelist dropped).
- Whitelist field of `relayd.Config` and the paired-mode check that requires it (`internal/relayd/relayd.go:62-64`).
- `relay_whitelist` view from `internal/store/schema.go`. (Gate's own `owner_pubkey` meta key stays; only the relay-side dependency on it goes.)

### 6.3 Modified

| Path | Change |
|---|---|
| `cmd/eidos/main.go` | Register `relay.Command()` after `supervisor.Command()`. |
| `internal/relayd/relayd.go` | Drop `Whitelist` field from `Config`. Paired-mode check requires only `OwnerHex != ""`. Add eventstore wiring at `New()` (accept a backend, append handlers). |
| `internal/config/config.go` | Remove `Relay` field; remove `RelayEnabled()`. |
| `cmd/eidos/gate/{start,stop,status,purge,init}.go` | Drop all `cfg.RelayEnabled()` branches. Init no longer accepts `--with-local-relay` / `--listen`. |
| `cmd/eidos/gate/config.go` | Drop `relay.*` keys from `configKeys`. |
| `internal/service/service.go` | `RelayUnitName = "eidos-relay"`. Constants reordered for clarity. |
| `internal/service/{systemd_linux,launchd_darwin,scm_windows}.go` | Split installer paths: gate's `Install()` writes only the daemon unit; new relay `Install()` writes only the relay unit, pointing `ExecStart` at `eidos relay start` with `WorkingDirectory` set to the relay config dir. |

## 7. Persistence

- **Backend**: `github.com/fiatjaf/eventstore/sqlite3`. Pin the version in `go.mod` at implementation time to whatever is current and stable.
- **Path**: `~/.config/eidos/relay/events.db`. Created at `eidos relay init`; opened on `eidos relay start`.
- **Wiring** (in `internal/relayd/relayd.go`):
  ```go
  store := &sqlite3.SQLite3Backend{DatabaseURL: cfg.EventStorePath}
  if err := store.Init(); err != nil { return nil, err }
  r.StoreEvent    = append(r.StoreEvent,    store.SaveEvent)
  r.QueryEvents   = append(r.QueryEvents,   store.QueryEvents)
  r.CountEvents   = append(r.CountEvents,   store.CountEvents)
  r.DeleteEvent   = append(r.DeleteEvent,   store.DeleteEvent)
  r.ReplaceEvent  = append(r.ReplaceEvent,  store.ReplaceEvent)
  ```
- **Retention**: unlimited. Operator purges manually if needed (`rm events.db` while relay is stopped, or a future `eidos relay purge --events` command). Per-kind TTL / age-based eviction is deferred.
- **Schema**: owned by the eventstore library; we don't run migrations against it.
- **Concurrency**: single writer (the relay process). Bind one open `*sqlite3.SQLite3Backend` per `Server`.
- **No data migration**: there is no existing persisted relay data to bring forward.

## 8. Migration

Pre-1.0 breaking change. CHANGELOG entry under `BREAKING CHANGES`:

> `eidos gate relay` removed. The relay is now a top-level subcommand: `eidos relay`. To upgrade an existing local-relay install:
>
> ```
> systemctl --user stop eidos-gate-relay
> systemctl --user disable eidos-gate-relay
> eidos gate service install         # re-runs without the relay unit
> eidos relay init --mode <paired|public> --listen <host:port> [--owner <npub>]
> eidos relay service install
> eidos relay service start
> ```
>
> The `[relay]` section of your gate `config.toml` is now ignored; remove it. The previous `--with-local-relay` and `--listen` flags on `eidos gate init` are removed; use `eidos relay init` separately.

`eidos gate start` detects a residual `[relay]` section in gate's config and prints a one-line warning pointing at the migration recipe; it does not auto-rewrite or absorb. (Mirrors the v0.4 → v0.5 detect-and-reject pattern from the 2026-05-07 doc.)

## 9. SPEC.md changes

In the same PR:

- `:137-141` — replace the "self-hosted local relay (推荐)" framing with neutral enumeration of the three deployment roles. Note that all relay deployments require the host to be reachable by senders.
- After `:137`, add a paragraph clarifying that **a relay does not have a network entity identity**. Identity belongs to gates / mindforms only. Optional NIP-11 admin `pubkey` is administrative metadata, not network participation.
- `:145` — note the relay-side publisher whitelist has been removed. Client-side whitelist in the gate is the sole social-graph filter.
- New short subsection on relay persistence: events stored in sqlite at `~/.config/eidos/relay/events.db`; retention is operator-controlled (manual purge for now); per-kind / TTL eviction is a future iteration.

## 10. Testing

- `internal/relayd/relayd_test.go` — drop whitelist-required tests. Add a persistence test: open `Server` with a temp `events.db`, publish an event, close, reopen, query `kind:1059 #p:owner`, expect to find it.
- `cmd/eidos/relay/init_test.go` — config write; `--mode paired` requires `--owner`; `--mode public` rejects `--owner`; rejects existing config without `--force`.
- `cmd/eidos/relay/start_test.go` — loads config, fails clean on missing `events.db`, fails clean on bind error.
- `internal/service/{systemd_linux,launchd_darwin,scm_windows}_test.go` — update `RelayUnitName` constant; add tests that gate's `Install` no longer touches relay paths and that the new relay `Install` writes a unit pointing at `eidos relay start`.
- Integration smoke (manual / scripted): two-host topology — host A runs `eidos gate init && eidos gate service install && eidos gate service start`; host B runs `eidos relay init --mode public --listen 0.0.0.0:7777 && eidos relay service install && eidos relay service start`; A's gate is configured with B's URL via `eidos gate relay-add`; another gate publishes to B; A retrieves the event after restart (verifies persistence).

## 11. Out of scope

- TTL / retention / per-kind eviction policy.
- Live config reload (`SIGHUP`); restart on config change.
- Gate-pushes-allowlist over an authenticated channel.
- A relay keypair / signed admin announcements.
- Auto-discovery between gate and relay.
- `eidos relay purge --events` admin command (manual `rm` is the recipe for now).
- ACME / autocert in the relay's TLS path (the 2026-05-08 doc already declared BYO certs).
