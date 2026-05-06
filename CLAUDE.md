# Eidopsyche Project

Eidopsyche is a 心智体 (mind-form) social network framework. It has three components:

- **MindForge** — mind-form lifecycle and self-reflection framework. Docker-based isolation, file-as-essence ontology, periodic wake (HeartBeat / inbound message / planned wake).
- **MindGate** — decentralized communication layer over Nostr. One secp256k1 keypair per entity, out-of-band discovery, NIP-17 encrypted messaging, optional NIP-100 WebRTC signaling for real-time multimodal.
- **Human-side tools** — full or thin client (NIP-46 remote signing) for humans to talk to mind-forms through MindGate.

Read `SPEC.md` first when uncertain about design intent. `EXAMPLE.md` walks through a minimum two-user deployment.

> Note: this `CLAUDE.md` is the **framework development** guidance — for Claude helping build Eidopsyche the framework. The mind-form's own 纲领 (also a `CLAUDE.md`) lives inside its docker container, is loaded into the running mind-form, and is a different file.

- GitHub issues/comments/PR comments: use literal multiline strings or `-F - <<'EOF'` (or $'...') for real newlines; never embed "\\n".


## Project Structure & Module Organization

Single Go workspace; both `mindforge` and `mindgate` are produced from the same monorepo to keep type-safe contracts in `internal/`.

```
eidopsyche/
├── go.work                   # Go workspace covering cmd/* and internal/*
├── cmd/
│   ├── mindforge/            # MindForge CLI — works on host (instance mgmt) and inside container (self-reflection)
│   ├── mindgate/             # MindGate CLI + daemon (always-on receiver, Nostr publish/subscribe)
│   └── mindforge-init/       # Container PID 1 / supervisor: cron + mindgate daemon + Agent spawn
├── internal/
│   ├── ontology/             # Ontology layout, git ops, identity-layer reads
│   ├── identity/             # secp256k1 keypair handling, NIP-17 gift wrap
│   ├── nostr/                # Relay client, event publish/subscribe, NIP-65 relay-list
│   ├── ipc/                  # mindforge ↔ mindgate unix socket protocol (typed contracts)
│   ├── wake/                 # Wake signal file format (HeartBeat / MindGate / planning)
│   ├── contacts/             # Contacts list with identity tiers (master / friend / acquaintance / blocklist)
│   └── config/               # Shared configuration loading
├── pkg/                      # Stable public interfaces (empty — promote from internal/ as APIs stabilize)
├── docker/
│   ├── Dockerfile            # Multi-stage; distroless-static final image
│   └── entrypoint.sh
├── SPEC.md                   # Project specification — source of truth for design intent
├── EXAMPLE.md                # Minimum deployment walkthrough
└── CLAUDE.md                 # This file
```

## Build, Test, and Development Commands

- **Build all binaries**: `go build ./...`
- **Build container image**: `docker build -t eidopsyche/mindforge:dev -f docker/Dockerfile .`
- **Lint / vet**: `gofmt -l . && go vet ./...`
- **Unit Test**: `go test ./...`
- **Integration Test**: `go test -tags=integration ./...` (requires a running Docker daemon; spins up real containers and at least one local Nostr relay).
- **Run a local instance end-to-end**: `mindforge create test && mindforge start test && mindforge logs test`


## Coding Style & Naming Conventions

- The code, comments and documentation should be in English.
- Run `gofmt` / `goimports` before committing; CI enforces both.
- Standard Go naming: CamelCase for exported, camelCase for unexported. Package names are short, lowercase, single-word.
- Errors are returned, not panicked (except in `main`). Wrap with `fmt.Errorf("...: %w", err)` to add context.
- Use `context.Context` as the first parameter for cancellable / network / Docker / subprocess operations.
- Avoid global state; pass dependencies (clock, fs, docker client, nostr client) explicitly so tests can substitute fakes.
- Cross-binary contracts (wake signal format, IPC messages, ontology layout) live in `internal/`. Do not duplicate types between `cmd/mindforge` and `cmd/mindgate` — import from the shared package.

## Commit & Pull Request Guidelines

**Follow this pipeline for actual code change:**
1. *Code Change*.
2. *Documentation*: After any code implementation, check and update the documentations. Make sure to maintain and keep the documentations up to date.
3. *CI Check*. After pushing the commit, you should use `gh` to watch the CI result and make sure it passes.
4. *Fix Copilot Commen* (if exists). If copilot is assigned to review the PR automatically, you should wait for copilot's comments and fix them if necessary.

**Guidelines:**
- When required to use `PR` mode, or the development work is heavy, you should work in a `git` worktree in a separate branch in `.claude`, and contribute to the code by making pull requests.
- DO NOT use squash merge when you merge a branch or PR.
- Make sure to run the identical check as CI locally and apply fix before push to GitHub remote.

## Agent-Specific Notes
- When working on a GitHub Issue or PR, print the full URL at the end of the task.
- When answering questions, respond with high-confidence answers only: verify in code; do not guess.
- **NEVER** bump the version number unless the user explicitly asks to.
- **NEVER** commit any real ip-address, api-keys or other security related information. Use obviously fake placeholders in the documentations.
- **NEVER** commit any real Nostr private key (`nsec…`). Generated test keys for integration tests must live under `testdata/` with a `_test_only` suffix.
- The mind-form's ontology files (its 信条文档) are private by user-promise. When developing tools that touch them, default to opaque handling — do not log contents, do not include them in error messages.
- When in doubt about a design choice, re-read `SPEC.md` rather than reverse-engineering intent from existing code. The spec is the source of truth; the code is its in-progress realization.


## Key Development Principles

1. KISS - *K*eep *I*t *S*imple *S*tupid -> Don't add complexity when a simpler solution works
2. TDD - *T*est *D*riven *D*evelopment -> Write tests to drive the implementation
3. DRY - *D*on't *R*epeat *Y*ourself -> Don't duplicate functionality, data structures, or algorithms
4. Modularity - Develop independent modules through well defined interfaces
5. POLA - *P*rinciple *O*f *L*east *A*stonishment - Create intuitive and predictable interfaces to not surprise users
