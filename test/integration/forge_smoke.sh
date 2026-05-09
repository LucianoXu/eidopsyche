#!/usr/bin/env bash
set -euo pipefail

# Two-instance smoke for MindForge v0.
#
# What this exercises:
#   1. eidos forge create alice (image pull + volume + ontology + key)
#   2. operator runs eidos forge login alice (interactive OAuth)
#   3. eidos forge start alice (container running)
#   4. eidos forge status alice
#   5. operator sends a message to alice's npub via their human gate
#   6. wait 30s; check operator's inbox for a reply from alice
#   7. eidos forge stop alice
#
# Prerequisites:
#   - docker daemon running, current user can talk to it
#   - `eidos` on $PATH (host install or `make build && PATH=$PWD/bin:$PATH`)
#   - host operator's gate already initialized (eidos gate init)
#   - one reachable relay; defaults to a public one
#
# Usage:
#   test/integration/forge_smoke.sh
#
# Note: this script exits non-zero on any failure (set -e). The
# operator's gate must be running and accepting messages.

RELAY="${RELAY:-wss://relay.damus.io}"

# Pull the operator's npub from `eidos gate whoami`. Output format:
#   Npub:  npub1...
OWNER_NPUB="$(eidos gate whoami | awk '/^Npub:/{print $2; exit}')"
if [ -z "$OWNER_NPUB" ]; then
  echo "could not determine operator npub from \`eidos gate whoami\`" >&2
  exit 1
fi

echo "==> creating mind-form alice (owner=$OWNER_NPUB, relay=$RELAY)"
eidos forge create alice \
  --owner "$OWNER_NPUB" \
  --relay "$RELAY" \
  --no-login

echo "==> logging in claude inside alice (interactive)"
eidos forge login alice

echo "==> starting alice"
eidos forge start alice

echo "==> status:"
eidos forge status alice

# Pull the mind-form's npub from its in-container whoami. Output format:
#   Npub:  npub1...
MIND_NPUB="$(eidos forge exec alice -- eidos gate whoami --state-dir /eidos/gate \
  | awk '/^Npub:/{print $2; exit}')"
if [ -z "$MIND_NPUB" ]; then
  echo "could not determine mind-form npub" >&2
  eidos forge stop alice
  exit 1
fi
echo "    mind-form npub: $MIND_NPUB"

echo "==> sending greeting from operator gate to alice"
eidos gate send "$MIND_NPUB" "are you awake?"

echo "==> waiting 30s for reply..."
sleep 30

echo "==> operator inbox (most recent):"
eidos gate inbox -n 1

echo "==> stopping alice"
eidos forge stop alice

echo "smoke ok"
