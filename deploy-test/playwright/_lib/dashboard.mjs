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

// ─── Phase 2: Contacts pane helpers ───────────────────────────────────────
//
// Selectors below mirror the actual phase-2 templates in
// internal/dashboard/templates/{settings_contacts,contact_row,
// contact_detail,contact_scan}.html. The row template does NOT carry a
// `data-pubkey` attribute; the full hex pubkey only appears inside the
// row's `hx-get="/settings/contacts/<hex>"` URL. We parse it out from
// there and use the `id="contact-<short>"` (where <short> is the first
// 16 hex chars per `truncate 16` in the template) for stable row
// addressing.

/**
 * Read all `tr.contact-row` rows in the address book table.
 *
 * Returns Array<{pubkey, pubkeyShort, label, tier}>. Empty array if the
 * empty-state placeholder (`tr.contacts-empty`) is showing instead.
 *
 * `pubkey` is recovered from the row's hx-get URL since
 * `contact_row.html` doesn't expose a data-pubkey attribute. `pubkeyShort`
 * is the row's id slug after `contact-`.
 */
export async function listContactsRows(page) {
  return await page.evaluate(() => {
    const rows = Array.from(document.querySelectorAll('tr.contact-row'));
    return rows.map((tr) => {
      const id = tr.getAttribute('id') || '';
      const pubkeyShort = id.startsWith('contact-') ? id.slice('contact-'.length) : '';
      const hxGet = tr.getAttribute('hx-get') || '';
      // hxGet is "/settings/contacts/<hex>"
      const m = hxGet.match(/\/settings\/contacts\/([0-9a-f]+)$/i);
      const pubkey = m ? m[1] : '';
      const label = (tr.querySelector('.contact-label')?.textContent || '').trim();
      const tier = (tr.querySelector('.contact-tier .tier-badge')?.textContent || '').trim();
      return { pubkey, pubkeyShort, label, tier };
    });
  });
}

/**
 * Submit the contact-add form with a card URI and optional label override,
 * clicking the Add button. Waits for either a new row to appear in the
 * table OR an error chip in `#contact-add-result` (or the pane re-rendering
 * with a `.form-flash.is-error`).
 *
 * Returns {ok, message}. On `ok` the new row is in the DOM; on failure the
 * message is the visible error text.
 */
export async function addContactByCard(page, cardURI, labelOverride = '') {
  await page.fill('#contact-add-form input[name="card"]', cardURI);
  await page.fill('#contact-add-form input[name="label"]', labelOverride);
  const beforeIDs = await page.evaluate(() =>
    Array.from(document.querySelectorAll('tr.contact-row')).map((r) => r.id),
  );

  // The Add button is the second submit (without formnovalidate); pick by
  // its hx-post attribute to avoid relying on DOM order.
  const addBtn = await page.$('#contact-add-form button[hx-post="/settings/contacts"]');
  if (!addBtn) throw new Error('addContactByCard: Add button not found');
  await Promise.all([
    page.waitForResponse((r) =>
      r.url().endsWith('/settings/contacts') && r.request().method() === 'POST',
      { timeout: 10000 },
    ),
    addBtn.click(),
  ]);
  await page.waitForTimeout(500);

  // Success path: handler returns the full pane (#settings-pane innerHTML
  // swap) with the new row appended. Detect by diffing the row id set.
  const after = await page.evaluate(() =>
    Array.from(document.querySelectorAll('tr.contact-row')).map((r) => r.id),
  );
  const fresh = after.filter((id) => !beforeIDs.includes(id));
  if (fresh.length > 0) {
    return { ok: true, message: '' };
  }

  // Failure path: an error flash either in #contact-add-result (when the
  // scan endpoint repurposed the slot) or as a top-of-pane .form-flash.is-error.
  const err = await page.evaluate(() => {
    const local = document.querySelector('#contact-add-result .form-flash.is-error');
    if (local) return (local.textContent || '').trim();
    const top = document.querySelector('.contacts-pane .form-flash.is-error');
    if (top) return (top.textContent || '').trim();
    return '';
  });
  return { ok: false, message: err || '(no error chip found, no row appeared)' };
}

/**
 * Submit the contact-add form with a card URI and click Scan (the
 * formnovalidate button). Waits for the preview block in
 * `#contact-add-result` to land. Returns the parsed preview as
 * {pubkey, label, relay, alreadyContact}.
 *
 * The Scan endpoint renders contact_scan.html into `#contact-add-result`
 * via `hx-target` on the form. Each labelled cell sits in a `dl.scan-grid`.
 */
export async function scanCardPreview(page, cardURI) {
  await page.fill('#contact-add-form input[name="card"]', cardURI);
  const scanBtn = await page.$('#contact-add-form button[hx-post="/settings/contacts/scan"]');
  if (!scanBtn) throw new Error('scanCardPreview: Scan button not found');
  await Promise.all([
    page.waitForResponse((r) =>
      r.url().endsWith('/settings/contacts/scan') && r.request().method() === 'POST',
      { timeout: 10000 },
    ),
    scanBtn.click(),
  ]);
  // Wait for either the preview region or an error chip.
  await page.waitForFunction(() => {
    const root = document.querySelector('#contact-add-result');
    if (!root) return false;
    return !!root.querySelector('.contact-scan, .form-flash.is-error');
  }, null, { timeout: 5000 });
  await page.waitForTimeout(200);

  return await page.evaluate(() => {
    const root = document.querySelector('#contact-add-result');
    const errEl = root?.querySelector('.form-flash.is-error');
    if (errEl) {
      return { error: (errEl.textContent || '').trim() };
    }
    const dl = root?.querySelector('dl.scan-grid');
    if (!dl) return { error: '(no scan-grid)' };
    const dts = Array.from(dl.querySelectorAll('dt'));
    const map = {};
    for (const dt of dts) {
      const key = (dt.textContent || '').trim().toLowerCase();
      const dd = dt.nextElementSibling;
      if (!dd) continue;
      map[key] = (dd.textContent || '').trim();
    }
    const already = !!root?.querySelector(
      '.contact-scan .form-flash.is-error',
    );
    // Strip the "— none" placeholder that appears when no relay is on file.
    const relay = map['relay'] && !/^—\s*none/.test(map['relay']) ? map['relay'] : '';
    return {
      pubkey: map['pubkey'] || '',
      label: map['label'] || '',
      relay,
      alreadyContact: already,
    };
  });
}

/**
 * Click a contact row (by full hex pubkey) and wait for the
 * `.contact-detail` pane to render. The row's hx-get targets
 * `#settings-pane`, so the whole pane is replaced.
 */
export async function openContactDetail(page, pubkey) {
  const lower = String(pubkey).toLowerCase();
  // Find the row by its hx-get URL (the only place the full hex appears).
  const sel = `tr.contact-row[hx-get="/settings/contacts/${lower}"]`;
  const row = await page.$(sel);
  if (!row) {
    throw new Error(`openContactDetail: no row with hx-get for pubkey ${lower.slice(0, 16)}…`);
  }
  await Promise.all([
    page.waitForResponse((r) =>
      r.url().endsWith(`/settings/contacts/${lower}`) && r.request().method() === 'GET',
      { timeout: 10000 },
    ),
    row.click(),
  ]);
  await page.waitForSelector('.contact-detail', { timeout: 5000 });
  await page.waitForTimeout(300);
}

/**
 * Read the open `.contact-detail` pane. Returns
 * {label, tier, npub, pubkey, relays:[], labelFlash, tierFlash}.
 *
 * Used both for assertions and for follow-up edits (the form inputs are
 * the editable affordances).
 */
export async function readContactDetail(page) {
  return await page.evaluate(() => {
    const root = document.querySelector('.contact-detail');
    if (!root) return null;
    const dl = root.querySelector('dl.identity-card');
    const pickDD = (label) => {
      const dts = Array.from(dl?.querySelectorAll('dt') || []);
      const dt = dts.find((d) => (d.textContent || '').trim().toLowerCase() === label);
      return dt?.nextElementSibling;
    };
    const labelDD = pickDD('label');
    const tierDD = pickDD('tier');
    const npubDD = pickDD('npub');
    const hexDD = pickDD('hex');
    const relayDD = pickDD('relays');
    const relays = relayDD
      ? Array.from(relayDD.querySelectorAll('li')).map((li) => (li.textContent || '').trim())
      : [];
    const labelFlash = root.querySelector('.contact-label-form .form-flash');
    const tierFlash = root.querySelector('.contact-tier-form .form-flash');
    return {
      label: (labelDD?.textContent || '').trim(),
      tier: (tierDD?.querySelector('.tier-badge')?.textContent || '').trim(),
      npub: (npubDD?.textContent || '').trim(),
      pubkey: (hexDD?.textContent || '').trim(),
      relays,
      labelFlash: labelFlash ? (labelFlash.textContent || '').trim() : '',
      tierFlash: tierFlash ? (tierFlash.textContent || '').trim() : '',
    };
  });
}

/**
 * Type a new label in the detail pane's rename form and submit. Waits
 * for the pane to re-render (the form does an innerHTML swap of
 * `#settings-pane`). Returns {ok, message} based on the resulting
 * `.form-flash` (success) or `.form-flash.is-error`.
 */
export async function setContactLabelInDetail(page, newLabel) {
  await page.fill('.contact-label-form input[name="label"]', newLabel);
  await Promise.all([
    page.waitForResponse((r) =>
      /\/settings\/contacts\/[0-9a-f]+\/label$/i.test(r.url())
        && r.request().method() === 'POST',
      { timeout: 10000 },
    ),
    page.click('.contact-label-form button[type="submit"]'),
  ]);
  await page.waitForSelector('.contact-detail', { timeout: 5000 });
  await page.waitForTimeout(400);
  return await page.evaluate(() => {
    const errEl = document.querySelector('.contact-label-form .form-flash.is-error');
    if (errEl) return { ok: false, message: (errEl.textContent || '').trim() };
    const okEl = document.querySelector('.contact-label-form .form-flash');
    return { ok: true, message: okEl ? (okEl.textContent || '').trim() : '' };
  });
}

/**
 * Pick a tier in the detail pane's tier select and submit. Waits for the
 * pane to re-render. Returns {ok, message}. Valid tiers:
 * "master" / "friend" / "acquaintance" / "blocked".
 */
export async function setContactTierInDetail(page, tier) {
  await page.selectOption('.contact-tier-form select[name="tier"]', tier);
  await Promise.all([
    page.waitForResponse((r) =>
      /\/settings\/contacts\/[0-9a-f]+\/tier$/i.test(r.url())
        && r.request().method() === 'POST',
      { timeout: 10000 },
    ),
    page.click('.contact-tier-form button[type="submit"]'),
  ]);
  await page.waitForSelector('.contact-detail', { timeout: 5000 });
  await page.waitForTimeout(400);
  return await page.evaluate(() => {
    const errEl = document.querySelector('.contact-tier-form .form-flash.is-error');
    if (errEl) return { ok: false, message: (errEl.textContent || '').trim() };
    const okEl = document.querySelector('.contact-tier-form .form-flash');
    return { ok: true, message: okEl ? (okEl.textContent || '').trim() : '' };
  });
}

/**
 * Click the row's Remove button (or the detail-pane's Remove), wait for
 * the typed-confirm modal to render in `#modal`, fill the expected
 * phrase, and click the danger button. Returns {ok} once the row
 * disappears from the DOM.
 *
 * `expectedPhrase` is the operator-typed string (today: the contact's
 * own label, see §7 of the dashboard-feature-parity spec).
 */
export async function removeContactWithConfirm(page, expectedPhrase) {
  // The Remove button is either the table-row danger-button-sm or the
  // detail-pane danger-button. Both share the hx-get to /confirm-remove.
  const removeBtn = await page.$(
    'button[hx-get*="/confirm-remove"]',
  );
  if (!removeBtn) {
    throw new Error('removeContactWithConfirm: no Remove button found in current pane');
  }
  await removeBtn.click();
  // Wait for the typed-confirm modal to render into #modal.
  await page.waitForSelector('.confirm-modal .confirm-card', { timeout: 5000 });
  await page.waitForTimeout(200);
  // Snapshot existing row ids so we can wait for one to vanish.
  const beforeIDs = await page.evaluate(() =>
    Array.from(document.querySelectorAll('tr.contact-row')).map((r) => r.id),
  );
  // Type the phrase; the danger button is initially disabled and enables on match.
  await page.fill('.confirm-modal input[name="confirm"]', expectedPhrase);
  await page.waitForFunction(
    () => {
      const btn = document.querySelector('.confirm-modal button.danger');
      return btn && !btn.disabled;
    },
    null,
    { timeout: 3000 },
  );
  await Promise.all([
    page.waitForResponse((r) =>
      /\/settings\/contacts\/[0-9a-f]+\/remove$/i.test(r.url())
        && r.request().method() === 'POST',
      { timeout: 10000 },
    ),
    page.click('.confirm-modal button.danger'),
  ]);
  // Wait for at least one row id to disappear OR for the pane to re-render
  // without the row.
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    const after = await page.evaluate(() =>
      Array.from(document.querySelectorAll('tr.contact-row')).map((r) => r.id),
    );
    const gone = beforeIDs.filter((id) => !after.includes(id));
    if (gone.length > 0 || (beforeIDs.length > 0 && after.length === 0)) {
      return { ok: true };
    }
    await page.waitForTimeout(150);
  }
  return { ok: false };
}

/**
 * List invite rows currently rendered on the Invites pane. Pass
 * 'active' (default), 'history' (the collapsed expired+revoked section
 * inside the <details>), or 'all'.
 *
 * Returns Array<{idShort, redeemer, uses, cap, expiresText, issuedText, status}>.
 * Selectors mirror invite_row.html.
 */
export async function listInviteRows(page, section = 'active') {
  return await page.evaluate((sec) => {
    let rows;
    if (sec === 'active') {
      rows = Array.from(document.querySelectorAll('#invite-rows-active tr.invite-row'));
    } else if (sec === 'history') {
      rows = Array.from(document.querySelectorAll('details.invites-history tr.invite-row'));
    } else {
      rows = Array.from(document.querySelectorAll('tr.invite-row'));
    }
    return rows.map((tr) => {
      const idShort = (tr.querySelector('td.invite-id code')?.textContent || '').trim();
      const redeemerEm = tr.querySelector('td.invite-redeemer em');
      const redeemer = redeemerEm
        ? (redeemerEm.textContent || '').trim()
        : ((tr.querySelector('td.invite-redeemer span.muted')?.textContent || '').trim() || null);
      const uses = (tr.querySelector('td.invite-uses .uses-count')?.textContent || '').trim();
      const cap = (tr.querySelector('td.invite-uses .uses-cap')?.textContent || '').trim();
      const expiresText = (tr.querySelector('td.invite-expiry')?.textContent || '').trim();
      const issuedText = (tr.querySelector('td.invite-issued')?.textContent || '').trim();
      const status = Array.from(tr.classList).find((c) => c.startsWith('is-'))?.replace(/^is-/, '') || '';
      return { idShort, redeemer, uses, cap, expiresText, issuedText, status };
    });
  }, section);
}

/**
 * Submit the "Issue an invitation" form. Returns
 * {ok, idShort, uri, error}. On success, the create-success slip is
 * rendered and we read the URI out of it.
 */
export async function createInvite(page, opts = {}) {
  const { redeemerLabel = '', expires = '', maxUses = '' } = opts;
  const form = await page.$('form.invite-issue-form');
  if (!form) {
    return { ok: false, error: 'no invite-issue-form found on page' };
  }
  if (redeemerLabel) {
    await page.fill('form.invite-issue-form input[name="redeemer_label"]', redeemerLabel);
  }
  if (expires) {
    await page.fill('form.invite-issue-form input[name="expires"]', expires);
  }
  if (maxUses) {
    await page.fill('form.invite-issue-form input[name="max_uses"]', maxUses);
  }
  await Promise.all([
    page.waitForResponse((r) =>
      /\/settings\/invites$/i.test(r.url()) && r.request().method() === 'POST',
      { timeout: 10000 },
    ),
    page.click('form.invite-issue-form button[type="submit"]'),
  ]);
  // Wait for either the slip or an error flash to land in #settings-pane.
  await page.waitForFunction(() =>
    document.querySelector('aside.invite-slip') ||
    document.querySelector('.invite-issue .form-flash.is-error'),
    null, { timeout: 5000 });
  const result = await page.evaluate(() => {
    const err = document.querySelector('.invite-issue .form-flash.is-error');
    if (err) return { ok: false, error: (err.textContent || '').trim() };
    const slip = document.querySelector('aside.invite-slip:not(.is-redeem)');
    if (!slip) return { ok: false, error: 'no slip rendered' };
    const idShort = (slip.querySelector('h4 code')?.textContent || '').trim();
    const uri = (slip.querySelector('code.invite-uri')?.textContent || '').trim();
    return { ok: true, idShort, uri };
  });
  return result;
}

/**
 * Submit the "Redeem an invitation" form with the given URI/token.
 * Returns {ok, issuerNpub, error}.
 */
export async function redeemInviteOnDashboard(page, token) {
  await page.fill('form.invite-redeem-form input[name="token"]', token);
  await Promise.all([
    page.waitForResponse((r) =>
      /\/settings\/invites\/redeem$/i.test(r.url()) && r.request().method() === 'POST',
      { timeout: 15000 },
    ),
    page.click('form.invite-redeem-form button[type="submit"]'),
  ]);
  await page.waitForFunction(() =>
    document.querySelector('aside.invite-slip.is-redeem') ||
    document.querySelector('.invite-redeem .form-flash.is-error'),
    null, { timeout: 8000 });
  const result = await page.evaluate(() => {
    const err = document.querySelector('.invite-redeem .form-flash.is-error');
    if (err) return { ok: false, error: (err.textContent || '').trim() };
    const slip = document.querySelector('aside.invite-slip.is-redeem');
    if (!slip) return { ok: false, error: 'no redeem slip rendered' };
    const dds = Array.from(slip.querySelectorAll('dd'));
    const issuerNpub = dds[0] ? (dds[0].textContent || '').trim() : '';
    return { ok: true, issuerNpub };
  });
  return result;
}

/**
 * Click Revoke on the active row whose IDShort matches `idShort`, then
 * type the confirm phrase and submit. Returns {ok}.
 */
export async function revokeInviteWithConfirm(page, idShort) {
  const rowSelector = `tr#invite-${idShort} button[hx-get*="/confirm-revoke"]`;
  const btn = await page.$(rowSelector);
  if (!btn) {
    throw new Error(`revokeInviteWithConfirm: no Revoke button found for ${idShort}`);
  }
  await btn.click();
  await page.waitForSelector('.confirm-modal .confirm-card', { timeout: 5000 });
  await page.waitForTimeout(200);
  await page.fill('.confirm-modal input[name="confirm"]', idShort);
  await page.waitForFunction(() => {
    const b = document.querySelector('.confirm-modal button.danger');
    return b && !b.disabled;
  }, null, { timeout: 3000 });
  await Promise.all([
    page.waitForResponse((r) =>
      /\/settings\/invites\/[0-9a-f]+\/revoke$/i.test(r.url()) &&
      r.request().method() === 'POST',
      { timeout: 10000 },
    ),
    page.click('.confirm-modal button.danger'),
  ]);
  // Wait for the modal to be cleared (OOB swap empties #modal).
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    const stillOpen = await page.$('.confirm-modal .confirm-card');
    if (!stillOpen) return { ok: true };
    await page.waitForTimeout(150);
  }
  return { ok: false };
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
