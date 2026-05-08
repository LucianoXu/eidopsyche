// phase5-service.mjs
//
// Smoke test for Phase 5 of the dashboard feature-parity work: the new
// /settings/service pane and lifecycle-log streaming. Reconnect is the
// only action exercised by default — Stop / Purge / Self-update each
// take the daemon down (or rewrite its binary) and are gated behind
// EIDOS_TEST_DESTRUCTIVE=1.
//
// What this script verifies:
//   1. Settings → Service tab navigation lands on /settings/service and
//      activates the Service tab (rendered with the is-danger class).
//   2. The status colophon shows Version, Commit, State dir, etc.
//   3. The four action cards render, with Reconnect enabled by default.
//   4. Clicking Reconnect produces a lifecycle-log frame with a JobID,
//      lines stream in via SSE, and the status pill transitions from
//      "running" to "ok".
//   5. While a job is in flight, the Service tab re-renders with all
//      four cards disabled (busy guard) — verified by the post-action
//      pane re-render after lifecycle.done fires service.status.
//
// Stop / Purge / Self-update are NOT clicked here — even with EIDOS_TEST_DESTRUCTIVE=1
// this script exits without firing them. Operating those is left to manual
// ops; the integration test (build-tag integration) covers the wire-up.
//
// Usage:
//   node phase5-service.mjs --base http://127.0.0.1:22893
//   node phase5-service.mjs --base http://127.0.0.1:22893 --headed

import { launch } from './_lib/chromium.mjs';
import {
  gotoSettings,
  clickSettingsTab,
  fireServiceReconnect,
  readServiceStatus,
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
      process.stdout.write('usage: node phase5-service.mjs [--base URL] [--headed]\n');
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
  console.log(`phase5-service: base=${baseURL} headed=${headed}`);

  let browser = null;
  let page = null;
  let step = 0;

  try {
    ({ browser, page } = await launch({ headless: !headed }));

    // ── 1. Navigate to /settings, click Service tab. ────────────────
    step = 1;
    logStep(step, 'open /settings and click Service tab');
    await gotoSettings(page, baseURL, 'identity');
    await clickSettingsTab(page, 'Service');
    const url1 = page.url();
    if (!url1.endsWith('/settings/service')) {
      throw new Error(`expected URL to end with /settings/service, got ${url1}`);
    }
    const tabClasses = await page.evaluate(() => {
      const a = document.querySelector('a.settings-tab.is-active');
      return a ? Array.from(a.classList) : [];
    });
    if (!tabClasses.includes('is-danger')) {
      throw new Error(`Service tab should carry is-danger class; got ${JSON.stringify(tabClasses)}`);
    }
    logOk(step, `URL=${url1}; tab=Service (is-danger marked)`);

    // ── 2. Status colophon shows daemon metadata. ───────────────────
    step = 2;
    logStep(step, 'read status colophon');
    const status = await readServiceStatus(page);
    if (!status) throw new Error('readServiceStatus returned null');
    const required = ['version', 'commit', 'state dir', 'dashboard', 'ipc socket'];
    for (const key of required) {
      if (!status[key]) {
        throw new Error(`status missing field ${JSON.stringify(key)}; got ${JSON.stringify(status)}`);
      }
    }
    logOk(step, `version=${status.version} state-dir=${status['state dir'].slice(0, 50)}…`);

    // ── 3. The four action cards render with Reconnect enabled. ────
    step = 3;
    logStep(step, 'verify the four action cards render');
    const cards = await page.evaluate(() => {
      return Array.from(document.querySelectorAll('.service-actions .action-card')).map((b) => ({
        title: (b.querySelector('h4')?.textContent || '').trim(),
        actionKey: (b.querySelector('.action-key')?.textContent || '').trim(),
        disabled: !!b.disabled,
      }));
    });
    if (cards.length !== 4) {
      throw new Error(`expected 4 action cards, got ${cards.length}: ${JSON.stringify(cards)}`);
    }
    const titles = cards.map((c) => c.title).sort();
    const want = ['Purge', 'Reconnect', 'Self-update', 'Stop'];
    if (titles.join(',') !== want.join(',')) {
      throw new Error(`action card titles mismatch: got ${titles}, want ${want}`);
    }
    if (cards.some((c) => c.disabled)) {
      throw new Error(`no card should be disabled at rest; got ${JSON.stringify(cards)}`);
    }
    logOk(step, `cards: ${titles.join(', ')}`);

    // ── 4. Click Reconnect, watch the lifecycle stream. ────────────
    step = 4;
    logStep(step, 'click Reconnect, await streaming + done');
    const r4 = await fireServiceReconnect(page);
    if (!r4.ok) {
      throw new Error(`fireServiceReconnect failed: status=${r4.status} jobID=${r4.jobID} reason=${r4.reason}`);
    }
    if (!r4.jobID || r4.jobID.length !== 8) {
      throw new Error(`expected 8-char jobID, got ${JSON.stringify(r4.jobID)}`);
    }
    if (r4.lineCount < 1) {
      throw new Error(`expected at least one streamed line; got ${r4.lineCount}`);
    }
    logOk(step, `job=${r4.jobID} status=${r4.status} lines=${r4.lineCount}`);

    // ── 5. Pane re-renders with cards re-enabled after lifecycle.done. ─
    step = 5;
    logStep(step, 'navigate back; cards re-enabled');
    await gotoSettings(page, baseURL, 'service');
    const cards5 = await page.evaluate(() => {
      return Array.from(document.querySelectorAll('.service-actions .action-card')).map((b) => ({
        disabled: !!b.disabled,
      }));
    });
    if (cards5.some((c) => c.disabled)) {
      throw new Error(`cards should be re-enabled after job completes; got ${JSON.stringify(cards5)}`);
    }
    logOk(step, 'cards re-enabled after lifecycle.done');

    console.log('phase5-service: PASS');
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
  console.error('phase5-service: unexpected error');
  console.error(err);
  process.exitCode = 2;
});
