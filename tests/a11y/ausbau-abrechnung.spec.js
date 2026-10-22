const { test: base, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides, invoice } = require('./helpers/overhaul-fixture');

// Ausbauplan Paket 4 (Abrechnung und Mail): Rechnung per Mail mit Versandstand,
// Sammel-Erinnerung, Vorschau des Rechnungslaufs, Mail-Automatik und „nicht mahnen“.
const test = base.extend({
  browserErrors: [async ({ page }, use) => {
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
    await use(errors);
    expect(errors, 'uncaught browser and console errors').toEqual([]);
  }, { auto: true }],
});
test.use({ serviceWorkers: 'block', locale: 'de-DE', timezoneId: 'Europe/Vienna', reducedMotion: 'reduce' });
test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date('2026-10-16T10:00:00Z'));
});
async function open(page, route) {
  await page.goto(origin + '/#/' + route);
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
}
const json = (body, status = 200) => route => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
const mailOn = { '/auth/capabilities': { mail: true } };

test('Rechnung: per Mail senden und Versandstand mit „Erneut senden“', async ({ page }) => {
  const calls = [];
  await mockUI(page, { role: 'editor', overrides: { ...overrides, ...mailOn,
    '/invoices/1/send': route => { calls.push(route.request().method() + ' send'); return json({ queued: true, to: 'demo0@example.invalid', outbox_id: 7 }, 202)(route); },
    '/invoices/1/mailings': json([
      { id: 7, kind: 'invoice', recipient: 'demo0@example.invalid', status: 'sent', attempts: 1, created_at: '2026-10-16T09:00:00Z', sent_at: '2026-10-16T09:00:20Z', next_attempt_at: '2026-10-16T09:00:00Z' },
      { id: 6, kind: 'reminder', level: 1, recipient: 'demo0@example.invalid', status: 'failed', attempts: 6, last_error: 'Endgültig fehlgeschlagen nach 6 Versuchen: 550 mailbox unavailable', created_at: '2026-10-10T09:00:00Z', next_attempt_at: '2026-10-11T09:00:00Z' }]),
    '/mail-outbox/6/retry': route => { calls.push('retry 6'); return json({ queued: true })(route); } } });
  await open(page, 'invoices/1');
  const versand = page.locator('.invoice-mailings');
  await expect(versand).toContainText('Rechnung als PDF');
  await expect(versand).toContainText('zugestellt');
  await expect(versand).toContainText('Zahlungserinnerung (automatisch)');
  await expect(versand).toContainText('550 mailbox unavailable');
  expect((await new AxeBuilder({ page }).include('.invoice-mailings').analyze()).violations.map(v => v.id)).toEqual([]);
  await versand.getByRole('button', { name: 'Erneut senden' }).click();
  await expect.poll(() => calls).toContain('retry 6');
  await page.getByRole('button', { name: 'Per Mail' }).click();
  await page.locator('#confirm').getByRole('button', { name: 'Senden' }).click();
  await expect.poll(() => calls).toContain('POST send');
});

test('Rechnungsliste: Überfällige erinnern schickt in Paketen und nennt Fehler', async ({ page }) => {
  const overdue = Array.from({ length: 12 }, (_, i) => ({ ...invoice, id: 100 + i, number: '2026-' + (100 + i), person_name: 'Demo Gruber ' + i, reminder_count: i % 3 }));
  const posted = [];
  await mockUI(page, { role: 'editor', overrides: { ...overrides, ...mailOn,
    '/invoices': overdue,
    '/invoices/remind': route => {
      const ids = JSON.parse(route.request().postData()).ids;
      posted.push(ids.length);
      return json({ sent: ids.length - (ids.includes(101) ? 1 : 0), results: ids.map(id => ({ invoice_id: id, sent: id !== 101, error: id === 101 ? 'Kein E-Mail-Kontakt für diese Person hinterlegt' : undefined })) })(route);
    } } });
  await open(page, 'invoices');
  await page.getByRole('button', { name: 'Überfällige erinnern' }).click();
  const dlg = page.locator('#modal');
  await expect(dlg.locator('.remind-row')).toHaveCount(12);
  await expect(dlg.locator('.remind-row').nth(2)).toContainText('2. Mahnung');
  await dlg.getByRole('button', { name: 'Senden' }).click();
  await expect(dlg.locator('.remind-out')).toContainText('11 von 12 versendet.');
  await expect(dlg.locator('.remind-out li')).toHaveText([/2026-101: Kein E-Mail-Kontakt/]);
  expect(posted).toEqual([10, 2]);
});

test('Rechnungsliste: Vorschau Rechnungslauf zeigt Beträge und Hinweise', async ({ page }) => {
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/invoices/preview': json({ count: 2, total: 765.5, as_of: '2026-10-16T10:00:00Z', persons: [
      { person_id: 1, name: 'Demo Berger 1', subtotal: 680, total: 680, lines: [{ label: 'Einstellplatz: Demo Wohnmobil 1 · 2026-09', amount: 680 }], warnings: [] },
      { person_id: 2, name: 'Demo Gruber 2', subtotal: 85.5, total: 85.5, lines: [{ label: 'Innenreinigung', amount: 85.5 }], warnings: ['Keine E-Mail-Adresse: Versand nur auf Papier oder über das Portal.'] }] }) } });
  await open(page, 'invoices');
  await page.getByRole('button', { name: 'Vorschau Rechnungslauf' }).click();
  const dlg = page.locator('#modal');
  await expect(dlg).toContainText('2 Rechnungen über zusammen 765,50 €');
  await expect(dlg.locator('.preview-person.has-warn')).toHaveCount(1);
  await dlg.locator('.preview-person.has-warn summary').click();
  await expect(dlg.locator('.preview-person.has-warn')).toContainText('Keine E-Mail-Adresse');
  await expect(dlg.locator('.preview-person.has-warn').getByRole('button', { name: 'Rechnung erstellen' })).toBeVisible();
});

test('Einstellungen: Mail-Automatik speichern; Person: nicht automatisch mahnen', async ({ page }) => {
  const puts = [];
  await mockUI(page, { role: 'admin', overrides: { ...overrides, ...mailOn,
    '/mail-automation': route => {
      if (route.request().method() === 'PUT') { puts.push(JSON.parse(route.request().postData())); return json(puts.at(-1))(route); }
      return json({ auto_send_invoices: false, auto_dunning: false, dunning_days: 14 })(route);
    },
    '/persons/1/dunning': route => { puts.push({ person: JSON.parse(route.request().postData()) }); return json({ no_dunning: true })(route); } } });
  await open(page, 'billing');
  const card = page.locator('.mail-automation');
  await card.getByText('Automatisch mahnen').click();
  await card.locator('#ma_days').fill('21');
  await card.getByRole('button', { name: 'Mail-Automatik speichern' }).click();
  await expect(card.locator('.mail-feedback')).toHaveText('Gespeichert.');
  expect(puts[0]).toEqual({ auto_send_invoices: false, auto_dunning: true, dunning_days: 21, end_reminder_days: 0 });
  await open(page, 'persons/1');
  await page.getByText('Nicht automatisch mahnen').click();
  await expect.poll(() => puts.length).toBe(2);
  expect(puts[1]).toEqual({ person: { no_dunning: true } });
});
