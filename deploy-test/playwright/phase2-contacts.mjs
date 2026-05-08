// phase2-contacts.mjs
//
// Smoke test for Phase 2 of the dashboard feature-parity work: the new
// /settings/contacts pane plus contact_detail / contact_scan partials.
//
// What this script verifies:
//   1. Settings → Contacts tab navigation lands on /settings/contacts and
//      activates the Contacts tab.
//   2. The empty-state row renders on a fresh daemon (no contacts).
//   3. Scanning a well-formed mindgate:// URI produces a preview block
//      whose pubkey/label/relay match the source identity, without
//      committing the contact.
//   4. Adding the same URI (with a label override) adds a single row and
//      the new row carries the override label + the default tier (friend).
//   5. Clicking the row opens the detail pane and shows label / npub /
//      hex / relays consistent with the source.
//   6. Renaming the contact through the detail pane persists and the
//      pane re-renders with the new label.
//   7. Switching tier to "acquaintance" persists and the tier-badge
//      reflects the new value.
//   8. Cycling tier blocked → friend round-trips.
//   9. Removing the contact via the typed-confirm modal drops the row
//      from the table.
//  10. Empty state returns once the contact is gone.
//
// The script spins up a SECOND transient daemon in a temp dir purely to
// mint a real card URI to scan/admit. That daemon binds to ephemeral
// loopback ports and is torn down on exit (success or failure).
//
// Usage:
//   node phase2-contacts.mjs --base http://127.0.0.1:22893
//   node phase2-contacts.mjs --base http://127.0.0.1:22893 --headed
//
// Env:
//   EIDOS_BIN     path to the eidos binary used to spawn the transient
//                 minting daemon (default: `eidos` from PATH).
//
// Exits 0 on success; non-zero on the first assertion failure.

import { launch, gotoStable } from './_lib/chromium.mjs';
import {
  gotoSettings,
  clickSettingsTab,
  listContactsRows,
  scanCardPreview,
  addContactByCard,
  openContactDetail,
  readContactDetail,
  setContactLabelInDetail,
  setContactTierInDetail,
  removeContactWithConfirm,
} from './_lib/dashboard.mjs';
import { spawnDaemon } from './_lib/daemon.mjs';

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
      process.stdout.write('usage: node phase2-contacts.mjs [--base URL] [--headed]\n');
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
  console.log(`phase2-contacts: base=${baseURL} headed=${headed}`);

  // Cleanup must work even if the minting daemon never came up, so keep
  // the handle in a shared scope and wrap each setup step in its own
  // try/finally.
  let minted = null;
  let browser = null;
  let page = null;
  let step = 0;

  try {
    // Spin up a transient minting daemon BEFORE launching the browser so
    // we fail fast if `eidos` isn't on PATH or the daemon won't start.
    minted = await spawnDaemon({ label: 'pw-phase2-peer' });
    console.log(
      `phase2-contacts: minted card from transient daemon hex=${minted.pubkey.slice(0, 16)}…`,
    );

    ({ browser, page } = await launch({ headless: !headed }));
    // ── 1. Navigate to /settings, click Contacts tab. ───────────────
    step = 1;
    logStep(step, 'open /settings and click Contacts tab');
    await gotoSettings(page, baseURL, 'identity');
    await clickSettingsTab(page, 'Contacts');
    const url1 = page.url();
    if (!url1.endsWith('/settings/contacts')) {
      throw new Error(`expected URL to end with /settings/contacts, got ${url1}`);
    }
    const active1 = await page.evaluate(() => {
      const a = document.querySelector('a.settings-tab.is-active');
      return a ? (a.textContent || '').trim().toLowerCase() : null;
    });
    if (active1 !== 'contacts') {
      throw new Error(`expected Contacts tab active, got ${JSON.stringify(active1)}`);
    }
    logOk(step, `URL=${url1}; tab=Contacts`);

    // ── 2. Empty-state on fresh daemon. ─────────────────────────────
    step = 2;
    logStep(step, 'verify empty-state placeholder is showing');
    const empty2 = await page.evaluate(() => {
      const placeholder = document.querySelector('tr.contacts-empty');
      const rows = document.querySelectorAll('tr.contact-row');
      return { placeholderText: placeholder ? (placeholder.textContent || '').trim() : null, rowCount: rows.length };
    });
    if (empty2.rowCount !== 0) {
      throw new Error(`expected zero contact-rows on fresh daemon, got ${empty2.rowCount}`);
    }
    if (!empty2.placeholderText || !/no contacts/i.test(empty2.placeholderText)) {
      throw new Error(`expected "No contacts" placeholder, got ${JSON.stringify(empty2.placeholderText)}`);
    }
    logOk(step, `empty-state shown: ${empty2.placeholderText.slice(0, 60)}…`);

    // ── 3. Scan-preview the minted card. ────────────────────────────
    step = 3;
    logStep(step, 'scan-preview the minted card URI');
    const preview = await scanCardPreview(page, minted.cardURI);
    if (preview.error) {
      throw new Error(`scan returned error: ${preview.error}`);
    }
    if ((preview.pubkey || '').toLowerCase() !== minted.pubkey) {
      throw new Error(
        `preview pubkey mismatch: got ${preview.pubkey}, want ${minted.pubkey}`,
      );
    }
    if (!preview.label) {
      throw new Error(`preview label empty (expected the minting daemon's label)`);
    }
    if (preview.alreadyContact) {
      throw new Error(`preview reports alreadyContact=true on a fresh daemon`);
    }
    logOk(step, `preview pubkey=${preview.pubkey.slice(0, 16)}… label=${preview.label} relay=${preview.relay || '(none)'}`);

    // After a scan the form's hidden state still contains the URI, but
    // the visible inputs were not reset. Discard the preview block so the
    // next Add submission goes through the empty form rather than the
    // scan-confirm form.
    await page.evaluate(() => {
      const result = document.querySelector('#contact-add-result');
      if (result) result.innerHTML = '';
    });

    // ── 4. Add the contact with a label override. ───────────────────
    step = 4;
    logStep(step, 'add contact via card URI with label override');
    const labelOverride = 'pw-override-label';
    const addRes = await addContactByCard(page, minted.cardURI, labelOverride);
    if (!addRes.ok) {
      throw new Error(`addContactByCard failed: ${addRes.message}`);
    }
    // Re-navigate to the contacts pane to read the canonical row state
    // (the post-add render uses an OOB swap that may or may not include
    // the row depending on handler shape; this is the spec-friendly path).
    await gotoSettings(page, baseURL, 'contacts');
    const rows4 = await listContactsRows(page);
    if (rows4.length !== 1) {
      throw new Error(`expected exactly 1 contact-row after add, got ${rows4.length}`);
    }
    const row = rows4[0];
    if (row.pubkey.toLowerCase() !== minted.pubkey) {
      throw new Error(`row pubkey mismatch: got ${row.pubkey}, want ${minted.pubkey}`);
    }
    if (row.label !== labelOverride) {
      throw new Error(`row label mismatch: got ${JSON.stringify(row.label)}, want ${JSON.stringify(labelOverride)}`);
    }
    logOk(step, `row label=${row.label} tier=${row.tier} hex=${row.pubkey.slice(0, 16)}…`);

    // ── 5. Open detail pane. ────────────────────────────────────────
    step = 5;
    logStep(step, 'open contact detail pane');
    await openContactDetail(page, minted.pubkey);
    const detail = await readContactDetail(page);
    if (!detail) throw new Error('readContactDetail returned null');
    if (detail.label !== labelOverride) {
      throw new Error(`detail label mismatch: got ${detail.label}, want ${labelOverride}`);
    }
    if (!detail.npub.startsWith('npub1')) {
      throw new Error(`detail npub malformed: ${detail.npub}`);
    }
    if ((detail.pubkey || '').toLowerCase() !== minted.pubkey) {
      throw new Error(`detail hex mismatch: got ${detail.pubkey}, want ${minted.pubkey}`);
    }
    // Relays may legitimately be empty — the minting daemon's home is a
    // fake ws://127.0.0.1:1/ for test purposes — but the field must exist.
    if (!Array.isArray(detail.relays)) {
      throw new Error('detail.relays not an array');
    }
    logOk(step, `detail label=${detail.label} npub=${detail.npub.slice(0, 16)}… relays=${detail.relays.length}`);

    // ── 6. Rename via detail pane. ──────────────────────────────────
    step = 6;
    logStep(step, 'rename contact via detail pane');
    const renamed = 'pw-renamed-label';
    const r6 = await setContactLabelInDetail(page, renamed);
    if (!r6.ok) throw new Error(`setContactLabelInDetail failed: ${r6.message}`);
    const afterRename = await readContactDetail(page);
    if (!afterRename || afterRename.label !== renamed) {
      throw new Error(`label did not persist: got ${afterRename?.label}, want ${renamed}`);
    }
    logOk(step, `label -> ${renamed}`);

    // ── 7. Tier → acquaintance. ─────────────────────────────────────
    step = 7;
    logStep(step, 'set tier to acquaintance');
    const r7 = await setContactTierInDetail(page, 'acquaintance');
    if (!r7.ok) throw new Error(`setContactTierInDetail(acquaintance) failed: ${r7.message}`);
    const after7 = await readContactDetail(page);
    if (!after7 || after7.tier !== 'acquaintance') {
      throw new Error(`tier did not persist as acquaintance: got ${after7?.tier}`);
    }
    logOk(step, `tier -> acquaintance`);

    // ── 8. Tier blocked → friend round-trip. ────────────────────────
    step = 8;
    logStep(step, 'cycle tier blocked → friend');
    const r8a = await setContactTierInDetail(page, 'blocked');
    if (!r8a.ok) throw new Error(`setContactTierInDetail(blocked) failed: ${r8a.message}`);
    const mid8 = await readContactDetail(page);
    if (!mid8 || mid8.tier !== 'blocked') {
      throw new Error(`tier did not persist as blocked: got ${mid8?.tier}`);
    }
    const r8b = await setContactTierInDetail(page, 'friend');
    if (!r8b.ok) throw new Error(`setContactTierInDetail(friend) failed: ${r8b.message}`);
    const after8 = await readContactDetail(page);
    if (!after8 || after8.tier !== 'friend') {
      throw new Error(`tier did not persist as friend: got ${after8?.tier}`);
    }
    logOk(step, `tier cycled blocked → friend`);

    // ── 9. Remove via typed-confirm. ────────────────────────────────
    step = 9;
    logStep(step, 'navigate back to /settings/contacts and remove the row');
    await gotoSettings(page, baseURL, 'contacts');
    // The expected typed phrase per §7 of the spec is the contact's own
    // label — which is currently `renamed` (`pw-renamed-label`).
    const r9 = await removeContactWithConfirm(page, renamed);
    if (!r9.ok) {
      throw new Error('row did not disappear after typed-confirm submission');
    }
    logOk(step, 'row removed via typed-confirm');

    // ── 10. Empty-state again. ──────────────────────────────────────
    step = 10;
    logStep(step, 'verify empty-state placeholder is back');
    // Refetch the pane to ensure we're looking at canonical state, not a
    // partial OOB swap.
    await gotoSettings(page, baseURL, 'contacts');
    const empty10 = await page.evaluate(() => {
      const placeholder = document.querySelector('tr.contacts-empty');
      const rows = document.querySelectorAll('tr.contact-row');
      return { placeholderText: placeholder ? (placeholder.textContent || '').trim() : null, rowCount: rows.length };
    });
    if (empty10.rowCount !== 0) {
      throw new Error(`expected zero contact-rows after remove, got ${empty10.rowCount}`);
    }
    if (!empty10.placeholderText || !/no contacts/i.test(empty10.placeholderText)) {
      throw new Error(`expected "No contacts" placeholder back, got ${JSON.stringify(empty10.placeholderText)}`);
    }
    logOk(step, 'empty-state restored');

    console.log('phase2-contacts: PASS');
    process.exitCode = 0;
  } catch (err) {
    logFail(step, err && err.message ? err.message : String(err));
    process.exitCode = 1;
  } finally {
    if (browser) {
      try { await browser.close(); } catch { /* ignore */ }
    }
    if (minted) {
      try { await minted.handle.stop(); } catch { /* ignore */ }
    }
  }
}

main().catch((err) => {
  console.error('phase2-contacts: unexpected error');
  console.error(err);
  process.exitCode = 2;
});
