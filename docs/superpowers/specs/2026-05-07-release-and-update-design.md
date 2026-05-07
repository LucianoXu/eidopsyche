# Eidopsyche Release & Update Pipeline Design

**Date**: 2026-05-07
**Status**: Approved (pending implementation)
**Scope**: One-line install, prompt-based auto-update, and standardized release process (versioning, changelog, CI/CD) for the Eidopsyche project. Includes the structural decision to merge the planned `mindforge` / `mindgate` / `mindforge-init` binaries into a single `eidos` binary.

## 1. Problem statement

Eidopsyche today is installed from source via `make install`. There is no tagged release, no published artifact, no install script, no update mechanism, and no CI. Two binaries (`mindforge`, `mindgate`) plus a container PID 1 supervisor were planned, which would split versioning, changelog, and self-update across components that always ship together.

We need:
1. A single one-line install command for end users.
2. An update notification flow so users learn about new releases without polling manually.
3. A repeatable release process that produces signed, checksummed artifacts on every tag.
4. A version & changelog discipline that scales as the project grows.
5. A binary structure that does not multiply the above by the number of components.

## 2. Single binary: `eidos`

The three planned binaries (`mindforge`, `mindgate`, `mindforge-init`) collapse into one binary `eidos`, with subcommands:

```
eidos forge <verb>          # MindForge: instance management + self-reflection
eidos gate <verb>           # MindGate: human client + daemon ops
eidos supervisor            # Container PID 1 entrypoint (cron + gate daemon + agent spawn)
eidos self-update           # Re-run install script
eidos version               # Print version / commit / build date
```

`MindForge` and `MindGate` remain as documentation and brand terms describing conceptual layers; they no longer correspond to separate executable files.

**Why single binary:**
- One version number, one changelog, one release artifact, one self-update path.
- `internal/` packages cannot drift between components — they are always at the same revision.
- The container image (distroless-static) carries one binary; the host install copies one file.
- KISS — composing release pipelines for multiple binaries multiplies CI/CD complexity for zero deployment-flexibility gain (the components ship together).

**Naming**: the project is **Eidopsyche**; the binary is **`eidos`** (shorter, easier to type as a frequent CLI). The install/update URL still references the `eidopsyche` GitHub repo path.

## 3. Versioning

**Scheme**: SemVer with `v` prefix on git tags. Format: `vMAJOR.MINOR.PATCH`. Examples: `v0.1.0`, `v0.1.1`, `v1.0.0`.

**Pre-1.0 conventions** (while `MAJOR == 0`):
- `MINOR` increment may carry breaking changes (CLI flags, config schema, on-disk formats, Nostr event semantics, IPC contracts). Each such bump documents the breakage in the `BREAKING CHANGES` changelog section.
- `PATCH` increment is bug-fix only, no behavioral or contract changes.
- `MAJOR` remains `0` until we commit to API stability for CLI + IPC + on-wire protocol.

**First standardized release**: `v0.1.0`. The previous hard-coded `0.0.1` (no tag) is treated as pre-history; we do not bump from `0.0.1 → 0.0.2`. The jump to `v0.1.0` signals "first time the full release pipeline ran end-to-end".

**Pre-release tags**: not used in 0.x. Re-evaluate when approaching 1.0.

**Version embedding**:
```go
// cmd/eidos/version.go
var (
    Version   = "dev"     // overridden by -ldflags "-X main.Version=v0.1.0"
    Commit    = "unknown" // overridden by -ldflags "-X main.Commit=<sha>"
    BuildDate = "unknown" // overridden by -ldflags "-X main.BuildDate=<RFC3339>"
)
```

`eidos version` prints all three. The string `dev` is reserved as a sentinel: when `Version == "dev"`, the update checker skips itself entirely (developer-built binaries never nag).

**Tag discipline**: tags are pushed manually by the maintainer. CLAUDE.md already enforces "NEVER bump the version number unless the user explicitly asks" — this rule extends to git tags.

## 4. Changelog

**Tooling**: GoReleaser auto-generates from Conventional Commits. No separate `git-cliff` / `release-please` layer.

**Conventional Commits**:
- Format: `<type>(<scope>): <subject>` — type is required, scope is optional but encouraged.
- Allowed types: `feat | fix | perf | security | refactor | chore | test | docs | build | ci`.
- Allowed scopes (suggested): `forge`, `gate`, `supervisor`, `core`, `internal/<pkg>`, or any short identifier.
- Breaking changes: `feat!:` / `fix!:` prefix or a `BREAKING CHANGE:` footer in the commit body. GoReleaser surfaces these in a dedicated changelog section.

**Filter**: only `feat | fix | perf | security` (and breaking-change items) appear in `CHANGELOG.md` and GitHub Release notes. `refactor`, `chore`, `test`, `docs`, `build`, `ci` are hidden — they are visible in `git log` for engineers but not in user-facing release notes.

**Highlights**: GitHub Release pages allow a hand-written `## Highlights` paragraph at the top, before the auto-generated lists. Used for non-trivial releases where a narrative helps users understand what changed and why.

**`CHANGELOG.md`**: kept in repo, written by GoReleaser at release time, committed as part of the release process. The auto-generation is deterministic from git history, so no manual editing is expected.

## 5. CI/CD pipeline

Two GitHub Actions workflows:

### `.github/workflows/ci.yml`
**Triggers**: pull request, push to `main`.
**Steps**:
1. Checkout, set up Go (matching `go.mod` toolchain directive).
2. `gofmt -l .` — fail if any file is unformatted.
3. `go vet ./...`.
4. `staticcheck ./...` (installed on demand).
5. `go test ./...` — unit tests.
6. `go test -tags=integration ./test/integration/...` — integration tests, with a Docker daemon and an ephemeral local Nostr relay (existing `test/integration/` infrastructure).
7. `go build -o /tmp/eidos ./cmd/eidos` — sanity-build the binary.

CI is the same set of checks as `make ci` locally. Local `make ci` must stay equivalent so contributors can validate before pushing.

### `.github/workflows/release.yml`
**Trigger**: tag push matching `v*`.
**Steps**:
1. Checkout (full history needed for changelog generation).
2. Set up Go.
3. Log in to ghcr.io using `GITHUB_TOKEN`.
4. `goreleaser release --clean` — runs the full pipeline configured in `.goreleaser.yml`.

GoReleaser handles: cross-compile, archive, checksum, GitHub Release creation, ghcr.io image push, changelog generation. No bespoke shell scripts.

### `.goreleaser.yml` (key configuration)
- **Builds**: one entry, target `./cmd/eidos`, GOOS × GOARCH matrix:
  - `linux × {amd64, arm64}`
  - `darwin × {amd64, arm64}`
  - `windows × {amd64, arm64}`
- **Ldflags**: `-s -w -X main.Version={{.Version}} -X main.Commit={{.ShortCommit}} -X main.BuildDate={{.Date}}`.
- **Archives**: `tar.gz` for linux/darwin, `zip` for windows. Naming: `eidos_{{.Version}}_{{.Os}}_{{.Arch}}.{ext}`.
- **Checksum**: `checksums.txt` (SHA256), uploaded alongside archives.
- **Changelog**:
  ```yaml
  changelog:
    use: github
    sort: asc
    filters:
      include:
        - "^feat"
        - "^fix"
        - "^perf"
        - "^security"
    groups:
      - title: Features
        regexp: "^feat"
        order: 0
      - title: Bug fixes
        regexp: "^fix"
        order: 1
      - title: Performance
        regexp: "^perf"
        order: 2
      - title: Security
        regexp: "^security"
        order: 3
  ```
- **Docker**:
  ```yaml
  dockers:
    - image_templates:
        - "ghcr.io/lucianoxu/eidopsyche:{{ .Version }}"
        - "ghcr.io/lucianoxu/eidopsyche:{{ .Major }}.{{ .Minor }}"
        - "ghcr.io/lucianoxu/eidopsyche:{{ .Major }}"
        - "ghcr.io/lucianoxu/eidopsyche:latest"
      dockerfile: docker/Dockerfile
  ```
- **Release**: GitHub Release auto-created, body includes generated changelog.

## 6. Install script

**Hosting**: `https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh`.

**One-line install**:
```sh
curl -fsSL https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh | sh
```

**Script responsibilities**:
1. Detect OS (linux / darwin / mingw / cygwin) and ARCH (amd64 / arm64); fail clearly on unsupported platforms.
2. Determine target version: latest by default, or `EIDOS_VERSION=v0.2.0` to pin.
3. Resolve install prefix:
   - Default: `~/.local/bin` (no sudo).
   - `PREFIX=/usr/local sudo curl ... | sh` for system-wide.
   - `DESTDIR=/tmp/stage PREFIX=/usr/local` for staged packaging.
4. Download `eidos_<version>_<os>_<arch>.{tar.gz|zip}` and `checksums.txt` from the matching GitHub Release.
5. **Verify SHA256** against `checksums.txt`. Non-optional. Abort and remove the partial download on mismatch.
6. Extract, install `eidos` to `$BINDIR` with mode `0755`.
7. Print PATH advice if `$BINDIR` is not on `$PATH`.
8. Print success line with installed version.

**Idempotence**: re-running the script upgrades in place. Re-running with the same version is a no-op (or a clean reinstall, decided at implementation time).

**Robustness**:
- Use `set -eu` and explicit error trapping; never leave half-installed state.
- Use `mktemp -d` for the staging area; clean up on exit (success or failure).
- Verify `curl` and `tar`/`unzip` are available; bail with an actionable message otherwise.

## 7. Update check

**Goal**: the binary discovers new releases and prompts the user, without polling on every command and without breaking offline use.

**Trigger**: every CLI invocation reads the cache file (cheap, synchronous, no network). If the cache is stale, the binary spawns a background goroutine to refresh it; the goroutine never blocks the user's command — its result lands in the cache file for the *next* invocation to read. The current invocation prints the prompt based on whatever the cache already says (which may be stale, but staleness is bounded by 24h + one command). Daemons (`eidos gate daemon`, `eidos supervisor`) also kick off the same background refresh once on startup.

**Cache file**: `$XDG_CACHE_HOME/eidos/update-check.json` (default `~/.cache/eidos/update-check.json`). Schema:
```json
{
  "checked_at":      "2026-05-07T03:42:11Z",
  "latest_version":  "v0.2.0",
  "current_version": "v0.1.0",
  "last_shown_at":   "2026-05-07T03:42:11Z"
}
```

**Cache TTL**: 24 hours. Past TTL → fire an async refresh, do not block the user's command.

**Async refresh**:
- 2-second timeout on the HTTP request.
- Errors silently ignored (no stderr noise on transient network failure).
- Endpoint: `https://api.github.com/repos/LucianoXu/eidopsyche/releases/latest`. Response field: `tag_name`.
- Refresh writes `checked_at` and `latest_version` even if the latter is unchanged, to push the next refresh out by another TTL.

**Comparison**: `latest_version` parsed via `golang.org/x/mod/semver`. If `semver.Compare(latest, current) > 0`, an update is available.

## 8. Update prompt UX

**When**: after the user's command finishes, if an update is known, the prompt prints once on stderr (does not pollute stdout pipes).

**Format**:
```
A new release of eidos is available: v0.1.0 → v0.2.0
To upgrade, run: eidos self-update
https://github.com/LucianoXu/eidopsyche/releases/tag/v0.2.0
```

**Display cooldown**: the prompt shows at most once per 24-hour window. Tracked via `last_shown_at` in the cache file. After printing, `last_shown_at` is updated to `now`. Subsequent invocations within 24h check `last_shown_at` and skip printing.

**Exception**: `eidos version` always shows the prompt regardless of cooldown. The user explicitly asked about version, so the answer should include "and there's a newer one available".

**Stream choice**: stderr (so `eidos something | jq` doesn't get the prompt mixed into JSON). Implementation must guarantee the prompt is written *after* the command's own output finishes — for daemon commands the prompt is logged at startup before the main loop begins.

## 9. Self-update

`eidos self-update` is a thin wrapper that re-executes the install script.

**Behavior**:
1. Refuse to run if `Version == "dev"` (developer build); print a hint to use `make build`.
2. Resolve the install URL (compile-time constant pointing at `raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh`).
3. Determine current install path via `os.Executable()`. Pass `PREFIX` corresponding to the parent `bin/` directory's parent so the install script targets the same location.
4. `exec` the install script: `sh -c "curl -fsSL <url> | PREFIX=<prefix> sh"`.
5. The install script handles version verification, checksum, atomic replacement.

**Why a wrapper instead of in-process self-replace**: the install script is the single source of truth for upgrade logic (PATH detection, prefix logic, sudo handling, checksum verification). Embedding a self-updater library would duplicate this logic and require maintaining two upgrade paths.

**Trade-off**: requires `curl` and `sh` available on the user's machine. Acceptable — that is the same requirement as the initial install.

**Container case**: containers update via `docker pull ghcr.io/lucianoxu/eidopsyche:<tag>` + restart, not via `self-update`. The supervisor inside the container detects via the standard update-check that a new image is available and surfaces a notice in container logs — but does not attempt to self-replace its own PID 1.

## 10. Opt-out and degradations

The update check skips itself when **any** of the following is true:

| Layer | Mechanism |
|---|---|
| Environment variable | `EIDOS_NO_UPDATE_CHECK=1` |
| Configuration file | `[update] check = false` in `~/.config/eidos/config.toml` |
| CI auto-skip | `CI` environment variable is set (matches GitHub Actions, GitLab CI, CircleCI, etc.) |
| Dev build skip | `Version == "dev"` |

The skip is total: no network, no cache read, no prompt. This guarantees that a user who explicitly opts out never sees update-related stderr noise.

## 11. New code organization

New top-level files:
- `install.sh` (root of repo) — the install/update script. Lives in `main` branch so the `raw.githubusercontent.com` URL is stable.
- `.goreleaser.yml` (root) — release config.
- `.github/workflows/ci.yml`, `.github/workflows/release.yml`.
- `CHANGELOG.md` (root, generated by GoReleaser).

New internal packages:
- `internal/update/` — update-check logic (`Checker`, `Cache`, `Prompt`).
  - `check.go` — fetch latest from GitHub API, compare versions.
  - `cache.go` — XDG cache file read/write, TTL logic.
  - `prompt.go` — format prompt, enforce display cooldown, write to stderr.
  - `optout.go` — env / config / CI / dev detection.
- `cmd/eidos/selfupdate.go` — the `eidos self-update` command (thin wrapper around install.sh).

`cmd/` reorganization:
- Current: `cmd/mindgate/` (only existing binary).
- Target: `cmd/eidos/`, with subcommand source files grouped by Cobra subcommand:
  - `cmd/eidos/main.go`
  - `cmd/eidos/version.go`
  - `cmd/eidos/selfupdate.go`
  - `cmd/eidos/forge/` — all forge subcommand sources
  - `cmd/eidos/gate/` — all gate subcommand sources (initial port from `cmd/mindgate/`)
  - `cmd/eidos/supervisor/` — container PID 1 supervisor

The current `cmd/mindgate/` source becomes `cmd/eidos/gate/`. Existing commands like `mindgate send`, `mindgate add-contact`, `mindgate daemon` move to `eidos gate send`, `eidos gate add-contact`, `eidos gate daemon`. Internal `internal/` packages need no path changes — the import paths are unchanged regardless of the binary's `cmd/` location.

## 12. Migration plan (current → target)

This is the implementation roadmap, in order. Each step is a separate commit (or set of commits) on a feature branch with PR.

1. **Reorganize `cmd/mindgate/` → `cmd/eidos/gate/`**: rename directory, wire up new top-level `cmd/eidos/main.go` with a Cobra root command and `gate` subcommand tree. Update imports, Makefile, INSTALL.md, USAGE.md, EXAMPLE.md to use `eidos gate`.
2. **Add `eidos version` with ldflags-based metadata**: replace the hard-coded `Version = "0.0.1"` constant with the three-variable `Version`/`Commit`/`BuildDate` pattern. Default to `dev`/`unknown`/`unknown`.
3. **Author `install.sh`**: with SHA256 verification, OS/arch detection, prefix logic. Test on linux/amd64 (CI environment) and locally on darwin if available. Windows install path is `.zip` extraction; documented separately.
4. **Author `.goreleaser.yml`**: configure builds, archives, checksums, changelog filter, docker images. Validate locally with `goreleaser release --snapshot --clean` (no publish).
5. **Author `.github/workflows/ci.yml`**: replicate `make ci` in CI.
6. **Author `.github/workflows/release.yml`**: trigger on tag push, run GoReleaser.
7. **Implement `internal/update/`**: checker + cache + prompt + opt-out, with unit tests. Wire it into `cmd/eidos/main.go` PreRunE / PostRunE so every subcommand triggers the check.
8. **Implement `eidos self-update`**: thin shell-out wrapper, with a `--prefix` flag.
9. **Stub `eidos forge` and `eidos supervisor`**: even before MindForge implementation lands, the subcommand stubs exist with helpful "not yet implemented" messages so `eidos --help` reflects the planned shape.
10. **Tag `v0.1.0` and validate full release pipeline end-to-end**: the first real release, built and published by the new pipeline. Verify install script works against the resulting release. Verify update check shows correct prompt when an artificially-old binary runs against `v0.1.0`.

## 13. Out of scope

Deliberately deferred:
- **Cosign / Sigstore signing**: revisit before 1.0. Adds GitHub OIDC plumbing to release pipeline; not needed at 0.1.
- **Custom install domain** (e.g., `install.eidopsyche.dev`): zero-infra GitHub raw URL is sufficient; can transparently switch later by updating README and documentation.
- **In-process self-replace** (e.g., `minio/selfupdate`): rejected — install script is single source of truth.
- **Full multi-OS install script abstractions** (PowerShell installer for Windows native, Homebrew tap, apt repo): defer until there is documented user demand. Windows users get the `.zip` artifact and manual extraction instructions; if `install.sh` runs in mingw/cygwin/WSL it works the same as on Linux.
- **Pre-release tags** (`v0.2.0-rc1`): not used while in 0.x.
- **Auto-generated release notes from PR labels** (alternative to commit-based generation): GoReleaser's commit-based path is simpler and our commit discipline is enforceable.

## 14. Open follow-ups

- ~~Pick the GitHub user/org name~~ — resolved: `LucianoXu/eidopsyche` for repo paths and api.github.com; `ghcr.io/lucianoxu/eidopsyche` for the container registry (lowercased per OCI spec).
- Decide whether `eidos forge` runs the same code on host vs in container, gated by environment detection (current SPEC says yes). The single-binary structure makes this trivial — same binary, different subcommand subtrees enabled by container vs host detection. Implementation detail for the forge subcommand, not for this design.
