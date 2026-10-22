const { test: base, expect } = require('@playwright/test');
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides, hall } = require('./helpers/overhaul-fixture');

// Ausbauplan, offene Punkte (Entscheidungen vom 22.10.2026): O1 Flächenauslastung auf
// der Startseite und im Planer, O3 Vorjahresplatz als erster Platzvorschlag, O6 „Ist
// zurück“ durch den Hallendienst — und das Speichern des Hallendiensts im Planer.
const test = base.extend({
  browserErrors: [async ({ page }, use) => {
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('console', message => { if (message.type() === 'error' && !/403/.test(message.text())) errors.push(message.text()); });
    await use(errors);
    expect(errors, 'uncaught browser and console errors').toEqual([]);
  }, { auto: true }],
});
test.use({ serviceWorkers: 'block', locale: 'de-DE', timezoneId: 'Europe/Vienna', reducedMotion: 'reduce' });
test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date('2026-10-22T10:00:00Z'));
});
const json = (body, status = 200) => route => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
const bodyOf = route => JSON.parse(route.request().postData() || '{}');
async function open(page, route) {
  await page.goto(origin + '/#/' + route);
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
}

test('Startseite: Belegung je Halle als Flächenauslastung wie im Planer', async ({ page }) => {
  await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/occupancy': { active: 5, placed: 3, trend: [], halls: [
      { hall_id: 1, name: 'Halle Nord', garage_name: 'Standort Nord', placed: 2, used_m2: 33.6, park_m2: 80, pct: 42 },
      { hall_id: 2, name: 'Halle Süd', garage_name: 'Standort Nord', placed: 1, used_m2: 8, park_m2: null, pct: null }] } } });
  await open(page, 'dashboard');
  const nord = page.getByRole('button', { name: /^Halle Nord \(Standort Nord\) im Planer öffnen/ });
  await expect(nord).toHaveAccessibleName('Halle Nord (Standort Nord) im Planer öffnen, 42 % belegt, 34 m² von 80 m², 2 Gefährte');
  await expect(nord.locator('.bar-val')).toHaveText('42 %');
  await expect(nord.locator('.dash-bar-fill')).toHaveAttribute('style', /width: ?42%/);
  const sued = page.getByRole('button', { name: /^Halle Süd/ });
  await expect(sued).toHaveAccessibleName(/1 Gefährt, Fläche im Planer noch nicht gerechnet$/);
  await expect(sued.locator('.dash-bar-fill')).toHaveCount(0);
});

test('Planer: Auslastung in Kopfzeile und Kennzahlen, Fläche wird einmal nachgetragen', async ({ page }) => {
  const sent = [];
  const { park, ...legacy } = hall.geometry; // eine Halle von vor O1
  await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/halls/1/plan': { ...overrides['/halls/1/plan'], hall: { ...hall, geometry: legacy } },
    '/halls/1/park-area': route => { sent.push(bodyOf(route)); return json({ park_m2: bodyOf(route).park_m2 })(route); } } });
  await open(page, 'hall/1');
  // Halle 14 × 9 m ohne Wände: nutzbar 126 m², belegt 6 × 2,4 = 14,4 m² → 11 %.
  await expect(page.locator('.gp-occ')).toContainText('1 Gefährt · 11 %');
  await expect(page.locator('.gp-metric', { hasText: 'Belegt' })).toContainText('Belegt · 1 · 11 %');
  await expect.poll(() => sent).toEqual([{ park_m2: 126 }]);
});

test('Planer: gespeicherte Fläche stimmt, nichts wird geschrieben', async ({ page }) => {
  const requests = await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/halls/1/plan': overrides['/halls/1/plan'] } }); // Fixture-Halle mit gespeicherter Fläche 126 m²
  await open(page, 'hall/1');
  await expect(page.locator('.gp-occ')).toContainText('11 %');
  await page.waitForTimeout(600);
  expect(requests.filter(r => r.method !== 'GET').map(r => r.method + ' ' + r.path)).toEqual([]);
});

test('Hallendienst verschiebt ein Gefährt: der Platz wird gespeichert, der Grundriss nicht angefasst', async ({ page }) => {
  let spotPut = null;
  const requests = await mockUI(page, { role: 'hallendienst', overrides: { ...overrides,
    '/halls/1': route => (route.request().method() === 'PUT' ? json({ error: 'forbidden' }, 403)(route) : json(overrides['/halls/1/plan'].hall)(route)),
    '/spots/1': route => { spotPut = bodyOf(route); return json({ id: 1 })(route); } } });
  await open(page, 'hall/1');
  const block = page.locator('.gp-block').first();
  await expect(block).toBeVisible();
  await page.waitForTimeout(400); // der Planer richtet sich im nächsten Frame ein (fitView)
  const box = await block.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + 40, box.y + box.height / 2 + 60, { steps: 6 });
  await page.mouse.up();
  await expect.poll(() => spotPut, { timeout: 5000 }).not.toBeNull();
  expect(spotPut.geometry.y).toBeGreaterThan(1);
  expect(requests.filter(r => r.method === 'PUT' && r.path === '/halls/1')).toEqual([]);
  await expect(page.getByText('Grundriss nicht gespeichert')).toHaveCount(0);
});

test('Platz vorschlagen: der Vorjahresplatz kommt zuerst, ein Tipp legt das Gefährt dorthin', async ({ page }) => {
  const calls = [];
  const plan = overrides['/halls/1/plan'];
  const unassigned = overrides['/vehicles/unassigned'].map((v, i) => i === 0 ? { ...v, prev_spot_id: 3, prev_hall_id: 1, prev_spot_label: 'Wohnmobil Huber #2', prev_from_copy: true } : v);
  await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/vehicles/unassigned': unassigned,
    '/halls/1/plan': { ...plan, spots: [...plan.spots, { id: 3, label: 'Wohnmobil Huber #2', geometry: { x: 7.5, y: 5, w: 6, h: 2.4, rot: 0 } }] },
    '/spots/3': route => { calls.push(['spot', bodyOf(route)]); return json({ id: 3 })(route); },
    '/spots/3/vehicle': route => { calls.push(['vehicle', bodyOf(route)]); return json({ ok: true })(route); } } });
  await open(page, 'hall/1');
  const pick = page.locator('.gp-vehc', { hasText: unassigned[0].label }).locator('.gp-vpick');
  await pick.press('Enter');
  await expect(pick).toHaveAttribute('aria-pressed', 'true');
  await page.getByRole('button', { name: 'Platz vorschlagen' }).click();
  const first = page.getByRole('button', { name: /^Vorschlag 1 übernehmen/ });
  await expect(first).toHaveAccessibleName('Vorschlag 1 übernehmen: Vorjahresplatz');
  await first.click();
  await expect.poll(() => calls).toEqual([
    ['spot', { label: 'Wohnmobil Huber #2', geometry: { x: 7.5, y: 5, w: 6, h: 2.4, rot: 0, status: 'busy' } }],
    ['vehicle', { vehicle_id: unassigned[0].id }]]);
  await expect(page.getByText('steht wieder auf dem Platz vom Vorjahr')).toBeVisible();
});

test('Platz vorschlagen: ist der Vorjahresplatz belegt, gibt es nur die üblichen Vorschläge', async ({ page }) => {
  const plan = overrides['/halls/1/plan'];
  const unassigned = overrides['/vehicles/unassigned'].map((v, i) => i === 0 ? { ...v, prev_spot_id: 1, prev_hall_id: 1, prev_from_copy: true } : v);
  await mockUI(page, { role: 'admin', overrides: { ...overrides, '/vehicles/unassigned': unassigned, '/halls/1/plan': plan } });
  await open(page, 'hall/1');
  await page.locator('.gp-vehc', { hasText: unassigned[0].label }).locator('.gp-vpick').focus();
  await page.keyboard.press('Enter');
  await page.getByRole('button', { name: 'Platz vorschlagen' }).click();
  await expect(page.locator('.gp-suggest').first()).toBeVisible();
  await expect(page.getByRole('button', { name: /Vorjahresplatz/ })).toHaveCount(0);
});

test('Hallendienst: „Ist zurück“ am Gefährt, verlängern und abholen bleiben im Büro', async ({ page }) => {
  let returned = null;
  const pause = { id: 5, from: '2026-10-01T00:00:00Z', until: '9999-12-31T00:00:00Z', planned: '2026-10-14T00:00:00Z', hold_spot: true };
  const fleet = overrides['/vehicles'].map((v, i) => i === 0 ? { ...v, status: 'stored', pauses: [pause] } : v);
  await mockUI(page, { role: 'hallendienst', overrides: { ...overrides, '/vehicles': fleet,
    '/vehicle-pauses/5/return': route => { returned = bodyOf(route); return json({ removed: false, relinked: false, paid_through: null, prev_spot_id: null, vehicle_id: 1 })(route); } } });
  await open(page, 'vehicle/1');
  await expect(page.locator('.badge-away').first()).toBeVisible();
  await expect(page.getByRole('button', { name: /eine Woche später/ })).toHaveCount(0);
  await expect(page.getByRole('button', { name: /doch abgeholt/ })).toHaveCount(0);
  await page.getByRole('button', { name: 'Demo Wohnmobil 1 ist zurück' }).first().click();
  await expect.poll(() => returned).toEqual({});
});
