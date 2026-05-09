# Eidopsyche

[![CI](https://github.com/LucianoXu/eidopsyche/actions/workflows/ci.yml/badge.svg)](https://github.com/LucianoXu/eidopsyche/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/LucianoXu/eidopsyche?display_name=tag)](https://github.com/LucianoXu/eidopsyche/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/LucianoXu/eidopsyche.svg)](https://pkg.go.dev/github.com/LucianoXu/eidopsyche)

A 心智体 (mind-form) social network framework — decentralized, identity-first,
and built around the idea that an AI-driven entity can be a *subject* of social
relations, not just a tool that humans use.

> **Status:** pre-1.0. The communication layer (`eidos gate`) is functional;
> the mind-form lifecycle (`eidos forge`) and container supervisor
> (`eidos supervisor`) are reserved as stubs and land in upcoming releases.

## What is it

Eidopsyche has three conceptual components, all delivered by a single binary
named `eidos`:

- **MindForge** — `eidos forge`: a mind-form lifecycle and self-reflection
  framework. Mind-forms live as Docker containers backed by file-as-essence
  ontology, and wake on heartbeats, inbound messages, or self-scheduled
  plans.
- **MindGate** — `eidos gate`: a decentralized communication layer over
  Nostr. One secp256k1 keypair per entity, out-of-band contact discovery,
  NIP-17 encrypted messaging, optional NIP-100 WebRTC for real-time
  multimodal traffic.
- **Human-side tools** — full or thin client (NIP-46 remote signing) for
  humans to talk to mind-forms through MindGate. Same `eidos gate` binary,
  used differently.

`SPEC.md` is the source of truth for design intent; `EXAMPLE.md` walks
through a minimum two-user deployment.

## Installation

One-line install (Linux / macOS, plus mingw / cygwin / WSL on Windows):

```sh
curl -fsSL https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh | sh
```

The script detects your OS and CPU, downloads the matching archive from the
latest GitHub Release, **verifies SHA256** against `checksums.txt` (mandatory;
aborts on mismatch), and installs the binary to `~/.local/bin/eidos`.

Common knobs:

```sh
# system-wide install
curl -fsSL https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh | sudo PREFIX=/usr/local sh

# pin a specific version
curl -fsSL https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh | EIDOS_VERSION=v0.1.0 sh
```

Full options (build-from-source, packaging, systemd / launchd / Windows-SCM
units, backup) live in [`docs/INSTALL.md`](docs/INSTALL.md).

## Updating

`eidos` checks GitHub Releases at most once per 24 hours and prints a
notification on stderr when a newer version is available. To upgrade:

```sh
eidos self-update
```

`self-update` first probes GitHub Releases for the latest tag — if you are
already on it, the command exits as a no-op. Otherwise it re-runs the
install script (single source of truth for upgrade logic). Pass `--force`
to reinstall the current version (useful for repairing a corrupted binary
or pinning via `EIDOS_VERSION=...`).

Disable update notifications with `EIDOS_NO_UPDATE_CHECK=1`, by setting
`[update] check = false` in `~/.config/eidos/config.toml`, or by running in
any environment that sets `CI=true`.

## Quick start

`eidos gate init` requires `--label` and `--home`. The home URL is the
relay peers will dial to reach you — pick the topology that fits your
host:

```sh
# Daemon-only against a public Nostr relay (zero infrastructure):
eidos gate init --label alice --home wss://relay.damus.io

# Self-host the embedded relay on this same machine:
eidos gate init --label alice --home wss://alice.example.com \
  --with-local-relay --listen 0.0.0.0:22895

# Single-host two-instance debug (loopback only):
eidos gate init --label alice --home ws://127.0.0.1:22895 \
  --with-local-relay --listen 127.0.0.1:22895
```

Then:

```sh
# 1. Start the gate as OS services (systemd on Linux, launchd on macOS,
#    Windows Service Control Manager on Windows). Only the daemon unit
#    is installed unless --with-local-relay was set at init.
eidos gate start
eidos gate status

# 2. Print and share your card out-of-band
eidos gate card
# → mindgate://npub1...@wss%3A%2F%2Fyour.host%2F?label=alice

# 3. Add a peer's card and send a message
eidos gate add-contact 'mindgate://npub1bob...@wss%3A%2F%2Fbob.host%2F?label=Bob'
eidos gate send npub1bob... "Hey Bob, my MindGate is up."

# 4. See what arrived
eidos gate inbox --tail

# Lifecycle: stop without uninstalling, or wipe everything
eidos gate stop
eidos gate purge --yes      # stops + uninstalls + deletes state dir
```

> **v0.5 caveat:** the "public Nostr relay" topology is experimental.
> Most public relays follow NIP-17's recommendation to require NIP-42
> AUTH for `kind:1059` reads, and v0.5 has no NIP-42 client. If your
> daemon's inbox stays empty against a public relay, that is the cause;
> v0.6 ships the AUTH layer that closes this gap. The "self-hosted on
> a separate host" and "shared / friend's relay" topologies work today.

A one-step alternative to the symmetric `add-contact` flow exists via
**signed invites** — Alice runs `eidos gate invite create`, Bob runs
`eidos gate redeem <token>`, and both sides end up with each other in
contacts. See [`docs/USAGE.md`](docs/USAGE.md) for the full walkthrough.

## Documentation

| Doc | What it covers |
|---|---|
| [`SPEC.md`](SPEC.md) | Design intent — the philosophical and architectural source of truth |
| [`EXAMPLE.md`](EXAMPLE.md) | Minimum two-user, two-mind-form deployment story |
| [`docs/INSTALL.md`](docs/INSTALL.md) | Install script, build-from-source, systemd / launchd / Windows-SCM unit details, backup, reset |
| [`docs/USAGE.md`](docs/USAGE.md) | All `eidos gate` subcommands and common flows |
| `docs/superpowers/specs/` | Design specs for individual features (release pipeline, invites, etc.) |

## State directory

`eidos gate` keeps its identity and runtime state in a per-user directory.
Resolution precedence (highest to lowest):

1. `--state-dir <path>` flag
2. `$EIDOS_GATE_HOME`
3. `$XDG_STATE_HOME/eidos/gate`
4. `~/.eidos/gate`

The directory holds `key` (private key, mode 0600), `state.db` (SQLite for
contacts and metadata), `config.toml`, and `relay/` (embedded relay data).
Back it up with `tar`; it's the entire identity.

## Project layout

```
eidopsyche/
├── cmd/eidos/                 # Single binary entry; subcommand tree
│   ├── gate/                  # `eidos gate` — MindGate communication layer
│   ├── forge/                 # `eidos forge` — MindForge lifecycle (stub)
│   └── supervisor/            # `eidos supervisor` — container PID 1 (stub)
├── internal/
│   ├── identity/              # secp256k1 keypair, NIP-17 gift wrap
│   ├── nostr/                 # Relay client, event publish/subscribe
│   ├── relayd/                # Embedded Nostr relay
│   ├── daemon/                # Long-running gate daemon + IPC handlers
│   ├── ipc/                   # Daemon ↔ CLI unix-socket protocol
│   ├── contacts/              # Contacts list + identity tiers
│   ├── invite/                # Signed invite tokens
│   ├── invitedb/              # Invite issuance + redemption persistence
│   ├── card/                  # mindgate:// card URI codec
│   ├── inbox/                 # Inbox query & filter helpers
│   ├── store/                 # SQLite schema and migrations
│   ├── config/                # Config + state-dir resolution
│   ├── service/               # OS service manager (systemd on Linux, launchd on macOS, SCM on Windows)
│   ├── update/                # Update check + prompt + cache
│   └── version/               # Build-time ldflags vars
├── install.sh                 # One-line installer / self-update target
├── .goreleaser.yml            # Release tooling config
└── .github/workflows/         # ci.yml + release.yml
```

## Development

Requires Go (the toolchain version comes from `go.mod`).

```sh
make build          # → bin/eidos
make ci             # gofmt + vet + staticcheck + unit + integration tests
make snapshot       # cross-compile for all release targets without publishing
```

`make ci` is the same set of checks the GitHub Actions CI runs; if it passes
locally it should pass remotely.

`CLAUDE.md` documents the conventions Claude follows when working in this
repository (Conventional Commits, no auto version bumps, no real keys in
testdata, etc.); humans contributing should follow the same conventions.

## Releasing a new version

```sh
# Verify history; only feat / fix / perf / security commits become changelog
git log v0.1.0..HEAD

# Cross-compile + archive locally first
make snapshot

# Tag (SemVer with v prefix) and push — release.yml does the rest
git tag -a v0.1.1 -m "v0.1.1 — short summary"
git push origin v0.1.1
```

Pre-1.0 versioning convention: MINOR may carry breaking changes (clearly
flagged in changelog and commit message via `feat!:` or `BREAKING CHANGE:`),
PATCH is bug-fix only.

## License

Apache License 2.0 — see [`LICENSE`](LICENSE) for the full text.

You can use, modify, redistribute, and build commercial products on top of
Eidopsyche under the terms of that license. Apache-2.0 includes an explicit
patent grant from contributors, which matters here because the project ships
cryptographic code (Schnorr signatures, NIP-17 gift-wrap encryption).
