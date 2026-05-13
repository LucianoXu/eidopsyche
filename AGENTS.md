# Eidopsyche Project

Eidopsyche is a 心智体 (mind-form) social network framework. It has three components:

- **MindForge** — mind-form lifecycle and self-reflection framework. Docker-based isolation, file-as-essence ontology, always-on agent loop driven by five wake kinds (HeartBeat / inbound MindGate message / planned / manual / birth).
- **MindGate** — decentralized communication layer over Nostr. One secp256k1 keypair per entity, out-of-band discovery (card / invite), NIP-17 encrypted messaging with envelope-v1 wire schema, optional NIP-100 WebRTC signaling for real-time multimodal.
- **Human-side tools** — full or thin client (NIP-46 remote signing) for humans to talk to mind-forms through MindGate.

Read `docs/specs/SPEC.md` first when uncertain about design intent. `docs/specs/FirstContact.md` is the source of truth for the summoning wizard. Deployment / usage walkthroughs live under `docs/INSTALL.md` and `docs/USAGE.md`.

> Note: this `CLAUDE.md` is the **framework development** guidance — for Claude helping build Eidopsyche the framework. The mind-form's own 纲领 (also a `CLAUDE.md`) lives inside its docker container, is loaded into the running mind-form, and is a different file.


## Product Guidelines

This are the guidelines of the project from the perspective as a product.
- **Good guidence**. The tool should provide enough guidence and explain itself. The command result and status query should be informative.
- **Usability**. Easy to deploy. Good UI and UX design. Commands for both beginners and advanced users.


## Project Stage: Pre-Production — Always Choose the Optimal Design

Eidopsyche has no production deployments, no existing user base, and no operational footprint to maintain. **Always choose the optimal design.** Do not factor in:

- Backward compatibility with previous binary / image / config versions
- Migration paths for existing data, sessions, or ontologies
- Opt-in feature flags or behind-flag rollouts for new behavior
- Deprecation cycles or legacy code kept "for compatibility"
- Phased delivery driven by rollback fear (phased delivery for *scope management* is still fine)

When a refactor or redesign requires changing on-disk formats, IPC contracts, command surface, or container semantics, **make the change directly**. Delete the old code path. There is no "v1 → v2 migrator" to write because there is no v1 anyone is running. This stance ends when the project ships a `1.0` and accumulates real-world deployments; revisit at that point.


## Project Structure & Module Organization

Single Go workspace producing a single binary `eidos` whose subcommand tree (`forge` / `gate` / `relay` / `supervisor` / `summon` + top-level `version` / `self-update`) carries the three component roles. MindForge and MindGate are conceptual layers and brand names — they do not correspond to separate executables.

```
eidopsyche/
├── go.work                   # Go workspace covering cmd/* and internal/*
├── cmd/
│   └── eidos/                # Single binary entry; subcommands under cmd/eidos/{forge,gate,relay,supervisor,summon,...}
├── internal/                 # Grouped by responsibility — see `ls internal/` for the full set.
│   #   Ontology & identity:  ontology, identity, contacts, firstcontact, card, invite, invitedb
│   #   Transport & relay:    nostr, relaycfg, relayd, envelope, ipc
│   #   Daemon & dispatch:    daemon, dashboard, state, store, sessionstate, authstate, dreamstate
│   #   Mind-form lifecycle:  agentloop, wake, scheduler, cron, inbox, transcript, prompts, claudeauth, claudeexec, forgectl
│   #   Host / platform:      service, update, version, config, fileops
├── pkg/                      # Stable public interfaces (empty — promote from internal/ as APIs stabilize)
├── template/                 # Canonical clean ontology tree, embedded into the binary
├── prefab/                   # Pre-authored mind-form catalogues (one dir per prefab id)
├── embed.go                  # Top-level //go:embed bundling template/ + prefab/
├── utils/                    # Dev-only Go utilities — see utils/CLAUDE.md (GOWORK=off invocation)
├── docker/
│   └── mindform/             # Mind-form runtime image — see docker/mindform/CLAUDE.md
│       ├── Dockerfile        # Multi-stage; alpine:3.19 final image
│       └── entrypoint.sh
├── install.sh                # One-line install / self-update script (hosted at raw.githubusercontent.com)
├── .goreleaser.yml           # Release tooling config
├── .github/workflows/        # ci.yml + release.yml
├── README.md                 # User-facing entry point — install, quick start, docs index
├── LICENSE                   # Apache License 2.0
├── docs/
│   ├── INSTALL.md            # Installation walkthrough
│   ├── USAGE.md              # CLI surface and common flows
│   ├── specs/                # Project specifications — SPEC.md, FirstContact.md
│   └── superpowers/specs/    # Dated implementation design docs (source of truth for in-progress work)
└── CLAUDE.md                 # This file (symlinked to AGENTS.md)
```


## `deploy-test/` (untracked)

The top-level `deploy-test/` directory holds machine-specific deploy-test
scripts and runbooks (cross-machine playwright drivers, single-host
smoke scripts, mind-form lifecycle drills, etc.). Its contents are
tied to the maintainer's workstations (codenames, IPs, SSH config,
absolute paths) and the whole directory is `.gitignore`d — agents and
contributors should treat it as a local-only scratch area. Layout
convention: one test per subfolder, each with a `script.md` runbook
plus any helper scripts; ordering prefixes (e.g. `001-…/`, `002-…/`)
are encouraged but not required.

Do not commit anything under `deploy-test/`. If a deploy-test pattern
turns out to be portable enough to ship, promote it into
`test/integration/` (real Go integration test, behind the `integration`
build tag) instead of trying to track it here.


## Prefab catalogue (top-level `prefab/`)

The summon wizard's "pick a preset" branch reads from `prefab/<id>/`.
Each prefab is a complete, self-contained ontology tree with one
sidecar `prefab.toml` for metadata (display name, kind, tagline,
preview text). The wizard streams the tree into the new mind-form's
docker volume via `internal/ontology.TarStreamPrefab`, rendering
`.tpl` files with `text/template` and `Option("missingkey=error")` —
typos in `.tpl` references fail loud rather than ship blank
substitutions.

Authoring conventions:
- `prefab/<id>/prefab.toml` keys: `id`, `kind` (`m` | `f` | `spirit`),
  `[display]`, `[tagline]`, `[preview]` — language-keyed maps; `zh`
  and `en` are the supported keys today.
- The substitution surface is `ontology.Params`: `Label`, `OwnerNpub`,
  `OwnerLabel`, `MindFormNpub`, `HomeRelay`, `CreatedDate`. Any other
  reference fails the missingkey check.
- Do **not** ship `journal/0000-summoning.md.tpl` in a prefab. The
  wizard renders the operator's seal book and writes it to that path
  via `forge.CreateOpts.JournalEntry`; if both a templated file and
  the literal entry are present, the literal entry wins (last-writer
  in tar) and the prefab's templated journal will be silently
  overwritten. Author the prefab's narrative in `essence/` or
  `memory/` instead.
- Top-level prefab dirs whose name starts with `_` are hidden from
  `ontology.List()` (so the menu is clean) but remain readable by
  `ontology.MetaFor` and `ontology.TarStreamPrefab` — used for test
  fixtures (`prefab/_test_fixture/`).
- The canonical clean ontology lives at `template/` (sibling of
  `prefab/`); prefab `CLAUDE.md` files are independent copies that
  authors are free to specialise. There is no automated check that
  these stay in sync — drift is accepted until it bites.

## `utils/` (dev-only utilities)

Standalone Go utilities — each is its own module **outside** the root
`go.work`, not built by CI, not shipped. Invocation requires
`GOWORK=off`. Full policy and per-utility usage live in
[`utils/CLAUDE.md`](utils/CLAUDE.md) and `utils/README.md`.

## Build, Test, and Development Commands

- **Build the binary**: `go build -o bin/eidos ./cmd/eidos`
- **Build mind-form container image**: `docker build -t ghcr.io/lucianoxu/eidopsyche-mindform:dev -f docker/mindform/Dockerfile .` (Makefile target: `make image`)
- **Lint / vet**: `gofmt -l . && go vet ./...`
- **Unit Test**: `go test ./...`
- **Integration Test**: `go test -tags=integration ./...` (requires a running Docker daemon; spins up real containers and at least one local Nostr relay).
- **Run a local instance end-to-end**: `eidos forge create test && eidos forge start test && eidos forge logs test`
- **Mind-form introspection family** (pair the two — one is the response side, the other is the request side):
    - `eidos forge watch <name>` streams the response side (stream-json transcript ndjson: assistant thinking, tool calls, tool results) from the running agent-loop.
    - `eidos forge prompt-dump <name>` captures the request side (default system prompt, tool catalogue, identity.toml, ontology `CLAUDE.md`, model) for a fresh session — does not affect the running agent-loop. Output mirrors `utils/promptdump`'s extension-driven JSON+Markdown shape.
- **Local release dry-run**: `goreleaser release --snapshot --clean` (validates `.goreleaser.yml` without publishing)


## Distribution & Release

- **Versioning**: SemVer with `v` prefix (`vMAJOR.MINOR.PATCH`). Pre-1.0: MINOR may carry breaking changes (clearly flagged in changelog), PATCH is bug-fix only. Manual tag bump only — never bump version without explicit user instruction.
- **Version embedding**: GoReleaser injects `Version`, `Commit`, `BuildDate` via `-ldflags -X`. Source builds default to `Version = "dev"`, which causes the update checker to skip itself.
- **Target platforms**: `linux/{amd64,arm64}` × `darwin/{amd64,arm64}` × `windows/{amd64,arm64}`. Archive: `.tar.gz` for linux/darwin, `.zip` for windows.
- **Changelog**: Auto-generated by GoReleaser from Conventional Commits. Filter rules: only `feat | fix | perf | security` enter `CHANGELOG.md` and GitHub Release notes; `refactor | chore | test | docs | build | ci` are hidden. Breaking changes get a dedicated section. The Highlights paragraph at the top of GitHub Release notes is hand-edited for non-trivial releases.
- **Release pipeline**: GitHub Actions `release.yml` triggers on tag push `v*`, runs GoReleaser end-to-end (cross-compile, archive, checksum, GitHub Release, ghcr.io image push). CI workflow `ci.yml` runs lint / vet / staticcheck / unit / integration on every PR and main push.
- **Container registry**: the mind-form runtime image is published to `ghcr.io/lucianoxu/eidopsyche-mindform` via `make image image-push` (manual today; an automated GoReleaser path is deferred until the registry release pipeline lands). The host release binary itself is distributed as platform archives via GoReleaser, not as a container image.
- **Update opt-out (any of)**: `EIDOS_NO_UPDATE_CHECK=1` env var; `[update] check = false` in `~/.config/eidos/config.toml`; auto-skip when `CI=true`; auto-skip when `Version == "dev"`.
- **Self-update**: `eidos self-update` is a thin wrapper that re-executes the install script (`exec curl -fsSL <install_url> | sh`). The install script is the single source of truth for upgrade logic; `--prefix` and equivalents are passed through.
- **Supply-chain security (current bar)**: SHA256 checksums in `checksums.txt`, verified by `install.sh`. Cosign / Sigstore keyless signing is deferred until approaching 1.0.


## Coding Style & Naming Conventions

- The code, comments and documentation should be in English.
- Run `gofmt` / `goimports` before committing; CI enforces both.
- Standard Go naming: CamelCase for exported, camelCase for unexported. Package names are short, lowercase, single-word.
- Errors are returned, not panicked (except in `main`). Wrap with `fmt.Errorf("...: %w", err)` to add context.
- Use `context.Context` as the first parameter for cancellable / network / Docker / subprocess operations.
- Avoid global state; pass dependencies (clock, fs, docker client, nostr client) explicitly so tests can substitute fakes.
- Cross-subcommand contracts (wake signal format, IPC messages, ontology layout) live in `internal/`. Do not duplicate types between subcommand packages under `cmd/eidos/` — import from the shared package.
- Commit messages follow Conventional Commits: `<type>(<scope>): <subject>` with type ∈ `feat | fix | perf | security | refactor | chore | test | docs | build | ci` and scope optional but encouraged (`forge`, `gate`, `supervisor`, `core`). Only `feat` / `fix` / `perf` / `security` enter the user-facing changelog. Breaking changes use `feat!:` or a `BREAKING CHANGE:` footer.

## Single Call Path

Every operator action — sending a message, adding a contact, setting a config key, creating an invite, running a lifecycle job — has exactly **one** code path. CLI, dashboard webui, and future agent MCP / NIP-46 thin-client surfaces all funnel through the same daemon method table.

Concretely:
- The daemon (`internal/daemon`) owns a `methodTable` keyed by method name (e.g. `send`, `contact.add`, `config.set`). Each handler validates its JSON params, performs the action, and returns a typed error code from `internal/ipc/protocol.go`.
- The CLI (`cmd/eidos/gate/*.go`) opens the IPC unix socket, packs args into the method's JSON params, calls, and renders the result.
- The dashboard adapter (`internal/daemon/dashboard_adapter.go`) packs args into the **same** JSON params and calls the **same** method through an in-process dispatch helper (no socket round-trip; same handler function). It does **not** reach into `*Daemon` internals to reimplement actions.
- The future MCP server is the same shape: MCP tool → JSON params → same dispatcher → MCP response.

**Add a new operation? It lands as an IPC method first.** Surfaces (CLI command, dashboard handler, MCP tool) wrap that method; they never reimplement the action.

The only sanctioned exception is bootstrap or diagnostic commands that operate on local files when the daemon is *guaranteed* to be down (e.g. first-time `eidos gate init`). Such exceptions must be justified in the implementation comment.

This rule is the structural answer to a class of bugs we hit in 2026-05: the dashboard's `Send` had silently forked from the IPC `send` handler — skipping the contact-existence check, the npub→hex resolution, and the two-phase outbox persistence. `config set` was a three-way fork (CLI direct-file-write, dashboard direct-file-write under a daemon mutex, no IPC method) racing on `config.toml`. See `docs/superpowers/specs/2026-05-09-unified-call-path-design.md` for the migration plan.

### Operator/mindform symmetry

Every adjustment and every read in the same state schema is reachable through the same IPC method table, regardless of whether the caller is the operator (against the host gate daemon or via `docker exec` into a mindform's container) or the mindform itself (in-container against its own daemon). Surfaces are parameter-packing wrappers; host-side `eidos forge config <name> ...` and in-container `eidos gate ...` funnel through identical method calls. Reads go through `state.get [path]`; mutations through the per-domain IPC methods, all of which route through `daemon.Mutate` (context check → lock → write → apply hook → emit `state.changed`).

**Don't add a host-only or container-only side-channel for state actions.** State that exists only in one context (e.g. `lifecycle.*` on the mindform) is fine — the schema can have context-specific subtrees — but the verb that touches it is the same. Container-only config keys (e.g. `heartbeat.interval`, `mindform.model`) declare `Contexts: ContainerCtx` in their Key registration; the Mutate framework rejects host-side writes with `CONTEXT_MISMATCH` and an error pointing to the correct verb. See `docs/superpowers/specs/2026-05-11-unified-state-interface-design.md`.


## Commit & Pull Request Guidelines

**Follow this pipeline for actual code change:**
1. *Code Change*.
2. *Third-party review*. If you are Claude Code, you should use `codex` and request a code review. If you are GPT model, you request a code review from `claude --dangerously-skip-permissions`.
3. *Documentation*: After any code implementation, check and update the documentations. Make sure to maintain and keep the documentations up to date.
4. *CI Check*. After pushing the commit, you should use `gh` to watch the CI result and make sure it passes.
5. *Fix Copilot Comments* (if exists). If copilot is assigned to review the PR automatically, you should wait for copilot's comments and fix them if necessary.

After merging the PR, you should always exit the worktree, delete the worktree, remove the local and remote feature branch, unless specificed otherwise.

**Guidelines:**
- When required to use `PR` mode, or the development work is heavy, you should work in a `git` worktree on a separate branch and contribute via pull request. Worktrees should be placed at `../eidopsyche-worktree/<worktree-name>/`, where `eidopsyche-worktree/` is a sibling path parallel to the main repo. Steps to follow:
  1. First use `git worktree add <path> <branch-name>` to create the worktree.
  2. Use `EnterWorktree` tool to enter the workspace.
- DO NOT use squash merge when you merge a branch or PR.
- Make sure to run the identical check as CI locally and apply fix before push to GitHub remote.
- GitHub issues/comments/PR comments: use literal multiline strings or `-F - <<'EOF'` (or $'...') for real newlines; never embed "\\n".



## Agent-Specific Notes

- We keep identical copies of `AGENTS.md` and `CLAUDE.md`, and use softlink to point `CLAUDE.md` to `AGENTS.md`. When creating new agent instructions in subfolders, always follow the practice: create `AGENTS.md` first, and build the softlink.
- When answering questions, respond with high-confidence answers only: verify in code; do not guess.
- **NEVER** bump the version number unless the user explicitly asks to.
- **NEVER** commit any real ip-address, api-keys or other security related information. Use obviously fake placeholders in the documentations.
- **NEVER** commit any real Nostr private key (`nsec…`). Generated test keys for integration tests must live under `testdata/` with a `_test_only` suffix.


## Key Development Principles

1. KISS - *K*eep *I*t *S*imple *S*tupid -> Don't add complexity when a simpler solution works
2. TDD - *T*est *D*riven *D*evelopment -> Write tests to drive the implementation
3. DRY - *D*on't *R*epeat *Y*ourself -> Don't duplicate functionality, data structures, or algorithms
4. Modularity - Develop independent modules through well defined interfaces
5. POLA - *P*rinciple *O*f *L*east *A*stonishment - Create intuitive and predictable interfaces to not surprise users
