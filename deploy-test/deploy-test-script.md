# Deployment Test Script

Cross-machine end-to-end test for an Eidopsyche release. Replaces the
informal "by hand" runbook with a maintained, replayable Playwright
harness that grows alongside each phase of the dashboard rollout.

The current machine is `selene`; the peer is `mbp`. Machine details
(SSH config, hostnames, codenames) live under `~/cns/`.

Use the label `YingteSelene` for `selene`, and `YingteXu` for `mbp`.

## Recipe

1. **Install the latest Eidopsyche build on both machines.**
   - `eidos self-update` (or run `install.sh` directly).
2. **Purge state on both machines.**
   - `eidos gate purge --yes` on each side.
3. **Initialise both.**
   - `selene`: `eidos gate init --label YingteSelene --home ws://yingte.io:22895 --with-local-relay --listen 0.0.0.0:22895`
   - `mbp`:    `eidos gate init --label YingteXu     --home ws://yingte.io:22895`
4. **Set selene's relay to public mode** (so mbp can publish to it):
   - on selene: `eidos gate config set relay.mode public`
5. **Start services on both.**
   - `eidos gate start` on each side.
6. **Establish trust** via invite:
   - on selene: `eidos gate invite create --redeemer-label YingteXu` (capture the printed URI)
   - on mbp:    `eidos gate redeem '<URI>'`
7. **Drive the cross-machine Playwright test.**
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
     The script asserts both sides see each other in their sidebar, then drives a webui send in each direction and verifies the inbound bubble appears on the recipient.
8. **Phase-specific surface check.**
   - `node deploy-test/playwright/phaseN-<topic>.mjs --base http://127.0.0.1:22893` for the phase under test.
   - Phase 1 (Identity + Settings shell + Config): `phase1-identity-config.mjs`.
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
step 7.

## Environment variables

`deploy-test/playwright/README.md` documents the env-var contract for
the runner (`BASE_URL`, `SELENE_URL`, `MBP_URL`, `SSH_KEY`, `MBP_HOST`,
`MBP_USER`) and the one-time Chromium-for-Testing install steps.
