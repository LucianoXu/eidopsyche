# Installing Eidopsyche

Eidopsyche ships as a single binary `eidos`. The current release exposes the
`gate` subcommand tree (MindGate — communication layer); `forge` (mind-form
lifecycle) and `supervisor` (container PID 1) are reserved as stubs and land
in future releases.

## Quick install (recommended)

```sh
curl -fsSL https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh | sh
```

The script:

- detects your OS and CPU (linux/darwin/windows × amd64/arm64),
- downloads the matching archive from the latest GitHub Release,
- **verifies SHA256** against `checksums.txt` (mandatory; aborts on mismatch),
- installs the binary to `~/.local/bin/eidos`.

Override the install location with `PREFIX`:

```sh
# system-wide
curl -fsSL https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh | sudo PREFIX=/usr/local sh

# pin a specific tag
curl -fsSL https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh | EIDOS_VERSION=v0.1.0 sh
```

If `$HOME/.local/bin` is not on your `$PATH`, the script prints a note;
add it to your shell rc:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

### Updating

`eidos` checks GitHub Releases at most once per 24 hours and prints a
notification when a newer version is available. To upgrade:

```sh
eidos self-update
```

`self-update` re-runs the install script and atomically replaces the binary
at the same prefix. Disable update notifications with
`EIDOS_NO_UPDATE_CHECK=1`.

## Build from source

```sh
git clone https://github.com/LucianoXu/eidopsyche.git
cd eidopsyche
make build         # → bin/eidos
make install       # → ~/.local/bin/eidos
```

`make install` accepts the same `PREFIX` / `DESTDIR` variables as the install
script:

```sh
make install                                       # default: ~/.local/bin/eidos
sudo make install PREFIX=/usr/local                # → /usr/local/bin/eidos
make install DESTDIR=/tmp/stage PREFIX=/usr/local  # staged for packaging
make uninstall                                     # symmetric removal
```

Source builds set `eidos version` to `dev`; the update checker recognises
this sentinel and skips itself entirely.

## Initialize a state directory

```sh
eidos gate init
```

Creates `~/.eidos/gate/` (override with `EIDOS_GATE_HOME`, `XDG_STATE_HOME`,
or `--state-dir`):

- `key`         — your private key (mode 0600)
- `state.db`    — SQLite for contacts, relays, metadata
- `config.toml` — daemon and relay configuration
- `relay/`      — relay data directory (created when relay starts)

State directory resolution precedence (highest to lowest):

1. `--state-dir <path>` flag
2. `$EIDOS_GATE_HOME`
3. `$XDG_STATE_HOME/eidos/gate`
4. `~/.eidos/gate`

`init` is **not** idempotent: it refuses to run if `key` already exists.
Remove `~/.eidos/gate/key` deliberately if you want a fresh identity (or
`rm -rf ~/.eidos/gate` to wipe everything).

Typical output:

```
✓ created /home/alice/.eidos/gate
✓ generated keypair → /home/alice/.eidos/gate/key (0600)
✓ wrote state.db (schema v1)
✓ wrote config.toml

your identity:
  npub: npub1...
  hex:  <64-hex-chars>

next steps:
  1) start daemon: eidos gate daemon
  2) start relay:  eidos gate relay
  3) share card:   eidos gate card
```

## Start the daemon and relay

In two terminals (or via systemd, see below):

```sh
eidos gate daemon
eidos gate relay
```

The default relay binds `127.0.0.1:22895`. To accept inbound from a peer on
another host, change `relay.listen` in `config.toml` to `0.0.0.0:22895` (and
configure firewall / DNS accordingly). The relay also requires
`relay.public_url` to be set to the externally reachable WebSocket URL so
that your card URI is correct.

## Sample systemd user units

`~/.config/systemd/user/eidos-gate-daemon.service`:

```ini
[Unit]
Description=Eidopsyche gate daemon
After=network-online.target

[Service]
Type=simple
ExecStart=%h/.local/bin/eidos gate daemon
Restart=on-failure

[Install]
WantedBy=default.target
```

`~/.config/systemd/user/eidos-gate-relay.service`:

```ini
[Unit]
Description=Eidopsyche gate paired relay
After=eidos-gate-daemon.service

[Service]
Type=simple
ExecStart=%h/.local/bin/eidos gate relay
Restart=on-failure

[Install]
WantedBy=default.target
```

Then `systemctl --user enable --now eidos-gate-daemon eidos-gate-relay`.

## Backup

`tar czf eidos-gate-state.tar.gz "${EIDOS_GATE_HOME:-$HOME/.eidos/gate}/"`.
The archive contains everything that defines your identity and history.
Restore by extracting on the target host and running `eidos gate daemon`
(and `eidos gate relay` if you also run your own).

## Resetting

To start over, stop the daemon and relay, then
`rm -rf "${EIDOS_GATE_HOME:-$HOME/.eidos/gate}"`. The next `eidos gate init`
generates a new identity (npub).
