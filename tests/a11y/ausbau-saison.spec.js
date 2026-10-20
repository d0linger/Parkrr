const { test: base, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

// Ausbauplan Paket 5 (Saison und Kunden): Saisonwechsel, Abholzeitfenster,
// Verlängerung und Zeitpunktwahl im Portal, Warteliste.
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

test('Saisonwechsel: nur abgeholte, noch nicht wieder eingestellte; Optionen gehen mit', async ({ page }) => {
  const base = overrides['/vehicles'][0];
  const fleet = [
    { ...base, id: 21, label: 'Eriba alt', license_plate: 'S-1', status: 'collected', end_date: '2026-04-15', effective_rate: 60, archived: true },
    { ...base, id: 22, label: 'Knaus alt', license_plate: 'S-2', status: 'collected', end_date: '2026-04-20', effective_rate: 85, archived: true },
    { ...base, id: 23, label: 'Knaus neu', license_plate: 'S-2', status: 'stored', end_date: null }, // S-2 ist schon wieder da
  ];
  const posted = [];
  await mockUI(page, { role: 'editor', overrides: { ...overrides, '/vehicles': fleet,
    '/vehicles/21/duplicate': route => { posted.push(bodyOf(route)); return json({ id: 99 }, 201)(route); } } });
  await open(page, 'vehicles');
  await page.getByRole('button', { name: 'Saisonwechsel' }).click();
  const dlg = page.locator('#modal');
  await expect(dlg.locator('.remind-row')).toHaveCount(1);
  await expect(dlg.locator('.remind-row')).toContainText('Eriba alt');
  await expect(dlg.locator('#sr_start')).toHaveValue('2026-11-01');
  await expect(dlg.locator('#sr_reserve')).toBeChecked();
  await dlg.getByText('Aktueller Tarif statt Vorjahrespreis').click();
  await dlg.getByRole('button', { name: 'Einstellen' }).click();
  await expect(dlg.locator('.remind-out')).toContainText('1 von 1 eingestellt.');
  expect(posted).toEqual([{ start_date: '2026-11-01', current_tariff: true, reserve: true }]);
});

test('Einstellungen: Abholzeitfenster anlegen und speichern', async ({ page }) => {
  let saved = null;
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/pickup-windows': route => {
      if (route.request().method() === 'PUT') { saved = bodyOf(route); return json(saved.map((w, i) => ({ ...w, id: i + 1 })))(route); }
      return json([])(route);
    } } });
  await open(page, 'settings');
  const card = page.locator('.pickup-windows');
  await expect(card).toContainText('Noch keine Fenster.');
  await card.getByRole('button', { name: '+ Fenster' }).click();
  await card.getByLabel('Abholungen je Zeitpunkt').fill('3');
  await card.getByLabel('Abholungen je Zeitpunkt').dispatchEvent('change');
  await card.getByRole('button', { name: 'Fenster speichern' }).click();
  await expect(card.locator('.mail-feedback')).toHaveText('Gespeichert.');
  expect(saved).toEqual([{ weekday: 6, start_min: 540, end_min: 720, slot_min: 30, capacity: 3 }]);
  expect((await new AxeBuilder({ page }).include('.pickup-windows').analyze()).violations.map(v => v.id)).toEqual([]);
});

test('Portal: Termin aus den freien Zeitpunkten, Verlängerung für ein eigenes Gefährt', async ({ page }) => {
  const sent = [];
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/portal/summary': { ...overrides['/portal/summary'], vehicles: [{ id: 1, label: 'Demo Wohnmobil 1', status: 'stored', end_date: '2026-11-30' }] },
    '/portal/pickup-slots': json([{ date: '2026-10-24', time: '09:00', free: 2 }, { date: '2026-10-24', time: '09:30', free: 1 }]),
    '/portal/requests': route => { sent.push(bodyOf(route)); return json({ status: 'eingereicht', id: 5 }, 201)(route); } } });
  await open(page, 'portal/demo');
  await page.getByText('Abholung anmelden').click();
  const slot = page.getByRole('combobox', { name: 'Termin' });
  await expect(slot).toBeVisible();
  await expect(page.getByLabel('Wunschtermin')).toBeHidden();
  await slot.selectOption({ index: 1 });
  await page.locator('.portal-request').nth(1).getByRole('button', { name: 'Absenden' }).click();
  await expect.poll(() => sent.length).toBe(1);
  expect(sent[0]).toEqual({ kind: 'pickup', date: '2026-10-24', time: '09:30', note: '' });
  await page.getByText('Einlagerung verlängern').click();
  await page.getByLabel('Verlängern bis').fill('2027-03-31');
  await page.locator('.portal-request').nth(2).getByRole('button', { name: 'Absenden' }).click();
  await expect.poll(() => sent.length).toBe(2);
  expect(sent[1]).toEqual({ kind: 'extend', vehicle_id: 1, until: '2027-03-31', note: '' });
});

test('Kundenwünsche: Verlängerung übernehmen speichert das Gefährt und schließt den Wunsch', async ({ page }) => {
  const calls = [];
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/portal-requests': [{ id: 9, person_id: 1, person_name: 'Demo Berger 1', kind: 'extend', status: 'offen', created_at: '2026-10-15T08:00:00Z',
      payload: { vehicle_id: '1', vehicle: 'Demo Wohnmobil 1', until: '2027-03-31', previous_end: '2026-11-30' } }],
    '/vehicles/1': route => {
      if (route.request().method() === 'PUT') { calls.push(['PUT', bodyOf(route).end_date]); return json({ id: 1 })(route); }
      return json({ ...overrides['/vehicles'][0], id: 1, rate: 85, start_date: '2026-01-01', notes: '', planner_symbol: null })(route);
    },
    '/portal-requests/9/resolve': route => { calls.push(['resolve', bodyOf(route).action]); return json({ ok: true })(route); } } });
  await open(page, 'dashboard');
  const row = page.locator('.pr-row', { hasText: 'Verlängerung' });
  await expect(row).toContainText('Demo Wohnmobil 1 bis 31.3.2027');
  await row.getByRole('button', { name: 'Übernehmen' }).click();
  await page.locator('#confirm').getByRole('button', { name: 'Übernehmen' }).click();
  await expect.poll(() => calls).toEqual([['PUT', '2027-03-31'], ['resolve', 'done']]);
});

test('Warteliste: eintragen mit Einwilligung, absagen nur mit bekannten Feldern', async ({ page }) => {
  const writes = [];
  const entry = { id: 3, name: 'Lena Integration', email: 'lena@example.invalid', phone: '', category_id: 1, category_name: 'Wohnmobil',
    length_m: 6.2, width_m: null, wanted_from: '2026-11-01', wanted_until: null, note: '', consent: true, status: 'offen', person_id: null, created_at: '2026-10-01T08:00:00Z' };
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/waitlist': route => {
      if (route.request().method() === 'POST') { writes.push(['POST', bodyOf(route)]); return json({ ...entry, id: 4 }, 201)(route); }
      return json([entry])(route);
    },
    '/waitlist/3': route => { writes.push([route.request().method(), bodyOf(route)]); return json(entry)(route); } } });
  await open(page, 'waitlist');
  await expect(page.locator('#page .card')).toContainText('Lena Integration');
  await page.getByRole('button', { name: 'Absagen' }).click();
  await expect.poll(() => writes.length).toBe(1);
  expect(Object.keys(writes[0][1]).sort()).toEqual(['category_id', 'consent', 'email', 'length_m', 'name', 'note', 'phone', 'status', 'wanted_from', 'wanted_until', 'width_m']);
  expect(writes[0][1].status).toBe('abgesagt');
  await page.getByRole('button', { name: '+ Eintrag' }).click();
  await page.locator('#modal').getByRole('textbox', { name: 'Name' }).fill('Max Neu');
  await page.locator('#modal').getByRole('textbox', { name: 'E-Mail' }).fill('max@example.invalid');
  await page.locator('#modal').getByText('Einwilligung liegt vor').click();
  await page.locator('#modal-submit').click();
  await expect.poll(() => writes.length).toBe(2);
  expect(writes[1][1]).toMatchObject({ name: 'Max Neu', email: 'max@example.invalid', consent: true, status: 'offen' });
});
