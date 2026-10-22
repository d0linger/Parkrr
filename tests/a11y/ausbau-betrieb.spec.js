const { test: base, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides, hall } = require('./helpers/overhaul-fixture');

// Ausbauplan Paket 3 (Betrieb): unvollständige Gefährte und Strom in der Heute-Liste,
// Stromzone im Planer, Etikettenbogen, Wochenliste, Kalender-Abo, Gefährte-Import und
// Schnellpreis. Alles gemockt.
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

test('Heute-Liste: Ladebedarf ohne Strom, fehlende Maße und Kennzeichen führen zu gefilterten Listen', async ({ page }) => {
  const vehicles = overrides['/vehicles'].map((v, i) => ({ ...v, length_m: i ? 6 : undefined, width_m: i ? 2.3 : undefined }));
  await mockUI(page, { role: 'admin', overrides: { ...overrides, '/vehicles': vehicles,
    '/vehicles/incomplete': { no_dimensions: 1, no_plate: 2, power_missing: [{ vehicle_id: 1, label: 'Demo Wohnmobil 1', hall_id: 1, hall_name: 'Halle Nord' }] } } });
  await open(page, 'dashboard');
  const today = page.getByRole('region', { name: 'Aufgaben und Hinweise' });
  const power = today.getByRole('link', { name: /1 Gefährt mit Ladebedarf ohne Strom/ });
  await expect(power).toHaveAttribute('href', '#/hall/1');
  await today.locator('summary').click();
  await expect(today.getByRole('link', { name: /2 Gefährte ohne Kennzeichen/ })).toHaveAttribute('href', '#/vehicles?missing=plate');
  await today.getByRole('link', { name: /1 Gefährt ohne Maße/ }).click();
  await expect(page).toHaveURL(/#\/vehicles\?missing=dims$/);
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
  await expect(page.getByRole('button', { name: 'Ohne Maße ×' })).toBeVisible();
  await expect(page.locator('#page .card')).toHaveCount(1);
});

test('Planer: Stromzone ist ein Werkzeug, Ladebedarf außerhalb warnt, innerhalb nicht', async ({ page }) => {
  const plan = (zone) => ({ ...overrides['/halls/1/plan'],
    hall: { ...hall, geometry: { ...hall.geometry, excl: [{ kind: 'power', x: zone.x, y: zone.y, w: 3, h: 3, rot: 0, label: 'Strom' }] } },
    spots: overrides['/halls/1/plan'].spots.map(s => ({ ...s, needs_power: true })) });
  // Zone weit weg von der Gefährtmitte (4 | 2,2): Warnung.
  await mockUI(page, { role: 'admin', overrides: { ...overrides, '/halls/1/plan': plan({ x: 10, y: 5 }) } });
  await open(page, 'hall/1');
  await expect(page.locator('.gp-power.gp-power-miss')).toHaveText('⚡ ohne Strom');
  // Das Werkzeug steht in der Grundriss-Bearbeitung ("Garagenplaner") neben Stellfläche & Co.
  await page.getByRole('button', { name: 'Garagenplaner', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Strom', exact: true })).toBeVisible();
  // Zone um die Mitte: nur das Ladesymbol.
  await page.unrouteAll({ behavior: 'ignoreErrors' });
  await mockUI(page, { role: 'admin', overrides: { ...overrides, '/halls/1/plan': plan({ x: 3, y: 1 }) } });
  await page.reload();
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
  await expect(page.locator('.gp-power')).toHaveText('⚡');
  await expect(page.locator('.gp-power-miss')).toHaveCount(0);
});

test('Garage: Etikettenbogen nur für Hallen mit Gefährten; Kalender: Woche als PDF', async ({ page }) => {
  await mockUI(page, { role: 'admin', overrides });
  await open(page, 'garage/1');
  const sheet = page.getByRole('link', { name: 'Etiketten für Halle Nord drucken' });
  await expect(sheet).toHaveAttribute('href', '/api/halls/1/labels');
  await expect(page.getByRole('link', { name: 'Etiketten für Halle Süd drucken' })).toHaveCount(0);
  await open(page, 'calendar');
  await expect(page.getByRole('link', { name: /Woche als PDF/ })).toHaveAttribute('href', '/api/reports/week.pdf');
});

test('Kalender-Abo: erzeugen zeigt die Adresse einmal, widerrufen fragt nach', async ({ page }) => {
  let active = false;
  const writes = [];
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/calendar/feed': route => {
      const m = route.request().method();
      if (m === 'POST') { writes.push('POST'); active = true; return json({ url: 'http://parkrr.test/ical/abcdefghijklmnopqrstuvwxyz0123456789' }, 201)(route); }
      if (m === 'DELETE') { writes.push('DELETE'); active = false; return route.fulfill({ status: 204, body: '' }); }
      return json(active ? { active: true, created_at: '2026-10-16T08:00:00Z' } : { active: false })(route);
    } } });
  await open(page, 'settings');
  const card = page.locator('.cal-feed');
  await card.getByRole('button', { name: 'Abo-Adresse erzeugen' }).click();
  await expect(card.getByRole('textbox', { name: 'Abo-Adresse' })).toHaveValue(/\/ical\/abcdefghij/);
  expect((await new AxeBuilder({ page }).include('.cal-feed').analyze()).violations.map(v => v.id)).toEqual([]);
  await page.reload();
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
  await card.getByRole('button', { name: 'Widerrufen' }).click();
  await page.locator('#confirm').getByRole('button', { name: 'Widerrufen' }).click();
  await expect(card.getByRole('button', { name: 'Abo-Adresse erzeugen' })).toBeVisible();
  expect(writes).toEqual(['POST', 'DELETE']);
});

test('Gefährte-Import: fehlerhafte Zeilen stehen mit Zeilennummer da', async ({ page }) => {
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/import/vehicles': json({ imported: 3, skipped: 1, failed: 2, errors: [{ row: 4, message: 'Tarif „Boot“ gibt es nicht' }, { row: 6, message: 'Beginn fehlt oder ist kein Datum (TT.MM.JJJJ)' }] }) } });
  await open(page, 'vehicles');
  await page.getByRole('button', { name: 'Import' }).click();
  await expect(page.locator('#modal-title')).toHaveText('Gefährte importieren');
  await expect(page.getByRole('link', { name: /Beispiel-Datei/ })).toHaveAttribute('href', '/api/import/vehicles/template');
  await page.locator('#modal input[type=file]').setInputFiles({ name: 'g.csv', mimeType: 'text/csv', buffer: Buffer.from('email;kategorie;beginn\n') });
  await expect(page.locator('#modal-title')).toHaveText('Nicht importierte Zeilen');
  await expect(page.locator('.import-errors li')).toHaveText([/Zeile 4: Tarif „Boot“ gibt es nicht/, /Zeile 6: Beginn fehlt/]);
});

test('Schnellpreis: rechnet wie die Abrechnung und übernimmt die Werte ins Gefährt', async ({ page }) => {
  await mockUI(page, { role: 'editor', overrides });
  await open(page, 'tariffs');
  await page.locator('.quick-price summary').click();
  await page.locator('#qp-from').fill('2026-11-15');
  await page.locator('#qp-to').fill('2026-12-31');
  await page.locator('#qp-to').dispatchEvent('change');
  // November 16 von 30 Tagen (45,33 €) + Dezember voll (85,00 €)
  await expect(page.locator('.quick-price-total')).toHaveText('130,33 €');
  await expect(page.locator('.quick-price-out')).toContainText('1 voller Monat, 16 Tage anteilig');
  await page.getByRole('button', { name: 'Als Gefährt anlegen' }).click();
  await expect(page.locator('#modal-title')).toHaveText('Neues Gefährt');
  await expect(page.locator('#f_start_date')).toHaveValue('2026-11-15');
  await expect(page.locator('#f_end_date')).toHaveValue('2026-12-31');
});
