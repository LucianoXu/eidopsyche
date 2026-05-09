// phase1-identity-config.mjs
//
// Smoke test for Phase 1 of the dashboard feature-parity work:
// the new /settings shell with Identity and Config tabs.
//
// What this script verifies (it does NOT exercise Contacts, Invites,
// Relays, or Service — those tabs do not exist yet):
//   1. The sidebar's "Settings" link lands on /settings (or /settings/identity).
//   2. The Identity tab is active by default and the identity card is
//      well-formed (label, npub1…, 64-hex, mindgate://… card URI).
//   3. Switching to Config swaps the active tab and renders the KV table
//      with at least the canonical config rows we depend on downstream.
//   4. The set-label form round-trips: change label, see flash; revert,
//      see flash; final identity matches the original label.
//   5. The set-config-value form round-trips for log_level: debug, then
//      back to info.
//
// Usage:
//   node phase1-identity-config.mjs --base http://127.0.0.1:22893
//   node phase1-identity-config.mjs --base http://127.0.0.1:22893 --headed
//
// Exits 0 on success; non-zero on the first assertion failure with a
// step number and a short reason. CI logs are scanned for "STEP n FAIL".

import { launch, gotoStable } from './_lib/chromium.mjs';
import {
  gotoSettings,
  clickSettingsTab,
  getIdentity,
  setLabel,
  getConfigRow,
  setConfigValue,
} from './_lib/dashboard.mjs';

function parseArgs(argv) {
  const out = { base: 'http://127.0.0.1:22893', headed: false };
  for (let i = 2; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--base') out.base = argv[++i];
    else if (a === '--headed') out.headed = true;
    else if (a === '--help' || a === '-h') {
      process.stdout.write('usage: node phase1-identity-config.mjs [--base URL] [--headed]\n');
      process.exit(0);
    }
  }
  return out;
}

const REQUIRED_CONFIG_ROWS = [
  'log_level',
  'daemon.socket',
  'dashboard.enabled',
  'dashboard.listen',
];

function logStep(n, msg)   { console.log(`STEP ${n}: ${msg}`); }
function logOk(n, msg)     { console.log(`STEP ${n} OK: ${msg}`); }
function logFail(n, msg)   { console.error(`STEP ${n} FAIL: ${msg}`); }

async function main() {
  const { base, headed } = parseArgs(process.argv);
  const baseURL = base.replace(/\/$/, '');
  console.log(`phase1-identity-config: base=${baseURL} headed=${headed}`);

  const { browser, page } = await launch({ headless: !headed });
  let step = 0;
  try {
    // ── 1. Open dashboard, click sidebar Settings link. ─────────────
    step = 1;
    logStep(step, 'open dashboard root and click sidebar Settings link');
    await gotoStable(page, `${baseURL}/`);
    const sidebarLink = await page.$('aside.sidebar a[href="/settings"]');
    if (!sidebarLink) throw new Error('sidebar Settings link not present');
    await sidebarLink.click();
    // settings shell is rendered into #main
    await page.waitForSelector('nav.settings-tabs', { timeout: 5000 });
    await page.waitForTimeout(400);
    logOk(step, 'sidebar -> settings shell rendered');

    // ── 2. Verify URL is /settings (or /settings/identity). ────────
    step = 2;
    logStep(step, 'verify URL is /settings or /settings/identity');
    const url2 = page.url();
    const ok2 = url2.endsWith('/settings') || url2.endsWith('/settings/identity');
    if (!ok2) throw new Error(`unexpected URL after click: ${url2}`);
    logOk(step, `URL=${url2}`);

    // ── 3. Identity tab is active. ─────────────────────────────────
    step = 3;
    logStep(step, 'verify Identity tab has is-active');
    const activeTab = await page.evaluate(() => {
      const a = document.querySelector('a.settings-tab.is-active');
      return a ? (a.textContent || '').trim() : null;
    });
    if (!activeTab || activeTab.toLowerCase() !== 'identity') {
      throw new Error(`expected Identity active, got ${JSON.stringify(activeTab)}`);
    }
    logOk(step, 'Identity tab is-active');

    // ── 4. Identity card values look well-formed. ──────────────────
    step = 4;
    logStep(step, 'read identity card and validate shape');
    const ident = await getIdentity(page);
    if (!ident) throw new Error('dl.identity-card not found');
    const reasons = [];
    if (!ident.label) reasons.push('label empty');
    if (!ident.npub.startsWith('npub1')) reasons.push(`npub bad: ${ident.npub}`);
    if (!/^[0-9a-f]{64}$/i.test(ident.hex)) reasons.push(`hex bad: ${ident.hex}`);
    if (!ident.cardURI.startsWith('mindgate://')) reasons.push(`cardURI bad: ${ident.cardURI}`);
    if (reasons.length) throw new Error(reasons.join('; '));
    const originalLabel = ident.label;
    logOk(step, `label=${originalLabel} npub=${ident.npub.slice(0, 16)}…`);

    // ── 5. Click Config tab; URL + active swap. ────────────────────
    step = 5;
    logStep(step, 'click Config tab and verify swap');
    await clickSettingsTab(page, 'Config');
    const url5 = page.url();
    if (!url5.endsWith('/settings/config')) {
      throw new Error(`expected /settings/config in URL, got ${url5}`);
    }
    const active5 = await page.evaluate(() => {
      const a = document.querySelector('a.settings-tab.is-active');
      return a ? (a.textContent || '').trim() : null;
    });
    if (!active5 || active5.toLowerCase() !== 'config') {
      throw new Error(`expected Config active, got ${JSON.stringify(active5)}`);
    }
    logOk(step, `URL=${url5}; active=Config`);

    // ── 6. Required config rows exist. ─────────────────────────────
    step = 6;
    logStep(step, 'verify required config rows are present');
    const missing = [];
    for (const path of REQUIRED_CONFIG_ROWS) {
      const row = await getConfigRow(page, path);
      if (!row) missing.push(path);
    }
    if (missing.length) {
      throw new Error(`missing config rows: ${missing.join(', ')}`);
    }
    logOk(step, `found ${REQUIRED_CONFIG_ROWS.length} required rows`);

    // ── 7. Set label to <orig>-pwprobe via Identity tab. ───────────
    step = 7;
    logStep(step, 'switch back to Identity tab and rename label');
    await clickSettingsTab(page, 'Identity');
    const renamed = `${originalLabel}-pwprobe`;
    const r7 = await setLabel(page, renamed);
    if (!r7.ok) throw new Error(`label rename failed: ${r7.message}`);
    const after7 = await getIdentity(page);
    if (!after7 || after7.label !== renamed) {
      throw new Error(`label did not update: got ${after7?.label}`);
    }
    logOk(step, `label -> ${renamed}; flash=${r7.message}`);

    // ── 8. Set label back to original. ─────────────────────────────
    step = 8;
    logStep(step, 'restore original label');
    const r8 = await setLabel(page, originalLabel);
    if (!r8.ok) throw new Error(`label restore failed: ${r8.message}`);
    const after8 = await getIdentity(page);
    if (!after8 || after8.label !== originalLabel) {
      throw new Error(`label did not restore: got ${after8?.label}`);
    }
    logOk(step, `label -> ${originalLabel}; flash=${r8.message}`);

    // ── 9. Toggle log_level via Config tab. ────────────────────────
    step = 9;
    logStep(step, 'set log_level=debug then back to info');
    await clickSettingsTab(page, 'Config');
    const before9 = await getConfigRow(page, 'log_level');
    const original9 = before9 ? before9.value : 'info';
    const r9a = await setConfigValue(page, 'log_level', 'debug');
    if (!r9a.ok) throw new Error(`set log_level=debug failed: ${r9a.message}`);
    const mid9 = await getConfigRow(page, 'log_level');
    if (!mid9 || mid9.value !== 'debug') {
      throw new Error(`log_level did not update: got ${mid9?.value}`);
    }
    const r9b = await setConfigValue(page, 'log_level', original9 || 'info');
    if (!r9b.ok) throw new Error(`set log_level back failed: ${r9b.message}`);
    const after9 = await getConfigRow(page, 'log_level');
    if (!after9 || after9.value !== (original9 || 'info')) {
      throw new Error(`log_level did not restore: got ${after9?.value}`);
    }
    logOk(step, `log_level cycled debug -> ${original9 || 'info'}`);

    // ── 10. Done. ──────────────────────────────────────────────────
    step = 10;
    logStep(step, 'close browser and exit 0');
    await browser.close();
    console.log('phase1-identity-config: PASS');
    process.exit(0);
  } catch (err) {
    logFail(step, err && err.message ? err.message : String(err));
    try { await browser.close(); } catch {}
    process.exit(1);
  }
}

main().catch((err) => {
  console.error('phase1-identity-config: unexpected error');
  console.error(err);
  process.exit(2);
});
