#!/usr/bin/env bash
# Single-host two-persona smoke for the eidos gate CLI.
# Spins up alice and bob in separate state directories on the same host,
# wires them as mutual contacts via cards, and sends one message.
#
# Prereqs: `eidos` on $PATH; nothing else listening on 22895/22896.
# Cleanup: `eidos --state-dir $MGA gate purge --yes` (and same for $MGB)
# when you're done; or just delete the directories.

set -eu

export MGA=/data/eidopsyche/deploy-test/alice
eidos gate --state-dir "$MGA" init --label alice --home ws://127.0.0.1:22895 --with-local-relay --listen 127.0.0.1:22895
eidos gate --state-dir "$MGA" relay  &
eidos gate --state-dir "$MGA" daemon &

export MGB=/data/eidopsyche/deploy-test/bob
eidos gate --state-dir "$MGB" init --label bob --home ws://127.0.0.1:22896 --with-local-relay --listen 127.0.0.1:22896
eidos gate --state-dir "$MGB" relay  &
eidos gate --state-dir "$MGB" daemon &

# ============ Mutual add-contact via card URIs ============
ACARD=$(eidos gate --state-dir "$MGA" card)
BCARD=$(eidos gate --state-dir "$MGB" card)

eidos gate --state-dir "$MGA" add-contact "$BCARD" --label bob
eidos gate --state-dir "$MGB" add-contact "$ACARD" --label alice

# ============ Send + receive ============
eidos gate --state-dir "$MGA" send bob "hi from alice"

eidos gate --state-dir "$MGB" inbox --tail
