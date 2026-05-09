# syntax=docker/dockerfile:1.7

# ---- Stage 1: build eidos binary ----
FROM golang:1.25-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.work go.work.sum ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/eidos ./cmd/eidos

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
RUN npm install -g @anthropic-ai/claude-code@latest

# ---- Stage 4: final image ----
FROM alpine:3.19
RUN apk add --no-cache busybox-extras ca-certificates git tini bash nodejs
COPY --from=build /out/eidos /usr/local/bin/eidos
COPY --from=repobundle /opt/eidopsyche-bundle /opt/eidopsyche-bundle.git
COPY --from=claude /usr/local/lib/node_modules /usr/local/lib/node_modules
COPY --from=claude /usr/local/bin/claude /usr/local/bin/claude
COPY docker/mindform/entrypoint.sh /usr/local/bin/entrypoint.sh
COPY docker/mindform/crontab /etc/crontabs/root
RUN chmod +x /usr/local/bin/entrypoint.sh

ENV EIDOS_IN_CONTAINER=1
ENV EIDOS_GATE_HOME=/eidos/gate
ENV HOME=/eidos/claude

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/usr/local/bin/entrypoint.sh"]
