# Mind-form Upgrade — Design

**Date:** 2026-05-14
**Status:** Approved (brainstorming)
**Scope:** `cmd/eidos/forge/`, `internal/forgectl/`, `internal/daemon/`, `internal/ipc/`, `internal/forge/` (new), `docker/mindform/Dockerfile`, `Makefile`, `.github/workflows/release.yml`

## Goal

Provide a host-side command `eidos forge upgrade <name>` that swaps an
existing mind-form's container image to a newer version **without
touching the volume**, and surface the bundled `eidos` binary version
and Claude Code CLI version as queryable metadata on every mind-form
image.

Today, `eidos self-update` upgrades the host binary and restarts the
managed gate daemon. Existing mind-form containers stay pinned to the
image tag they were created with (`orchestrate.go:213-219`); the host
binary's `version.Version` only affects new `forge create` invocations
via `DefaultImage()` (`orchestrate.go:34-40`). There is no in-tree path
for upgrading a running mind-form to the matching image of a newer host
binary while preserving its volume (ontology, identity, contacts,
Claude auth, agentloop state).

This design closes that gap and additionally records the Claude Code
version each mind-form is running — the host-side `eidos` binary's
behaviour depends on Claude Code CLI surface (stream-json transcript,
`-p` flags), so the operator needs to see what version any given
mind-form is actually executing.

## Non-Goals

- No automatic mindform upgrade on `eidos self-update`. Upgrade is
  always operator-initiated. (Self-update remains
  binary-only + gate restart; mindforms unaffected until the operator
  runs `forge upgrade`.)
- No bulk `--all` flag in this iteration. Single-name only. `--all`
  may follow once the per-name flow is stable; designing it now is
  scope creep.
- No "merge new template files into existing volume" behaviour. The
  volume is operator/mind-form territory; upgrade does not touch it.
  Cross-version state schema migration (e.g. `state.json`,
  `session.json` shape changes) is the mind-form image's own concern,
  handled by its entrypoint or supervisor on next start.
- No dashboard UI in this iteration. The IPC method shape is designed
  so that a future dashboard handler can call the same method
  in-process; the UI itself is deferred.
- No rollback to the previous image after a failed start. The previous
  image remains in the local docker store (we do not prune); the
  operator can manually `docker run` it for emergency recovery. The
  volume is untouched on any failure path, so re-running
  `forge upgrade` is the standard recovery.

## Design

### A. Dockerfile — embed version metadata

`docker/mindform/Dockerfile` (Stage 4) gains:

```dockerfile
ARG CLAUDE_CODE_VERSION              # existing, already declared
ARG EIDOS_VERSION=dev                # new build arg

LABEL org.eidopsyche.claude-code-version="${CLAUDE_CODE_VERSION}"
LABEL org.eidopsyche.eidos-version="${EIDOS_VERSION}"

RUN mkdir -p /etc/eidos \
 && echo "${CLAUDE_CODE_VERSION}" > /etc/eidos/claude-code.version \
 && echo "${EIDOS_VERSION}"       > /etc/eidos/eidos.version
```

Two write sites because they serve different readers:

- **OCI LABELs** are readable from the host via `docker image inspect`
  *without starting the container*. `forge upgrade` preflight reads
  them off the new image (just pulled) and the current container's
  image, computes the diff line, and renders it. `forge status` reads
  them off the container's image to display in the status block.
- **`/etc/eidos/*.version` files** are readable from inside the
  container without spawning subprocesses. If any future
  in-container code path needs to feature-gate on the bundled Claude
  Code version, this is the cheap read path. No current consumer; the
  files are written defensively because the cost is one `RUN echo`.

The LABEL key prefix `org.eidopsyche.*` follows OCI's reverse-DNS
recommendation. Key spelling chosen for stability — once shipped, both
the LABEL key and the file path constitute a public contract between
the image and the host binary, and any future rename forces a coupled
rebuild of host + image.

`Makefile`'s `image` target adds `--build-arg EIDOS_VERSION=$(VERSION)`
where `VERSION` is the same source the host binary's ldflags use
(`git describe --tags --always` for source builds, the release tag in
CI). `.github/workflows/release.yml`'s `mindform-image` job passes
`EIDOS_VERSION=${{ github.ref_name }}` so released images carry the
clean `vX.Y.Z` value.

### B. `internal/forgectl` — image label inspection

New method on `forgectl.Client`:

```go
// ImageInspectLabels returns the OCI labels declared on ref. Empty
// map when the image carries no labels (or pre-this-change images
// that lack the eidos labels). Callers must tolerate missing keys.
ImageInspectLabels(ctx context.Context, ref string) (map[string]string, error)
```

Thin wrapper over the docker client's `ImageInspectWithRaw`, reading
`InspectResponse.Config.Labels`.

Companion helper (pure function, not on `Client`):

```go
// VersionsFromLabels parses the two org.eidopsyche.* labels. Empty
// strings for missing keys — callers render them as "unknown".
type ImageVersions struct {
    Eidos      string
    ClaudeCode string
}
func VersionsFromLabels(labels map[string]string) ImageVersions
```

Both go into `internal/forgectl/labels.go` (new file). Unit-tested with
a fake docker client returning crafted label maps.

### C. `internal/forge` — extracted orchestration package

The existing `cmd/eidos/forge/orchestrate.go` defines `Orchestrate`
(create flow) plus the `orchestrateStep` + `runSteps` step-runner.
Both `forge create` and the new `forge upgrade` flow want the same
step-with-undo runner, and we want the upgrade flow accessible from
the daemon's IPC handler (not just from a cobra command).

Action: extract the orchestration core into `internal/forge/`:

- `internal/forge/steps.go`: `Step`, `RunSteps` (renamed exports from
  the current package-private `orchestrateStep` / `runSteps`).
- `internal/forge/create.go`: the existing `Orchestrate` body, now
  taking `forgectl.Client` and a `CreateOpts` struct moved here.
- `internal/forge/upgrade.go`: new `Upgrade(ctx, c forgectl.Client, opts UpgradeOpts) (UpgradeResult, error)`.
- `internal/forge/default_image.go`: existing `DefaultImage` /
  `MindFormImageRepo` move here so the IPC handler can call them
  without importing `cmd/eidos/forge`.

`cmd/eidos/forge/orchestrate.go` becomes a thin shim that calls
`internal/forge.Orchestrate` (preserves the existing call sites in
`cmd/eidos/forge/create.go` and the First Contact wizard). This
extraction is the only refactor in this spec; it stays scoped to the
files mentioned.

`internal/forge.Upgrade` step list:

1. `image-inspect-current`: read LABELs off the running container's
   image (for diff output and the no-op check).
2. `image-pull-new`: pull `opts.Image` if not local. Read its LABELs.
   On `Skipped` short-circuit: if `opts.Image` resolves to the same
   image ID as the current container's image, return
   `{Skipped: true}` without running further steps.
3. `wait-idle` (conditional on `opts.WaitIdle`): poll the mind-form's
   gate daemon (via its container-internal IPC, reached through
   `docker exec` or the existing transcript polling pattern used by
   `forge watch`) until agentloop reports idle. Timeout returns
   `E_FORGE_IDLE_TIMEOUT`; no container mutation has happened yet,
   so this is a clean abort.
4. `stop`: `ContainerStop(name, grace)`. Undo: best-effort
   `ContainerStart` (rarely useful in practice but reflects step
   semantics).
5. `remove`: `ContainerRemove(name)`. Undo: nil — we have committed
   past the point of trivial rollback. From here on, failures leave
   the volume intact but no container; the recovery is "re-run
   `forge upgrade`", which the idempotency handling in step 6 covers.
6. `create`: `ContainerCreate(name, newImage, sameVolume)`. Idempotent
   re-entry: if `Upgrade` is invoked when the container is already
   absent (e.g. previous run was SIGINT'd between `remove` and
   `create`), `image-inspect-current` falls back to "no current
   image", the diff line shows `current: <absent>`, and the run
   proceeds straight to step 6.
7. `start`: `ContainerStart(name)`.
8. `health-verify`: poll the mind-form's gate IPC socket inside the
   container (or `forge status` runtime-state — match the existing
   pattern in `cmd/eidos/forge/status.go`) until either healthy or
   30s elapsed. Timeout returns `E_FORGE_HEALTH_TIMEOUT`. Note: this
   is a *warning* condition — the container is created and started;
   only the health probe failed. The volume is intact; the operator
   inspects logs and decides whether to roll forward or roll back
   manually.

`UpgradeOpts` and `UpgradeResult` mirror the IPC params/result types
in section E so the daemon handler is a one-line passthrough.

### D. CLI — `cmd/eidos/forge/upgrade.go`

```
eidos forge upgrade <name> [--image <tag>]
                           [--wait-idle] [--idle-timeout 10m]
                           [--grace 10]
                           [--dry-run]
```

- `--image` defaults to `internal/forge.DefaultImage()` (host binary's
  paired tag).
- `--wait-idle` opt-in. `--idle-timeout` only meaningful with
  `--wait-idle`.
- `--grace` mirrors `forge stop`'s flag (seconds before SIGKILL).
- `--dry-run` runs the preview-only path described in section E.

Flow:

1. Open IPC socket (pattern from existing `cmd/eidos/gate/send.go`).
2. Call `forge.upgrade` method with `DryRun: true` — always, regardless
   of whether the user passed `--dry-run`. The DryRun response carries
   the version diff fields, which the CLI renders as:

   ```
   upgrading alice:
     image      : ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2
                → ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3
     eidos      : v0.11.2 → v0.11.3
     claude-code: 2.1.138 → 2.1.140
   ```

   Missing LABELs (pre-this-change images) render as `unknown` rather
   than an error.

3. If `Skipped: true` (image IDs match), print
   `alice already at v0.11.2; no-op` and exit 0.
4. If the user passed `--dry-run`, exit 0 here.
5. Confirmation: if stdin is a TTY, prompt `proceed? [Y/n]`. If stdin
   is not a TTY (CI / script), auto-confirm. No `--yes` flag — upgrade
   is non-destructive (volume intact), so the high-friction
   confirmation pattern used by `forge purge` is not warranted.
6. Call `forge.upgrade` again with `DryRun: false`. Stream progress
   lines as steps complete:
   ```
   pulling ghcr.io/...:v0.11.3 ... done
   waiting for agentloop idle ... done
   stopping alice ... done
   creating new container ... done
   starting alice ... done
   verifying health ... done
   ✓ alice upgraded to v0.11.3
   ```
7. Non-zero exit on any returned error code; the IPC error message
   becomes the CLI's stderr line.

### E. IPC — `forge.upgrade` method

New method on the host gate daemon's method table.

```go
type ForgeUpgradeParams struct {
    Name        string `json:"name"`
    Image       string `json:"image,omitempty"`        // default: forge.DefaultImage()
    WaitIdle    bool   `json:"wait_idle,omitempty"`
    IdleTimeout string `json:"idle_timeout,omitempty"` // duration string, default "10m"
    Grace       int    `json:"grace,omitempty"`        // seconds, default 10
    DryRun      bool   `json:"dry_run,omitempty"`      // preview-only when true
}

type ForgeUpgradeResult struct {
    Name             string `json:"name"`
    OldImage         string `json:"old_image"`
    NewImage         string `json:"new_image"`
    OldEidos         string `json:"old_eidos,omitempty"`        // "" when label missing
    NewEidos         string `json:"new_eidos,omitempty"`
    OldClaudeCode    string `json:"old_claude_code,omitempty"`
    NewClaudeCode    string `json:"new_claude_code,omitempty"`
    Skipped          bool   `json:"skipped,omitempty"`           // image ID unchanged
    SkippedReason    string `json:"skipped_reason,omitempty"`    // e.g. "already at v0.11.2"
    DryRun           bool   `json:"dry_run,omitempty"`           // echoes the param
}
```

Handler in `internal/daemon/methods_forge.go` (new file, or merged
into the existing forge-methods file once one exists):

- Validate params (non-empty name passing
  `forgectl.ValidateName`; non-negative grace; `IdleTimeout` parses).
- Acquire per-mindform mutex (the daemon already has the lock map for
  state mutations; verify and reuse — if absent, this spec adds it
  under the same name pattern as the state-locks scheme in
  `2026-05-11-unified-state-interface-design.md`).
- Construct `UpgradeOpts` from params.
- Call `internal/forge.Upgrade(ctx, daemon.forgectlClient, opts)`.
- For DryRun, `Upgrade` runs only steps 1–2 (`image-inspect-current`,
  `image-pull-new`) and returns the populated result without mutating
  the container.
- Map `internal/forge` typed errors to IPC error codes per the table
  below.

**Error codes** (new, in `internal/ipc/protocol.go`):

| Code | Meaning |
|---|---|
| `E_FORGE_NOT_FOUND` | named mind-form has no container and no volume |
| `E_FORGE_IMAGE_PULL` | docker pull of `opts.Image` failed |
| `E_FORGE_IMAGE_INSPECT` | docker inspect failed on either old or new image |
| `E_FORGE_IDLE_TIMEOUT` | `--wait-idle` exceeded `--idle-timeout`; no mutation occurred |
| `E_FORGE_CONTAINER_STOP` | `ContainerStop` returned an error |
| `E_FORGE_CONTAINER_REMOVE` | `ContainerRemove` returned an error |
| `E_FORGE_CONTAINER_CREATE` | `ContainerCreate` returned an error |
| `E_FORGE_CONTAINER_START` | `ContainerStart` returned an error |
| `E_FORGE_HEALTH_TIMEOUT` | container started but health probe did not pass within 30s |

Several of these may already exist (e.g. `E_FORGE_NOT_FOUND`); reuse
when so, add when not. The error-code body in `protocol.go` is the
source of truth.

### F. `forge status` — surface the bundled versions

`cmd/eidos/forge/status.go` (and the IPC `forge.status` method's
response type) gain two fields read from the running container's
image LABELs:

```
mind-form alice
  state      : running
  image      : ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2
  eidos      : v0.11.2
  claude-code: 2.1.138
  ...
```

Field source: container's image ref (already retrieved by status) →
`forgectl.ImageInspectLabels` → `VersionsFromLabels`. Pre-this-change
images render `eidos: unknown` and `claude-code: unknown` rather than
erroring.

This is the only `forge status` change in this spec — no new
mindform-side state plumbing.

### G. Single-call-path posture

CLAUDE.md mandates that every operator action have a single IPC-routed
code path; container creation (`orchestrate.go:82-91`) is the
sanctioned bootstrap exception. `forge upgrade` is **not** taking that
exception — it routes through `forge.upgrade` on the host gate
daemon, enabling the future dashboard and MCP surfaces to call the
same method in-process. The CLI is the parameter-packing wrapper.

`forge create` remains a bootstrap exception for now; its migration
to IPC belongs with the broader `unified-call-path` plan
(`docs/superpowers/specs/2026-05-09-unified-call-path-design.md`),
not this spec. The asymmetry is acknowledged but tolerated until the
migration sweep.

## Testing

### Unit (no docker)

- `internal/forgectl/labels_test.go`: fake docker client returning
  varied `Config.Labels` maps; verifies `VersionsFromLabels` handles
  both keys present / one present / neither / unrelated labels mixed
  in.
- `internal/forge/upgrade_test.go`:
  - Mock `forgectl.Client` records every call. DryRun must produce
    zero `ContainerStop` / `ContainerRemove` / `ContainerCreate` /
    `ContainerStart` calls.
  - Live run executes the documented step order.
  - `WaitIdle` with a fake runtime-state poller that flips idle on
    the Nth poll: verifies the step sequence advances only after the
    flip.
  - `WaitIdle` timeout: returns `E_FORGE_IDLE_TIMEOUT`; no container
    mutation observed.
  - `Skipped` path: when the resolved new image ID equals the current
    container's image ID, result is `Skipped: true` with no mutation.
  - Idempotent re-entry: starting `Upgrade` with the container
    already absent (SIGINT mid-prev-run scenario) lands at `create` +
    `start` without erroring.
- `internal/daemon/methods_forge_test.go`: param validation (empty
  name, negative grace, malformed `idle_timeout` duration) returns
  appropriate error codes; per-mindform mutex prevents concurrent
  `forge.upgrade` calls for the same name (second call blocks or
  fails-fast per existing lock policy).
- `cmd/eidos/forge/upgrade_test.go`: mock IPC client.
  - DryRun path renders the diff and exits without a second IPC call.
  - TTY path: ANSWER yes / no behaviour; "no" answer aborts without
    second call.
  - Non-TTY path: auto-confirm; second call fires with
    `DryRun: false`.
  - `Skipped` result renders the "already at" line and exits 0.

### Integration (`-tags=integration`, real docker)

New file `test/integration/forge_upgrade_test.go` (or
`cmd/eidos/forge/upgrade_integration_test.go` if existing convention
keeps integration alongside the package — match what
`orchestrate_image_test.go` does):

1. **Baseline upgrade.** Build two test images locally (Makefile
   target or inline `docker build` from a fixture Dockerfile) with
   distinct `EIDOS_VERSION` / `CLAUDE_CODE_VERSION` LABEL values
   (e.g. `v9.9.1` / `v9.9.2`). `forge create alice` against the
   first → record `docker volume inspect` mountpoint hashes for
   `identity.toml`, `essence/`, `journal/`. `forge upgrade alice
   --image <second-tag>`. Assert: container's image ref now matches
   the second tag; volume contents byte-identical to the recorded
   hashes; `forge status alice` reports the second tag's LABELs.
2. **Wait-idle timeout.** Stage a fixture prefab whose CLAUDE.md
   makes the agentloop hold for >60s on the first wake. `forge
   upgrade alice --wait-idle --idle-timeout 5s` must exit with
   `E_FORGE_IDLE_TIMEOUT` and the container must still be running on
   the original image.
3. **No-op.** With container already on the latest image, run
   `forge upgrade alice` (no `--image`). Result `Skipped: true`;
   container untouched (verify by recording inspect timestamps before
   and after).
4. **Health-probe timeout.** Build a fixture image with an entrypoint
   that exits immediately. `forge upgrade alice --image <bad-tag>`
   must surface `E_FORGE_HEALTH_TIMEOUT`. Verify the volume is intact
   (sample file hashes match pre-run); operator can recover by
   `forge upgrade alice --image <good-tag>`.

## Edge Cases

- **Container already stopped at upgrade time.** `wait-idle` is a
  no-op (nothing running to wait on); `stop` is a no-op (already
  stopped); flow proceeds to `remove` → `create` → `start`. Exit
  state is "running on new image" — same as the from-running case.
- **`forge upgrade` SIGINT'd mid-flow.** Worst case: between `remove`
  (step 5) and `create` (step 6). Container is absent, volume intact.
  Re-running `forge upgrade alice` lands the idempotent recovery path:
  `image-inspect-current` returns "no current image"; the diff shows
  `current: <absent>`; flow proceeds straight to `create` + `start`.
- **`--image <tag>` resolves to a tag the registry does not host.**
  `image-pull-new` fails with `E_FORGE_IMAGE_PULL`. Old container
  untouched (still running).
- **Concurrent `forge upgrade` for the same name.** Per-mindform
  mutex in the daemon serializes; second caller either blocks or
  receives the standard "operation in progress" error code per the
  existing lock policy.
- **Pre-this-change image (no LABELs).** `OldEidos` / `OldClaudeCode`
  empty strings. Diff renders `eidos: unknown → v0.11.3`. Not an
  error.
- **DryRun with image not present locally.** `image-pull-new` still
  pulls (required to read its LABELs). DryRun thus has the benign
  side-effect of leaving one new image in the local docker store.
  Documented in CLI help text.
- **Docker daemon down mid-upgrade.** No graceful recovery. IPC
  handler returns the docker error code as `E_FORGE_*`; operator
  inspects, restarts docker, re-runs `forge upgrade`. Pre-1.0
  stance — not pursuing docker-down resilience.
- **Cross-version state schema drift in the volume.** Out of scope
  here. The mind-form image's own entrypoint / supervisor handles
  any required migration of `state.json` / `session.json` /
  transcripts on next start; or, if a hard break is acceptable
  per the pre-1.0 "always optimal" stance, the upgrade documentation
  for that release calls it out and instructs operators to `forge
  stop` + back up the volume + `forge upgrade` + accept loss.

## Future Work

- **`forge upgrade --all`** — iterate every mind-form on the host
  with the same `--image` / `--wait-idle` flags. Trivial loop over
  `forge list` once the per-name flow is stable.
- **Dashboard upgrade UI.** Calls `forge.upgrade` with `DryRun: true`
  to render the diff page; user clicks "Upgrade" → calls
  `forge.upgrade` with `DryRun: false`. Same handler, no new IPC
  shape.
- **Migrate `forge create` to IPC.** Eliminate the bootstrap
  exception, restoring single-call-path uniformity. Tracked under
  `unified-call-path` migration, not this spec.
- **Cosign / Sigstore verification of pulled images.** Currently the
  release pipeline ships SHA256 checksums for binaries
  (`install.sh` verifies them) but image pulls rely on docker's TLS
  + content trust defaults. Tightening image supply chain is
  release-pipeline scope, not upgrade-flow scope.

## Implementation Notes

- The `internal/forge` extraction must preserve the existing
  `Orchestrate` export semantics — the First Contact wizard imports
  it directly (`internal/firstcontact/*.go`). The shim in
  `cmd/eidos/forge/orchestrate.go` re-exports `Orchestrate` and
  `CreateOpts` so external import sites continue to compile.
- `DefaultImage()` moves with the extraction; the CLI flag
  registration in `create.go` already references it as
  `internal/forge.DefaultImage` after the move, no signature change.
- The new IPC method registration must include the method in the
  daemon's `dashboard_adapter.go` allow-list (per CLAUDE.md's
  "Single Call Path" section) so the future dashboard handler can
  reach it without extra plumbing.
- Health probe (`health-verify` step) reuses the existing
  `forge.runtime-state` IPC call inside the new container.
  Container-internal gate daemon takes ~1–3s to bring up its IPC
  socket on a fresh `ContainerStart`; the 30s budget covers cold
  starts comfortably.
