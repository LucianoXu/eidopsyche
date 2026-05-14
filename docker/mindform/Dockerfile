# syntax=docker/dockerfile:1.7

# ---- Pinned upstream versions ----
# Bumped manually when we want to upgrade. Pinning lets BuildKit cache
# `npm install -g` across rebuilds (vs always pulling latest). Version
# of claude-code used at the time of writing — if you change this,
# verify it still has compatible CLI surface (claude -p ... etc).
ARG CLAUDE_CODE_VERSION=2.1.138

# ---- Stage 1: build eidos binary ----
FROM golang:1.25-alpine AS build
WORKDIR /src
# Inject the host-binary version (from --build-arg, default "dev") into
# the in-container eidos binary's internal/version.Version. Keeps the
# image's binary in sync with the org.eidopsyche.eidos-version LABEL
# declared in Stage 4. Mirrors goreleaser's host build (.goreleaser.yml).
ARG EIDOS_VERSION=dev
RUN apk add --no-cache git ca-certificates
# Layered cache: dep manifest → mod download → full source → build.
# Editing source doesn't invalidate `go mod download`. Cache mounts
# share the module cache + build cache across rebuilds.
COPY go.mod go.sum go.work go.work.sum ./
RUN --mount=type=cache,target=/root/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    go mod download
COPY . .
RUN --mount=type=cache,target=/root/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 go build -trimpath \
        -ldflags="-s -w -X github.com/LucianoXu/eidopsyche/internal/version.Version=${EIDOS_VERSION}" \
        -o /out/eidos ./cmd/eidos

# ---- Stage 2: produce eidopsyche bare-repo bundle ----
# Note: the bare repo is cloned to /opt/eidopsyche-bundle (no .git suffix) here
# because BuildKit's COPY --from=<stage> treats source paths ending in ".git"
# as git remote references.  The COPY in Stage 4 renames it to the canonical
# /opt/eidopsyche-bundle.git path consumed by `eidos forge init-volume`.
FROM alpine:3.19 AS repobundle
RUN apk add --no-cache git
WORKDIR /src
COPY . /src/eidopsyche-src
# The COPY'd .git entry may be either a file (worktree pointer, when
# building locally from a git worktree) or a directory (regular clone, as
# in actions/checkout@v4).  In either case git inside the container cannot
# safely use the host's pointer or commit history, so drop it entirely
# with `rm -rf` (handles both shapes), then initialise a fresh repo from
# the source snapshot and produce the bare clone at the canonical path
# consumed by `eidos forge init-volume`.
RUN rm -rf /src/eidopsyche-src/.git \
 && cd /src/eidopsyche-src \
 && git init \
 && git config user.email "build@eidopsyche" \
 && git config user.name "Docker build" \
 && git add -A \
 && git commit -m "snapshot" \
 && git clone --bare /src/eidopsyche-src /opt/eidopsyche-bundle \
 && rm -rf /src/eidopsyche-src

# ---- Stage 3: claude-code from upstream ----
FROM node:20-alpine AS claude
ARG CLAUDE_CODE_VERSION
RUN --mount=type=cache,target=/root/.npm,sharing=locked \
    npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}

# ---- Stage 4: final image ----
FROM alpine:3.19

# busybox-extras provides crond. sudo gives the eidos user a NOPASSWD
# escalation path for explicit privilege; the container itself is the
# trust boundary.
RUN apk add --no-cache \
        busybox-extras ca-certificates git tini bash nodejs sudo

# Non-root daily-ops user. claude-code refuses to run as uid 0 with
# --dangerously-skip-permissions, so daily ops (supervisor, gate
# daemon, claude, crond) run as eidos:1000. sudo NOPASSWD lets alice
# escalate when she genuinely needs root; passwd -l locks the root
# account so the only path to root is via sudo.
RUN addgroup -g 1000 eidos \
 && adduser  -u 1000 -G eidos -h /eidos/claude -s /bin/sh -D eidos \
 && echo 'eidos ALL=(ALL) NOPASSWD: ALL' > /etc/sudoers.d/eidos \
 && chmod 0440 /etc/sudoers.d/eidos \
 && passwd -l root

# COPYs ordered least-volatile → most-volatile so an `eidos` binary
# rebuild (the common case) only invalidates the bottom two layers.
COPY --from=claude /usr/local/lib/node_modules /usr/local/lib/node_modules
COPY --from=claude /usr/local/bin/claude /usr/local/bin/claude
COPY docker/mindform/entrypoint.sh /usr/local/bin/entrypoint.sh
COPY docker/mindform/mcp.json /etc/eidos/mcp.json
# Crontab lives in the per-user spool. busybox crond -c reads each
# file as that named user's crontab and runs the job as that user
# (via setuid). The crontab file itself MUST be owned by root, mode
# 0600 — busybox crond silently ignores user-owned crontab files
# (presumably to prevent privilege escalation by tampering with the
# spool). The supervisor then runs crond as root via sudo so it can
# setuid into eidos to run the heartbeat.
#
# The crontab body is rendered at runtime by the supervisor from the
# mind-form's [heartbeat] interval config — see
# cmd/eidos/supervisor/crontab.go. We pre-create the spool dir with
# the right permissions and clear any stray default crontabs so cron
# starts clean.
RUN chmod +x /usr/local/bin/entrypoint.sh \
 && mkdir -p /var/spool/cron/crontabs \
 && chown root:root /var/spool/cron/crontabs \
 && chmod 0755 /var/spool/cron/crontabs \
 && rm -f /etc/crontabs/root

# Volatile COPYs last: every code change rebuilds these two layers but
# leaves the upper apk/adduser/claude/config layers cached.
COPY --from=repobundle /opt/eidopsyche-bundle /opt/eidopsyche-bundle.git
COPY --from=build /out/eidos /usr/local/bin/eidos

# Version metadata. CLAUDE_CODE_VERSION is the same arg used by Stage 3
# (re-declared here so Stage 4 can reference it). EIDOS_VERSION is
# passed from Makefile / release.yml so the image self-identifies the
# host binary version it shipped with.
ARG CLAUDE_CODE_VERSION
ARG EIDOS_VERSION=dev

LABEL org.eidopsyche.claude-code-version="${CLAUDE_CODE_VERSION}"
LABEL org.eidopsyche.eidos-version="${EIDOS_VERSION}"

RUN mkdir -p /etc/eidos \
 && echo "${CLAUDE_CODE_VERSION}" > /etc/eidos/claude-code.version \
 && echo "${EIDOS_VERSION}"       > /etc/eidos/eidos.version \
 && chmod 0644 /etc/eidos/*.version

ENV EIDOS_IN_CONTAINER=1
ENV EIDOS_GATE_HOME=/eidos/gate
ENV HOME=/eidos/claude

USER eidos

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/usr/local/bin/entrypoint.sh"]
