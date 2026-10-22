const { test: base, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

// Ruhezeit („Kurz weg“): eintragen mit Vorschau, während der Ruhezeit „Ist zurück“,
// „+1 Woche“ und „Doch abgeholt“, Heute-Liste, Anfrage im Portal und ihre Übernahme.
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
const stored = (extra = {}) => overrides['/vehicles'].map((v, i) => i === 0 ? { ...v, status: 'stored', ...extra } : v);

test('Kurz weg eintragen: zwei Wochen, Platz halten, Vorschau aus dem Server', async ({ page }) => {
  let posted = null;
  await mockUI(page, { role: 'editor', overrides: { ...overrides, '/vehicles': stored(),
    '/vehicles/1/pause-preview': json({ flat_rate: false, rate: 85, period: 'monthly', days: 14, saved: 38.39 }),
    '/vehicles/1/pause': route => { posted = bodyOf(route); return json({ id: 5 }, 201)(route); } } });
  await open(page, 'vehicle/1');
  await page.getByRole('button', { name: 'Kurz weg: Demo Wohnmobil 1' }).click();
  const dlg = page.locator('#modal');
  await expect(dlg.locator('.pause-preview')).toHaveText('14 Ruhetage: 38,39 € Miete ruhen. Gebundene Nebenkosten ruhen mit.');
  await expect(dlg.locator('#f_back_on')).toBeHidden();
  expect((await new AxeBuilder({ page }).include('#modal').analyze()).violations.map(v => v.id)).toEqual([]);
  await page.locator('#modal-submit').click();
  await expect.poll(() => posted).toEqual({ from: '2026-10-16', until: '2026-10-29', hold_spot: true, note: '' });
});

test('Unterwegs: Ist zurück, +1 Woche und Doch abgeholt', async ({ page }) => {
  const calls = [];
  const pause = { id: 5, from: '2026-10-01T00:00:00Z', until: '9999-12-31T00:00:00Z', planned: '2026-10-14T00:00:00Z', hold_spot: true };
  await mockUI(page, { role: 'editor', overrides: { ...overrides, '/vehicles': stored({ pauses: [pause] }),
    '/vehicle-pauses/5': route => { calls.push(['put', bodyOf(route)]); return json({ ...pause })(route); },
    '/vehicle-pauses/5/return': route => { calls.push(['return']); return json({ removed: false, relinked: false, prev_spot_id: null, vehicle_id: 1 })(route); },
    '/vehicle-pauses/5/collect': route => { calls.push(['collect']); return json({ collected: true })(route); } } });
  await open(page, 'vehicle/1');
  await expect(page.locator('.badge-away').first()).toHaveText('Unterwegs, Rückkehr 15.10.2026');
  await page.getByRole('button', { name: 'Rückkehr von Demo Wohnmobil 1 eine Woche später' }).first().click();
  await expect.poll(() => calls[0]).toEqual(['put', { until: '2026-10-21' }]);
  await page.getByRole('button', { name: 'Demo Wohnmobil 1 ist zurück' }).first().click();
  await expect.poll(() => calls.length).toBe(2);
  await page.getByRole('button', { name: 'Demo Wohnmobil 1 doch abgeholt' }).first().click();
  await page.locator('#confirm').getByRole('button', { name: 'Abgeholt' }).click();
  await expect.poll(() => calls.map(c => c[0])).toEqual(['put', 'return', 'collect']);
});

test('Heute-Liste: Rückkehr fällig führt zum Gefährt', async ({ page }) => {
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/pauses/due': [{ pause_id: 5, vehicle_id: 1, label: 'Demo Wohnmobil 1', person_name: 'Demo Berger 1', from: '2026-10-01T00:00:00Z', until: '2026-10-14T00:00:00Z', reason: 'return' }] } });
  await open(page, 'dashboard');
  const item = page.locator('a', { hasText: 'Rückkehr fällig' }).first();
  await expect(item).toHaveAttribute('href', '#/vehicle/1');
  await expect(page.getByText('geplant 15.10.2026', { exact: false }).first()).toBeVisible();
});

test('Portal: Kurz abholen anfragen', async ({ page }) => {
  const sent = [];
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/portal/summary': { ...overrides['/portal/summary'], vehicles: [{ id: 1, label: 'Demo Wohnmobil 1', status: 'stored' }] },
    '/portal/requests': route => { sent.push(bodyOf(route)); return json({ status: 'eingereicht', id: 7 }, 201)(route); } } });
  await open(page, 'portal/demo');
  await page.getByText('Kurz abholen (z. B. Urlaub)').click();
  await page.getByLabel('Abholtag').fill('2026-12-19');
  await page.getByLabel('Zurück am (leer = offen)').fill('2027-01-09');
  await page.locator('.portal-request', { hasText: 'Kurz abholen' }).getByRole('button', { name: 'Absenden' }).click();
  await expect.poll(() => sent[0]).toEqual({ kind: 'pause', vehicle_id: 1, date: '2026-12-19', until: '2027-01-08', note: '' });
});

test('Kundenwünsche: Kurz weg übernehmen trägt die Ruhezeit ein und schließt den Wunsch', async ({ page }) => {
  const calls = [];
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/portal-requests': [{ id: 9, person_id: 1, person_name: 'Demo Berger 1', kind: 'pause', status: 'offen', created_at: '2026-10-15T08:00:00Z',
      payload: { vehicle_id: '1', vehicle: 'Demo Wohnmobil 1', from: '2026-12-19', until: '2027-01-08', note: 'Wintercamping' } }],
    '/vehicles/1/pause': route => { calls.push(['pause', bodyOf(route)]); return json({ id: 5 }, 201)(route); },
    '/portal-requests/9/resolve': route => { calls.push(['resolve', bodyOf(route).action]); return json({ ok: true })(route); } } });
  await open(page, 'dashboard');
  const row = page.locator('.pr-row', { hasText: 'Kurz weg' });
  await expect(row).toContainText('Demo Wohnmobil 1 ab 19.12.2026, zurück 9.1.2027 — Wintercamping');
  await row.getByRole('button', { name: 'Übernehmen' }).click();
  await page.locator('#confirm').getByRole('button', { name: 'Eintragen' }).click();
  await expect.poll(() => calls).toEqual([['pause', { from: '2026-12-19', until: '2027-01-08', hold_spot: true, note: 'Kundenwunsch: Wintercamping' }], ['resolve', 'done']]);
});
