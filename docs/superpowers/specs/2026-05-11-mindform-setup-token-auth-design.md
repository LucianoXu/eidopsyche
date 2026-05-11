# Mindform Setup-Token Auth Design

**Date:** 2026-05-11
**Status:** Spec — pending implementation plan
**Scope:** Replace the host-shared OAuth credentials path in `eidos forge login` with isolated per-mindform `claude setup-token` tokens, and introduce a classifier for claude exit failures so 401-on-exit-1 stops failing silently.

## Background

### What broke

Today's `eidos forge login --from-host` (the default) copies the operator's `~/.claude.json` and `~/.claude/.credentials.json` verbatim into the mindform's docker volume. The container's `claude` runs as `HOME=/eidos/claude` and reads the same OAuth tokens the host's `claude` is using.

When the host's `claude` refreshes its access token (which Anthropic's server triggers by rotating both the access token and the refresh token), the server invalidates the previous tokens. The container still holds the stale copy and gets `401 Invalid authentication credentials` on every wake.

Observed on 2026-05-11: a mindform created at 15:55 UTC failed every wake after 16:30 UTC, because the host's `claude` (used in the same session) refreshed at ~17:30 UTC, invalidating the container's tokens server-side well before their on-paper `expiresAt`. Ten failed wakes accumulated silently before the operator noticed.

### Why the silent failure

`cmd/eidos/supervisor/agent_runner.go:697-707`'s `isAuthError` heuristic matches only exit codes 47 and 41. Claude Code currently exits **1** on 401. So the marker `/eidos/run/auth_required.json` was never written, the self-gate at `runAgent`'s top never activated, `eidos forge status` did not show `auth: REQUIRED`, and the supervisor kept invoking claude every heartbeat.

## Goals

1. **Each mindform owns an independent setup-token** — generated specifically for it, never shared with the host's claude session. Host activity cannot invalidate the mindform's credentials.
2. **The setup interface offers two operator paths** — paste an existing token, or generate one on the spot. Same prompt in CLI and in the first-contact wizard.
3. **Claude exit failures get classified** — auth, rate-limit, server, bad-request, network, killed, unknown — so the supervisor reacts correctly and operators see a meaningful status instead of a bare `failed(1)`.

## Non-goals

- Notification to master via mindgate on auth failure (the marker + `forge status` is the surface).
- Host-side auto-heal watcher that re-runs `forge login` proactively (decoupling creds means rotation no longer randomly invalidates the mindform, so the operator-runs-`forge login`-when-needed loop is enough).
- Anthropic API key (`sk-ant-api03-...`) path. We stay on subscription via `setup-token` to preserve subscription billing.
- Backward compatibility with mindforms created before this change — operator runs `eidos forge login <name>` once after upgrading; that's the migration.

## Architecture

```
┌─ host ────────────────────────────────────────────────────────────┐
│                                                                   │
│  eidos forge login <name>     (or wizard phase 4)                 │
│       │                                                           │
│       ├── path 1 (paste/--token-file): reads .credentials.json    │
│       │      blob from stdin or a file                            │
│       │                                                           │
│       ├── path 2 (--generate, default): spawns                    │
│       │      `claude setup-token` with HOME=/tmp/eidos-setup-XXX/ │
│       │      Inherits stdin/stdout so the operator sees the URL.  │
│       │      Reads /tmp/eidos-setup-XXX/.claude/.credentials.json │
│       │                                                           │
│       └── result (either path): a single JSON blob in memory      │
│                                                                   │
│  Validate: parse blob → require claudeAiOauth.accessToken,        │
│            refreshToken, expiresAt; reject otherwise              │
│                                                                   │
│  writeIntoVolume(name, /eidos/claude/.claude/.credentials.json)   │
│  clearAuthRequiredInVolume(name)                                  │
└───────────────────────────────────────────────────────────────────┘
                              │
                              ▼  docker volume eidos-mindform-<name>
┌─ container ───────────────────────────────────────────────────────┐
│  HOME=/eidos/claude                                               │
│  /eidos/claude/.claude/.credentials.json   ← ONLY this file       │
│  /eidos/claude/.claude.json                ← bootstrapped fresh   │
│                          on first claude run; not copied from host│
│  claude auto-refreshes accessToken via refreshToken — no host     │
│  involvement. refreshToken rotation stays inside this volume.     │
└───────────────────────────────────────────────────────────────────┘
```

Key invariant: **the container never sees the host's `~/.claude.json` or the host's `~/.claude/.credentials.json`.** The current code copies both; that is the root cause being removed.

## File layout in the volume

| Path | Source | Notes |
|---|---|---|
| `/eidos/claude/.claude/.credentials.json` | written by `eidos forge login` | chmod 600; the only auth-bearing file |
| `/eidos/claude/.claude.json` | created by claude on first run inside the container | NOT pre-seeded from host |
| `/eidos/claude/.claude/sessions/`, `projects/`, etc. | created by claude as needed | unchanged |

Token refresh inside the container: when `accessToken` expires, claude reads `refreshToken`, calls Anthropic, writes the new `accessToken` and (rotated) `refreshToken` back to `.claude/.credentials.json` in the volume. Host is never involved, so no cross-rotation invalidation. The refreshToken's own expiry (months, typically) is the only thing the operator eventually has to re-login for.

Host-side `--generate` flow:

1. `mkdir -p /tmp/eidos-setup-<uuid>/.claude` (mode 0700)
2. `HOME=/tmp/eidos-setup-<uuid> claude setup-token` — inherits the operator's terminal stdin/stdout so they see the URL and any prompts
3. On exit 0, read `/tmp/eidos-setup-<uuid>/.claude/.credentials.json`
4. `defer os.RemoveAll("/tmp/eidos-setup-<uuid>")` — wipe immediately; the credentials live only in the volume now

If `claude setup-token` exits non-zero (operator cancelled, network failure, etc.), the temp dir is wiped, the volume is untouched, the existing credentials (if any) survive. No partial-write hazard.

## CLI surface

`eidos forge login <name>` — interactive prompt when no flag is given:

```
How will alice authenticate?
  [1] Paste a setup-token I've already generated
  [2] Generate a fresh setup-token now (browser will be needed)
> _
```

Path 1 then asks:

```
Source?
  [1] Read from a file
  [2] Paste contents (blank line ends)
> _
```

Path 2 runs `claude setup-token` against an isolated HOME, inheriting stdin/stdout so the operator sees the URL and any progress messages directly. On a headless / SSH-only host this still works: the operator copies the URL from the SSH terminal, opens it on whichever device has a browser, completes OAuth, and the local-loopback callback completes inside `claude setup-token`.

CLI flags (non-interactive):

- `--token-file <path>` — read `.credentials.json` from a file. Wins over everything else.
- `--paste` — read `.credentials.json` from stdin (for `cat creds.json | eidos forge login alice --paste`).
- `--generate` — explicit form of path 2 (the interactive default).
- No flag → interactive prompt.

Flags removed (no backward compat retained per spec scope):

- `--from-host` — removed. The shared-token failure mode is the reason for the redesign.
- `--from-file` — renamed to `--token-file`.
- `--method` (in-container OAuth fallback) — removed. Only `--console` worked under SSH and it billed via API, not subscription — a quiet trap. Path 2's URL-display covers the SSH-only case properly.

Wizard integration: the first-contact wizard's phase 4 already calls `forge.InstallLoginFromHost`. That call is replaced with `forge.InstallLoginInteractive` which runs the same prompt above. No new wizard phase.

`eidos forge create <name>` (non-wizard path): unchanged for volume + ontology setup. At the end:

- If the operator passed `--token-file`/`--paste`/`--generate`, runs login inline.
- Otherwise prints:
  > _Mind-form created but not yet logged in. Run `eidos forge login alice` to authenticate Claude Code before her first wake._

## Claude error classifier

Replaces today's binary `isAuthError` with a typed verdict computed once at exit:

```go
type ClaudeErrorKind int
const (
    ClaudeOK            ClaudeErrorKind = iota
    ClaudeAuthRequired                  // 401, OAuth invalid, "Please run /login"
    ClaudeRateLimit                     // 429
    ClaudeServerError                   // 5xx
    ClaudeBadRequest                    // 400 — likely an eidos bug
    ClaudeNetwork                       // dial/timeout/EOF before any HTTP status
    ClaudeKilled                        // signal: OOM, SIGTERM
    ClaudeUnknown                       // catch-all; preserves today's behaviour
)

type ClaudeVerdict struct {
    Kind       ClaudeErrorKind
    HTTPStatus int       // 0 if not an API error
    Snippet    string    // first ~200 chars of relevant stderr line, for logging
    Retryable  bool      // hint for supervisor — true means "next wake may succeed"
}

func ClassifyClaudeExit(err error, st *os.ProcessState, stderr []byte) ClaudeVerdict
```

Classification rules (all substring scans, no regex needed):

- Exit code 47 / 41 → `ClaudeAuthRequired` (forward-compat with future claude exit codes).
- Exit code 1 + stderr contains `"401"` / `"Please run /login"` / `"OAuth token is invalid"` → `ClaudeAuthRequired`.
- Exit code 1 + stderr contains `"429"` / `"rate limit"` → `ClaudeRateLimit`.
- Exit code 1 + stderr contains `" 500 "` / `" 502 "` / `" 503 "` → `ClaudeServerError`.
- Exit code 1 + stderr contains `"400"` / `"invalid request"` → `ClaudeBadRequest`.
- Exit code 1 + stderr contains `"ECONNREFUSED"` / `"dial"` / `"i/o timeout"` / `"EOF"` → `ClaudeNetwork`.
- Killed by signal (`st.Sys().(syscall.WaitStatus).Signaled()`) → `ClaudeKilled`.
- Anything else → `ClaudeUnknown`.

Per-kind supervisor reactions. The table below is the eventual target; this PR ships only the rows marked **(now)**, with the remaining rows wired as classifier output + wake-status rendering but no marker writes yet:

| Kind | Marker | Retry next wake? | Operator surface | Scope |
|---|---|---|---|---|
| `ClaudeAuthRequired` | `/eidos/run/auth_required.json` | no (self-gate) | `forge status` → `auth: REQUIRED` | **(now)** |
| `ClaudeRateLimit` | `/eidos/run/rate_limited.json` (with `retry_after`) | yes after expiry | `forge status` → `rate-limited until HH:MM` | (follow-up) |
| `ClaudeServerError` | none | yes, immediately | wake logged as `failed(server)` | (now: status rendering only) |
| `ClaudeBadRequest` | `/eidos/run/bad_request.json` | no (self-gate) — eidos bug | `forge status` → `auth: BAD REQUEST (see logs)` | (follow-up) |
| `ClaudeNetwork` | none | yes | wake logged as `failed(network)` | (now: status rendering only) |
| `ClaudeKilled` | none | yes | wake logged with exit signal | (now: status rendering only) |
| `ClaudeUnknown` | none | yes (today's behaviour) | wake logged with stderr snippet | (now: status rendering only) |

Rationale for shipping only `ClaudeAuthRequired`'s marker: the auth bug is the one currently silently bleeding wakes and operator confidence. The rate-limit / bad-request markers introduce their own gating semantics (retry-after parsing, "is this really an eidos bug or transient?") that benefit from being designed against observed production behaviour rather than speculated up front. The classifier itself is the central abstraction; reactions are progressively wired without further refactors.

Three new properties this gives us:

1. **Central recognition** of claude failures, instead of scattered exit-code checks.
2. **Categorised wake status** in `eidos forge watch <name> --list` — `failed(auth)`, `failed(rate)`, `failed(server)`, `failed(unknown)`, etc. — instead of today's bare `failed(1)`.
3. **Future-proofing** — new claude exit conventions land as new classifier branches, not refactors at every call site.

## Migration and cleanup

No backward compatibility for existing mindforms; the operator runs `eidos forge login <name>` once after upgrading. That's the migration.

**Code to remove:**

- `cmd/eidos/forge/login.go::installFromHost(name, image, runSetupToken bool)` — the whole function.
- `--from-host` flag and its CLI doc.
- `--method` flag and `runInContainerLogin`.
- The "copy `~/.claude.json`" leg in any current code (e.g., `writeIntoVolume(..., "/eidos/claude/.claude.json", ...)`).
- `cmd/eidos/forge/login.go::InstallLoginFromHost` (the wizard shim) — replaced by `InstallLoginInteractive`.

**Code to add:**

- `internal/claudeauth/` (new package) — owns the temp-HOME orchestration, token validation, volume writing.
  - `Generate(stdin, stdout, stderr) ([]byte, error)` — drives `claude setup-token` with isolated HOME; returns the credentials.json bytes.
  - `Validate(blob []byte) error` — parses, requires `claudeAiOauth.{accessToken,refreshToken,expiresAt}`, rejects otherwise.
  - `WriteToVolume(name, image string, blob []byte) error` — wraps `forgectl.WriteToVolume` + `clearAuthRequiredInVolume`.
- `internal/claudeexec/classify.go` — the classifier from above, exported as `ClassifyClaudeExit`.

**Code to change:**

- `cmd/eidos/forge/login.go` — rewrite around `internal/claudeauth/`.
- `internal/firstcontact/phase4_seal.go` (or wherever the wizard calls login today) — call the new `InstallLoginInteractive`.
- `cmd/eidos/supervisor/agent_runner.go`'s claude call site — tee stderr to a capped buffer (~4KB), pass to classifier, react per the table.
- `cmd/eidos/supervisor/birth.go::productionBirthHandler` — same change.
- `cmd/eidos/forge/watch.go` (and `cmd/eidos/forge/watch_test.go`) — extend the wake-list status column to render verdict kinds.

**Code that stays:**

- `internal/authstate/` — already correct; classifier just feeds it properly now.
- `cmd/eidos/forge/login.go::clearAuthRequiredInVolume` — still needed after every successful login.
- The supervisor's self-gate in `runAgent` — unchanged.

## Testing

| Layer | Test | Where |
|---|---|---|
| Token validation | `Validate` accepts well-formed `claudeAiOauth` blobs; rejects missing `accessToken`/`refreshToken`/`expiresAt`/wrong types/empty file | `internal/claudeauth/validate_test.go` (table-driven) |
| Token generation | `Generate` runs a stub `claude` (shell script that writes a fixture into `${HOME}/.claude/.credentials.json` and exits 0), then asserts captured blob equals fixture; second case: stub exits 1 → `Generate` returns error, temp HOME removed | `internal/claudeauth/generate_test.go` |
| Volume write | `WriteToVolume` with mocked `forgectl.WriteToVolume`: asserts dst path is `/eidos/claude/.claude/.credentials.json`, chmod 600 invoked, `auth_required.json` cleared after | `internal/claudeauth/volume_test.go` |
| Error classifier | Each `ClaudeErrorKind` arm: pass `(exit-code, stderr-fixture)` → expect verdict. Table-driven covering all rules. Test catch-all `ClaudeUnknown` for empty stderr. | `internal/claudeexec/classify_test.go` |
| Marker writer reaction | When classifier returns `ClaudeAuthRequired`, supervisor writes `auth_required.json`; when `ClaudeBadRequest`, writes `bad_request.json`; when `ClaudeServerError`, writes nothing. | `cmd/eidos/supervisor/agent_runner_test.go` — extend existing stub-`claude` tests with three new fixtures |
| Login CLI flag matrix | `--token-file` wins; `--paste` reads stdin; `--generate` drives stub; no flag → interactive prompt resolves to a path. Use `cobra.Command.SetArgs` + faked stdin. | `cmd/eidos/forge/login_test.go` |
| Wizard integration | Phase-4 calls `InstallLoginInteractive`; on success continues to ritual-complete; on failure stops the ritual with a clean error and leaves the volume + ontology in place so operator can retry `forge login`. | `internal/firstcontact/phase4_seal_test.go` |
| Integration | E2E: `make image` → create mindform → `eidos forge login <name> --generate` against a fake `claude` stub returning a valid fixture → first wake succeeds. Tagged `integration`, runs in CI. | `test/integration/auth_setup_token_test.go` (new) |

**Anti-test (negative case kept explicit so we do not regress):**

A mindform's `.claude/.credentials.json` is NEVER identical-by-content to the host's `~/.claude/.credentials.json` after a `forge login`. The integration test asserts the two files diverge after login — guards against accidental re-introduction of the host-copy path.

## Open questions

None at spec time. If `claude setup-token`'s callback flow turns out to differ from observed (e.g., a future version moves to a device-code flow rather than localhost-callback), the `--generate` path may need an SSH-tunnel fallback. That is a follow-up if and when it bites.
