// Page-object helpers for the eidos gate dashboard webui.
//
// Each function takes a Playwright `page` and returns a Promise that
// resolves to a small structured value (plain object, string, etc.).
// Selectors mirror the templates in
// internal/dashboard/templates/{settings,settings_identity,settings_config,
// sidebar,thread,bubble}.html. If a template selector changes, update the
// corresponding helper here — that's the whole point of this file.

import { gotoStable } from './chromium.mjs';

/**
 * dotted config path -> tr id slug. Mirrors the Slug computed server-side
 * in settings_config_row.html (tr id="cfg-{slug}").
 */
export function pathSlug(path) {
  return String(path).replaceAll('.', '-');
}

/**
 * Navigate to /settings or /settings/<tab>. Defaults to identity, which
 * is what the shell renders when the operator clicks Settings the first
 * time.
 */
export async function gotoSettings(page, baseURL, tab = 'identity') {
  const url = tab ? `${baseURL.replace(/\/$/, '')}/settings/${tab}`
                  : `${baseURL.replace(/\/$/, '')}/settings`;
  await gotoStable(page, url);
}

/**
 * Click a settings tab by its visible name (case-insensitive). Waits for
 * the active class to actually swap before returning, so the next
 * helper sees a settled DOM.
 */
export async function clickSettingsTab(page, tabName) {
  const want = String(tabName).toLowerCase();
  const tabs = await page.$$('a.settings-tab');
  let target = null;
  for (const tab of tabs) {
    const text = (await tab.textContent() || '').trim().toLowerCase();
    if (text === want) {
      target = tab;
      break;
    }
  }
  if (!target) {
    throw new Error(`clickSettingsTab: no tab named "${tabName}" found`);
  }
  await target.click();
  // htmx swaps #settings-pane and pushes URL; wait for the active class
  // to land on the clicked tab.
  await page.waitForFunction((wantText) => {
    const tabs = Array.from(document.querySelectorAll('a.settings-tab'));
    const active = tabs.find((t) => t.classList.contains('is-active'));
    return active && (active.textContent || '').trim().toLowerCase() === wantText;
  }, want, { timeout: 5000 });
  // Small settle for SSE/htmx event handlers attaching.
  await page.waitForTimeout(300);
}

/**
 * Read the identity card. Returns {label, npub, hex, cardURI}. Reads
 * the dl.identity-card built by settings_identity.html.
 */
export async function getIdentity(page) {
  return await page.evaluate(() => {
    const dl = document.querySelector('dl.identity-card');
    if (!dl) return null;
    const dts = Array.from(dl.querySelectorAll('dt'));
    const map = {};
    for (const dt of dts) {
      const key = (dt.textContent || '').trim().toLowerCase();
      const dd = dt.nextElementSibling;
      if (!dd) continue;
      // Card cell wraps the URI in <code class="card-uri">.
      const codeURI = dd.querySelector('code.card-uri');
      map[key] = codeURI
        ? (codeURI.textContent || '').trim()
        : (dd.textContent || '').trim();
    }
    return {
      label: map['label'] || '',
      npub: map['npub'] || '',
      hex: map['hex'] || '',
      cardURI: map['card'] || '',
    };
  });
}

/**
 * Submit the rename-label form. Waits for the form-flash to land
 * (success or error). Returns {ok, message}.
 *
 * The handler does an outerHTML swap of #set-label-form, so after the
 * htmx request finishes the new form contains either:
 *   .form-flash         -> "Saved"  (ok)
 *   .form-flash.is-error -> error message
 */
export async function setLabel(page, newLabel) {
  await page.fill('#set-label-form input[name="label"]', newLabel);
  await Promise.all([
    page.waitForResponse((r) =>
      r.url().endsWith('/settings/identity/label') && r.request().method() === 'POST',
      { timeout: 10000 },
    ),
    page.click('#set-label-form button[type="submit"]'),
  ]);
  // The swap is outerHTML; give htmx a beat to finish DOM update.
  await page.waitForTimeout(400);
  const result = await page.evaluate(() => {
    const flash = document.querySelector('#set-label-form .form-flash');
    if (!flash) return { ok: false, message: '(no flash)' };
    const isError = flash.classList.contains('is-error');
    return { ok: !isError, message: (flash.textContent || '').trim() };
  });
  return result;
}

/**
 * Locate the row for `path`, return {path, value, error}. value is read
 * from the editable input or the readonly span depending on Editable.
 */
export async function getConfigRow(page, path) {
  const slug = pathSlug(path);
  return await page.evaluate((slug) => {
    const tr = document.getElementById(`cfg-${slug}`);
    if (!tr) return null;
    const input = tr.querySelector('input[name="value"]');
    const ro = tr.querySelector('.readonly-value');
    const err = tr.querySelector('.row-error');
    return {
      path: (tr.querySelector('td.key')?.textContent || '').trim(),
      value: input ? input.value : (ro ? (ro.textContent || '').trim() : ''),
      error: err ? (err.textContent || '').trim() : '',
    };
  }, slug);
}

/**
 * Set a config value. Returns {ok, message}.
 *
 * The row form does an outerHTML swap of tr#cfg-<slug>. On success the
 * new tr lacks .is-error and .row-error; on failure it gains both.
 */
export async function setConfigValue(page, path, value) {
  const slug = pathSlug(path);
  const rowSel = `#cfg-${slug}`;
  const form = await page.$(`${rowSel} form.inline-form`);
  if (!form) {
    throw new Error(`setConfigValue: row ${rowSel} has no editable form (readonly or missing)`);
  }
  await page.fill(`${rowSel} input[name="value"]`, value);
  await Promise.all([
    page.waitForResponse((r) =>
      r.url().endsWith('/settings/config') && r.request().method() === 'POST',
      { timeout: 10000 },
    ),
    page.click(`${rowSel} button[type="submit"]`),
  ]);
  await page.waitForTimeout(400);
  return await page.evaluate((rowSel) => {
    const tr = document.querySelector(rowSel);
    if (!tr) return { ok: false, message: '(row vanished)' };
    const err = tr.querySelector('.row-error');
    if (err) return { ok: false, message: (err.textContent || '').trim() };
    return { ok: true, message: '' };
  }, rowSel);
}

/**
 * Click the contact thread link in the sidebar for the given peer hex.
 * Waits for the thread body to render.
 */
export async function openContactThread(page, peerHex) {
  const sel = `aside.sidebar a[href="/thread/${peerHex}"]`;
  await page.waitForSelector(sel, { timeout: 5000 });
  await page.click(sel);
  await page.waitForSelector('#thread-body', { timeout: 5000 });
  await page.waitForTimeout(500);
}

/**
 * Send a message in the currently-open thread. Captures the set of
 * existing self-bubble event-ids, submits the form, and waits until
 * a NEW self bubble appears. Returns the new bubble's data-event-id.
 *
 * (We diff the set rather than waiting for ".bubble.self" so this works
 * whether or not the thread already had self bubbles.)
 */
export async function sendInThread(page, message) {
  const before = await page.evaluate(() =>
    Array.from(document.querySelectorAll('#thread-body .bubble.self'))
      .map((b) => b.getAttribute('data-event-id'))
      .filter(Boolean),
  );
  await page.fill('form.compose input[name="text"]', message);
  await page.click('form.compose button[type="submit"]');

  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    const after = await page.evaluate(() =>
      Array.from(document.querySelectorAll('#thread-body .bubble.self'))
        .map((b) => b.getAttribute('data-event-id'))
        .filter(Boolean),
    );
    const fresh = after.filter((id) => !before.includes(id));
    if (fresh.length > 0) return fresh[fresh.length - 1];
    await page.waitForTimeout(250);
  }
  throw new Error(`sendInThread: no new self bubble appeared within 10s for "${message}"`);
}

/**
 * Poll the open thread for an inbound bubble whose text contains
 * `expectedText`. Returns the matching bubble's data-event-id, or
 * throws on timeout.
 *
 * Note: `peerHex` is currently unused by the DOM query (the open thread
 * already filters to that peer) but is kept in the signature so callers
 * can document intent and so we can add a sanity assertion later if the
 * thread URL drifts.
 */
export async function waitForInbound(page, peerHex, expectedText, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const hit = await page.evaluate((needle) => {
      const bubbles = Array.from(document.querySelectorAll('#thread-body .bubble:not(.self)'));
      for (const b of bubbles) {
        const text = b.querySelector(':scope > div:last-child')?.textContent || '';
        if (text.includes(needle)) {
          return b.getAttribute('data-event-id') || '';
        }
      }
      return null;
    }, expectedText);
    if (hit !== null) return hit;
    await page.waitForTimeout(300);
  }
  throw new Error(
    `waitForInbound: peer=${peerHex} did not deliver "${expectedText}" within ${timeoutMs}ms`,
  );
}

/**
 * Read sidebar contact rows. Returns Array<{label, tier, href, pubkey}>.
 * Used by full-bidirectional.mjs to confirm both sides know each other.
 */
export async function listSidebarContacts(page) {
  return await page.evaluate(() => {
    const links = Array.from(document.querySelectorAll('aside.sidebar a[href^="/thread/"]'));
    return links.map((a) => {
      const href = a.getAttribute('href') || '';
      const pubkey = href.replace(/^\/thread\//, '');
      const label = (a.querySelector('span > span') ? null : a.querySelector('span'))?.firstChild?.textContent?.trim()
        || (a.querySelector('span')?.textContent || '').trim();
      const tier = (a.querySelector('span.tier-badge')?.textContent || '').trim();
      // The label cell has structure: <span>Label <span class="tier-badge">tier</span></span>
      // We extract by removing the tier text from the outer span.
      const outer = a.querySelector('span');
      let cleanLabel = label;
      if (outer && tier) {
        cleanLabel = (outer.textContent || '').replace(tier, '').trim();
      }
      return { label: cleanLabel, tier, href, pubkey };
    });
  });
}
