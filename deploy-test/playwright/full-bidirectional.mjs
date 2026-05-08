// full-bidirectional.mjs
//
// Cross-machine regression test for the dashboard. The surface grows
// with each phase: today this script asserts identity rendering, mutual
// contact state on both sidebars and Contacts panes, and a bidirectional
// chat round-trip — and (Phase 3) drives invite create + redeem over
// the webui when the two sides aren't already mutual contacts.
//
// Pre-conditions (NOT done by this script — run once on the CLI per
// deploy-test/deploy-test-script.md):
//   1. eidos installed on both selene + mbp.
//   2. State purged + re-init'd on both.
//   3. Daemons running on both.
//
// Phase 3 changed step 4 of the §8.5 list: invite create + redeem now
// run THROUGH the dashboard, not the CLI. The script auto-detects
// whether the pairing is already done (re-runs are idempotent) and
// only provisions the invite if needed.
//
// Selene normally talks to mbp's loopback dashboard via SSH tunnel:
//   ssh -i $SSH_KEY -L 22894:127.0.0.1:22893 -fN $MBP_USER@$MBP_HOST
//   MBP_URL=http://127.0.0.1:22894
//
// Usage:
//   node full-bidirectional.mjs --selene http://127.0.0.1:22893 \
//                               --mbp    http://127.0.0.1:22894
//   node full-bidirectional.mjs --selene ... --mbp ... --headed

import { launch, gotoStable } from './_lib/chromium.mjs';
import {
  gotoSettings,
  clickSettingsTab,
  getIdentity,
  openContactThread,
  sendInThread,
  waitForInbound,
  listSidebarContacts,
  listContactsRows,
  createInvite,
  redeemInviteOnDashboard,
} from './_lib/dashboard.mjs';

function parseArgs(argv) {
  const out = {
    selene: process.env.SELENE_URL || 'http://127.0.0.1:22893',
    mbp: process.env.MBP_URL || 'http://127.0.0.1:22894',
    headed: false,
  };
  for (let i = 2; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--selene') out.selene = argv[++i];
    else if (a === '--mbp') out.mbp = argv[++i];
    else if (a === '--headed') out.headed = true;
    else if (a === '--help' || a === '-h') {
      process.stdout.write(
        'usage: node full-bidirectional.mjs --selene URL --mbp URL [--headed]\n',
      );
      process.exit(0);
    }
  }
  return out;
}

function logStep(n, msg)   { console.log(`STEP ${n}: ${msg}`); }
function logOk(n, msg)     { console.log(`STEP ${n} OK: ${msg}`); }
function logFail(n, msg)   { console.error(`STEP ${n} FAIL: ${msg}`); }

const HINT_PROVISION_FAILED = [
  'Phase 3 drives invite create + redeem through the webui. If this',
  'failed, check that both daemons are running and reachable, that',
  'the SSH tunnel for mbp is up, and that selene can reach mbp at',
  'the redeem time (the redeemer must publish a kind:25001 wrap to',
  'the issuer\'s home relay).',
].join('\n  ');

async function main() {
  const args = parseArgs(process.argv);
  const selene = args.selene.replace(/\/$/, '');
  const mbp = args.mbp.replace(/\/$/, '');
  console.log(`full-bidirectional: selene=${selene} mbp=${mbp} headed=${args.headed}`);

  const seleneH = await launch({ headless: !args.headed });
  const mbpH = await launch({ headless: !args.headed });
  const sp = seleneH.page;
  const mp = mbpH.page;

  let step = 0;
  try {
    // ── 1. Selene identity. ────────────────────────────────────────
    step = 1;
    logStep(step, 'read selene identity from /settings/identity');
    await gotoSettings(sp, selene, 'identity');
    const seleneId = await getIdentity(sp);
    if (!seleneId || !seleneId.label || !seleneId.npub.startsWith('npub1')
        || !/^[0-9a-f]{64}$/i.test(seleneId.hex)) {
      throw new Error(`selene identity malformed: ${JSON.stringify(seleneId)}`);
    }
    logOk(step, `selene label=${seleneId.label} hex=${seleneId.hex.slice(0, 16)}…`);

    // ── 2. Mbp identity. ───────────────────────────────────────────
    step = 2;
    logStep(step, 'read mbp identity from /settings/identity (via tunnel)');
    await gotoSettings(mp, mbp, 'identity');
    const mbpId = await getIdentity(mp);
    if (!mbpId || !mbpId.label || !mbpId.npub.startsWith('npub1')
        || !/^[0-9a-f]{64}$/i.test(mbpId.hex)) {
      throw new Error(`mbp identity malformed: ${JSON.stringify(mbpId)}`);
    }
    logOk(step, `mbp label=${mbpId.label} hex=${mbpId.hex.slice(0, 16)}…`);

    // ── 3. Provision mutual contact via webui (Phase 3). ───────────
    step = 3;
    logStep(step, 'check mutual-contact state; provision via webui invite/redeem if needed');
    await gotoStable(sp, `${selene}/`);
    await gotoStable(mp, `${mbp}/`);
    let seleneContacts = await listSidebarContacts(sp);
    let mbpContacts = await listSidebarContacts(mp);
    const alreadyPaired =
      seleneContacts.some((c) => c.pubkey === mbpId.hex) &&
      mbpContacts.some((c) => c.pubkey === seleneId.hex);

    if (alreadyPaired) {
      logOk(step, 'already mutual contacts; skipping invite provisioning');
    } else {
      console.log('  not mutual contacts yet; driving invite create on selene + redeem on mbp via webui');
      await gotoSettings(sp, selene, 'invites');
      const issued = await createInvite(sp, {
        redeemerLabel: mbpId.label || 'peer',
        expires: '24h',
        maxUses: '1',
      });
      if (!issued.ok || !issued.uri) {
        throw new Error(`selene: createInvite failed: ${issued.error || '(no uri)'}\n  ${HINT_PROVISION_FAILED}`);
      }
      console.log(`  selene issued invite id=${issued.idShort} uriLen=${issued.uri.length}`);
      await gotoSettings(mp, mbp, 'invites');
      const redeemed = await redeemInviteOnDashboard(mp, issued.uri);
      if (!redeemed.ok) {
        throw new Error(`mbp: redeem failed: ${redeemed.error}\n  ${HINT_PROVISION_FAILED}`);
      }
      console.log(`  mbp redeemed; issuer=${(redeemed.issuerNpub || '').slice(0, 16)}…`);
      // Allow the redemption to propagate back to selene (kind:25001
      // → contact.added). The SSE consumer on the sidebar updates on
      // its own; we just need to give the relay round-trip a window
      // before re-reading the sidebars.
      await sp.waitForTimeout(2500);
      await mp.waitForTimeout(500);
      await gotoStable(sp, `${selene}/`);
      await gotoStable(mp, `${mbp}/`);
      seleneContacts = await listSidebarContacts(sp);
      mbpContacts = await listSidebarContacts(mp);
      logOk(step, 'webui-driven invite create + redeem complete; re-checking sidebars');
    }

    const seleneSeesMbp = seleneContacts.filter((c) => c.pubkey === mbpId.hex);
    const mbpSeesSelene = mbpContacts.filter((c) => c.pubkey === seleneId.hex);

    if (seleneSeesMbp.length !== 1) {
      throw new Error(
        `selene sidebar should list mbp (${mbpId.hex.slice(0, 16)}…) exactly once, got ${seleneSeesMbp.length}\n  ${HINT_PROVISION_FAILED}`,
      );
    }
    if (mbpSeesSelene.length !== 1) {
      throw new Error(
        `mbp sidebar should list selene (${seleneId.hex.slice(0, 16)}…) exactly once, got ${mbpSeesSelene.length}\n  ${HINT_PROVISION_FAILED}`,
      );
    }
    if (seleneSeesMbp[0].label !== mbpId.label) {
      console.log(
        `  warn: selene's contact label "${seleneSeesMbp[0].label}" != mbp's own label "${mbpId.label}" (rename mismatch is non-fatal)`,
      );
    }
    if (mbpSeesSelene[0].label !== seleneId.label) {
      console.log(
        `  warn: mbp's contact label "${mbpSeesSelene[0].label}" != selene's own label "${seleneId.label}" (rename mismatch is non-fatal)`,
      );
    }
    logOk(step, 'mutual contact confirmed on both sidebars');

    // ── 4. Phase 2: Selene's Contacts tab lists mbp as a friend. ───
    step = 4;
    logStep(step, 'selene: open Contacts tab, verify mbp appears as friend');
    await gotoSettings(sp, selene, 'identity');
    await clickSettingsTab(sp, 'Contacts');
    const seleneContactRows = await listContactsRows(sp);
    const seleneRow = seleneContactRows.find(
      (r) => (r.pubkey || '').toLowerCase() === mbpId.hex.toLowerCase(),
    );
    if (!seleneRow) {
      throw new Error(
        `selene Contacts pane should list mbp (${mbpId.hex.slice(0, 16)}…); rows=${JSON.stringify(seleneContactRows)}`,
      );
    }
    if (seleneRow.tier !== 'friend') {
      throw new Error(
        `selene's row for mbp expected tier=friend, got ${JSON.stringify(seleneRow.tier)}`,
      );
    }
    logOk(step, `selene Contacts: mbp row label=${seleneRow.label} tier=${seleneRow.tier}`);

    // ── 5. Phase 2: mbp's Contacts tab lists selene as a friend. ───
    step = 5;
    logStep(step, 'mbp: open Contacts tab, verify selene appears as friend');
    await gotoSettings(mp, mbp, 'identity');
    await clickSettingsTab(mp, 'Contacts');
    const mbpContactRows = await listContactsRows(mp);
    const mbpRow = mbpContactRows.find(
      (r) => (r.pubkey || '').toLowerCase() === seleneId.hex.toLowerCase(),
    );
    if (!mbpRow) {
      throw new Error(
        `mbp Contacts pane should list selene (${seleneId.hex.slice(0, 16)}…); rows=${JSON.stringify(mbpContactRows)}`,
      );
    }
    if (mbpRow.tier !== 'friend') {
      throw new Error(
        `mbp's row for selene expected tier=friend, got ${JSON.stringify(mbpRow.tier)}`,
      );
    }
    logOk(step, `mbp Contacts: selene row label=${mbpRow.label} tier=${mbpRow.tier}`);

    // ── 6. Bidirectional chat round-trip. ──────────────────────────
    step = 6;
    logStep(step, 'open contact threads and exchange messages both ways');
    await openContactThread(sp, mbpId.hex);
    await openContactThread(mp, seleneId.hex);

    const ts1 = new Date().toISOString();
    const msgSeleneToMbp = `webui-phase1 selene→mbp ${ts1}`;
    const seleneSentId = await sendInThread(sp, msgSeleneToMbp);
    if (!seleneSentId) throw new Error('selene -> mbp: no self bubble id captured');
    const mbpRcvId = await waitForInbound(mp, seleneId.hex, msgSeleneToMbp, 10000);
    console.log(`  selene→mbp: sent=${seleneSentId.slice(0, 12)}… received=${mbpRcvId.slice(0, 12)}…`);

    const ts2 = new Date().toISOString();
    const msgMbpToSelene = `webui-phase1 mbp→selene ${ts2}`;
    const mbpSentId = await sendInThread(mp, msgMbpToSelene);
    if (!mbpSentId) throw new Error('mbp -> selene: no self bubble id captured');
    const seleneRcvId = await waitForInbound(sp, mbpId.hex, msgMbpToSelene, 10000);
    console.log(`  mbp→selene: sent=${mbpSentId.slice(0, 12)}… received=${seleneRcvId.slice(0, 12)}…`);

    logOk(step, 'bidirectional chat round-trip complete');

    // ── 7. Tear down. ──────────────────────────────────────────────
    step = 7;
    logStep(step, 'close browsers and exit 0');
    await seleneH.browser.close();
    await mbpH.browser.close();
    console.log('full-bidirectional: PASS');
    process.exit(0);
  } catch (err) {
    logFail(step, err && err.message ? err.message : String(err));
    try { await seleneH.browser.close(); } catch {}
    try { await mbpH.browser.close(); } catch {}
    process.exit(1);
  }
}

main().catch((err) => {
  console.error('full-bidirectional: unexpected error');
  console.error(err);
  process.exit(2);
});
