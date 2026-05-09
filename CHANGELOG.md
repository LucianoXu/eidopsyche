# Changelog

## v0.10.0 — 2026-05-09

### Highlights

This release ships **MindForge v0** — the first end-to-end mind-form lifecycle layered on top of the existing MindGate communication layer. A fresh mind-form is one `eidos forge create` away: a Docker volume holds the ontology, an in-container supervisor spawns the gate daemon and per-wake agent, and inbound NIP-17 messages wake the agent through a wake-signal file. `eidos forge` provides host-side instance management and in-container reflection. See `EXAMPLE.md` for the two-instance walkthrough.

This release also lands **Phase 1 of the unified-call-path migration** (SPEC §"调用路径统一", AGENTS §"Single Call Path"): every operator action — CLI, dashboard, future Agent MCP, NIP-46 thin clients — must funnel through one daemon method dispatcher with one JSON parameter schema. Phase 1 adds `(*daemon.Daemon).Call` (the in-process mirror of `ipc.Client.Call`) and migrates the dashboard's `Send` to use it.

### Added

- **MindForge** subcommand tree (`eidos forge`):
  - Host-side: `create`, `start`, `stop`, `status`, `list`, `purge`, `exec`, `logs`, `wake`, `login`, `ontology export/import`.
  - In-container reflection: `whoami`, `inbox`, `send`, `memory`, `ontology-status`, `wake`.
  - `forge create --owner <npub> --relay <ws://…>` orchestrates volume + init container in one step (template stdin pipe, no host bind-mount of the ontology).
  - `forge login` defaults to reusing host-side Claude credentials; falls back to setup-token; in-container fallback to `claude /login --method=console`.
- **Container PID 1 supervisor** (`eidos supervisor run` / `agent-runner`): inotify-driven wake-signal promotion, agent harness with file lock + wake-prompt template + `EXIT_AUTH_REQUIRED` handling, root-spawned crond.
- **Wake signal protocol** (`internal/wake`): coalescing pending/active slot model with flock-protected merging. Producer `Submit` and consumer `PromoteToActive` are safe under concurrent gate / cron / manual triggers.
- **Mind-form container image** (`docker/mindform/Dockerfile`): busybox + crond + git + Claude Code + embedded eidopsyche bundle. Built and pushed to `ghcr.io/lucianoxu/eidopsyche-mindform` per release.
- **Ontology scaffold** (`internal/ontology`): embedded v0 template (CLAUDE.md + self / memory / desk / drawer) renders into a fresh volume at init.
- **Single-call-path principle** declared in `SPEC.md` ("调用路径统一") and `AGENTS.md` ("Single Call Path"). Migration plan: `docs/superpowers/specs/2026-05-09-unified-call-path-design.md` (six phases, this release ships Phase 1).
- **`(*daemon.Daemon).Call(ctx, method, params, out) error`** — in-process IPC dispatcher. Dashboard adapter, future MCP server, and NIP-46 bridges share the same handler functions as the CLI's socket transport.
- **`SendParams` / `SendResult`** exported on `internal/daemon` so callers build typed payloads instead of `map[string]any`.
- **`*ipc.Error` implements `error`**: callers can `errors.As` to recover the typed code.
- `eidos gate init --service` installs and starts the daemon (and relay, if configured) in one step.
- Inbound NIP-17 messages write a wake signal to `[wake] dir` when configured (used by the in-container supervisor).

### Fixed

- **Dashboard `Send` to a non-contact npub** previously published silently via fallback relays. Now refuses with `CONTACT_NOT_FOUND` surfaced through the toast layer, matching CLI behavior.
- Gate auto-restarts the daemon after `eidos self-update` so the running process picks up the new binary.
- `forge create` skips image pull when the tag is already present locally.
- `relay status` surfaces `unavailable` instead of a hard error when the relay process holds the event-store lock.
- `gate` v0.4 upgrade detector uses `[daemon].socket` as the marker, not `[relay].enabled` (which no longer exists post-v0.9.0).
- Cross-compile to windows for `cmd/eidos/{forge,supervisor}/` and `internal/wake/` — Linux/Unix-only syscalls (`flock`, `Setpgid`, `Kill`) are now gated behind build constraints; the supervisor namespace exists on Windows but has no subcommands, since mind-form containers are Linux-only.

### Migration

No breaking API changes. Existing `eidos gate ...` workflows are unchanged. To start using `eidos forge`:

1. Pull the new image (or let `eidos forge create` pull on first use):
   ```
   docker pull ghcr.io/lucianoxu/eidopsyche-mindform:v0.10.0
   ```
2. Optionally enable the inbound-message wake hook by adding `[wake] dir = "/path/to/wake-dir"` to `~/.config/eidos/config.toml`. Only relevant if a mind-form container will run against this gate.


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
