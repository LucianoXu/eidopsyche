# Playwright deploy-test scripts

Browser-driven smoke tests for the `eidos gate` dashboard webui. Each
script targets a specific phase of the dashboard feature-parity work
(see `docs/superpowers/specs/2026-05-08-dashboard-feature-parity-design.md`
§8.5) plus a cross-machine `full-bidirectional.mjs` regression script
that grows alongside the dashboard surface.

The scripts are plain ESM, run directly with `node`, and depend only
on the `playwright` npm package (no test runner). Errors print a step
number plus a one-line reason; success exits 0. CI greps for
`STEP n FAIL` to surface failures.

## What's here today

| Script | Purpose |
|---|---|
| `_lib/chromium.mjs` | Chromium launcher; pinned binary path; `--no-sandbox` |
| `_lib/dashboard.mjs` | Page-object helpers (`gotoSettings`, `setLabel`, `setConfigValue`, `sendInThread`, `waitForInbound`, `listContactsRows`, `addContactByCard`, `scanCardPreview`, `openContactDetail`, `setContactLabelInDetail`, `setContactTierInDetail`, `removeContactWithConfirm`, …) |
| `_lib/daemon.mjs` | Spawns a transient `eidos gate daemon` in a temp state-dir on ephemeral loopback ports; used to mint a real card URI for Phase 2 admit-correspondent flows |
| `phase1-identity-config.mjs` | Phase 1 smoke: Settings shell + Identity tab + Config tab round-trips |
| `phase2-contacts.mjs` | Phase 2 smoke: empty-state → scan-preview → admit → detail pane → rename → tier cycle → typed-confirm remove → empty-state |
| `full-bidirectional.mjs` | Selene + mbp regression: identity cards on both, mutual contact in sidebar AND Contacts tab, chat round-trip |

## One-time setup

The scripts use Chromium-for-Testing as installed by Playwright, not
the host browser. Install it once per machine:

```bash
# Pulls Chromium into ~/.cache/ms-playwright/chromium-1217/...
npx -y playwright install chromium --no-shell
```

If Chromium fails to launch with a `libnspr4.so` / `libatk-1.0.so` /
`libgbm.so` error, you also need the Chromium system dependencies:

```bash
# One-time, distro-specific. apt-based hosts:
sudo npx playwright install-deps chromium
```

`install-deps` is interactive and uses sudo; CI hosts should bake the
deps into the image instead. The install location used by the scripts is

```
~/.cache/ms-playwright/chromium-1217/chrome-linux64/chrome
```

If your Playwright version drops a different chromium-N directory, update
`CHROMIUM_BIN` in `_lib/chromium.mjs`. The script also auto-discovers
the `playwright/index.mjs` entry point under `~/.npm/_npx/*/node_modules/`,
so the typical `npx playwright install chromium` flow leaves both pieces
in place.

## Environment variables

The scripts honour these env vars (CLI flags override them):

| Env | Default | Used by |
|---|---|---|
| `BASE_URL` | `http://127.0.0.1:22893` | `phase1-identity-config.mjs`, `phase2-contacts.mjs` (when no `--base`) |
| `SELENE_URL` | `http://127.0.0.1:22893` | `full-bidirectional.mjs` (when no `--selene`) |
| `MBP_URL` | `http://127.0.0.1:22894` | `full-bidirectional.mjs` (when no `--mbp`) |
| `EIDOS_BIN` | `eidos` (PATH-resolved) | `phase2-contacts.mjs` (which spawns a transient daemon to mint a card) |
| `SSH_KEY` | — | SSH-tunnel recipe below (not read by the scripts) |
| `MBP_HOST` | — | SSH-tunnel recipe below |
| `MBP_USER` | — | SSH-tunnel recipe below |

`phase1-identity-config.mjs` currently parses `--base` only; export
`BASE_URL` for ergonomic shell usage and pass `--base "$BASE_URL"`.

`phase2-contacts.mjs` honours `BASE_URL` directly when `--base` is
omitted. It additionally needs the `eidos` binary on PATH (or pointed at
via `EIDOS_BIN`) to spawn a second, transient daemon in a temp state-dir
— that daemon's sole purpose is producing a real `mindgate://…` card URI
to scan and admit through the dashboard. The transient daemon is bound
to ephemeral loopback ports and torn down on script exit.

## SSH tunnel: reaching mbp's loopback dashboard from selene

mbp's dashboard binds to `127.0.0.1:22893` (loopback only — that's the
trust boundary). To drive it from selene, open an SSH tunnel that maps
mbp's `:22893` to a free local port (`:22894` here):

```bash
ssh -i "$SSH_KEY" \
    -L 22894:127.0.0.1:22893 \
    -fN \
    "$MBP_USER@$MBP_HOST"

# Then:
export SELENE_URL="http://127.0.0.1:22893"
export MBP_URL="http://127.0.0.1:22894"
```

`-fN` puts ssh into the background and disables remote command exec.
Tear it down later with `pkill -f '22894:127.0.0.1:22893'` or by
killing the matching `ssh` PID.

## Running the scripts

Phase 1 smoke (selene-local, no tunnel needed):

```bash
node deploy-test/playwright/phase1-identity-config.mjs --base http://127.0.0.1:22893
# debug with a visible browser:
node deploy-test/playwright/phase1-identity-config.mjs --base http://127.0.0.1:22893 --headed
```

Phase 2 smoke (selene-local; spawns a transient peer-daemon for a real card URI):

```bash
# `eidos` must be on PATH or EIDOS_BIN must point at the binary
node deploy-test/playwright/phase2-contacts.mjs --base http://127.0.0.1:22893
EIDOS_BIN=$(go env GOPATH)/bin/eidos node deploy-test/playwright/phase2-contacts.mjs --base http://127.0.0.1:22893 --headed
```

Cross-machine bidirectional (selene + mbp via tunnel):

```bash
node deploy-test/playwright/full-bidirectional.mjs \
     --selene "$SELENE_URL" \
     --mbp    "$MBP_URL"
```

Both scripts exit 0 on success and non-zero on the first assertion
failure. They print a numbered step trace (`STEP 1 OK: …` / `STEP 4
FAIL: …`) so a CI log is human-skimmable.

## Pre-conditions for `full-bidirectional.mjs`

Phase 1 deliberately does NOT drive the invite flow over the webui
(that lands in Phase 3). Before running the bidirectional script, run
on the CLI per `deploy-test/deploy-test-script.md`:

1. Install latest `eidos` on both selene and mbp.
2. `eidos gate purge && eidos gate init --label …` on both (label
   `YingteSelene` for selene, `YingteXu` for mbp per the runbook).
3. `eidos gate start` on both.
4. `eidos gate invite create --redeemer <peer-label>` on one side;
   capture the invite URI.
5. `eidos gate redeem <uri>` on the other side.

The script asserts each side has the other as exactly one contact;
if not, it prints the same recipe and exits non-zero.
