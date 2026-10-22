const { test: base, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

// Ausbauplan Paket 7 (Geld und Recht): Kontoauszug, Datenauskunft, Wertsicherung,
// Einstellvertrag, Kautionen.
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
  await expect(page.locator(route.startsWith('portal') ? '#portal-view' : '#page')).toHaveAttribute('aria-busy', 'false');
}
const json = (body, status = 200) => route => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
const bodyOf = route => JSON.parse(route.request().postData() || '{}');

test('Kontoauszug: Vorschlag über die Rechnungsnummer zuordnen, sonst von Hand', async ({ page }) => {
  const calls = [];
  const items = [
    { id: 1, booked_on: '2026-10-10T00:00:00Z', amount: 85, currency: 'EUR', name: 'Demo Berger 1', iban: '', remittance: 'Rechnung 2026-0007', status: 'offen',
      suggestion: { invoice_id: 7, number: '2026-0007', person_id: 1, person_name: 'Demo Berger 1', open_amount: 85, reason: 'Rechnungsnummer im Verwendungszweck', strong: true } },
    { id: 2, booked_on: '2026-10-11T00:00:00Z', amount: 40, currency: 'EUR', name: 'Unbekannt', iban: '', remittance: 'Miete', status: 'offen', suggestion: null },
  ];
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/bank-transactions': { items, open_invoices: [{ invoice_id: 7, number: '2026-0007', person_id: 1, person_name: 'Demo Berger 1', open_amount: 85 }] },
    '/bank-transactions/1/assign': route => { calls.push(['assign1', bodyOf(route)]); return json({ payment_id: 5, person_id: 1 })(route); },
    '/bank-transactions/2/assign': route => { calls.push(['assign2', bodyOf(route)]); return json({ payment_id: 6, person_id: 2 })(route); } } });
  await open(page, 'bank');
  await expect(page.locator('.bank-sugg.is-strong')).toContainText('Rechnung 2026-0007');
  expect((await new AxeBuilder({ page }).include('#page').analyze()).violations.map(v => v.id)).toEqual([]);
  await page.getByRole('button', { name: /der Rechnung 2026-0007 zuordnen/ }).click();
  await expect.poll(() => calls[0]).toEqual(['assign1', { invoice_id: 7 }]);
  await page.locator('.bank-row', { hasText: 'Miete' }).getByRole('button', { name: 'Zuordnen …' }).click();
  await page.locator('#modal #f_person_id').selectOption({ label: 'Demo Gruber 2' });
  await page.locator('#modal-submit').click();
  await expect.poll(() => calls[1]).toEqual(['assign2', { person_id: 2 }]);
});

test('Person: Kaution einbehalten verlangt einen Grund; Admin sieht die Datenauskunft', async ({ page }) => {
  const posts = [];
  await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/persons/1/deposits': route => {
      if (route.request().method() === 'POST') { posts.push(bodyOf(route)); return json({ id: 9, payment_id: 3 }, 201)(route); }
      return json({ balance: 200, entries: [{ id: 8, person_id: 1, kind: 'erhalten', amount: 200, booked_on: '2026-04-01T00:00:00Z', method: 'bar', note: '', payment_id: null }] })(route);
    } } });
  await open(page, 'person/1');
  const card = page.locator('.person-deposit');
  await expect(card).toContainText('offen 200,00 €');
  await expect(page.getByRole('link', { name: 'Datenauskunft (ZIP)' })).toHaveAttribute('href', '/api/persons/1/export');
  await card.getByRole('button', { name: 'Einbehalten' }).click();
  await expect(page.locator('#modal #f_amount')).toHaveValue('200.00');
  await page.locator('#modal #f_amount').fill('50');
  await page.locator('#modal #f_note').fill('Delle am Heck');
  await page.locator('#modal-submit').click();
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0]).toMatchObject({ kind: 'einbehalten', amount: 50, note: 'Delle am Heck', booked_on: '2026-10-16' });
});

test('Wertsicherung: Vorschau zeigt alt und neu, Anpassen ohne Mail ist Standard', async ({ page }) => {
  let applied = null;
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/cpi': [{ month: '2026-09-01T00:00:00Z', value: 126.6 }, { month: '2025-10-01T00:00:00Z', value: 120 }],
    '/indexation': { categories: [{ id: 1, name: 'Wohnmobil', base_month: '2025-10', threshold: 5 }] },
    '/indexation/preview': { month: '2026-09-01T00:00:00Z', value: 126.6, categories: [{ category_id: 1, name: 'Wohnmobil', base_month: '2025-10-01T00:00:00Z', base_value: 120,
      change_pct: 5.5, threshold: 5, due: true, new_monthly: 73.85, new_yearly: 738.5,
      vehicles: [{ vehicle_id: 1, person_id: 1, label: 'Demo Wohnmobil 1', person_name: 'Demo Berger 1', period: 'monthly', old_rate: 60, new_rate: 63.3, effective_from: '2026-11-01T00:00:00Z' }],
      skipped: [] }] },
    '/indexation/apply': route => { applied = bodyOf(route); return json({ categories: 1, vehicles: 1, mails: 0 })(route); } } });
  await open(page, 'tariffs');
  const card = page.locator('.indexation-card');
  await card.locator('summary').click();
  await card.getByRole('button', { name: 'Vorschau' }).click();
  await expect(card.locator('.idx-table')).toContainText('63,30 €');
  await expect(card.locator('.idx-table')).toContainText('1.11.2026');
  expect((await new AxeBuilder({ page }).include('.indexation-card').analyze()).violations.map(v => v.id)).toEqual([]);
  await card.getByRole('button', { name: 'Anpassen' }).click();
  await page.locator('#confirm').getByRole('button', { name: 'Anpassen' }).click();
  await expect.poll(() => applied).toEqual({ month: '2026-09', category_ids: [1], notify: false });
});

test('Gefährt: Vertrag als PDF, künftige Preisänderung lässt sich zurücknehmen', async ({ page }) => {
  let deleted = false;
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/contract-template': { template: { id: 2, version: 2, title: 'Einstellvertrag', body: '…', created_at: '2026-10-01T00:00:00Z' }, placeholders: [] },
    '/vehicles/1/rate-changes': [{ id: 4, effective_from: '2026-11-01T00:00:00Z', rate: 63.3, previous_rate: 60, index_month: '2026-09-01T00:00:00Z', reason: 'Wertsicherung VPI 11/2025 → 09/2026 (+5,50 %)' }],
    '/vehicle-rate-changes/4': route => { deleted = true; return json({ deleted: true })(route); } } });
  await open(page, 'vehicle/1');
  await expect(page.getByRole('link', { name: 'Vertrag' })).toHaveAttribute('href', '/api/vehicles/1/contract.pdf');
  await page.getByRole('button', { name: 'Preisänderung ab 1.11.2026 zurücknehmen' }).click();
  await page.locator('#confirm').getByRole('button', { name: 'Zurücknehmen' }).click();
  await expect.poll(() => deleted).toBe(true);
});

test('Portal: Vertrag erst lesen, dann annehmen', async ({ page }) => {
  let accepted = null;
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/portal/summary': { ...overrides['/portal/summary'], contract: { version: 2, title: 'Einstellvertrag' },
      vehicles: [{ id: 1, label: 'Demo Wohnmobil 1', status: 'stored' }] },
    '/portal/contract': route => route.fulfill({ contentType: 'application/pdf', body: '%PDF-1.4\n%%EOF' }),
    '/portal/contract/accept': route => { accepted = bodyOf(route); return json({ id: 1, version: 2 }, 201)(route); } } });
  await open(page, 'portal/demo');
  const card = page.locator('.portal-contract');
  const accept = card.getByRole('button', { name: 'Gelesen und angenommen' });
  await expect(accept).toBeDisabled();
  const popup = page.waitForEvent('popup');
  await card.getByRole('button', { name: 'Vertrag lesen' }).click();
  await (await popup).close();
  await expect(accept).toBeEnabled();
  await accept.click();
  await expect.poll(() => accepted).toEqual({ vehicle_id: 1, version: 2 });
});
