// phase3-invites.mjs
//
// Smoke test for Phase 3 of the dashboard feature-parity work: the new
// /settings/invites pane, including issue, list, revoke (with typed
// confirm), and a deliberately-failing redeem against a malformed URI.
// The full happy-path redeem is exercised in full-bidirectional.mjs
// against a real peer; this script only needs the local-state surface.
//
// What this script verifies:
//   1. Settings → Invites tab navigation lands on /settings/invites and
//      activates the Invites tab.
//   2. The empty-state placeholder renders on a fresh daemon (no
//      invites exist yet).
//   3. Issuing an invite via the Issue form produces a create-success
//      slip with the URI and an 8-char ID.
//   4. The issued invite shows up as a single Active row matching the
//      slip's ID.
//   5. Issuing a second invite leaves both in Active.
//   6. Revoking the first invite with the typed-confirm phrase removes
//      it from Active and lands it in the collapsed history (revoked).
//   7. Redeeming a malformed URI surfaces an inline form-flash error
//      without hanging the page or polluting the contacts list.
//
// Usage:
//   node phase3-invites.mjs --base http://127.0.0.1:22893
//   node phase3-invites.mjs --base http://127.0.0.1:22893 --headed
//
// Exits 0 on success; non-zero on the first assertion failure.

import { launch } from './_lib/chromium.mjs';
import {
  gotoSettings,
  clickSettingsTab,
  listInviteRows,
  createInvite,
  redeemInviteOnDashboard,
  revokeInviteWithConfirm,
} from './_lib/dashboard.mjs';

function parseArgs(argv) {
  const out = {
    base: process.env.BASE_URL || 'http://127.0.0.1:22893',
    headed: false,
  };
  for (let i = 2; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--base') out.base = argv[++i];
    else if (a === '--headed') out.headed = true;
    else if (a === '--help' || a === '-h') {
      process.stdout.write('usage: node phase3-invites.mjs [--base URL] [--headed]\n');
      process.exit(0);
    }
  }
  return out;
}

function logStep(n, msg) { console.log(`STEP ${n}: ${msg}`); }
function logOk(n, msg)   { console.log(`STEP ${n} OK: ${msg}`); }
function logFail(n, msg) { console.error(`STEP ${n} FAIL: ${msg}`); }

async function main() {
  const { base, headed } = parseArgs(process.argv);
  const baseURL = base.replace(/\/$/, '');
  console.log(`phase3-invites: base=${baseURL} headed=${headed}`);

  let browser = null;
  let page = null;
  let step = 0;

  try {
    ({ browser, page } = await launch({ headless: !headed }));

    // ── 1. Navigate to /settings, click Invites tab. ────────────────
    step = 1;
    logStep(step, 'open /settings and click Invites tab');
    await gotoSettings(page, baseURL, 'identity');
    await clickSettingsTab(page, 'Invites');
    const url1 = page.url();
    if (!url1.endsWith('/settings/invites')) {
      throw new Error(`expected URL to end with /settings/invites, got ${url1}`);
    }
    const active1 = await page.evaluate(() => {
      const a = document.querySelector('a.settings-tab.is-active');
      return a ? (a.textContent || '').trim().toLowerCase() : null;
    });
    if (active1 !== 'invites') {
      throw new Error(`expected Invites tab active, got ${JSON.stringify(active1)}`);
    }
    logOk(step, `URL=${url1}; tab=Invites`);

    // ── 2. Empty-state on fresh daemon. ─────────────────────────────
    step = 2;
    logStep(step, 'verify empty-state placeholder on fresh daemon');
    const empty2 = await page.evaluate(() => {
      const placeholder = document.querySelector('tr.invites-empty');
      const rows = document.querySelectorAll('tr.invite-row');
      return {
        placeholderText: placeholder ? (placeholder.textContent || '').trim() : null,
        rowCount: rows.length,
      };
    });
    if (empty2.rowCount !== 0) {
      throw new Error(`expected zero invite-rows on fresh daemon, got ${empty2.rowCount}`);
    }
    if (!empty2.placeholderText || !/no active invitations/i.test(empty2.placeholderText)) {
      throw new Error(`expected "No active invitations" placeholder, got ${JSON.stringify(empty2.placeholderText)}`);
    }
    logOk(step, `empty-state shown: ${empty2.placeholderText.slice(0, 60)}…`);

    // ── 3. Issue a single-use invite. ───────────────────────────────
    step = 3;
    logStep(step, 'issue a single-use invite');
    const created1 = await createInvite(page, { redeemerLabel: 'pw-redeemer-A', expires: '24h', maxUses: '1' });
    if (!created1.ok) {
      throw new Error(`createInvite failed: ${created1.error}`);
    }
    if (!created1.uri || !created1.uri.startsWith('mindgate-invite://')) {
      throw new Error(`expected mindgate-invite:// URI, got ${JSON.stringify(created1.uri)}`);
    }
    if (!created1.idShort || created1.idShort.length !== 8) {
      throw new Error(`expected 8-char idShort, got ${JSON.stringify(created1.idShort)}`);
    }
    logOk(step, `issued id=${created1.idShort} uriLen=${created1.uri.length}`);

    // ── 4. Active list shows exactly the new row. ───────────────────
    step = 4;
    logStep(step, 'verify Active list shows the issued invite');
    await gotoSettings(page, baseURL, 'invites');
    const rows4 = await listInviteRows(page, 'active');
    if (rows4.length !== 1) {
      throw new Error(`expected exactly 1 Active row, got ${rows4.length}`);
    }
    if (rows4[0].idShort !== created1.idShort) {
      throw new Error(`Active row id mismatch: got ${rows4[0].idShort}, want ${created1.idShort}`);
    }
    if (rows4[0].redeemer !== 'pw-redeemer-A') {
      throw new Error(`Active row redeemer mismatch: got ${rows4[0].redeemer}`);
    }
    if (rows4[0].uses !== '0' || rows4[0].cap !== '1') {
      throw new Error(`Active row uses/cap mismatch: got ${rows4[0].uses}/${rows4[0].cap}`);
    }
    logOk(step, `Active row id=${rows4[0].idShort} redeemer=${rows4[0].redeemer} uses=${rows4[0].uses}/${rows4[0].cap}`);

    // ── 5. Issue a second invite (unlimited / never). ───────────────
    step = 5;
    logStep(step, 'issue a second invite (unlimited / never)');
    const created2 = await createInvite(page, { redeemerLabel: '', expires: 'never', maxUses: 'unlimited' });
    if (!created2.ok) {
      throw new Error(`createInvite #2 failed: ${created2.error}`);
    }
    await gotoSettings(page, baseURL, 'invites');
    const rows5 = await listInviteRows(page, 'active');
    if (rows5.length !== 2) {
      throw new Error(`expected 2 Active rows after second issue, got ${rows5.length}`);
    }
    const ids5 = rows5.map((r) => r.idShort).sort();
    const want5 = [created1.idShort, created2.idShort].sort();
    if (ids5.join(',') !== want5.join(',')) {
      throw new Error(`Active row IDs mismatch: got ${ids5}, want ${want5}`);
    }
    logOk(step, `Active rows: ${ids5.join(', ')}`);

    // ── 6. Revoke the first invite via typed-confirm. ───────────────
    step = 6;
    logStep(step, `revoke invite ${created1.idShort} via typed-confirm`);
    const r6 = await revokeInviteWithConfirm(page, created1.idShort);
    if (!r6.ok) {
      throw new Error('revoke modal did not close after confirm submit');
    }
    const rowsActive6 = await listInviteRows(page, 'active');
    if (rowsActive6.length !== 1) {
      throw new Error(`expected 1 Active row after revoke, got ${rowsActive6.length}`);
    }
    if (rowsActive6[0].idShort !== created2.idShort) {
      throw new Error(`expected ${created2.idShort} to remain Active, got ${rowsActive6[0].idShort}`);
    }
    // The revoke also fires sse:invite.revoked, which the pane subscribes
    // to and uses to refetch itself outerHTML. Give that race a moment to
    // settle so we read post-SSE state, not the partial OOB+main response
    // mid-swap.
    await page.waitForTimeout(400);
    // Open the history <details> to inspect the revoked row.
    await page.evaluate(() => {
      const d = document.querySelector('details.invites-history');
      if (d) d.open = true;
    });
    const rowsHist6 = await listInviteRows(page, 'history');
    const revokedFound = rowsHist6.find((r) => r.idShort === created1.idShort && r.status === 'revoked');
    if (!revokedFound) {
      throw new Error(
        `expected ${created1.idShort} in history with status=revoked; got history rows: ${JSON.stringify(rowsHist6)}`,
      );
    }
    logOk(step, `${created1.idShort} now in history (revoked); ${created2.idShort} still active`);

    // ── 7. Redeem with a malformed URI surfaces inline error. ───────
    step = 7;
    logStep(step, 'redeem a malformed URI; expect inline error, no crash');
    await gotoSettings(page, baseURL, 'invites');
    const r7 = await redeemInviteOnDashboard(page, 'mindgate-invite://garbage-not-a-real-token');
    if (r7.ok) {
      throw new Error(`expected redeem of malformed URI to fail, got ok=true (issuerNpub=${r7.issuerNpub})`);
    }
    if (!r7.error || !/Redeem failed/i.test(r7.error)) {
      throw new Error(`expected error to mention "Redeem failed", got ${JSON.stringify(r7.error)}`);
    }
    logOk(step, `inline error rendered: ${r7.error.slice(0, 80)}…`);

    console.log('phase3-invites: PASS');
    process.exitCode = 0;
  } catch (err) {
    logFail(step, err && err.message ? err.message : String(err));
    process.exitCode = 1;
  } finally {
    if (browser) {
      try { await browser.close(); } catch { /* ignore */ }
    }
  }
}

main().catch((err) => {
  console.error('phase3-invites: unexpected error');
  console.error(err);
  process.exitCode = 2;
});
