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
  2) share card:   eidos gate card
  (optional) set up local relay: eidos relay init --mode paired --listen 0.0.0.0:22895
```

## Start the daemon

```sh
eidos gate start    # installs OS service unit for the daemon and starts it
eidos gate status   # show installed / enabled / active state per unit
eidos gate stop     # stop without uninstalling
```

`start` writes the appropriate unit files for your OS and asks the OS
service manager to enable + start them. Both units restart on non-zero
exit (`Restart=on-failure` on systemd; `KeepAlive`+`SuccessfulExit=false`
on launchd; an explicit SCM recovery ladder of restart-after-1s, 2s, 5s
with a 60s reset window on Windows — Windows' own default would be "take
no action") so a clean `eidos gate stop` actually stops, while a crash
brings the service back automatically.

| OS | Service manager | User-mode unit path | System-mode unit path |
|---|---|---|---|
| Linux | systemd | `~/.config/systemd/user/eidos-gate-daemon.service` | `/etc/systemd/system/eidos-gate-daemon.service` |
| macOS | launchd | `~/Library/LaunchAgents/eidos-gate-daemon.plist` | `/Library/LaunchDaemons/eidos-gate-daemon.plist` |
| Windows | SCM (`services.msc`) | `eidos-gate-daemon` (host-wide; LocalSystem account) | same — SCM has no per-user database |

The relay service unit is installed separately via `eidos relay service install` and uses the unit name `eidos-relay`:

| OS | Service manager | User-mode unit path | System-mode unit path |
|---|---|---|---|
| Linux | systemd | `~/.config/systemd/user/eidos-relay.service` | `/etc/systemd/system/eidos-relay.service` |
| macOS | launchd | `~/Library/LaunchAgents/eidos-relay.plist` | `/Library/LaunchDaemons/eidos-relay.plist` |
| Windows | SCM (`services.msc`) | `eidos-relay` (host-wide; LocalSystem account) | same |

On Linux user-mode, services survive your shell exiting; for a full
logout-survives experience on a headless host, run
`loginctl enable-linger <username>` once. macOS LaunchAgents auto-start at
GUI login. On Windows the SCM service is configured with `StartType =
Automatic` so it comes back at boot; `eidos gate start / stop / purge`
must be run from an elevated PowerShell (Administrator) because SCM
mutations require Administrator privileges.

By default, `eidos gate init` creates a daemon-only install: no embedded
relay process is started and only the daemon unit is registered with
your service manager. The `--home <url>` you pass to `init` tells peers
how to dial you. See "Self-hosting the embedded relay" below if you
want to run one on this host as well.

### Self-hosting the embedded relay

The relay is an independent subcommand (`eidos relay`), separate from the gate.
Initialize it after initializing the gate:

```sh
eidos relay init --mode paired --listen 0.0.0.0:22895 --owner <npub>
```

`--listen` controls the bind address (default `0.0.0.0:22895`).
The gate's `--home` URL embedded in your card / invite is independent —
for a public deployment, you typically:

1. Bind the relay to all interfaces (`--listen 0.0.0.0:22895`).
2. Terminate TLS at a reverse proxy (Caddy / nginx / Cloudflare Tunnel)
   that forwards `wss://your.host` to the local plain-WS port.
3. Pass `--home wss://your.host` to `eidos gate init` so peers dial the
   public URL.

Install and start the relay service:

```sh
eidos relay service install
eidos relay service start
eidos relay status
```

To stop or uninstall the relay service independently:

```sh
eidos relay service stop
eidos relay service uninstall
```

Relay config lives at `~/.config/eidos/relay/config.toml` and is managed
with `eidos relay config get/set`. Event data is persisted at
`~/.config/eidos/relay/events/` (badger, pure Go — no CGO required).

#### Native TLS (BYO certs)

The relay can terminate TLS itself when you give it a cert + key — no
reverse proxy needed for the simple "one relay, no other web services on
this host" deployment. Set both paths in `~/.config/eidos/relay/config.toml`:

```toml
[tls]
  cert_file = "/etc/letsencrypt/live/your.host/fullchain.pem"
  key_file  = "/etc/letsencrypt/live/your.host/privkey.pem"
```

The relay calls `http.ListenAndServeTLS(cert_file, key_file)` when both
are set. Setting only one is a startup error (the asymmetry would
otherwise surface as a silent TLS handshake failure with no indication
that config was the cause).

Cert lifecycle is BYO — typical certbot workflow on a host where the
relay binds 80/443 directly:

```sh
sudo certbot certonly --standalone -d your.host \
  --pre-hook  'eidos relay service stop' \
  --post-hook 'eidos relay service start'
```

For a multi-service host where nginx / Caddy already owns 80/443,
prefer the reverse-proxy posture above and leave `[tls]` empty.

After config changes, restart so the relay picks up the new cert files:

```sh
eidos relay service stop && eidos relay service start
```

NIP-42 AUTH on the relay is enabled by default per NIP-17
§Recommendations (`auth.required = true`); flip to `false` only
if you're knowingly running an open relay for experiments. The
`service_url` override under `[auth]` is for the reverse-proxy
case where the proxy-facing URL differs from the bind address:

```toml
[auth]
  required    = true
  service_url = "wss://your.host"
```

### Migrating from pre-relay-top-level versions

Older versions embedded the relay inside `eidos gate`. If you are upgrading
from a version that had `eidos gate relay` or `--with-local-relay`:

1. Stop and disable the old relay unit:
   ```sh
   systemctl --user stop eidos-gate-relay
   systemctl --user disable eidos-gate-relay
   ```
   On macOS substitute `launchctl bootout`; on Windows substitute
   `sc stop eidos-gate-relay` / `sc delete eidos-gate-relay`.

2. Re-run gate's service installer to drop the old relay unit reference:
   ```sh
   eidos gate service install
   ```

3. Initialize the new relay config:
   ```sh
   eidos relay init --mode paired --listen <host:port> [--owner <npub>]
   ```

4. Install and start the renamed unit:
   ```sh
   eidos relay service install
   eidos relay service start
   ```

5. Edit `~/.config/eidos/config.toml` and remove the `[relay]` section.
   (Gate ignores it after upgrade and prints a one-line warning at start;
   the cleanup is cosmetic but recommended.)

**Re-init from scratch** (loses contacts, invites, inbox):

```sh
eidos gate purge --yes
eidos gate init --label <your-label> --home <url>
eidos relay init --mode paired --listen 0.0.0.0:22895
```

### Logs

- Linux: `journalctl --user -u eidos-gate-daemon` (gate daemon),
  `journalctl --user -u eidos-relay` (relay)
  or `journalctl -u ...` for `--system`.
- macOS: launchd does not aggregate logs the way journald does, so the
  plist redirects stdout / stderr to
  `<state-dir>/logs/eidos-gate-daemon.log` (gate daemon) and
  `~/.config/eidos/relay/logs/eidos-relay.log` (relay). Tail with
  `tail -f ~/.eidos/gate/logs/eidos-gate-daemon.log`.
- Windows: SCM captures the daemon's stdout/stderr only when explicitly
  redirected. Run `Get-EventLog -LogName Application -Source eidos-gate-daemon`
  for SCM lifecycle events; for daemon-internal logs, use the foreground
  mode (`eidos.exe gate daemon` from PowerShell) for live tailing.

### System-wide install

For shared / production hosts, run with `--system` so units land in the
system path (and survive across users / reboots):

```sh
sudo eidos gate start --system
sudo eidos gate status --system
sudo eidos gate stop --system
```

System-mode units run as root by default; tighten with the appropriate
`User=` (systemd) or `UserName` (launchd) directive if you need
least-privilege.

### Foreground mode

For debugging or on hosts without a service manager, use foreground commands:
- `eidos gate daemon` — run the gate daemon in the foreground (exactly what
  the `eidos-gate-daemon` unit invokes)
- `eidos relay start` — run the relay in the foreground (exactly what the
  `eidos-relay` unit invokes)
On Windows, when the binary is launched by SCM (`svc.IsWindowsService()`
returns true), the same subcommands transparently dispatch through
`golang.org/x/sys/windows/svc` so SCM's Stop / Shutdown control messages
become a context cancellation; on a normal command-line invocation the
ctrl-C / SIGTERM path is used as on linux/darwin.

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

`purge` stops the services, removes the OS service unit files (systemd
.service, launchd .plist, or SCM service entry depending on platform),
reloads the service manager where applicable, and deletes the gate
state directory (key, state.db, config.toml, relay/, and the launchd
`logs/` directory). It is idempotent on partially-installed setups, so
it is safe to run as a
teardown step in test harnesses.

## Resetting to a fresh identity

`eidos gate purge --yes` is the supported reset path; the next
`eidos gate init --label <name>` generates a new identity (and a new npub).
