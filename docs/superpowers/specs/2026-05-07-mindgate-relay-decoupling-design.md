# MindGate Daemon / Relay Decoupling

**Date**: 2026-05-07
**Status**: Approved (pending implementation)
**Scope**: Drop the implicit assumption that every MindGate install runs its own embedded relay. Make the local relay one of three first-class home-relay topologies (public Nostr relay, self-hosted on a separate host, shared relay run by a friend or community). v0.5 covers the UX, service-orchestration, and default-value layer only. v0.6 adds NIP-42 AUTH so public Nostr relays become production-grade — tracked as a separate spec.

## 1. Problem statement

`eidos gate init` and `eidos gate start` currently assume that every host runs its own embedded relay (`relayd`). This is unworkable for hosts without a public IP — the majority of laptops, phones, and residential boxes — because peers cannot dial back to a NAT'd address. The current default `127.0.0.1:22895` is "secure but unusable for real cross-machine traffic"; the recent shift to `0.0.0.0:22895` is "exposed but still unusable without DNS / TLS / firewall configuration". Neither default reflects how mind-form social networking actually deploys.

Three deployment topologies need to be first-class:

1. **Public Nostr relay** — daemon-only host points at e.g. `wss://relay.damus.io`. Zero infrastructure, weakest privacy (relay operator sees gift-wrap metadata).
2. **Self-hosted on a separate host** — daemon on a NAT'd box, relay on a VPS / homelab box with a public address that the user controls. Metadata stays inside the user's trust circle.
3. **Shared relay run by a friend / community** — small group co-locates on a single trusted relay. Intermediate trust posture, no individual infrastructure overhead.

The current architecture already supports all three at the protocol layer: the daemon publishes / subscribes to whatever URLs sit in the `own_relays` table (`internal/daemon/daemon.go:232`, `internal/daemon/methods.go:707`), and treats local vs. remote relays identically. The decoupling is therefore a UX, configuration, and service-orchestration change — not a protocol change.

## 2. Design intent

Three commitments shape the rest of the spec:

1. **Relay becomes opt-in.** The default `eidos gate init` produces a daemon-only deployment. Running the embedded relay requires explicit opt-in via `--with-local-relay`. Status, start, stop, and preflight all tolerate the relay being absent.

2. **Single source of truth.** A new explicit boolean `relay.enabled` decides whether the embedded relay is part of this install. The home URL embedded in cards and invites comes solely from `own_relays(role='home')`. The two are independent: a host can run a local relay (bind on `0.0.0.0:22895`) while telling peers to reach it via `wss://my.host` (set in `own_relays`).

3. **Clean break, no auto-migration.** v0.5 is a breaking release for state-directory layout. Existing v0.4 installs are detected and rejected at command entry with a pointer to the migration recipe. There is no half-state where v0.5 silently rewrites or absorbs v0.4 state.

NIP-42 AUTH is **out of scope for v0.5** and tracked as a v0.6 milestone (see §11). In v0.5, "public Nostr relay" topology is documented as experimental: it works against relays that do not require AUTH for kind:1059 reads; it silently fails (empty inbox) against relays that follow the NIP-17 §Recommendations and do require AUTH.

## 3. Configuration

### 3.1 New schema

```toml
state_dir = ""
log_level = "info"

[daemon]
  socket = "sock"
  shutdown_grace_seconds = 5

[relay]
  enabled  = false                # true to run the embedded relay on this host
  listen   = "0.0.0.0:22895"      # only consulted when enabled = true
  mode     = "paired"
  data_dir = "relay"

[publish]

[subscribe]
```

`Defaults()` returns `Relay.Enabled = false`, `Listen = "0.0.0.0:22895"`, `Mode = "paired"`, `DataDir = "relay"`.

### 3.2 Removed fields

- `relay.public_url` — never read by code; vestigial init-time placeholder. Removed from the struct, from `configKeys` in `cmd/eidos/gate/config.go`, and from `init.go`'s write path. The card and invite encoders read the URL from `own_relays(role='home')`, not from this field.

### 3.3 Helper

```go
func (c Config) RelayEnabled() bool { return c.Relay.Enabled }
```

All branch points that today implicitly assume "the relay exists" call this helper. There are no other code paths sniffing `Listen != ""` or otherwise inferring relay presence.

## 4. CLI surface

### 4.1 `eidos gate init` (changed)

```
eidos gate init --label <name> --home <url> [--with-local-relay [--listen <bind>]]
```

| Flag | Required | Behavior |
|---|---|---|
| `--label <name>` | yes | Existing semantics. |
| `--home <url>` | **yes (new)** | The URL peers will use to reach you. Must parse as `ws://` or `wss://`. Written verbatim to `own_relays(role='home')`. |
| `--with-local-relay` | no | When present: writes `relay.enabled = true` to config. |
| `--listen <bind>` | no | When present: writes `relay.listen = <bind>`. Requires `--with-local-relay` (rejected otherwise). When `--with-local-relay` is present without `--listen`, defaults to `0.0.0.0:22895`. |

**`--home` and `--listen` are independent.** A host can run the embedded relay on `0.0.0.0:22895` while declaring `--home wss://my.host` (e.g., when a reverse proxy fronts the relay). The relay's bind address never leaks into card / invite URIs.

**Validation errors at init**:

| Input | Error message |
|---|---|
| `--home` missing | `--home is required (the inbound relay URL peers will dial); see docs/USAGE.md for topology choices` |
| `--home` not `ws://` or `wss://` | `--home must start with ws:// or wss://` |
| `--listen` set without `--with-local-relay` | `--listen requires --with-local-relay` |

### 4.2 `eidos gate start` (changed)

Behavior split on `cfg.RelayEnabled()`:

- Always: install and `enable --now` the daemon unit.
- `cfg.RelayEnabled() == true`: also run the bind preflight, install and `enable --now` the relay unit.
- `cfg.RelayEnabled() == false`: skip the relay unit entirely. **Do not** install, **do not** start, **do not** uninstall any residual relay unit. Leave whatever state pre-exists alone — the user manages it via `systemctl` / `launchctl` directly, or via `eidos gate purge` (§4.5).

### 4.3 `eidos gate status` (changed)

When `cfg.RelayEnabled() == false`, the relay line reads `not installed`:

```
$ eidos gate status                          # daemon-only
  eidos-gate-daemon      active  pid=4123
  eidos-gate-relay       not installed

$ eidos gate status                          # with --with-local-relay
  eidos-gate-daemon      active  pid=4123
  eidos-gate-relay       active  pid=4131
```

`not installed` is the fact-level signal: the unit file is not in the install path that this `eidos` would manage. If the user has a residual unit in place from a previous opt-in, it still shows `active` (status reads from the service manager, not from config).

### 4.4 `eidos gate stop` (changed)

Iterates only over units that exist in the managed install path. Tolerates the relay unit being absent. No error.

### 4.5 `eidos gate purge` (unchanged semantics, refined implementation)

`purge` removes everything `eidos` ever installed: both unit names (`DaemonUnitName` and `RelayUnitName`), regardless of `cfg.RelayEnabled()`. This is the escape hatch for users who toggled local-relay on then off and want to clean up residual unit files. `purge --yes` skips the confirmation prompt as today.

### 4.6 `eidos gate relay` foreground command (changed)

Refuses to start when `cfg.RelayEnabled() == false`:

```
$ eidos gate relay
error: local relay is disabled (relay.enabled = false in config.toml).
to enable: eidos gate config set relay.enabled true && eidos gate config set relay.listen <host:port>
```

This prevents the foreground command's behavior from drifting from the unit's behavior.

### 4.7 `eidos gate config` (changed)

`configKeys` map gains `relay.enabled` (bool, "true" / "false") and loses `relay.public_url`. `relay.listen` validation continues to require a non-empty `host:port`; setting it to the empty string is rejected at set time. Disabling the local relay is done via `relay.enabled = false`, not by clearing `relay.listen` — separating the two concerns avoids the half-state of "relay enabled but no listen address".

### 4.8 No new shorthand commands

A `eidos gate use-local-relay` / `disable-local-relay` shorthand was considered and rejected. `eidos gate config set relay.enabled true|false` is sufficient and avoids duplicate entry points.

## 5. Data flow

### 5.1 Init

```
eidos gate init --label X --home <url> [--with-local-relay [--listen <bind>]]
  │
  ├─ Validate flags (§4.1)
  ├─ Generate identity → key file
  ├─ Migrate state.db; SetMeta(label, owner_pubkey, created_at, mindgate_version)
  ├─ INSERT INTO own_relays(role='home', url=<--home>)
  └─ Save config.toml:
       Relay.Enabled = (--with-local-relay present)
       Relay.Listen  = (--listen value, else the default "0.0.0.0:22895")
       # Listen is always written. When Enabled=false it is dormant (read by no
       # code path until the user later flips Enabled=true via config set).
```

### 5.2 Start

```
eidos gate start
  │
  ├─ Detect v0.4 state directory (§7) → if present, abort with migration error.
  ├─ Load config.toml.
  ├─ Install daemon unit (idempotent).
  ├─ if cfg.RelayEnabled():
  │     ├─ Preflight bind cfg.Relay.Listen.
  │     └─ Install relay unit (idempotent).
  └─ Enable + start configured units (daemon always; relay only if RelayEnabled).
```

### 5.3 Send / receive

Unchanged. Daemon publishes to (recipient's relays) ∪ (own_relays) ∪ (publish.fallback_relays); subscribes to all own_relays for `{kinds:[1059], #p:[ownerHex]}`. Whether each URL points to a process running on this host, on a VPS the user owns, or on a third-party Nostr relay does not affect the path.

### 5.4 Topology table

| Topology | own_relays | config.relay | Notes |
|---|---|---|---|
| Daemon + self-hosted home (VPS) | `[{home: wss://my-vps.example.com}]` | `enabled=false` | Single wss connection to VPS handles all in/out. |
| Daemon + public Nostr relay | `[{home: wss://relay.damus.io}]` | `enabled=false` | **Experimental in v0.5**; if the relay enforces NIP-17's AUTH-for-1059-reads recommendation, subscription returns no events until v0.6 ships NIP-42. |
| Daemon + bundled local relay | `[{home: ws://127.0.0.1:22895}]` | `enabled=true, listen="127.0.0.1:22895"` | Equivalent to v0.4. Suited for single-host two-instance debug. |
| Daemon + bundled local relay + public-facing URL | `[{home: wss://my.host}]` | `enabled=true, listen="0.0.0.0:22895"` | Local relay binds publicly; reverse proxy or direct DNS gives peers `wss://my.host`. |
| Hybrid (local + remote fallback) | `[{home: ws://127.0.0.1:22895}, {fallback: wss://my-vps.example.com}]` | `enabled=true, listen="127.0.0.1:22895"` | Daemon double-subscribes; sends fan out to both. Card / invite still embeds the home URL only. |

## 6. Components changed

| Path | Change |
|---|---|
| `internal/config/config.go` | Add `Relay.Enabled bool`. Remove `Relay.PublicURL`. Update `Defaults()`. Add `RelayEnabled()` helper. |
| `cmd/eidos/gate/init.go` | Add `--home` (required), `--with-local-relay`, refine `--listen` validation. Replace direct `homeRelayURL` literal with `--home` value. Drop `PublicURL` write. Add v0.4 detection (§7). |
| `cmd/eidos/gate/start.go` + `internal/service/{systemd_linux,launchd_darwin}.go` | Branch unit install on `cfg.RelayEnabled()`. Daemon unit always; relay unit conditional. |
| `cmd/eidos/gate/stop.go` + service implementations | Iterate over only the units that exist; tolerate missing relay unit. |
| `cmd/eidos/gate/status.go` | Render `not installed` for relay when `cfg.RelayEnabled() == false` and the unit file is not present. Read service manager for actual state. |
| `cmd/eidos/gate/purge.go` + service implementations | No-op change to behavior; ensure both unit names are unconditionally targeted for removal. |
| `cmd/eidos/gate/preflight.go` | Skip when `cfg.RelayEnabled() == false`. |
| `cmd/eidos/gate/relay.go` (foreground) | Refuse to run when `cfg.RelayEnabled() == false` with the §4.6 error. |
| `cmd/eidos/gate/config.go` | Add `relay.enabled`. Remove `relay.public_url`. |
| `cmd/eidos/gate/root.go` | Add v0.4 detection as `PersistentPreRunE` on `rootCmd`. `purge` and `version` subcommands set their own `PreRunE` to opt out. |
| `docs/USAGE.md` | Rewrite Step 0–4 around the three topologies. Demote local-relay walkthrough to a "single-host two-instance debug" subsection. |
| `docs/INSTALL.md` | Lead with daemon-only. Move TLS / reverse-proxy guidance to a "Self-hosting the embedded relay" subsection. Add "Migrating from v0.4" subsection. |
| `EXAMPLE.md` | Rewrite around the self-hosted topology (most representative for a deployment guide). |
| `README.md` | Quick-start uses `init --label X --home <url>`. |

**Not changed**: `internal/daemon/`, `internal/relayd/`, `internal/nostr/`, `internal/invite/`, `internal/card/`, `internal/contacts/`, `internal/inbox/`, `internal/envelope/`, `cmd/eidos/forge/`, `cmd/eidos/supervisor/`. The protocol-level code is already topology-agnostic.

## 7. v0.4 → v0.5 break

### 7.1 Detection

A v0.4 state directory has a `config.toml` with no `[relay].enabled` field (the field did not exist in v0.4). Detection uses BurntSushi/toml's `MetaData.IsDefined("relay", "enabled")` after `toml.DecodeFile` — Go's bool zero-value cannot otherwise distinguish "field missing" from "field present and false". The check is wired as `PersistentPreRunE` on the gate root command (`cmd/eidos/gate/root.go`), so it runs before every subcommand by default. Two subcommands explicitly opt out via their own `PreRunE` returning nil before the persistent hook can fire:

- `eidos gate purge` — skips detection so v0.4 users can actually clean up.
- `eidos gate version` (and any pure read-only top-level command that does not touch state) — skips detection so version diagnostics work even on a stale state dir.

For all other subcommands, after resolving the state directory and loading config, if the config file existed on disk **and** `IsDefined("relay", "enabled")` is false, the command exits with:

```
error: this state directory was created by an older eidos version (pre-v0.5).

v0.5 changes how the gate is initialized: the embedded relay is now opt-in,
and `eidos gate init` requires --home <url>.

Choose one:
  (A) Re-init from scratch (loses contacts, invites, inbox history):
        eidos gate purge --yes
        eidos gate init --label <your-label> --home <url> [--with-local-relay]

  (B) Migrate in place (keeps state):
        See docs/INSTALL.md#migrating-from-v04 for the SQL + config recipe.
```

Detection is implemented as a pre-dispatch hook on the root command, runs once, returns the error to stderr, exits non-zero. Tests assert this message for several v0.4-shaped fixtures.

The detection must distinguish "v0.4 state" (config.toml present, missing field) from "fresh install" (config.toml absent). A bare state directory with no config.toml is treated as fresh and dispatch proceeds as today.

### 7.2 Manual migration (B) recipe

In `docs/INSTALL.md#migrating-from-v04`:

```
1. Stop services:
   eidos gate stop

2. Edit config.toml — replace the [relay] block with:

   [relay]
     enabled  = true                          # if you want to keep the local relay
     listen   = "127.0.0.1:22895"             # whatever your previous bind was
     mode     = "paired"
     data_dir = "relay"

   To switch to daemon-only, set enabled = false and edit own_relays as below.

3. (Optional, daemon-only) Replace the home row in own_relays:

   sqlite3 ~/.eidos/gate/state.db <<'SQL'
     DELETE FROM own_relays WHERE role='home';
     INSERT INTO own_relays(relay_url, role, added_at)
       VALUES('wss://your-relay.example.com', 'home', strftime('%s','now'));
   SQL

4. eidos gate start
```

This is documentation only — no code helper. v0.5 does not ship a migrate subcommand. The expected user volume on this path is small (project is pre-1.0, install base is small) and a script would itself be a code surface to maintain.

### 7.3 No silent absorption

If detection fails to fire for a corner case (e.g., the user manually added `relay.enabled = false` to a v0.4 config to bypass the check), v0.5 will treat the state as a v0.5 install. This is acceptable: the user has explicitly stated "I know this is v0.5-shaped state".

## 8. Documentation changes

### 8.1 README.md

Quick-start replaces the implicit-local-relay path with a topology-explicit example:

```
# Daemon-only against a public Nostr relay (simplest):
eidos gate init --label alice --home wss://relay.damus.io
eidos gate start

# Self-host the relay on this same machine:
eidos gate init --label alice --home wss://alice.example.com \
  --with-local-relay --listen 0.0.0.0:22895
eidos gate start
```

A "Choosing a home relay" sub-section in the README points to docs/USAGE.md for the trade-off discussion.

### 8.2 docs/USAGE.md

Rewrite Steps 0–4 to lead with the daemon-only public-relay flow, then walk through self-hosted, then add a "Single-host two-instance debug" subsection that uses `--with-local-relay`. Drop the assumption that init implies a local relay.

### 8.3 docs/INSTALL.md

- Default-behavior paragraph: "By default, `eidos gate init` creates a daemon-only install: no local relay process is started."
- New subsection "Self-hosting the embedded relay" — covers `--with-local-relay`, TLS / reverse-proxy guidance, firewall, NAT.
- New subsection "Migrating from v0.4" — the §7.2 recipe.

### 8.4 EXAMPLE.md

Rewrite around the self-hosted topology — the most representative real-world deployment. Two users, each with their own VPS running the relay on a public hostname. Their daemons run on laptops / phones and connect to their own VPS over `wss://`.

### 8.5 CLAUDE.md (project root)

No structural change. Add one line under "Project Structure" clarifying that the embedded relay is opt-in and the daemon is the always-present component.

## 9. Testing

### 9.1 Unit tests

| File | Coverage |
|---|---|
| `internal/config/config_test.go` | `Defaults().Relay.Enabled == false`. `RelayEnabled()` returns the boolean directly. Loading a TOML file lacking `relay.enabled` → `Enabled` is the Go zero value `false`. |
| `cmd/eidos/gate/init_test.go` (new) | `--home` missing → error; `--home` not ws/wss → error; `--listen` without `--with-local-relay` → error; `--with-local-relay` without `--listen` → defaults to `0.0.0.0:22895`; legal combinations write the right `own_relays` row (always equals `--home`, never `--listen`) and the right config.toml. |
| `cmd/eidos/gate/root_test.go` (new) | v0.4 detection table: a state dir containing a config.toml with no `[relay].enabled` field triggers the §7 error on subcommands that wire through `PersistentPreRunE`; a config.toml with `relay.enabled = false` does not trigger; an empty / missing config.toml does not trigger; `purge` and `version` skip detection even on a v0.4-shaped state dir. |
| `cmd/eidos/gate/config_test.go` | `relay.enabled` round-trips through set/get. `relay.public_url` is no longer in the keys map. |
| `internal/service/systemd_linux_test.go`, `launchd_darwin_test.go` (new or extended) | Install path with daemon-only; install path with both units; uninstall tolerates missing relay unit; status reports `not installed` correctly. |

### 9.2 Integration tests

Existing `test/integration/{loopback,envelope,invite,selfheal}_test.go` use the in-process `bringUp` helper that bypasses CLI / service-manager. They continue to work without change.

New `test/integration/daemon_only_test.go`: two instances, one full (`bringUp`), one daemon-only (`bringUpDaemonOnly` writes `own_relays(role='home')` pointing at the first instance's relay URL, leaves `relay.enabled` false, starts only the daemon). Verifies:
- Daemon-only B publishes a chat envelope to A; A receives it (B's send fan-out reached A's relay).
- A publishes a chat envelope to B; B receives it (B's subscriber on A's relay sees the gift wrap).
- B's `eidos gate inbox` returns the same envelope payloads as a full install would.

### 9.3 Smoke tests (manual, in CHANGELOG)

```
# Fresh install, daemon-only:
eidos gate init --label test --home wss://example.com
eidos gate status                                       # eidos-gate-relay: not installed
cat ~/.eidos/gate/config.toml | grep enabled            # relay.enabled = false

# Re-init with local relay:
eidos gate purge --yes
eidos gate init --label test --home ws://127.0.0.1:9999 \
  --with-local-relay --listen 127.0.0.1:9999
eidos gate status                                       # both units active
ss -tlnp 'sport = :9999'                                # bound on the loopback port

# v0.4 detection:
mkdir /tmp/v04 && touch /tmp/v04/config.toml
echo 'log_level = "info"' > /tmp/v04/config.toml        # no [relay].enabled
EIDOS_GATE_HOME=/tmp/v04 eidos gate status              # exits non-zero with §7 error
```

## 10. Acceptance criteria

- `internal/config/config.go`: `Relay.Enabled` field exists; `Relay.PublicURL` does not. `RelayEnabled()` helper exists.
- `eidos gate init` exits non-zero when `--home` is missing, malformed, or when `--listen` is present without `--with-local-relay`.
- `eidos gate init --label X --home <url>` writes config.toml with `relay.enabled = false` and inserts a single `own_relays(role='home', url=<url>)` row.
- `eidos gate init ... --with-local-relay [--listen <bind>]` writes `relay.enabled = true` and the right `relay.listen` value; the home row URL still equals `--home`, never `--listen`.
- `eidos gate start` with `relay.enabled = false` installs only the daemon unit; with `relay.enabled = true` installs both.
- `eidos gate stop` and `eidos gate status` tolerate the relay unit being absent.
- `eidos gate purge` removes both unit names regardless of `relay.enabled`.
- `eidos gate relay` foreground command refuses with §4.6 error when `relay.enabled = false`.
- v0.4 state directories are detected and rejected with the §7.1 error on every subcommand entry.
- README.md, docs/USAGE.md, docs/INSTALL.md, EXAMPLE.md updated per §8.
- `go test ./...` and `go test -tags=integration ./...` both pass, including the new `daemon_only_test.go`.
- `gofmt` and `go vet` clean.

## 11. Future direction (v0.6)

NIP-42 AUTH end-to-end:

- Daemon-side client: respond to inbound `["AUTH", challenge]` frames by signing a `kind:22242` event and returning `["AUTH", <signed>]`. Wire this into both subscribe and publish paths. Use the existing `go-nostr` `nip42` package.
- Embedded `relayd`: enforce AUTH-for-1059-reads. Issue AUTH challenges on connect; restrict `kind:1059` REQ responses to clients whose authenticated pubkey matches the `p` tag (i.e., the owner reading their own gift wraps). Use khatru's `OnAuth` and `RejectFilter` hooks.

Strict YAGNI applies to v0.6 envelope and IPC fields: do **not** add `client.relay_caps`, `auth_state` exposures, or other speculative wire fields without a concrete consumer. The v0.6 spec records its own deferrals.

After v0.6 ships, the "public Nostr relay" topology becomes production-grade: a v0.6 daemon connecting to a NIP-17-compliant public relay authenticates on connect, receives its kind:1059 events, and operates indistinguishably from a self-hosted topology at the user-visible level.

## 12. Out of scope

- NIP-42 AUTH (deferred to v0.6 spec; see §11).
- NIP-65 relay-list discovery. Currently we attach a relay list to each contact at add-contact / invite-redemption time; NIP-65's kind:10002 events would let us discover them automatically. Tangential to decoupling. Tracked as a separate future improvement.
- Rate-limiting / abuse prevention on the embedded relay. The kind:1059 + p-tag filter (`internal/relayd/relayd.go:46`) is the only check today; spam mitigation is a separate concern.
- Multi-home support. `cardExport` and `inviteCreate` read `own_relays WHERE role='home' LIMIT 1`. Allowing multiple home relays (declared in card and invite URIs) would improve resilience but complicates the URI format. Not in v0.5.
- A separate `eidos-relay` binary for relay-only deployments. The same binary covers both roles via subcommand selection; users running a relay-only VPS can ignore the daemon subcommand. Splitting the binary is deferred until packaging size or dependency surface becomes a real complaint.
- Telemetry / observability for the home relay (latency, ban list, AUTH success rate). Future work.
