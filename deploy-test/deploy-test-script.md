# Deployment Test Script

Cross-machine end-to-end test for an Eidopsyche release. Replaces the
informal "by hand" runbook with a maintained, replayable Playwright +
CLI harness that grows alongside each phase of the dashboard rollout.

The driving machine is `selene`; the peers are `mbp` (macOS laptop) and
`msi` (Windows 11 desktop). Machine details (SSH config, hostnames,
codenames) live under `~/cns/`.

| Machine | OS | Codename label | Role |
|---|---|---|---|
| selene | linux  | `YingteSelene` | hub: runs the public embedded relay (`yingte.io:22895`) |
| mbp    | darwin | `YingteXu`     | leaf: dials selene's relay as home |
| msi    | windows/amd64 | `YingteMsi` | leaf: dials selene's relay as home |

## Recipe

1. **Install the latest Eidopsyche release on all three machines.**
   - selene + mbp: `curl -fsSL https://raw.githubusercontent.com/LucianoXu/eidopsyche/main/install.sh | sh`
     (or `eidos self-update` if a previous install is already present).
   - msi (PowerShell — `install.sh` is POSIX-shell only):
     ```powershell
     $ver = (Invoke-RestMethod https://api.github.com/repos/LucianoXu/eidopsyche/releases/latest).tag_name
     $num = $ver.TrimStart('v')
     $url = "https://github.com/LucianoXu/eidopsyche/releases/download/$ver/eidos_${num}_windows_amd64.zip"
     $zip = Join-Path $env:TEMP "eidos.zip"
     $dst = Join-Path $env:USERPROFILE ".local\bin"
     New-Item -ItemType Directory -Force -Path $dst | Out-Null
     Invoke-WebRequest $url -OutFile $zip
     Expand-Archive -Path $zip -DestinationPath $dst -Force
     # Make sure $dst is on $PATH (User scope) — once per machine:
     $u = [Environment]::GetEnvironmentVariable('Path','User')
     if ($u -notlike "*$dst*") { [Environment]::SetEnvironmentVariable('Path',"$u;$dst",'User') }
     ```
2. **Purge state on all three machines.**
   - All platforms: `eidos gate purge --yes`. The Windows backend uses
     SCM (Service Control Manager) for `start / stop / status / purge`,
     same surface as systemd / launchd; it must be invoked from an
     elevated PowerShell (Administrator) because SCM mutations require
     Administrator.
3. **Initialise all three.**
   - `selene`: `eidos gate init --label YingteSelene --home ws://yingte.io:22895 --with-local-relay --listen 0.0.0.0:22895`
   - `mbp`:    `eidos gate init --label YingteXu     --home ws://yingte.io:22895`
   - `msi`:    `eidos.exe gate init --label YingteMsi --home ws://yingte.io:22895`
4. **Set selene's relay to public mode** (so mbp + msi can publish to it):
   - on selene: `eidos gate config set relay.mode public`
5. **Start services on all three.**
   - `eidos gate start` on each side. On Linux this loads systemd user
     units; on macOS it bootstraps launchd LaunchAgents; on Windows it
     creates and starts SCM services running as LocalSystem.
6. **Establish trust** via invite. Two ways:
   - **CLI (works on every platform):** for each pair `(A, B)` in
     `{selene↔mbp, selene↔msi, mbp↔msi}` — on issuer
     `eidos gate invite create --redeemer-label <peer>`; on redeemer
     `eidos gate redeem '<URI>'`.
   - **Webui (Phase 3, recommended for selene↔mbp):** skip the CLI for
     that pair. `full-bidirectional.mjs` auto-detects whether the two
     sides are already mutual contacts and, if not, drives invite
     create on selene's webui + redeem on mbp's webui through the SSH
     tunnel — see step 7. Idempotent on re-run. There is no Windows
     webui SSH-tunnel recipe in the script yet, so use the CLI for the
     two msi-involved pairs.
7. **Drive the cross-machine Playwright test (selene ↔ mbp).**
   - From selene, open an SSH tunnel to mbp's loopback dashboard:
     ```
     ssh -i $SSH_KEY -L 22894:127.0.0.1:22893 -fN $MBP_USER@$MBP_HOST
     ```
   - Then run the canonical regression script:
     ```
     node deploy-test/playwright/full-bidirectional.mjs \
       --selene http://127.0.0.1:22893 \
       --mbp    http://127.0.0.1:22894
     ```
     The script (a) provisions the invite/redeem pair via webui if they aren't already mutual contacts, (b) asserts both sides see each other in their sidebar AND Contacts tab, (c) drives a webui send in each direction and verifies the inbound bubble appears on the recipient.
8. **CLI cross-machine matrix (covers msi until a 3-side webui driver lands).**
   For each ordered pair `(A, B)` in `{selene↔mbp, selene↔msi, mbp↔msi}`:
   - `eidos gate send <B-pubkey> "hello from <A> to <B>"` on A.
   - `eidos gate inbox` on B — assert the just-sent line appears.
   - Repeat with A and B swapped.
   `eidos gate contacts` on each machine prints the pubkeys; pipe through
   `awk` / `grep` to script the assertions. The intended invariant is six
   green sends across three pairs; any failure prints the failing pair
   and direction.
9. **Phase-specific surface check.**
   - `node deploy-test/playwright/phaseN-<topic>.mjs --base http://127.0.0.1:22893` for the phase under test.
   - Phase 1 (Identity + Settings shell + Config): `phase1-identity-config.mjs`.
   - Phase 2 (Contacts editor): `phase2-contacts.mjs` — needs `eidos` on PATH (or `EIDOS_BIN` env var) so it can spawn a transient peer-daemon for a real card URI to scan.
   - Phase 3 (Invites): `phase3-invites.mjs` — local-only smoke; the cross-machine redeem path is exercised by `full-bidirectional.mjs`.
   - Phase 4 (Relays): `phase4-relays.mjs` — local-only smoke; exercises the IsLastHome guard, fallback add/remove, and the home-relay typed-confirm modal. Idempotent: restores the seeded home before exit.
   - Phase 5 (Service control): `phase5-service.mjs` — local-only smoke; exercises the Service tab + Reconnect lifecycle stream end-to-end. Stop / Purge / Self-update are intentionally NOT clicked by the script (they'd take the daemon down or rewrite its binary); the build-tag `integration` test covers their wire-up via a fake spawner. Manual ops verification of those actions is left to the operator.
   - Each script exits 0 on success with a numbered step log; non-zero on the first failure with the failing step name.

The Playwright runner doubles as the project's regression net: every
phased PR adds or extends `phaseN.mjs` plus any new assertions to
`full-bidirectional.mjs`.

## Skill triggers

When invoked from Claude Code, this runbook is the canonical entrypoint
for a deploy test. Drive the webui via the `playwright` MCP when
available; otherwise run the `.mjs` scripts directly with `node` (they
are self-contained and re-use the locally-cached Chromium for Testing).
For the webui on the `mbp` side, test through the SSH tunnel set up in
step 7. msi participation is CLI-only today (step 8); a 3-side webui
driver is future work.

## Environment variables

`deploy-test/playwright/README.md` documents the env-var contract for
the runner (`BASE_URL`, `SELENE_URL`, `MBP_URL`, `SSH_KEY`, `MBP_HOST`,
`MBP_USER`) and the one-time Chromium-for-Testing install steps.
