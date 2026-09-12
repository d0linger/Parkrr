const { test, expect } = require('@playwright/test');
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides, charges } = require('./helpers/overhaul-fixture');
test.use({ serviceWorkers: 'block' });

test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date('2026-09-10T12:00:00Z'));
  await page.addInitScript(() => localStorage.setItem('parkrr_portal_lang', 'de'));
});

test('expired sessions leave settings and return to login', async ({ page }) => {
  await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/auth/sessions': route => route.fulfill({ status: 401, json: { error: 'unauthorized' } }),
  } });
  await page.goto(origin + '/#/settings');
  await expect(page.locator('#login-view')).toBeVisible();
  await expect(page.locator('#app-view')).toBeHidden();
});

test('invoice-paid standalone charges use the paid filter and cannot be paid twice', async ({ page }) => {
  const paid = { ...charges[0], id: 10, description: 'Beglichene Rechnung', vehicle_id: null, invoiced: true, invoice_open: false, paid: false };
  const open = { ...paid, id: 11, description: 'Offene Rechnung', invoice_open: true };
  await mockUI(page, { role: 'admin', overrides: { ...overrides, '/charges': [paid, open, charges[0]] } });
  await page.goto(origin + '/#/finance');
  await page.getByRole('combobox', { name: 'Zahlstatus filtern' }).selectOption('paid');
  const paidRow = page.locator('.charge-card', { hasText: paid.description });
  await expect(paidRow).toBeVisible();
  await expect(paidRow).toContainText('bezahlt · Rechnung');
  await expect(paidRow.getByRole('radiogroup')).toHaveCount(0);
  await page.getByRole('combobox', { name: 'Zahlstatus filtern' }).selectOption('open');
  await expect(paidRow).toHaveCount(0);
  const openRow = page.locator('.charge-card', { hasText: open.description });
  await expect(openRow).toContainText('fakturiert');
  await expect(openRow.getByRole('radiogroup')).toHaveCount(0);
  await expect(page.locator('.charge-card', { hasText: charges[0].description }).getByRole('radiogroup')).toBeVisible();
});

test('billing validates payload, guards duplicate saves, preserves failed input and reloads persisted settings', async ({ page }) => {
  let saved = { ...overrides['/billing/settings'] };
  const writes = [];
  let release;
  const pending = new Promise(resolve => { release = resolve; });
  await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/billing/settings': async route => {
      if (route.request().method() === 'GET') return route.fulfill({ json: saved });
      expect(route.request().method()).toBe('POST');
      writes.push(route.request().postDataJSON());
      if (writes.length === 1) {
        await pending;
        return route.fulfill({ status: 503, json: { error: 'Speicher vorübergehend nicht erreichbar' } });
      }
      saved = writes.at(-1);
      return route.fulfill({ json: saved });
    },
  } });
  await page.goto(origin + '/#/billing');
  await page.getByLabel('Name / Firma', { exact: true }).fill('Prüfbetrieb ÄÖÜ');
  await page.getByLabel('Nächste Nummer', { exact: true }).fill('23');
  await page.getByLabel('Zahlungsziel (Tage)', { exact: true }).fill('0');
  await page.getByLabel('IBAN', { exact: true }).fill('AT611904300234573201');
  await page.getByLabel('Fußnote', { exact: true }).fill('Danke für Ihr Vertrauen.');
  await page.getByRole('button', { name: 'Einstellungen speichern' }).click();
  await expect(page.getByRole('button', { name: 'Speichert …' })).toBeDisabled();
  await page.locator('.billing-form').evaluate(form => { form.requestSubmit(); form.requestSubmit(); });
  expect(writes).toHaveLength(1);
  expect(writes[0]).toEqual({ seller_name: 'Prüfbetrieb ÄÖÜ', seller_address: 'Musterstraße 12', seller_uid: '',
    kleinunternehmer: true, ust_rate: 20, invoice_prefix: '2026-', next_invoice_no: 23, number_pad: 4,
    payment_terms_days: 0, iban: 'AT611904300234573201', bic: '', footer_note: 'Danke für Ihr Vertrauen.' });
  release();
  await expect(page.locator('.save-feedback')).toContainText('Nicht gespeichert');
  await expect(page.getByLabel('Name / Firma', { exact: true })).toHaveValue('Prüfbetrieb ÄÖÜ');
  await page.getByRole('button', { name: 'Einstellungen speichern' }).click();
  await expect(page.locator('.save-feedback')).toHaveText('Einstellungen gespeichert.');
  expect(writes).toHaveLength(2);
  await page.reload();
  await expect(page.getByLabel('Nächste Nummer', { exact: true })).toHaveValue('23');
  await expect(page.getByLabel('IBAN', { exact: true })).toHaveValue('AT611904300234573201');
});

test('portal QR decode failure is visible and can retry successfully', async ({ page }) => {
  let attempts = 0;
  await mockUI(page, { overrides: { ...overrides,
    '/portal/invoices/1/pay-qr': route => {
      attempts++;
      return route.fulfill(attempts === 1
        ? { contentType: 'image/png', body: 'invalid image' }
        : { contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="2" height="2"><rect width="2" height="2"/></svg>' });
    },
  } });
  await page.goto(origin + '/#/portal/demo');
  const disclosure = page.locator('.portal-qr summary');
  await disclosure.click();
  await expect(page.locator('.portal-qr [role=status]')).toContainText('QR-Code nicht verfügbar');
  await expect(page.locator('.portal-qr img')).toHaveCount(0);
  await disclosure.click();
  await disclosure.click();
  await expect(page.locator('.portal-qr img')).toBeVisible();
  await expect.poll(() => page.locator('.portal-qr img').evaluate(img => img.naturalWidth)).toBeGreaterThan(0);
  expect(attempts).toBe(2);
  await disclosure.click();
  await disclosure.click();
  expect(attempts).toBe(2);
});

test('portal contact submission uses the exact contract and ignores duplicate submits while pending', async ({ page }) => {
  const writes = [];
  let release;
  const pending = new Promise(resolve => { release = resolve; });
  await mockUI(page, { overrides: { ...overrides,
    '/portal/requests': async route => {
      expect(route.request().method()).toBe('POST');
      expect(route.request().headers().authorization).toBe('Bearer demo');
      writes.push(route.request().postDataJSON());
      await pending;
      return route.fulfill({ json: { id: 1 } });
    },
  } });
  await page.goto(origin + '/#/portal/demo');
  const contact = page.locator('.portal-form').first();
  await contact.getByLabel('Neue E-Mail', { exact: true }).fill('kein-email');
  await contact.getByRole('button').click();
  expect(writes).toHaveLength(0);
  await contact.getByLabel('Neue E-Mail', { exact: true }).fill('test@example.invalid');
  await contact.getByLabel('Neue Telefonnummer', { exact: true }).fill('  +43 123 456  ');
  await contact.getByRole('button').click();
  await expect(contact.getByRole('button')).toBeDisabled();
  await contact.evaluate(form => { form.requestSubmit(); form.requestSubmit(); });
  expect(writes).toEqual([{ kind: 'contact_update', email: 'test@example.invalid', phone: '+43 123 456', address: '' }]);
  release();
  await expect(page.getByText('Übermittelt — der Betreiber meldet sich.', { exact: true })).toBeVisible();
  await expect(contact.getByRole('button')).toBeEnabled();
});

for (const role of ['reader', 'editor']) {
  test(`${role} cannot reach admin settings or administrator shortcuts`, async ({ page }) => {
    const requests = await mockUI(page, { role, overrides });
    for (const route of ['billing', 'users', 'backup', 'audit']) {
      await page.goto(origin + '/#/' + route);
      await expect(page.getByText('Nur für Administratoren.', { exact: true })).toBeVisible();
    }
    expect(requests.filter(r => ['/billing/settings', '/users', '/backup/status', '/audit'].includes(r.path))).toHaveLength(0);
    await page.goto(origin + '/#/settings');
    await expect(page.getByRole('heading', { name: 'Aktive Sitzungen', exact: true })).toBeVisible();
    await expect(page.locator('.admin-links')).toHaveCount(0);
    await page.goto(origin + '/#/finance');
    await expect(page.locator('.charge-card').first()).toBeVisible();
    if (role === 'reader') await expect(page.locator('.charge-card').getByRole('radiogroup')).toHaveCount(0);
    else await expect(page.locator('.charge-card').getByRole('radiogroup').first()).toBeVisible();
  });
}

for (const destination of ['persons', 'portal/new-link']) {
  test(`late portal response cannot overwrite ${destination}`, async ({ page }) => {
    let release;
    let started = false;
    const pending = new Promise(resolve => { release = resolve; });
    await mockUI(page, { role: 'admin', overrides: { ...overrides,
      '/portal/summary': async route => {
        const token = route.request().headers().authorization;
        if (token === 'Bearer old-link') { started = true; await pending; }
        return route.fulfill({ json: { ...overrides['/portal/summary'], person_name: token === 'Bearer old-link' ? 'Alte Antwort' : 'Neue Antwort' } });
      },
    } });
    await page.goto(origin + '/#/dashboard');
    await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
    await page.evaluate(() => { location.hash = '#/portal/old-link'; });
    await expect.poll(() => started).toBe(true);
    await page.evaluate(route => { location.hash = '#/' + route; }, destination);
    if (destination === 'persons') await expect(page.locator('#page h2')).toHaveText('Personen');
    else await expect(page.locator('#portal-view')).toContainText('Neue Antwort');
    const oldResponse = page.waitForResponse(response => new URL(response.url()).pathname === '/api/portal/summary' && response.request().headers().authorization === 'Bearer old-link');
    release();
    await oldResponse;
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await expect(page.locator('#portal-view')).not.toContainText('Alte Antwort');
    if (destination === 'persons') {
      await expect(page.locator('#app-view')).toBeVisible();
      await expect(page.locator('#portal-view')).toBeHidden();
      await expect(page.locator('#page h2')).toHaveText('Personen');
    } else await expect(page.locator('#portal-view')).toContainText('Neue Antwort');
  });
}
