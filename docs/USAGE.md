# MindGate v0 Usage

Two-user walkthrough mirroring `EXAMPLE.md`.

## Step 0 — Both users initialize

```
$ mindgate init
✓ created /home/alice/.mindgate
✓ generated keypair → /home/alice/.mindgate/key (0600)
✓ wrote state.db (schema v1)
✓ wrote config.toml

your identity:
  npub: npub1alice...
  hex:  <64-hex-chars>

next steps:
  1) start daemon: mindgate daemon
  2) start relay:  mindgate relay
  3) share card:   mindgate card
```

## Step 1 — Each starts daemon and relay

```
$ mindgate daemon &
$ mindgate relay &
```

The daemon logs to stderr: `mindgate-daemon starting state_dir=...`
The relay logs to stderr: `mindgate-relay listening 127.0.0.1:7777 mode=paired`

## Step 2 — Each prints their card

```
$ mindgate card
mindgate://npub1alice...@ws%3A%2F%2Falice.host%3A7777/?label=alice
```

Send this URI to the peer out-of-band (Signal, email, scan, etc.).

## Step 3 — Each adds the peer's contact

Alice runs:

```
$ mindgate add-contact 'mindgate://npub1bob...@wss%3A%2F%2Fbob.host%3A7777/?label=Bob'
added npub1bob...
```

Bob does the symmetric add. Both directions are required for traffic to flow:
each home relay only accepts inbound writes addressed to its owner via NIP-17
gift wraps.

## Step 4 — Send and receive

Alice:

```
$ mindgate send npub1bob... "Hey Bob, my MindGate is up."
event_id: 5f8e...
accepted_by:
  ws://127.0.0.1:7777
  wss://bob.host:7777
```

Bob (in another terminal):

```
$ mindgate inbox --tail
2026-05-06 22:14:01  npub1alice...0000  Hey Bob, my MindGate is up.
```

## Other commands

- `mindgate whoami` — your identity, label, home relays
- `mindgate contacts` — list contacts
- `mindgate remove-contact <npub>` — remove a contact
- `mindgate relays` — list own relays
- `mindgate relay-add <url>` — add a relay (`--role home|fallback`, default `fallback`)
- `mindgate relay-remove <url>` — remove a relay
- `mindgate outbox` — sent history
- `mindgate scan <uri>` — parse a card URI without storing
- `mindgate version` — print version (`mindgate 0.0.1`)

### Flags common to most commands

- `--state-dir <path>` — override the state directory (also via `$MINDGATE_HOME`)
- `mindgate inbox --from <npub>` — filter inbox by sender
- `mindgate inbox --since <unix-seconds>` — show messages since timestamp
- `mindgate inbox --limit <n>` — cap results (default 50)
- `mindgate send --stdin` — read message body from stdin instead of argument
- `mindgate outbox --to <npub>` — filter outbox by recipient
