#!/usr/bin/env bash
#
# Deployment test for the heartbeat-plans-dreams PR.
#
# What this script does (in order):
#   1. Builds eidos from the current worktree.
#   2. Stands up a fully isolated host gate + relay using --state-dir.
#   3. Creates a disposable mindform "dt-alice" with --no-login.
#   4. Patches its config.toml inside the volume to enable a 1m
#      heartbeat, all-day quiet hours, and a 30s dream cooldown so we
#      can exercise eligibility quickly.
#   5. Starts the container.
#   6. Submits a plan via `forge plan add --in 90s` and waits for it
#      to fire (verified via plans/fired/).
#   7. Drives `forge dream begin/end` and asserts dream-state.json
#      reflects it.
#   8. Forces a manual wake and asserts the wake context surfaces the
#      new dream fields.
#   9. Stops + purges the disposable mindform.
#
# Isolation properties:
#   - Uses --state-dir under deploy-test/dt-forge-host so the user's
#     ~/.config/eidos and running daemon are untouched.
#   - Listens on 127.0.0.1:22897 (separate from the user's typical
#     22895/22896 ports used by alice/bob smoke tests).
#   - Uses a unique mindform name "dt-alice" to avoid colliding with
#     any production mindform.
#
# Prereqs: docker daemon running, Go toolchain, no other process
# bound to 127.0.0.1:22897.
#
# Cleanup: trap on EXIT plus an explicit `forge purge --yes`.

set -euo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
DT="$REPO/deploy-test/dt-forge-host"
NAME=dt-alice
RELAY=ws://127.0.0.1:22897

cd "$REPO"

echo "==> Building eidos"
go build -o "$REPO/bin/eidos" ./cmd/eidos
export PATH="$REPO/bin:$PATH"

echo "==> Cleaning previous run"
rm -rf "$DT"
docker rm -f "eidos-mindform-$NAME" 2>/dev/null || true
docker volume rm "eidos-mindform-$NAME" 2>/dev/null || true

echo "==> Starting isolated host gate + relay"
eidos gate --state-dir "$DT" init \
  --label dt-host \
  --home "$RELAY" \
  --with-local-relay \
  --listen 127.0.0.1:22897
eidos gate --state-dir "$DT" relay  &
RELPID=$!
eidos gate --state-dir "$DT" daemon &
DMNPID=$!
trap 'kill $RELPID $DMNPID 2>/dev/null || true; rm -rf "$DT"' EXIT

# Give the daemon a moment to come up.
sleep 2
HOST_NPUB=$(eidos gate --state-dir "$DT" whoami | awk '/Npub:/{print $2}')
echo "Host npub: $HOST_NPUB"

echo "==> Creating disposable mindform $NAME (--no-login)"
eidos forge --state-dir "$DT" create "$NAME" \
  --owner "$HOST_NPUB" \
  --relay "$RELAY" \
  --no-login

echo "==> Patching mindform config.toml for fast iteration"
docker run --rm -v "eidos-mindform-$NAME:/eidos" alpine sh -c '
  cat >> /eidos/gate/config.toml <<EOF

[heartbeat]
interval = "1m"

[mindform]
quiet_start = "00:00"
quiet_end = "23:59"
tz = "UTC"
dream_min_interval = "30s"
EOF
'

echo "==> Starting mindform"
eidos forge --state-dir "$DT" start "$NAME"
sleep 5

echo "==> Submitting plan (--in 90s)"
docker exec "eidos-mindform-$NAME" eidos forge plan add --in 90s --hint "deploy-test"

echo "==> Listing plans (sanity check)"
docker exec "eidos-mindform-$NAME" eidos forge plan list

echo "==> Waiting 100s for plan to fire"
sleep 100

echo "==> Asserting plan moved to fired/"
FIRED=$(docker exec "eidos-mindform-$NAME" sh -c '
  ls /eidos/run/plans/fired/ 2>/dev/null | wc -l
' | tr -d '[:space:]')
if [ "$FIRED" = "0" ]; then
  echo "FAIL: no plan in /eidos/run/plans/fired/"
  exit 1
fi
echo "OK: $FIRED plan(s) in fired/"

echo "==> Driving dream begin/end"
docker exec "eidos-mindform-$NAME" eidos forge dream begin --note "deploy-test-intent"
docker exec "eidos-mindform-$NAME" eidos forge dream end \
  --note "consolidated dt"

echo "==> Asserting dream-state.json"
docker exec "eidos-mindform-$NAME" sh -c '
  grep -q "\"dream_count\": 1" /eidos/run/dream-state.json
' || { echo "FAIL: dream_count != 1"; exit 1; }
docker exec "eidos-mindform-$NAME" sh -c '
  grep -q "consolidated dt" /eidos/run/dream-state.json
' || { echo "FAIL: dream note missing"; exit 1; }
echo "OK: dream-state.json correct"

echo "==> Forcing manual wake and inspecting context"
docker exec "eidos-mindform-$NAME" eidos forge wake --reason manual --hint "dt-final"
sleep 3
WAKE_CTX=$(docker exec "eidos-mindform-$NAME" sh -c '
  for f in /eidos/run/wake/active.json /eidos/run/wake/pending.json; do
    [ -f "$f" ] || continue
    cat "$f"
    echo "---"
  done
')
echo "$WAKE_CTX" | grep -q "since_last_dream_seconds" \
  || { echo "FAIL: wake context lacks since_last_dream_seconds"; echo "$WAKE_CTX"; exit 1; }
echo "OK: wake context surfaces dream fields"

echo "==> Status surface check"
eidos forge --state-dir "$DT" status "$NAME"

echo "==> Cleaning up disposable mindform"
eidos forge --state-dir "$DT" stop "$NAME"
eidos forge --state-dir "$DT" purge "$NAME" --yes

echo "==> ALL CHECKS PASSED"
