// Chromium launcher used by every script in deploy-test/playwright/.
//
// Why this file exists:
// - We always want the same Chromium-for-Testing binary that
//   `npx playwright install chromium --no-shell` drops in
//   ~/.cache/ms-playwright. Pinning executablePath avoids surprising
//   "browser not installed" errors that vary by host.
// - `--no-sandbox` is required on selene (no user namespace) and is
//   harmless on dev laptops; this is a loopback-only test runner so
//   the sandbox loss does not change the threat model.
// - `domcontentloaded` + a small settle wait is the SSE-friendly pattern
//   we ironed out in /tmp/pw-debug — the dashboard keeps an open /events
//   stream so `networkidle` never fires.
//
// The returned `lines` array captures console + non-noise responses so
// tests can assert on what actually happened on the wire.

import { existsSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { homedir } from 'node:os';

function findPlaywrightModule() {
  // Operator override — if you've cloned playwright somewhere specific.
  if (process.env.EIDOS_PLAYWRIGHT_MODULE) {
    return process.env.EIDOS_PLAYWRIGHT_MODULE;
  }

  // Scan ~/.npm/_npx/*/node_modules/playwright/index.mjs. `npx playwright
  // install chromium` puts the package under a hash-named cache dir; we
  // don't pin a specific hash because that varies per host and per
  // playwright-version.
  const root = join(homedir(), '.npm/_npx');
  if (existsSync(root)) {
    for (const entry of readdirSync(root)) {
      const candidate = join(root, entry, 'node_modules/playwright/index.mjs');
      try {
        if (statSync(candidate).isFile()) return candidate;
      } catch {
        // ignore
      }
    }
  }

  // Last-ditch fallback: bare specifier — works if the script is run from
  // a directory where `playwright` is npm-resolvable.
  return 'playwright';
}

const CHROMIUM_BIN = join(
  homedir(),
  '.cache/ms-playwright/chromium-1217/chrome-linux64/chrome',
);

/**
 * Launch a Chromium browser, return {browser, context, page, lines}.
 *
 * `lines` is a captured array of `{type, text, source, body}` for console
 * messages, page errors, and HTTP responses (excluding /events SSE,
 * /static/*, /favicon.ico — those are pure noise for assertions).
 *
 * Callers must `await browser.close()` at the end.
 */
export async function launch({ headless = true } = {}) {
  const playwrightPath = findPlaywrightModule();
  const { chromium } = await import(playwrightPath);

  const browser = await chromium.launch({
    executablePath: CHROMIUM_BIN,
    headless,
    args: ['--no-sandbox'],
  });
  const context = await browser.newContext();
  const page = await context.newPage();

  /** @type {Array<{type: string, text?: string, source?: string, body?: string}>} */
  const lines = [];

  page.on('console', (msg) => {
    lines.push({
      type: `console.${msg.type()}`,
      text: msg.text(),
      source: `${msg.location().url}:${msg.location().lineNumber}`,
    });
  });
  page.on('pageerror', (err) => {
    lines.push({ type: 'pageerror', text: err.message });
  });
  page.on('response', async (resp) => {
    let url;
    try {
      url = new URL(resp.url());
    } catch {
      return;
    }
    if (url.pathname === '/events') return;
    if (url.pathname.startsWith('/static/')) return;
    if (url.pathname === '/favicon.ico') return;
    let body = '';
    try {
      body = (await resp.text()).slice(0, 400);
    } catch {
      // body unavailable (e.g. redirect, aborted) — leave empty
    }
    lines.push({
      type: 'response',
      text: `${resp.status()} ${resp.request().method()} ${url.pathname}${url.search}`,
      body,
    });
  });

  return { browser, context, page, lines };
}

/**
 * Navigate to `url` using the SSE-friendly pattern: wait for
 * domcontentloaded, then settle for `settleMs` ms so htmx + SSE wiring
 * has a chance to attach. Pages with a long-lived /events stream never
 * fire `networkidle`, which is why we don't use it.
 */
export async function gotoStable(page, url, { settleMs = 1500 } = {}) {
  await page.goto(url, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(settleMs);
}
