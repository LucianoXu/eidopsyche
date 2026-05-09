# Changelog

## v0.9.0 — 2026-05-09

### Breaking changes

- `eidos gate relay` is removed. The relay is now a top-level subcommand: `eidos relay`. See migration recipe below.
- `[relay]` section in gate's `config.toml` is no longer read; remove it after upgrade.
- `eidos gate init` no longer accepts `--with-local-relay` or `--listen`. Use `eidos relay init` separately to set up a local relay.
- The systemd / launchd unit `eidos-gate-relay` is renamed to `eidos-relay`. The `eidos-gate-daemon` unit name is unchanged.
- The relay-side publisher whitelist is removed. Client-side filtering in the gate is the sole social-graph filter (per SPEC :145).

### Migration

To upgrade an existing local-relay install:

1. Stop and disable the old relay unit:
   ```
   systemctl --user stop eidos-gate-relay
   systemctl --user disable eidos-gate-relay
   ```
   On macOS substitute `launchctl bootout`; on Windows substitute `sc stop eidos-gate-relay` / `sc delete eidos-gate-relay`.
2. Re-run gate's service installer to drop the old relay unit reference:
   ```
   eidos gate service install
   ```
3. Initialize the new relay config:
   ```
   eidos relay init --mode <paired|public> --listen <host:port> [--owner <npub>]
   ```
4. Install and start the renamed unit:
   ```
   eidos relay service install
   eidos relay service start
   ```
5. Edit `~/.config/eidos/config.toml` and remove the `[relay]` section. (Gate ignores it after upgrade and prints a one-line warning at start; the cleanup is cosmetic but recommended.)

### Added

- `eidos relay` top-level subcommand tree: `init`, `start`, `status`, `config get/set`, `service {install,start,stop,status,uninstall}`.
- Persistent event storage for the embedded relay via `github.com/fiatjaf/eventstore/badger` (pure Go; no CGO required for the released binary).

### Removed

- `eidos gate relay` foreground command.
- `--with-local-relay`, `--listen` flags on `eidos gate init`.
- `[relay]` block in gate `config.toml`; `RelayEnabled()` helper; `relay.*` keys from `eidos gate config get/set`.
- Dashboard's "relay enabled" badge (relay state lives in the relay process now; query via `eidos relay status`).
- `relay_whitelist` SQL view from gate state.db. SchemaVersion bumped to 3; existing dbs `DROP VIEW IF EXISTS` on first Migrate.
