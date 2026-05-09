# Eidopsyche Usage

Two-user walkthrough mirroring `EXAMPLE.md`.

## Step 0 — Pick a topology

`eidos gate init` requires two flags: `--label` (the name peers see by
default) and `--home <url>` (the URL peers will dial to reach you). The
home URL can point to three kinds of relay; pick the one that matches
where you are running this gate:

### A) Public Nostr relay (zero infrastructure)

```
$ eidos gate init --label alice --home wss://relay.damus.io
```

The relay operator sees gift-wrap metadata (who, when, how often).
Content stays end-to-end encrypted via NIP-17.

The daemon authenticates to the relay via NIP-42 automatically using
your gate's identity key on every connection — no setup needed. When
the relay enforces NIP-17's recommended AUTH-for-`kind:1059`-reads
gate (the universal posture of public relays), the daemon's REQ
succeeds only after AUTH; failed-AUTH states show up in
`eidos gate status` and the dashboard's Relays panel.

### B) Self-hosted relay on a separate host (recommended)

Run the relay on a box with a public address (a $5 VPS works); run the
daemon wherever you actually use eidos.

```
# On the laptop / phone / NAT'd box (this gate):
$ eidos gate init --label alice --home wss://my-vps.example.com

# On the VPS, run a separate daemon-and-relay install whose --listen
# binds publicly. Front it with TLS (Caddy / nginx / Cloudflare Tunnel)
# so peers can reach the wss:// URL.
```

See `docs/INSTALL.md#self-hosting-the-embedded-relay` for the VPS side.

### C) Self-hosted relay on the same box (or single-host two-instance debug)

`eidos relay` is an independent top-level subcommand; initialize and start it
separately from the gate:

```
# Gate side:
$ eidos gate init --label alice --home wss://alice.example.com

# Relay side (run once to create ~/.config/eidos/relay/config.toml):
$ eidos relay init --mode paired --listen 0.0.0.0:22895
```

`--home` and `--listen` are independent: the relay binds to `--listen`
(`0.0.0.0:22895`), but peers dial the URL in `--home`
(`wss://alice.example.com`, typically a reverse-proxied TLS endpoint
forwarding to the local port).

For pure local debug (one host, two instances):

```
# Instance A
$ eidos gate init --label alice --home ws://127.0.0.1:22895
$ eidos relay init --dir /tmp/relay-a --mode paired --listen 127.0.0.1:22895

# Instance B
$ eidos gate --state-dir /tmp/mg-b init --label bob --home ws://127.0.0.1:22896
$ eidos relay init --dir /tmp/relay-b --mode paired --listen 127.0.0.1:22896
```

## Step 1 — Each starts services

```
$ eidos gate start
✓ gate services started
  eidos-gate-daemon      active  pid=4123
```

`eidos gate start` installs the appropriate OS service units for the
gate daemon and brings them up: systemd units on Linux
(`~/.config/systemd/user/`), launchd plists on macOS
(`~/Library/LaunchAgents/`). Use `eidos gate status` to check,
`eidos gate stop` to halt without uninstalling, and `eidos gate purge`
to remove everything. Append `--system` to any of these to install to
the system path (`/etc/systemd/system/` or `/Library/LaunchDaemons/`,
respectively) — requires root.

If you set up a local relay, start it separately:

```
$ eidos relay service install
$ eidos relay service start
$ eidos relay service status
  eidos-relay            active  pid=4124
$ eidos relay status   # only readable when the relay process is stopped
config dir: /home/alice/.config/eidos/relay
mode:       paired
listen:     0.0.0.0:22895
owner:      0123…cdef
tls:        cert="" key=""
auth:       required=true service_url=""
events:     0 stored
```

`eidos relay status` opens the badger event store directly to count
events, so it cannot run while the relay process holds the directory
lock. Use `eidos relay service status` for liveness while the unit
is active; stop the unit before running `eidos relay status` for the
event-count probe.

On Linux user-mode, services survive your shell exiting; for survival
across a full logout on a headless host, run
`loginctl enable-linger <username>` once. macOS LaunchAgents auto-start
at GUI login.

For ad-hoc / debugging runs, use the foreground commands:
- `eidos gate daemon` — run the gate daemon in the foreground
- `eidos relay start` — run the relay in the foreground

Logs:
- Gate daemon on Linux: `journalctl --user -u eidos-gate-daemon`
- Relay on Linux: `journalctl --user -u eidos-relay`
- macOS: launchd redirects stdout/stderr to
  `<state-dir>/logs/eidos-gate-daemon.log` and
  `~/.config/eidos/relay/logs/eidos-relay.log`.

Daemon startup line: `eidos-gate-daemon starting state_dir=...`. Relay
startup line: `eidos-relay listening 0.0.0.0:22895 mode=paired`.

## Step 2 — Each prints their card

```
$ eidos gate card
mindgate://npub1alice...@ws%3A%2F%2Falice.host%3A22895/?label=alice
```

Send this URI to the peer out-of-band (Signal, email, scan, etc.).

## Step 3 — Each adds the peer's contact

Alice runs:

```
$ eidos gate add-contact 'mindgate://npub1bob...@wss%3A%2F%2Fbob.host%3A22895/?label=Bob'
added npub1bob...
```

Bob does the symmetric add. Both directions are required for traffic to flow:
each home relay only accepts inbound writes addressed to its owner via NIP-17
gift wraps.

## Step 4 — Send and receive

Alice:

```
$ eidos gate send npub1bob... "Hey Bob, my MindGate is up."
event_id: 5f8e...
accepted_by:
  ws://127.0.0.1:22895
  wss://bob.host:22895
```

Bob (in another terminal):

```
$ eidos gate inbox --tail
2026-05-06 22:14:01  npub1alice...0000  Hey Bob, my MindGate is up.
```

### Sending an operator command

You can send a v1 command envelope to your own daemon (e.g., from a
thin-client device) using `--command`:

```
$ eidos gate send <your-own-npub> --command status
event_id: 5f8e...
accepted_by:
  ws://127.0.0.1:22895
```

The reply lands in your own inbox a moment later:

```
$ eidos gate inbox --tail
2026-05-07 10:14:18  npub1self...0000  eidos-gate v0.4.0  uptime 2h 3m
contacts: 1 master, 4 friend, 0 acquaintance, 0 blocked
relays:   2 configured
forge:    n/a (forge subcommand not yet integrated)
```

Commands are only executed when the sender pubkey matches your own
identity. Other senders' commands are stored in your inbox marked
`[malformed: unauthorized_command]` and never run.

Available commands in this release: `status` (read-only).

### Message wire format

MindGate puts a structured v1 envelope in the NIP-17 rumor content:

```json
{
  "v": 1,
  "type": "chat",
  "text": "hello",
  "client": { "name": "eidos", "ver": "0.4.0" }
}
```

Messages from peers running a pre-envelope MindGate version (or any other
NIP-17 client that sends raw text) are persisted in your inbox marked as
`[malformed: not_envelope]` and are not delivered to your mind-form. See
the spec at `docs/superpowers/specs/2026-05-07-envelope-v1-design.md`.

## Web Dashboard

The daemon serves a local web dashboard at `http://127.0.0.1:22893` whenever
it is running. It is **loopback-only** by default — anyone with shell access
to the host already has access to your gate state, so the dashboard inherits
that trust boundary and adds no auth on top.

Open the dashboard:

```
$ eidos gate dashboard
http://127.0.0.1:22893
# (browser opens)
```

Use `--no-open` for a headless host:

```
$ eidos gate dashboard --no-open
http://127.0.0.1:22893
```

If `eidos gate dashboard` errors with "dashboard not reachable", start the
daemon first (`eidos gate start` or `eidos gate daemon`).

Disable the dashboard entirely by setting `[dashboard] enabled = false` in
`config.toml`. Non-loopback bind (e.g. `0.0.0.0:22893`) is refused in v1
with an error log line; for remote access, use SSH port-forwarding:

```
$ ssh -L 22893:localhost:22893 user@your-server
$ open http://localhost:22893    # in another terminal on your laptop
```

What's in v1: chat thread per contact (primary), all-messages /
soft-rejected list views, in-place compose, compose-to-npub for new
recipients. Setup actions (`init`, contact / relay / invite management,
label changes) stay in the CLI.

## Other commands

Service lifecycle:
- `eidos gate start` — install + start the daemon and relay as OS service
  units (systemd on Linux, launchd on macOS)
- `eidos gate stop` — stop without uninstalling
- `eidos gate restart` — restart the daemon so a freshly-installed binary
  takes effect (idempotent: starts the daemon if it was stopped). The
  install script invokes `eidos gate restart --if-running` automatically
  after `eidos self-update` so a managed daemon picks up the new code
  without manual intervention; pass `--if-running` yourself when
  scripting against it to make the call a no-op on hosts where the
  daemon is not installed as a service
- `eidos gate status` — show installed / enabled / active state per unit
- `eidos gate purge` — stop, uninstall units, and delete the state directory
  (`--yes` skips the confirmation prompt; `--system` operates on
  the system path — `/etc/systemd/system/` on Linux,
  `/Library/LaunchDaemons/` on macOS — instead of user units)

Foreground (debug) mode:
- `eidos gate daemon` — run the daemon in the foreground
- `eidos relay start` — run the relay in the foreground

Identity & contacts:
- `eidos gate whoami` — your identity, label, home relays
- `eidos gate set-label <new-label>` — change your own label (the one shown
  in `whoami` and embedded in your card URI)
- `eidos gate contacts` — list contacts
- `eidos gate set-contact-label <target> <new-label>` — rename a contact
  (target accepts npub / hex / current label)
- `eidos gate remove-contact <npub>` — remove a contact
- `eidos gate relays` — list own relays
- `eidos gate relay-add <url>` — add a relay (`--role home|fallback`, default `fallback`)
- `eidos gate relay-remove <url>` — remove a relay
- `eidos gate outbox` — sent history
- `eidos gate scan <uri>` — parse a card URI without storing
- `eidos gate reconnect` — force the daemon to recompute its relay subscription
  set and reattach. Useful after manual `config set` changes or to trigger a
  refresh without restarting.

Top-level:
- `eidos version` — print version, commit, build date
- `eidos self-update` — upgrade to the latest published release; no-op when
  already on latest. Pass `--force` to reinstall the same version. After
  the new binary is in place the install script runs `eidos gate restart
  --if-running` so a managed daemon picks up the new code automatically;
  pass `--no-restart` (or export `EIDOS_NO_RESTART=1`) to suppress that
  step.

### Flags common to most commands

- `--state-dir <path>` — override the state directory (also via `$EIDOS_GATE_HOME`)
- `eidos gate inbox --from <npub>` — filter inbox by sender
- `eidos gate inbox --since <unix-seconds>` — show messages since timestamp
- `eidos gate inbox --limit <n>` — cap results (default 50)
- `eidos gate send --stdin` — read message body from stdin instead of argument
- `eidos gate outbox --to <npub>` — filter outbox by recipient

## Targeting a contact: npub / hex / label

Wherever a `<npub>` argument appears (`send`, `inbox --from`, `outbox --to`,
`remove-contact`), you may pass any of:

- a bech32 npub: `npub1alice...`
- a 64-char hex pubkey: `79be667ef9dcbbac...`
- a contact label: `Alice` (matched case-sensitively against the `label` you
  set when adding the contact)

If multiple contacts share a label, eidos gate refuses to disambiguate and asks
you to use the npub instead. `add-contact` continues to take only an npub or a
`mindgate://` URI — labels are an output convenience, not a way to introduce
new identities.

## Inviting a contact (one-step)

The `add-contact` flow requires both sides to exchange and add each other manually. For most cases, an **invite** does it in one OOB hop:

```
# Alice creates an invite (default: single-use, expires in 7 days)
$ eidos gate invite create --issuer-label "Alice" --redeemer-label "Bob"
mindgate-invite://eyJ2IjoxLCJpc3N1ZXJfbnB1YiI6Im5wdWIxYWxpY2UuLi4i...
id=ab12cd34ef56  max_uses=1  expires=2026-05-14T22:14:01+00:00

# Alice sends the URI to Bob via Signal/email/etc.
# Bob runs:
$ eidos gate redeem mindgate-invite://eyJ2IjoxLCJp...
redeemed: npub1alice...
relay:    ws://alice.host:22895
accepted_by:
  ws://alice.host:22895
  ws://127.0.0.1:22895
```

Both ends now have each other in `contacts`. Alice sees a `contact.added` event in `eidos gate inbox --tail` if she's listening.

Reusable invites:

```
eidos gate invite create --max-uses 5 --expires 24h --issuer-label "Alice"
eidos gate invite create --unlimited --no-expiry          # for a public-ish self-promo
```

Manage:

```
eidos gate invite list
eidos gate invite revoke <id-prefix>     # 12 chars usually unique
```

The token is a self-contained signed credential. Anyone who holds it can redeem (until exhausted/revoked/expired); share it only via channels you trust.

## Running two instances on one host (debugging)

Each gate instance needs its own state directory; each relay needs its own
config directory and port. Gate and relay are independent processes:

```
# Instance A — gate
$ eidos gate --state-dir /tmp/mg-a init --label alice --home ws://127.0.0.1:22895
$ eidos gate --state-dir /tmp/mg-a daemon &
# Instance A — relay
$ eidos relay init --dir /tmp/relay-a --mode paired --listen 127.0.0.1:22895
$ eidos relay start --dir /tmp/relay-a &

# Instance B — gate
$ eidos gate --state-dir /tmp/mg-b init --label bob --home ws://127.0.0.1:22896
$ eidos gate --state-dir /tmp/mg-b daemon &
# Instance B — relay
$ eidos relay init --dir /tmp/relay-b --mode paired --listen 127.0.0.1:22896
$ eidos relay start --dir /tmp/relay-b &
```

`--home` (in gate) and `--listen` (in relay) are independent. For local debug
they typically point to the same `host:port`; in real deployments they diverge
(relay binds `0.0.0.0:22895` while peers dial `wss://your.host`).

Start order does not matter: if the daemon starts before its relay is
listening, it will retry the subscription with exponential backoff (1 s, 2 s,
… up to 60 s) and reconnect automatically once the relay is reachable. Adding
or removing a contact or relay via CLI also triggers an immediate refresh, so
the new relay URL becomes live without restarting the daemon.

## Changing settings after init

```
$ eidos gate config get               # dump all gate scalar keys
$ eidos gate config set log_level debug

$ eidos relay config get                    # dump all relay scalar keys
$ eidos relay config get relay.listen       # print one key
$ eidos relay config set relay.mode public
```

`eidos gate config` and `eidos relay config` each write their respective
`config.toml` directly; the daemon / relay must be restarted to pick up
changes. Adding/removing relay entries in the SQLite `own_relays` table goes
through `eidos gate relay-add` / `relay-remove` instead.
