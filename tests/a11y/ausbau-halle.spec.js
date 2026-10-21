const fs = require('node:fs');
const path = require('node:path');
const { test: base, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides, hall } = require('./helpers/overhaul-fixture');

// Ausbauplan Paket 6 (Halle): Zustandsfotos an der Übergabe, Serviceaufgaben,
// Platzvorschlag im Planer, Schlüssel- und Handsenderbuch.
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
const bodyOf = route => JSON.parse(route.request().postData() || '{}');
const png = fs.readFileSync(path.join(__dirname, '../../web/static/icons/icon-192.png'));
const image = route => route.fulfill({ contentType: 'image/png', body: png });

test('Übergabe: Abholung zeigt die Einlagerung, nimmt Schäden, Fotos und den Handsender zurück', async ({ page }) => {
  const calls = [];
  const einlagerung = { id: 70, vehicle_id: 1, direction: 'einlagerung', notes: '', signer_name: 'Demo Berger 1', has_signature: false,
    created_at: '2026-04-02T08:00:00Z', damages: [{ area: 'vorne links', note: 'Kratzer 10 cm' }], photos: [{ id: 501, view: 'vorne' }], photos_open: false };
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/vehicles/1/handovers': route => {
      if (route.request().method() === 'POST') { calls.push(['handover', bodyOf(route)]); return json({ ...einlagerung, id: 77, direction: 'auslagerung', photos: [], photos_open: true }, 201)(route); }
      return json([einlagerung])(route);
    },
    '/persons/1/access-media': [{ id: 9, medium_id: 5, kind: 'handsender', label: '7', person_id: 1, person_name: 'Demo Berger 1', issued_on: '2026-05-01T00:00:00Z', returned_on: null, note: '' }],
    '/photos/501': image,
    '/handovers/77/photos': route => { calls.push(['photo', route.request().postDataBuffer().toString('latin1')]); return json({ id: 900, view: 'vorne' }, 201)(route); },
    '/access-media/5/return': route => { calls.push(['return']); return json({ id: 5 })(route); } } });
  await open(page, 'vehicle/1');
  const card = page.locator('.card', { has: page.getByRole('heading', { name: 'Übergabeprotokolle' }) });
  await expect(card.locator('.ho-dlist')).toContainText('vorne links: Kratzer 10 cm');
  await expect(card.getByRole('button', { name: 'Vorne groß ansehen' })).toBeVisible();
  await card.getByRole('button', { name: '+ Protokoll' }).click();

  const dlg = page.locator('#modal');
  await dlg.getByRole('button', { name: 'Auslagerung', exact: true }).click();
  await expect(dlg.locator('.ho-prev')).toContainText('Bei der Einlagerung am 2.4.2026 vermerkt');
  await expect(dlg.locator('.ho-slot[data-view="vorne"] .ho-ref img')).toHaveAttribute('alt', 'Einlagerung: Vorne');
  await dlg.getByRole('button', { name: '+ Schaden' }).click();
  await dlg.getByRole('textbox', { name: 'Schaden 1: Ort' }).fill('Heck');
  await dlg.getByRole('textbox', { name: 'Schaden 1: Beschreibung' }).fill('Delle');
  await dlg.locator('.ho-slot[data-view="vorne"] input[type=file]').setInputFiles({ name: 'vorne.png', mimeType: 'image/png', buffer: png });
  await expect(dlg.locator('.ho-slot[data-view="vorne"] .ho-thumb')).toHaveCount(1);
  await dlg.getByText('Handsender 7 (seit 1.5.2026)').click();
  expect((await new AxeBuilder({ page }).include('#modal').analyze()).violations.map(v => v.id)).toEqual([]);
  await page.locator('#modal-submit').click();
  await expect.poll(() => calls.map(c => c[0])).toEqual(['handover', 'photo', 'return']);
  expect(calls[0][1]).toMatchObject({ direction: 'auslagerung', damages: [{ area: 'Heck', note: 'Delle' }] });
  expect(calls[1][1]).toContain('name="view"\r\n\r\nvorne');
});

test('Serviceaufgaben: fällig, erledigt mit Abrechnung und Rückgängig; Ladevorschlag füllt das Formular', async ({ page }) => {
  const calls = [];
  const due = { overdue: 1, power_without_task: 2, items: [{ task_id: 3, title: 'Batterie laden', vehicle_id: 1, vehicle_label: 'Demo Wohnmobil 1', license_plate: 'DEMO-1',
    person_id: 1, person_name: 'Demo Berger 1', hall_name: 'Halle Nord', due_on: '2026-10-14T00:00:00Z', last_done: '2026-09-14', service_type_id: 2, service_name: 'Laden', service_amount: 15 }] };
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/service-tasks/due': due,
    '/service-tasks': route => {
      if (route.request().method() === 'POST') { calls.push(['create', bodyOf(route)]); return json({ id: 4 }, 201)(route); }
      return json([{ id: 3, title: 'Batterie laden', scope: 'category', category_id: 1, category_name: 'Wohnmobil', interval_days: 30, service_type_id: 2, service_name: 'Laden', service_amount: 15, note: '', active: true }])(route);
    },
    '/service-tasks/3/done': route => { calls.push(['done', bodyOf(route)]); return json({ id: 61, done_on: '2026-10-16', charge_id: 88 }, 201)(route); },
    '/service-task-log/61': route => { calls.push(['undo']); return json({ deleted: true, charge_id: 88 })(route); },
    '/charges/88': route => { calls.push(['charge-del']); return json({ status: 'deleted' })(route); } } });
  await open(page, 'service');
  const row = page.locator('.svc-row.is-overdue');
  await expect(row).toContainText('seit 2 Tagen fällig');
  expect((await new AxeBuilder({ page }).include('#page').analyze()).violations.map(v => v.id)).toEqual([]);
  await row.getByRole('button', { name: /erledigt und 15,00 € abrechnen/ }).click();
  await expect.poll(() => calls[0]).toEqual(['done', { vehicle_id: 1, bill: true }]);
  await page.getByRole('button', { name: 'Rückgängig' }).click();
  await expect.poll(() => calls.map(c => c[0])).toEqual(['done', 'undo', 'charge-del']);

  await page.getByRole('button', { name: '„Erhaltungsladung prüfen“ anlegen' }).click();
  const dlg = page.locator('#modal');
  await expect(dlg.locator('#f_title')).toHaveValue('Erhaltungsladung prüfen');
  await expect(dlg.locator('#f_scope')).toHaveValue('power');
  await expect(dlg.locator('#f_vehicle_id')).toBeHidden();
  await page.locator('#modal-submit').click();
  await expect.poll(() => calls.length).toBe(4);
  expect(calls[3][1]).toMatchObject({ title: 'Erhaltungsladung prüfen', scope: 'power', interval_days: 30, vehicle_id: null, category_id: null, active: true });
});

test('Schlüsselbuch: frei ausgeben, ausgegebene zurücknehmen; Heute-Liste meldet fällige Serviceaufgaben', async ({ page }) => {
  const calls = [];
  await mockUI(page, { role: 'editor', overrides: { ...overrides,
    '/access-media': [
      { id: 5, kind: 'handsender', label: '7', hall_id: 1, hall_name: 'Halle Nord', note: '', retired: false, loan_id: 9, person_id: 1, person_name: 'Demo Berger 1', issued_on: '2026-05-01T00:00:00Z', loan_count: 1 },
      { id: 6, kind: 'schluessel', label: '12', hall_id: null, hall_name: '', note: '', retired: false, loan_id: null, person_id: null, person_name: '', issued_on: null, loan_count: 0 }],
    '/access-media/6/issue': route => { calls.push(['issue', bodyOf(route)]); return json({ id: 6 }, 201)(route); },
    '/access-media/5/return': route => { calls.push(['return', bodyOf(route)]); return json({ id: 5 })(route); },
    '/service-tasks/due': { overdue: 1, power_without_task: 0, items: [{ task_id: 3, title: 'Batterie laden', vehicle_id: 1, vehicle_label: 'Demo Wohnmobil 1', due_on: '2026-10-14T00:00:00Z' }] } } });
  await open(page, 'keys');
  const issued = page.locator('#page .card', { hasText: 'Handsender 7' });
  await expect(issued).toContainText('bei Demo Berger 1 seit 1.5.2026');
  expect((await new AxeBuilder({ page }).include('#page').analyze()).violations.map(v => v.id)).toEqual([]);
  await page.getByRole('button', { name: 'Schlüssel 12 ausgeben' }).click();
  await page.locator('#modal #f_person_id').selectOption({ label: 'Demo Gruber 2' });
  await page.locator('#modal-submit').click();
  await expect.poll(() => calls[0]?.[0]).toBe('issue');
  expect(calls[0][1]).toMatchObject({ person_id: 2, issued_on: '2026-10-16' });
  await page.getByRole('button', { name: 'Handsender 7 zurücknehmen' }).click();
  await page.locator('#modal-submit').click();
  await expect.poll(() => calls[1]).toEqual(['return', { returned_on: '2026-10-16' }]);

  await open(page, 'dashboard');
  await expect(page.locator('a[href="#/service"]').first()).toBeVisible();
  await expect(page.getByText('1 Serviceaufgabe fällig')).toBeVisible();
});

test('Planer: Platz vorschlagen legt das Gefährt mit Ladebedarf in die Stromzone', async ({ page }) => {
  const zone = { kind: 'power', x: 8, y: 5, w: 6, h: 4, rot: 0, label: 'Strom' };
  const unassigned = overrides['/vehicles/unassigned'].map((v, i) => i === 0 ? { ...v, needs_power: true, end_date: null } : v);
  let posted = null;
  await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/vehicles/unassigned': unassigned,
    '/halls/1/plan': { ...overrides['/halls/1/plan'], hall: { ...hall, geometry: { ...hall.geometry, excl: [zone] } } },
    '/halls/1/spots': route => { posted = bodyOf(route); return json({ id: 50, label: posted.label }, 201)(route); },
    '/spots/50/vehicle': json({ ok: true }) } });
  await open(page, 'hall/1');
  const pick = page.locator('.gp-vehc', { hasText: unassigned[0].label }).locator('.gp-vpick');
  await pick.focus();
  await page.keyboard.press('Enter');
  await page.getByRole('button', { name: 'Platz vorschlagen' }).click();
  await expect(page.locator('.gp-suggest')).toHaveCount(3);
  const first = page.getByRole('button', { name: /^Vorschlag 1 übernehmen/ });
  await expect(first).toHaveAccessibleName(/Strom/);
  await first.click();
  await expect.poll(() => posted).not.toBeNull();
  const g = posted.geometry;
  const turned = g.rot === 90;
  const cx = g.x + g.w / 2, cy = g.y + g.h / 2;
  expect(cx >= zone.x && cx <= zone.x + zone.w && cy >= zone.y && cy <= zone.y + zone.h, JSON.stringify(g)).toBe(true);
  // Liegt ganz in der Halle (14 × 9 m) und überlappt das stehende Gefährt (1|1, 6 × 2,4 m) nicht.
  const fw = turned ? g.h : g.w, fh = turned ? g.w : g.h;
  expect(cx - fw / 2 >= 0 && cy - fh / 2 >= 0 && cx + fw / 2 <= 14 && cy + fh / 2 <= 9).toBe(true);
  expect(cx - fw / 2 >= 7 - 1e-6 || cy - fh / 2 >= 3.4 - 1e-6, JSON.stringify(g)).toBe(true); // bündig anliegen ist erlaubt
});
