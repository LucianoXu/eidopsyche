# Installing MindGate v0

MindGate is a single binary (`mindgate`) that provides three modes:

- `mindgate daemon` — long-running per-user agent
- `mindgate relay`  — embedded Nostr relay (paired or public mode)
- `mindgate <verb>` — CLI subcommands

## Build

```
git clone <repo> eidopsyche
cd eidopsyche
make build
```

Equivalent to `go build -o bin/mindgate ./cmd/mindgate`. The binary depends on no system libraries (pure-Go SQLite via `modernc.org/sqlite`); it runs on Linux/macOS/Windows.

## Install

To put `mindgate` on your `$PATH`:

```
make install                                       # → ~/.local/bin/mindgate (default; no sudo)
sudo make install PREFIX=/usr/local                # → /usr/local/bin/mindgate (system-wide)
make install DESTDIR=/tmp/stage PREFIX=/usr/local  # staged install for packaging
make uninstall                                     # symmetric removal (matches the PREFIX you used)
```

`PREFIX` defaults to `$HOME/.local` (XDG user-local convention; works without sudo). `DESTDIR` is prepended for staged builds (e.g. when packaging into a `.deb` or `.tar.gz`).

If `~/.local/bin` is not yet on your `$PATH`, `make install` will print a note. Add this to your shell rc:

```
export PATH="$HOME/.local/bin:$PATH"
```

## Initialize a state directory

```
./bin/mindgate init
```

This creates `~/.mindgate/` (override with `MINDGATE_HOME`, `XDG_STATE_HOME`, or `--state-dir`):

- `key`           — your private key (mode 0600)
- `state.db`      — SQLite for contacts, relays, metadata
- `config.toml`   — daemon and relay configuration
- `relay/`        — relay data directory (created when relay starts)

State directory resolution precedence (highest to lowest):

1. `--state-dir <path>` flag
2. `$MINDGATE_HOME`
3. `$XDG_STATE_HOME/mindgate`
4. `~/.mindgate`

`init` is **not** idempotent: it refuses to run if `key` already exists. Remove `~/.mindgate/key` deliberately if you want a fresh identity (or `rm -rf ~/.mindgate` to wipe everything).

Typical `init` output:

```
✓ created /home/alice/.mindgate
✓ generated keypair → /home/alice/.mindgate/key (0600)
✓ wrote state.db (schema v1)
✓ wrote config.toml

your identity:
  npub: npub1...
  hex:  <64-hex-chars>

next steps:
  1) start daemon: mindgate daemon
  2) start relay:  mindgate relay
  3) share card:   mindgate card
```

## Start the daemon and relay

In two terminals (or via systemd, see below):

```
./bin/mindgate daemon
./bin/mindgate relay
```

The default relay binds `127.0.0.1:22895`. To accept inbound from a peer on
another host, change `relay.listen` in `config.toml` to `0.0.0.0:22895` (and
configure firewall / DNS accordingly). The relay also requires `relay.public_url`
to be set to the externally reachable WebSocket URL so that your card URI is
correct.

## Sample systemd user units

`~/.config/systemd/user/mindgate-daemon.service`:

```ini
[Unit]
Description=MindGate daemon
After=network-online.target

[Service]
Type=simple
ExecStart=%h/bin/mindgate daemon
Restart=on-failure

[Install]
WantedBy=default.target
```

`~/.config/systemd/user/mindgate-relay.service`:

```ini
[Unit]
Description=MindGate paired relay
After=mindgate-daemon.service

[Service]
Type=simple
ExecStart=%h/bin/mindgate relay
Restart=on-failure

[Install]
WantedBy=default.target
```

Then `systemctl --user enable --now mindgate-daemon mindgate-relay`.

## Backup

`tar czf mindgate-state.tar.gz $MINDGATE_HOME/`. The archive contains
everything that defines your identity and history. Restore by extracting on the
target host and running `mindgate daemon` (and `mindgate relay` if you also run
your own).

## Resetting

To start over, stop the daemon and relay, then `rm -rf $MINDGATE_HOME`. The
next `mindgate init` generates a new identity (npub).
