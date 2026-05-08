// phase4-relays.mjs
//
// Smoke test for Phase 4 of the dashboard feature-parity work: the new
// /settings/relays pane, including list-with-health-merge, add (URL +
// role), and remove for both fallback (hx-confirm) and the path through
// the typed-confirm modal for home rows. Runs locally against a single
// daemon — the cross-machine relay-state surface is exercised by
// full-bidirectional.mjs.
//
// What this script verifies:
//   1. Settings → Relays tab navigation lands on /settings/relays and
//      activates the Relays tab.
//   2. The seeded home relay shows in the list with its role pill.
//   3. The single-home row's Remove button is rendered DISABLED (the
//      IsLastHome guard) — defense-in-depth before the daemon refuses.
//   4. Adding a fallback relay produces a new row.
//   5. Adding the same URL again surfaces an inline "Add failed" flash
//      (the daemon's duplicate-url sentinel) without breaking the page.
//   6. Removing the fallback drops the row (no typed-confirm needed).
//   7. After a second home is added, the original home becomes
//      removable; the typed-confirm modal opens, accepts the URL, and
//      drops the row.
//
// Usage:
//   node phase4-relays.mjs --base http://127.0.0.1:22893
//   node phase4-relays.mjs --base http://127.0.0.1:22893 --headed

import { launch } from './_lib/chromium.mjs';
import {
  gotoSettings,
  clickSettingsTab,
  listOwnRelayRows,
  addOwnRelay,
  removeOwnRelay,
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
      process.stdout.write('usage: node phase4-relays.mjs [--base URL] [--headed]\n');
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
  console.log(`phase4-relays: base=${baseURL} headed=${headed}`);

  let browser = null;
  let page = null;
  let step = 0;

  try {
    ({ browser, page } = await launch({ headless: !headed }));

    // ── 1. Navigate to /settings, click Relays tab. ─────────────────
    step = 1;
    logStep(step, 'open /settings and click Relays tab');
    await gotoSettings(page, baseURL, 'identity');
    await clickSettingsTab(page, 'Relays');
    const url1 = page.url();
    if (!url1.endsWith('/settings/relays')) {
      throw new Error(`expected URL to end with /settings/relays, got ${url1}`);
    }
    const active1 = await page.evaluate(() => {
      const a = document.querySelector('a.settings-tab.is-active');
      return a ? (a.textContent || '').trim().toLowerCase() : null;
    });
    if (active1 !== 'relays') {
      throw new Error(`expected Relays tab active, got ${JSON.stringify(active1)}`);
    }
    logOk(step, `URL=${url1}; tab=Relays`);

    // ── 2. Seeded home relay shows. ─────────────────────────────────
    step = 2;
    logStep(step, 'verify the seeded home relay is listed');
    let rows = await listOwnRelayRows(page);
    const homes = rows.filter((r) => r.role === 'home');
    if (homes.length === 0) {
      throw new Error(`expected at least one home relay seeded; got rows=${JSON.stringify(rows)}`);
    }
    const seedHome = homes[0];
    logOk(step, `home relay: ${seedHome.url} state=${seedHome.state}`);

    // ── 3. Single-home row's Remove is disabled. ───────────────────
    step = 3;
    logStep(step, 'verify single-home Remove is disabled (IsLastHome guard)');
    if (homes.length === 1 && !seedHome.isLastHome) {
      throw new Error(`expected IsLastHome=true on the only home row; got ${JSON.stringify(seedHome)}`);
    }
    logOk(step, `seeded home Remove disabled: isLastHome=${seedHome.isLastHome}`);

    // ── 4. Add a fallback relay. ────────────────────────────────────
    step = 4;
    const fallbackURL = 'wss://pw-fallback-' + Date.now() + '.example/';
    logStep(step, `add fallback relay ${fallbackURL}`);
    const r4 = await addOwnRelay(page, fallbackURL, 'fallback');
    if (!r4.ok) throw new Error(`addOwnRelay(fallback) failed: ${r4.error}`);
    rows = await listOwnRelayRows(page);
    const fallbackRow = rows.find((r) => r.url === fallbackURL);
    if (!fallbackRow) throw new Error(`fallback row missing after add; rows=${JSON.stringify(rows)}`);
    if (fallbackRow.role !== 'fallback') {
      throw new Error(`fallback row role mismatch: ${fallbackRow.role}`);
    }
    logOk(step, `fallback row added: role=${fallbackRow.role}`);

    // ── 5. Adding the same URL again surfaces an inline error. ─────
    step = 5;
    logStep(step, 'add the same URL again; expect inline error, no crash');
    const r5 = await addOwnRelay(page, fallbackURL, 'fallback');
    if (r5.ok) throw new Error(`expected duplicate-add to fail, got ok=true`);
    if (!/Add failed/i.test(r5.error || '')) {
      throw new Error(`duplicate-add error message missing 'Add failed': ${r5.error}`);
    }
    logOk(step, `inline error rendered: ${(r5.error || '').slice(0, 80)}…`);

    // ── 6. Remove the fallback (no typed-confirm). ─────────────────
    step = 6;
    logStep(step, `remove fallback relay ${fallbackURL}`);
    const r6 = await removeOwnRelay(page, fallbackURL);
    if (!r6.ok) throw new Error(`removeOwnRelay(fallback) failed`);
    rows = await listOwnRelayRows(page);
    if (rows.find((r) => r.url === fallbackURL)) {
      throw new Error(`fallback row still present after remove`);
    }
    logOk(step, `fallback row removed`);

    // ── 7. Add a second home, then remove the original home via modal. ─
    step = 7;
    const home2URL = 'wss://pw-home2-' + Date.now() + '.example/';
    logStep(step, `add second home ${home2URL}, then remove original home via typed-confirm`);
    const r7a = await addOwnRelay(page, home2URL, 'home');
    if (!r7a.ok) throw new Error(`addOwnRelay(home) failed: ${r7a.error}`);
    rows = await listOwnRelayRows(page);
    const homesAfter = rows.filter((r) => r.role === 'home');
    if (homesAfter.length !== 2) {
      throw new Error(`expected 2 home relays after add, got ${homesAfter.length}`);
    }
    if (homesAfter.some((r) => r.isLastHome)) {
      throw new Error(`no home should be IsLastHome with 2 homes; rows=${JSON.stringify(homesAfter)}`);
    }
    const r7b = await removeOwnRelay(page, seedHome.url);
    if (!r7b.ok) throw new Error(`removeOwnRelay(home) failed`);
    rows = await listOwnRelayRows(page);
    if (rows.find((r) => r.url === seedHome.url)) {
      throw new Error(`original home still present after typed-confirm remove`);
    }
    // Now the second home should become IsLastHome.
    const remainingHomes = rows.filter((r) => r.role === 'home');
    if (remainingHomes.length !== 1 || !remainingHomes[0].isLastHome) {
      throw new Error(`expected the surviving home to be IsLastHome; got ${JSON.stringify(remainingHomes)}`);
    }
    logOk(step, `original home removed; ${home2URL} is now the (locked) only home`);

    // ── 8. Restore the original home so the daemon stays in a known state. ─
    step = 8;
    logStep(step, `restore the seeded home ${seedHome.url} as a second home`);
    const r8 = await addOwnRelay(page, seedHome.url, 'home');
    if (!r8.ok) throw new Error(`addOwnRelay(restore) failed: ${r8.error}`);
    // Clean up the throwaway home2 so the daemon's state is the same as before.
    const r8b = await removeOwnRelay(page, home2URL);
    if (!r8b.ok) throw new Error(`failed to clean up throwaway home2`);
    rows = await listOwnRelayRows(page);
    const finalHomes = rows.filter((r) => r.role === 'home');
    if (finalHomes.length !== 1 || finalHomes[0].url !== seedHome.url) {
      throw new Error(`final state should match seeded home ${seedHome.url}; got ${JSON.stringify(finalHomes)}`);
    }
    logOk(step, 'seeded home restored, daemon state is back to baseline');

    console.log('phase4-relays: PASS');
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
  console.error('phase4-relays: unexpected error');
  console.error(err);
  process.exitCode = 2;
});
