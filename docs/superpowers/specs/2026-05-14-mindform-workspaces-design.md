# Mindform workspaces (shared mount folders) — design

**Status:** draft
**Author:** Claude (with Yingte)
**Date:** 2026-05-14
**Scope:** `internal/config/`, `internal/forgectl/`, `internal/daemon/`, `internal/prompts/`, `cmd/eidos/forge/`, `docker/mindform/`, `docs/USAGE.md`, `docs/specs/SPEC.md`
**Related specs:** `2026-05-09-mindforge-v0-design.md` (one-container/one-volume baseline), `2026-05-09-unified-call-path-design.md` (CLI/dashboard single call path), `2026-05-11-unified-state-interface-design.md` (operator/mindform IPC symmetry, `daemon.Mutate` framework), `2026-05-14-mindform-upgrade-design.md` (sibling spec; `internal/forge/` package extraction and per-mind-form mutex are introduced there)

**Dependency on upgrade spec.** This spec depends on the package layout introduced by `2026-05-14-mindform-upgrade-design.md`: the `internal/forge/` extraction (`Step`, `RunSteps`, `Orchestrate`, `CreateOpts`, `DefaultImage`) and the per-mind-form mutex in the host gate daemon. **Implementation order: upgrade spec lands first; this spec rebases onto it.** Files this spec edits in `cmd/eidos/forge/orchestrate.go` will instead live at `internal/forge/create.go` once the extraction merges. The `forge restart` recreate flow (§6.3) intentionally mirrors the upgrade spec's step sequence so the two flows can be unified into a single `internal/forge.Recreate` primitive later if desired — but doing the unification now is out of scope for either spec.

## §1 — Goal

Let the operator bind host directories into a mind-form's container so that the host directory and the in-container path are the **same files** — operator-native edits, mind-form `Read` / `Write` / `Edit` tool calls, and `git` operations all act on a single underlying directory. The bind point lives outside `/eidos/`, preserving the ontology boundary in SPEC.md:137 unchanged.

Configuration is a per-mind-form list of bind mounts. The operator can bind the same host path into multiple mind-forms; when they do, those mind-forms share a substrate implicitly through the common backing directory (no MindGate envelope needed for file-level collaboration, no extra mechanism beyond "use the same `host_path` twice"). There is no first-class workspace registry or membership object; sharing is the natural consequence of per-mind-form lists pointing at the same path.

## §2 — Why now

Today a mind-form's filesystem is fully isolated from the host: the only mount is the `eidos-mindform-<slug>` named volume at `/eidos`, populated at create-time from the embedded ontology template. This is the right default for SPEC.md:137's privacy commitment (the ontology is **the** life-edge; bind-mounting it would dissolve the boundary). But it is too strong as the **only** mode:

- Operators routinely want to collaborate with a mind-form on a directory they own (a code repository, a notes tree, a working dataset). Today the only path is `docker cp` of a tar in, plus reverse `docker cp` to pull edits back — losing live editing, losing git history sharing, asymmetric.
- Multiple mind-forms cannot share file-shaped state (a common dataset, a shared scratch directory) without round-tripping through MindGate envelopes.

The fix is to **add a second mount class, orthogonal to the ontology**, mounted at `/workspace/<name>/` (outside `/eidos/`), and to make that class first-class enough that operators can manage it without learning `docker volume` arcana.

## §3 — Scope, non-goals, and what stays

**In scope (this spec):**

- Per-mind-form mount list stored in host gate `config.toml`.
- IPC method table for add / remove / list, mirrored to CLI (`eidos forge workspace …`) and dashboard, following the Single Call Path rule.
- Container recreate on `eidos forge restart <name>` to apply mount changes (the `/eidos` named volume survives the recreate, so the mind-form's ontology is unaffected).
- Status surfacing: `eidos forge status <name>` reports `pending_restart: workspaces changed` when desired ≠ actual.
- UID-mismatch detection at `forge.workspace.add` time, surfaced as a non-blocking warning.
- System prompt addendum so mind-forms discover and understand `/workspace/`.

**Non-goals:**

- **No first-class workspace registry.** No `eidos workspace create / join / leave` verbs and no "workspace object" with a membership list. Sharing across mind-forms is operator-configured (same `host_path` on N mind-forms). Rationale: KISS; the registry layer adds an abstraction whose only payoff is `workspace list` showing a membership graph — a real but small benefit, deferred until somebody hits it.
- **No live-add of mounts on a running container.** Docker cannot add/remove mounts to a running container, and the rejected alternatives (`rshared` propagation + host `mount --bind`; FUSE bridge) are Linux-only and/or invasive. Recreate-on-restart is the chosen mechanism. See §11 for the rejected alternatives.
- **No mounting inside `/eidos/`.** Container `target` is fixed as `/workspace/<name>/`. This preserves SPEC.md:137's "ontology = life-edge, not bind-mounted" commitment intact.
- **No mind-form-initiated mounts.** Only the host operator can call `forge.workspace.add` (HostCtx-only key). The mind-form cannot grant itself filesystem access.

**What stays the same:**

- `/eidos` continues to be a named Docker volume, not a bind mount.
- Mind-form runs as `eidos` (uid 1000) per `docker/mindform/Dockerfile:73`.
- `eidos forge create` flow is unchanged (workspaces are added post-create via `forge workspace add` + `forge restart`).

## §4 — Data model

Per-mind-form workspace list lives in **host gate** `~/.config/eidos/config.toml` under `[forge.<name>]`:

```toml
[forge.alice]
workspaces = [
  { name = "proj-x",  host_path = "/home/op/code/project-x", mode = "rw" },
  { name = "photos",  host_path = "/home/op/Pictures",       mode = "ro" },
]

[forge.bob]
workspaces = [
  { name = "proj-x",  host_path = "/home/op/code/project-x", mode = "rw" },  # E: same host_path as alice → both see same files
]
```

Field rules:

| Field | Type | Constraint |
|---|---|---|
| `name` | string | Matches `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`. Unique within a mind-form's list. Becomes the last segment of the container path `/workspace/<name>/`. |
| `host_path` | string | Absolute path; `filepath.Clean` is a no-op (no `..`, no double slashes). Must exist on the host and be a directory at `forge.workspace.add` time. |
| `mode` | string | `"ro"` or `"rw"`. Default `"rw"` when the field is omitted. |

**No `state.set` path.** Unlike `mindform.model` / `mindform.effort` (single scalar keys registered in `internal/config/keys.go`), the workspace list is a structured per-mind-form slice that the existing key registry does not model — its `Path` field is a literal string with no wildcard support. Rather than extend the key registry with wildcards or a `forge.<name>.workspaces` family, the workspace surface goes through **dedicated IPC verbs only**: `forge.workspace.add` / `remove` / `list`. The verbs perform their own validation and call `daemon.Mutate` against a per-mind-form lock; no `state.set forge.<name>.workspaces` path is exposed.

The verbs are only registered on the host daemon. The container daemon does not know `forge.workspace.*` at all, so an in-container caller (e.g. a malicious mind-form) cannot smuggle a mount addition through its local IPC — there is no handler to reach. The host↔container symmetry rule (`docs/superpowers/specs/2026-05-11-unified-state-interface-design.md`) is preserved through asymmetry-by-design: just as `lifecycle.*` is container-only, `forge.workspace.*` is host-only.

## §5 — IPC method table

| Method | Caller | Params | Result |
|---|---|---|---|
| `forge.workspace.add` | host | `{ mindform, name, host_path, mode? }` | `{ pending_restart: true, warnings: []string }` |
| `forge.workspace.remove` | host | `{ mindform, name }` | `{ pending_restart: true }` |
| `forge.workspace.list` | host | `{ mindform }` | `{ desired: [...], actual: [...], pending_restart: bool }` |
| `state.get forge.workspaces` | container (mind-form) | `{}` | `[{ name, mode }]` (no `host_path`) |

`desired` is the list parsed from host gate `config.toml`. `actual` is derived from `docker inspect <container>` filtered to mounts whose `Target` starts with `/workspace/`. `pending_restart = !equal(desired, actual)`.

Both `desired` and `actual` entries include `name`, `mode`, and `host_path` in the host-side response (operator already knows the host path; no privacy concern).

**Container-side `state.get forge.workspaces`** is implemented by parsing `/proc/self/mounts` inside the mind-form's container, filtering entries with target under `/workspace/`, and reading the `ro` / `rw` flag from the mount options. There is no host→container sync of workspace metadata (§9). This guarantees the container's view always reflects the current bind state — if a workspace is mounted, the container sees it, with no possibility of drift between a synced index and reality.

**Daemon implementation** lives in `internal/daemon/forge_workspace.go` and routes through `daemon.Mutate` (context check → lock → write config → apply hook → emit `state.changed`). The "apply hook" for `forge.<name>.workspaces` does **not** restart the container; it only marks the mind-form's pending-restart bit. The container recreate happens on the next `forge.restart` IPC call.

**CLI** (`cmd/eidos/forge/workspace.go`, new file):

```
eidos forge workspace add <mindform> <name> <host-path> [--mode=ro|rw] [--no-warn-uid]
eidos forge workspace remove <mindform> <name>
eidos forge workspace list <mindform>
```

**Dashboard** uses the same dispatcher through `internal/daemon/dashboard_adapter.go`; the dashboard's "workspaces" panel is a thin renderer over `forge.workspace.list`.

**Mind-form in-container** uses the existing `eidos state get` CLI: `eidos state get workspaces` prints the name/mode pairs.

## §6 — Container orchestration changes

### §6.1 `internal/forgectl/docker.go`

Today `CreateOpts.Mount` is a single struct. Replace with a slice:

```go
type CreateOpts struct {
    Name       string
    Image      string
    Mounts     []Mount         // was: Mount Mount
    Env        []string
    Entrypoint []string
    Cmd        []string
}

type MountType string

const (
    MountVolume MountType = "volume"
    MountBind   MountType = "bind"
)

type Mount struct {
    Type     MountType
    Source   string  // volume name (Type=volume) or host path (Type=bind)
    Target   string  // container path
    ReadOnly bool    // bind-only; volumes ignore
}
```

`realClient.ContainerCreate` translates each `Mount` into the Docker SDK's `mount.Mount` with `Type` and `ReadOnly` set appropriately.

`RunInitOpts.Mount` stays a single `Mount` (init container only ever needs `/eidos`).

### §6.2 `cmd/eidos/forge/orchestrate.go` — create-container step

The `create-container` step (currently `orchestrate.go:212-222`) builds the mount list by combining the always-present ontology volume mount with the operator's workspace list from config:

```go
mounts := []forgectl.Mount{
    {Type: forgectl.MountVolume, Source: vol, Target: "/eidos"},
}
for _, w := range cfg.Forge[name].Workspaces {
    mounts = append(mounts, forgectl.Mount{
        Type:     forgectl.MountBind,
        Source:   w.HostPath,
        Target:   "/workspace/" + w.Name,
        ReadOnly: w.Mode == "ro",
    })
}
if err := c.ContainerCreate(ctx, forgectl.CreateOpts{
    Name:   cont,
    Image:  image,
    Mounts: mounts,
}); err != nil { ... }
```

### §6.3 `forge restart` — recreate, not docker-restart

Today `eidos forge restart` is a `docker restart` (signals the container, container stops, container starts with the **same** mount list it was created with). To pick up new workspaces we must instead **recreate** the container:

1. `docker stop <container>` (grace period, then SIGKILL — same as today's stop).
2. **Read the current container's `Config.Image`** via `docker inspect`. This is the image that `create` (step 4) will use.
3. `docker rm <container>` (the container is gone; the **`/eidos` named volume is untouched** because the rm only deletes the container, not its volumes).
4. `docker create` with the image from step 2 and the latest desired mount list (assembled per §6.2).
5. `docker start`.

**Image preservation is load-bearing.** Step 2 reads the *current* container's image rather than re-resolving `internal/forge.DefaultImage()`. Otherwise a `forge restart alice` issued any time after `forge upgrade alice --image v0.11.3` would silently roll alice back to whatever `DefaultImage()` currently resolves to — a quiet downgrade no operator asked for. The principle: `forge restart` changes mounts; it never changes image. Image changes go through `forge upgrade`.

The `/eidos` ontology, gate state, claudeauth, transcripts, dream-state, inbox — all live in the named volume and survive. The only thing lost is the in-flight Claude session, which is normal session-reset behaviour (sessions are bounded; wakes are idempotent).

`forge start` (stopped → running) and `forge stop` (running → stopped) are unchanged. Only `forge restart` swaps from "signal" semantics to "recreate" semantics.

### §6.4 `forge status` enhancement

`eidos forge status <name>` adds a line when desired ≠ actual:

```
mindform: alice
state:    running
uptime:   3h12m
heartbeat: 1m
workspaces (desired):  proj-x (rw), photos (ro)
workspaces (actual):   proj-x (rw)
⚠ pending restart: workspaces changed — run 'eidos forge restart alice' to apply
```

The diagnostic is purely informational; no auto-restart.

## §7 — UID handling

Mind-form container runs as `eidos` (uid 1000). For `mode=rw` workspaces, the mind-form needs read+write on the host directory. If the host directory's owner uid ≠ 1000, writes will fail with `EACCES`.

`forge.workspace.add` handler stats `host_path` before writing config:

- `mode=ro` → no uid check (read permission usually requires only world-readable bits, common enough not to warn).
- `mode=rw` and `stat(host_path).uid == 1000` → silent.
- `mode=rw` and `stat(host_path).uid != 1000` → write config (do not block), include in the response:

  ```
  warning: host_path /home/op/code/project-x is owned by uid=501, but the
  mind-form container runs as uid=1000. Writes from the mind-form may fail
  with EACCES. Run 'chown -R 1000 /home/op/code/project-x' (or change uid
  with --uid in a future release) to align. Pass --no-warn-uid to suppress.
  ```

The CLI renders `warnings` as yellow `stderr` lines; the dashboard renders them as toasts; programmatic callers see them in the IPC response.

`--no-warn-uid` on the CLI sets a flag on the request that suppresses the warning. It does **not** disable the stat call; it only silences the response side. (Operators who deliberately keep external uids — e.g. `~/Pictures` owned by their host user — can silence the noise.)

## §8 — Validation and warnings

`forge.workspace.add` rejects (hard error, no write):

- `name` doesn't match `^[a-z0-9][a-z0-9-]*$`.
- `name` already exists in this mind-form's workspaces.
- `host_path` is not absolute or `filepath.Clean(host_path) != host_path` (catches `..`, trailing slashes, double slashes).
- `host_path` does not exist or is not a directory at the time of the call.
- `mode` is neither `"ro"` nor `"rw"`.

`forge.workspace.add` warns (writes config, returns success with `warnings`):

- `host_path` is one of the "dangerous path" heuristics, with a path-specific message:
  - `/`, `/proc`, `/sys`, `/dev` → "mounting a system directory; the mind-form will see (and may modify) host kernel state".
  - `/var/run/docker.sock`, anywhere matching `*/docker.sock` → "mounting the docker socket gives the mind-form full control over the host's docker daemon, including breaking out of its own container".
  - `~/.ssh`, `*/.ssh` → "mounting an SSH key directory exposes private keys".
  - `~/.config/eidos`, `~/.config/eidos/*` → "mounting eidos host config; the mind-form will be able to modify other mind-forms' configurations".
  - `/etc` → "mounting host system configuration".
- UID mismatch per §7.

Warnings are **non-blocking**. The user explicitly asked for "allow mounting any directory; warn on dangerous". Warnings are advisory — the operator may legitimately need to mount any of these in unusual setups (e.g. a security-research mind-form that *should* see `/proc`).

## §9 — Mind-form self-awareness

**Discovery is filesystem-based.** Mind-form runs `ls /workspace/` to see what is mounted. There is no in-volume sync of workspace metadata.

**Mode discovery is by trial.** If a write returns `EROFS` ("Read-only file system"), the mind-form should treat the workspace as ro and not retry.

**System prompt addendum.** Add the following section to `internal/prompts/assets/system1-instructions.txt`, placed after the ontology introduction and before the tool catalogue:

> ### Shared workspaces
>
> Beyond your ontology at `/eidos/`, you may have shared workspaces mounted under `/workspace/<name>/`. These are host directories bound into your container, intended for collaboration (e.g. a code repository you and the operator both edit, or a reference directory shared with another mind-form).
>
> - A workspace may be read-only — if a write fails with `EROFS` / "Read-only file system", treat it as ro and do not retry.
> - Files in `/workspace/<name>/` may change concurrently (operator edits, other mind-forms write). Re-read before relying on their contents.
> - You cannot add or remove workspaces yourself.

The prompt does **not** enumerate the workspaces. Enumeration is left to the mind-form's own `ls /workspace/` because the list can change between wakes and we do not want the system prompt to lie when the operator edits the list after the last session was built.

## §10 — Implementation surface (file map)

New:

- `cmd/eidos/forge/workspace.go` — `eidos forge workspace add/remove/list` CLI bindings.
- `internal/daemon/forge_workspace.go` — IPC method handlers: `forge.workspace.add`, `forge.workspace.remove`, `forge.workspace.list`.
- `internal/forgectl/workspace_test.go` — table tests for `Mount` translation.
- Tests sprinkled across the touched packages (see §11).

Modified:

- `internal/config/config.go` — add `Workspaces []WorkspaceMount` to `ForgeMindForm` (or equivalent per-mindform config struct).
- `internal/config/keys.go` — register `forge.*.workspaces` as HostCtx-only.
- `internal/forgectl/docker.go` — `CreateOpts.Mount → Mounts []Mount`; new `MountType` / `Mount` shape; `ContainerCreate` translates.
- `cmd/eidos/forge/orchestrate.go` — assemble mount list from config in `create-container` step.
- `cmd/eidos/forge/restart.go` (or wherever `forge restart` is wired) — switch from `docker restart` to stop → rm → create → start.
- `cmd/eidos/forge/status.go` — print `pending_restart` diagnostic.
- `internal/prompts/assets/system1-instructions.txt` — workspaces section per §9.
- `docs/USAGE.md` — new "Workspaces" subsection.
- `docs/specs/SPEC.md` — patch the paragraph at `:137` to note that `/workspace/` exists outside the ontology boundary.
- `docker/mindform/AGENTS.md` — one-line note that `/workspace/` is the conventional bind-mount target.

## §11 — Testing

**Unit:**

- `internal/config/keys_test.go`:
  - `forge.<name>.workspaces` is registered as HostCtx-only.
  - Container-context `state.set` of this key returns `CONTEXT_MISMATCH`.
- `internal/config/config_test.go`:
  - Round-trips `[[forge.alice.workspaces]]` entries through load/save.
  - Defaults `mode` to `"rw"` when omitted.
- `internal/forgectl/docker_test.go`:
  - `CreateOpts.Mounts` with one volume + two binds (one ro, one rw) translates to the expected Docker SDK `mount.Mount` slice.
- `internal/daemon/forge_workspace_test.go`:
  - `forge.workspace.add` validates name regex, name uniqueness, absolute/clean host_path, host_path existence + directory check, mode enum.
  - Dangerous-path warning is emitted (and config is still written).
  - UID mismatch warning is emitted for rw, suppressed for ro, suppressed under `no_warn_uid`.
  - `forge.workspace.list` returns `pending_restart=true` when desired ≠ actual.
- `cmd/eidos/forge/orchestrate_test.go`:
  - `create-container` step assembles mounts from config (using a fake `forgectl.Client`).

**Integration** (`-tags=integration`, requires Docker):

- Create mind-form, add an rw workspace, confirm `pending_restart=true` and `actual` mounts unchanged.
- `forge restart`, confirm `/workspace/<name>/` is writable inside the container and writes appear on the host.
- Remove workspace, restart, confirm the mount is gone and the container's `/workspace/` directory is empty.
- Add the same `host_path` to two mind-forms; after both restart, write in mind-form A, read in mind-form B — confirm the read sees A's write (shared-substrate scenario).
- Add an `mode=ro` workspace, restart, confirm container write returns EROFS.

## §12 — Rejected alternatives (kept here for the next person who asks)

- **P1 — bind-mount propagation (`rshared`) + host `mount --bind`.** Truly dynamic (no container restart). Rejected because: Linux-only (Docker Desktop on macOS/Windows uses virtiofs/grpcfuse which does not propagate mounts across the host↔VM boundary); requires `CAP_SYS_ADMIN` on the host; rootless Docker complicates further; failure modes are opaque to operators.
- **P2 — FUSE bridge.** Cross-platform in theory; in practice a separate daemon, a separate protocol, a separate failure surface. KISS rejects it.
- **P4 — bind mounts pinned at `forge create`, no later changes.** Operationally unacceptable — D requires being able to add workspaces to an existing mind-form without destroying its ontology.
- **Symlink-based bridge directory.** The original user suggestion: maintain a single host directory of symlinks pointing to real host paths, bind-mount that bridge into the container, and "add a workspace" reduces to "add a symlink" with no container restart. Rejected because symlinks inside a bind-mounted directory are resolved against the **container's** filesystem namespace, not the host's: a symlink target like `/home/op/code/project1` is looked up under the container's `/`, which does not have that path, so the link is broken. The trick only works if the symlink target also resolves inside the container — which requires bind-mounting the target too, defeating the dynamism.
- **First-class workspace registry (`eidos workspace create/join`).** Considered for the multi-mind-form shared-substrate case. Rejected because the registry's only payoff is `workspace list` showing membership graphs; the cost is a new top-level concept and CLI surface. KISS prevails; revisit if N>5 mind-forms commonly share workspaces.
- **In-volume workspace metadata sync (`/eidos/gate/workspaces.toml` mirrored from host).** Considered for mind-form self-awareness. Rejected in favour of filesystem discovery (`ls /workspace/`) plus the system-prompt addendum — the mind-form already needs to read the directory anyway, and the sync would just be a denormalised copy that could drift.
- **Hard-block dangerous host paths.** Replaced with warnings per operator request — there are legitimate uses (security-research mind-forms inspecting `/proc`, debugging mind-forms with access to `/etc`) that a hard block would lock out.
