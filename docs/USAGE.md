# Eidopsyche Usage

Two-user walkthrough mirroring `EXAMPLE.md`.

## Step 0 — Both users initialize

`--label` is required: every identity must declare a non-empty label that
peers will see by default in your card URI. Change it later with
`eidos gate set-label`.

```
$ eidos gate init --label alice
✓ created /home/alice/.eidos/gate
✓ generated keypair → /home/alice/.eidos/gate/key (0600)
✓ wrote state.db (schema v1)
✓ wrote config.toml

your identity:
  npub: npub1alice...
  hex:  <64-hex-chars>

next steps:
  1) start services: eidos gate start
  2) share card:     eidos gate card
```

## Step 1 — Each starts daemon and relay

```
$ eidos gate start
✓ gate services started
  eidos-gate-daemon      active  pid=4123
  eidos-gate-relay       active  pid=4131
```

`eidos gate start` installs the appropriate OS service units for the
daemon and relay and brings both up: systemd units on Linux
(`~/.config/systemd/user/`), launchd plists on macOS
(`~/Library/LaunchAgents/`). Use `eidos gate status` to check,
`eidos gate stop` to halt without uninstalling, and `eidos gate purge` to
remove everything. Append `--system` to any of these to install to the
system path (`/etc/systemd/system/` or `/Library/LaunchDaemons/`,
respectively) — requires root.

On Linux user-mode, services survive your shell exiting; for survival
across a full logout on a headless host, run
`loginctl enable-linger <username>` once. macOS LaunchAgents auto-start
at GUI login.

For ad-hoc / debugging runs, the original `eidos gate daemon` and
`eidos gate relay` foreground commands still work — those are exactly
what the OS service units invoke.

Logs:
- Linux: `journalctl --user -u eidos-gate-daemon` (or `-u ...` for
  `--system` mode).
- macOS: launchd redirects stdout/stderr to
  `<state-dir>/logs/eidos-gate-{daemon,relay}.log`.

Daemon startup line: `eidos-gate-daemon starting state_dir=...`. Relay
startup line: `eidos-gate-relay listening 0.0.0.0:22895 mode=paired`.

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

## Other commands

Service lifecycle:
- `eidos gate start` — install + start the daemon and relay as OS service
  units (systemd on Linux, launchd on macOS)
- `eidos gate stop` — stop without uninstalling
- `eidos gate status` — show installed / enabled / active state per unit
- `eidos gate purge` — stop, uninstall units, and delete the state directory
  (`--yes` skips the confirmation prompt; `--system` operates on
  the system path — `/etc/systemd/system/` on Linux,
  `/Library/LaunchDaemons/` on macOS — instead of user units)

Foreground (debug) mode:
- `eidos gate daemon` — run the daemon in the foreground
- `eidos gate relay` — run the embedded relay in the foreground

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
  already on latest. Pass `--force` to reinstall the same version.

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

Each instance needs its own state directory and its own relay port:

```
# Instance A
$ eidos gate --state-dir /tmp/mg-a init --label alice
$ eidos gate --state-dir /tmp/mg-a daemon &
$ eidos gate --state-dir /tmp/mg-a relay &

# Instance B
$ eidos gate --state-dir /tmp/mg-b init --label bob --listen 127.0.0.1:22896
$ eidos gate --state-dir /tmp/mg-b daemon &
$ eidos gate --state-dir /tmp/mg-b relay &
```

The `--listen` flag aligns config.toml's `relay.listen` and the `own_relays` home row with the relay's actual bind address.

Start order does not matter: if the daemon starts before its relay is
listening, it will retry the subscription with exponential backoff (1 s, 2 s,
… up to 60 s) and reconnect automatically once the relay is reachable. Adding
or removing a contact or relay via CLI also triggers an immediate refresh, so
the new relay URL becomes live without restarting the daemon.

## Changing settings after init

```
$ eidos gate config get               # dump all scalar keys
$ eidos gate config get relay.listen
$ eidos gate config set log_level debug
$ eidos gate config set relay.mode public
```

`config` writes `config.toml` directly; the daemon and relay must be restarted to pick up changes. Adding/removing relay entries in the SQLite `own_relays` table goes through `eidos gate relay-add` / `relay-remove` instead.
