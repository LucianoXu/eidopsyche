# Agent notes for `docker/mindform/`

Mind-form runtime container image. Built and pushed manually today —
automated GoReleaser path is deferred until the registry release
pipeline lands.

## Build / push

```bash
make image                      # IMAGE_TAG defaults to "dev"
make image       IMAGE_TAG=v0.x.y
make image-push  IMAGE_TAG=v0.x.y
```

Registry: `ghcr.io/lucianoxu/eidopsyche-mindform`.

## Final image is Alpine, not distroless

`FROM alpine:3.19`. The container needs a real userland — busybox `crond`,
`tini`, `bash`, `sudo`, `git`, `nodejs` (Claude Code is a Node CLI), and a
non-root `eidos:1000` with NOPASSWD sudo. A distroless final image cannot
host this surface; don't try to switch to one.

## Layer-cache invariant

Multi-stage layout: `build` (golang-alpine → `eidos`) → `repobundle` (bare
git repo at `/opt/eidopsyche-bundle`, no `.git` suffix because BuildKit
treats `COPY --from=` source paths ending in `.git` as remote refs) →
`claude` (node-alpine → `claude-code` pinned via `ARG CLAUDE_CODE_VERSION`)
→ final (`alpine:3.19`).

COPYs in the final stage are ordered **least- to most-volatile**. The
`COPY --from=build /out/eidos` MUST stay last — otherwise every routine
code rebuild invalidates the upper layers (apk install, adduser, claude
COPY). When adding a new COPY: place it *before* the `eidos` line only
if it changes less often than the binary.

## crond silent-ignore (heartbeat gotcha)

Busybox `crond` **silently ignores** crontab files that aren't owned by
root mode 0600 — no log line, no error, jobs just don't fire. If
heartbeat isn't firing inside a mind-form, check
`/var/spool/cron/crontabs/eidos` perms first.

The supervisor renders that file at runtime from `[heartbeat] interval`
config (see `cmd/eidos/supervisor/crontab.go`); the Dockerfile pre-creates
the spool with the right perms and removes the stray default
`/etc/crontabs/root`. Supervisor runs `crond` via `sudo` so it can
`setuid` into `eidos` for each job.

## Filesystem layout

The container's `/eidos` volume holds the mind-form's ontology (persistent state,
memory, configuration). Operators may additionally bind host directories under
`/workspace/<name>/` via `eidos forge workspace add` (see `docs/USAGE.md`);
these are orthogonal to the ontology and survive across `eidos forge restart`.

## Runtime entrypoint

`/sbin/tini -- entrypoint.sh` → `eidos supervisor run`. The supervisor
starts the gate daemon, the wake loop, and `crond`.

Env baked into the image (read by host/container-aware code paths):

- `EIDOS_IN_CONTAINER=1`
- `EIDOS_GATE_HOME=/eidos/gate`
- `HOME=/eidos/claude` — so `claude-code` writes its state into the
  mind-form's volume, not into root's home.

## Pinned upstream versions

- `CLAUDE_CODE_VERSION` — top of Dockerfile. Bump manually; verify the
  CLI surface (`claude -p ...` etc.) is still compatible afterwards.
- Base images (`golang:1.25-alpine`, `node:20-alpine`, `alpine:3.19`) —
  bump in lockstep when upgrading Go or Node.
