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

`self-update` first probes GitHub Releases for the latest tag. If you are
already on it, the command exits as a no-op. Otherwise it re-runs the install
script and atomically replaces the binary at the same prefix. Pass `--force`
to reinstall the current version unconditionally. Disable update
notifications with `EIDOS_NO_UPDATE_CHECK=1`.

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

`--label` is required: every identity must declare a non-empty label that
peers see by default in your card URI. Change it later with
`eidos gate set-label <new-label>`.

```sh
eidos gate init --label alice
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

```sh
eidos gate start    # installs systemd user units, enables and starts both
eidos gate status   # show installed / enabled / active state per unit
eidos gate stop     # stop without uninstalling
```

`start` writes `eidos-gate-daemon.service` and `eidos-gate-relay.service`
under `~/.config/systemd/user/`, then `systemctl --user enable --now`s them.
The units restart on failure and survive your shell exiting. To survive a
full logout (e.g., on a headless server), enable lingering once for your
user: `loginctl enable-linger <username>`.

The default relay binds `127.0.0.1:22895`. To accept inbound from a peer on
another host, change `relay.listen` in `config.toml` to `0.0.0.0:22895` (and
configure firewall / DNS accordingly). The relay also requires
`relay.public_url` to be set to the externally reachable WebSocket URL so
that your card URI is correct. After config changes, run `eidos gate start`
again — it re-applies the unit files and is idempotent — or restart with
`systemctl --user restart eidos-gate-{daemon,relay}`.

### System-wide install

For shared / production hosts, write units to `/etc/systemd/system/`
instead so they survive across users and start at boot:

```sh
sudo eidos gate start --system
sudo eidos gate status --system
sudo eidos gate stop --system
```

System-mode units run as root by default; tighten with a dedicated
service-level `User=` directive if you need least-privilege.

### Foreground mode

The original `eidos gate daemon` and `eidos gate relay` commands still work
and run in the foreground — useful for debugging or for environments
without systemd. They are exactly what `eidos gate start`'s units invoke.

## Backup

```sh
tar czf eidos-gate-state.tar.gz "${EIDOS_GATE_HOME:-$HOME/.eidos/gate}/"
```

The archive contains everything that defines your identity and history.
Restore by stopping the services (`eidos gate stop`), extracting on the
target host, and running `eidos gate start` to bring the daemon and relay
back up.

## Wiping everything

```sh
eidos gate purge          # confirms first
eidos gate purge --yes    # for scripts / CI
```

`purge` stops the services, removes the systemd unit files, reloads
systemd, and deletes the gate state directory (key, state.db, config.toml,
relay/). It is idempotent on partially-installed setups, so it is safe to
run as a teardown step in test harnesses.

## Resetting to a fresh identity

`eidos gate purge --yes` is the supported reset path; the next
`eidos gate init --label <name>` generates a new identity (and a new npub).
