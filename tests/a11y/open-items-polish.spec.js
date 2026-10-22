const { test: base, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

// Die zwölf offenen Punkte aus dem Vorher-Nachher-Vergleich vom 14.10.2026:
// Planer (Telefon, Tippen statt Ziehen, helles Theme), Gefährte-Filter, Kennzeichen,
// schwebendes Hinzufügen, ein Stil für Abschnitts-Hinzufügen, Heldenzahl bei 200 %,
// Fehlertexte, Nur-Lesen-Hinweis, Rückgängig-Hinweis und Umschalter. Alles gemockt.
const test = base.extend({
  browserErrors: [async ({ page }, use) => {
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('console', message => { if (message.type() === 'error' && !/503|Service Unavailable/.test(message.text())) errors.push(message.text()); });
    await use(errors);
    expect(errors, 'uncaught browser and console errors').toEqual([]);
  }, { auto: true }],
});
test.use({ serviceWorkers: 'block', locale: 'de-DE', timezoneId: 'Europe/Vienna', reducedMotion: 'reduce' });
test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date('2026-09-10T12:00:00Z'));
  await page.addInitScript(() => { localStorage.setItem('parkrr-veh', '0'); localStorage.setItem('parkrr_portal_lang', 'de'); });
});

async function arrange(page, width, theme, options = {}) {
  await page.setViewportSize({ width, height: 900 });
  await page.emulateMedia({ colorScheme: theme });
  return mockUI(page, { role: 'admin', ...options, overrides: { ...overrides, ...options.overrides } });
}
async function open(page, route) {
  await page.goto(origin + '/#/' + route);
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
}
const writes = requests => requests.filter(r => !['GET', 'HEAD'].includes(r.method));

test('planner on a phone: unplaced strip above the plan, rare tools behind “Mehr”', async ({ page }) => {
  await arrange(page, 390, 'dark');
  await open(page, 'hall/1');
  await expect(page.locator('.gp-unplaced')).toBeVisible();
  // In einem Schritt messen: der Planer zeichnet die Seitenleiste bei jedem draw() neu.
  const order = await page.evaluate(() => document.querySelector('.gp-unplaced').getBoundingClientRect().top - document.querySelector('.gp-canvas').getBoundingClientRect().top);
  expect(order, 'Nicht platziert sits above the plan').toBeLessThan(0);
  const exportBtn = page.getByRole('button', { name: 'Als PNG / PDF / SVG / DXF exportieren' });
  await expect(exportBtn).toBeHidden();
  await page.getByRole('button', { name: 'Weitere Werkzeuge' }).click();
  await expect(exportBtn).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
});

test('planner: tap a vehicle, then Enter places it at the suggested free spot', async ({ page }) => {
  const requests = await arrange(page, 1440, 'light', { overrides: {
    '/halls/1/spots': route => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ id: 77, label: 'Demo Wohnmobil 2 #2' }) }),
  } });
  await open(page, 'hall/1');
  const card = page.locator('.gp-unplaced .gp-vehc').first();
  await card.click();
  await expect(page.locator('.gp-armbar')).toContainText('bereit');
  await expect(page.locator('.gp-armprev')).toBeVisible();
  await expect(page.locator('.gp-armprev')).not.toHaveClass(/bad/);
  await page.keyboard.press('Enter');
  await expect.poll(() => writes(requests).map(r => r.method + ' ' + r.path)).toEqual(['POST /halls/1/spots', 'PUT /spots/77/vehicle']);
  await expect(page.locator('.gp-armbar')).toBeHidden();
  await expect(page.locator('#toast')).toContainText('platziert');
});

test('planner: keyboard selects, arrows move the preview, Escape cancels without writing', async ({ page }) => {
  const requests = await arrange(page, 1440, 'dark');
  await open(page, 'hall/1');
  const pick = page.getByRole('button', { name: /zum Platzieren auswählen$/ }).first();
  await pick.focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('.gp-armbar')).toBeVisible();
  const before = await page.locator('.gp-armprev').evaluate(n => parseFloat(n.style.left));
  await page.keyboard.press('ArrowRight');
  expect(await page.locator('.gp-armprev').evaluate(n => parseFloat(n.style.left))).toBeGreaterThan(before);
  await page.keyboard.press('Escape');
  await expect(page.locator('.gp-armbar')).toBeHidden();
  expect(writes(requests)).toEqual([]);
});

test('planner in the light theme: app-coloured frame, dark plan surface, no serious axe findings', async ({ page }) => {
  await arrange(page, 1440, 'light');
  await open(page, 'hall/1');
  expect(await page.locator('.gp-appbar').evaluate(n => getComputedStyle(n).backgroundColor)).toBe('rgb(255, 255, 255)');
  expect(await page.locator('.gp-planwrap').evaluate(n => getComputedStyle(n).backgroundColor)).toBe('rgb(6, 11, 17)');
  const result = await new AxeBuilder({ page }).include('.gp').withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
  expect(result.violations.filter(v => ['serious', 'critical'].includes(v.impact)).map(v => v.id)).toEqual([]);
});

test('vehicles on a phone: filters fold behind “Filter (n)”; desktop keeps them inline', async ({ page }) => {
  await arrange(page, 390, 'light');
  await open(page, 'vehicles');
  const status = page.getByRole('combobox', { name: 'Status filtern' });
  await expect(status).toBeHidden();
  const toggle = page.locator('.filter-toggle');
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.click();
  await expect(status).toBeVisible();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await page.setViewportSize({ width: 1440, height: 900 });
  await expect(toggle).toBeHidden();
  await expect(status).toBeVisible();
});

test('vehicle cards show the plate; the new-vehicle dialog shows the plate field without expanding', async ({ page }) => {
  await arrange(page, 390, 'light');
  await open(page, 'vehicles');
  await expect(page.locator('#page .card').first().locator('.plate')).toHaveText('Kennzeichen DEMO-0');
  await page.getByRole('button', { name: '+ Gefährt', exact: true }).click();
  await expect(page.locator('#f_license_plate')).toBeVisible();
  await page.keyboard.press('Escape');
});

test('add action floats within thumb reach on a phone and stays in the header on desktop', async ({ page }) => {
  await arrange(page, 390, 'light');
  await open(page, 'persons');
  const add = page.getByRole('button', { name: '+ Person', exact: true });
  expect((await add.boundingBox()).y).toBeGreaterThan(600);
  await page.setViewportSize({ width: 1440, height: 900 });
  expect((await add.boundingBox()).y).toBeLessThan(200);
});

test('person page: every section add uses the same text button', async ({ page }) => {
  await arrange(page, 1440, 'light');
  await open(page, 'persons/1');
  for (const name of ['+ Pauschale', '+ Gefährt', '+ Position', '+ Zahlung', '+ Rechnung']) {
    await expect(page.getByRole('button', { name, exact: true })).toHaveClass('btn-sect');
  }
});

test('“Offen gesamt” stays inside its tile at 200 % text size on a phone', async ({ page }) => {
  await arrange(page, 390, 'light');
  await open(page, 'dashboard');
  await page.evaluate(() => { document.documentElement.style.fontSize = '32px'; });
  const right = await page.locator('.dash-hero-num').evaluate(n => { const r = document.createRange(); r.selectNodeContents(n); return Math.max(...[...r.getClientRects()].map(c => c.right)); });
  expect(right).toBeLessThanOrEqual(390);
});

test('route errors speak plain language and keep the raw text under details', async ({ page }) => {
  await arrange(page, 390, 'light', { overrides: {
    '/overview': route => route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'database unavailable' }) }),
  } });
  await page.goto(origin + '/#/dashboard');
  const box = page.locator('.route-error');
  await expect(box.locator('p')).toContainText('Die Datenbank antwortet gerade nicht');
  await box.getByText('Technische Details').click();
  await expect(box.locator('code')).toHaveText('HTTP 503 · database unavailable');
  await expect(page.getByRole('button', { name: 'Erneut versuchen' })).toBeVisible();
});

test('read-only accounts get an honest page hint on vehicles', async ({ page }) => {
  await arrange(page, 390, 'light', { role: 'reader' });
  await open(page, 'vehicles');
  await expect(page.locator('.page-context')).toHaveText('Status und Zahlung ansehen. Ändern können Bearbeiter und Admins.');
});

test('undo toast: shown at the top on a phone, Ctrl+Z undoes the paid toggle', async ({ page }) => {
  const requests = await arrange(page, 390, 'light');
  await open(page, 'vehicles');
  const pay = page.locator('#page .card').first().getByRole('radiogroup', { name: 'Zahlstatus' });
  await pay.getByRole('radio', { name: 'Zahlung offen' }).click();
  const toast = page.locator('#toast');
  await expect(toast).toContainText('Als offen markiert');
  expect((await toast.boundingBox()).y).toBeLessThan(200);
  await page.keyboard.press('Control+z');
  await expect.poll(() => writes(requests).filter(r => r.path === '/vehicles/1/paid').length).toBe(2);
  await expect(toast).toBeHidden();
});

test('a paused undo window keeps the delete from running until the toast actually expires', async ({ page }) => {
  const requests = await arrange(page, 1440, 'light');
  await open(page, 'persons/1');
  await page.getByRole('button', { name: 'Bearbeiten', exact: true }).click();
  // Erst die Rückfrage, danach das Rückgängig-Fenster.
  await page.locator('.person-danger').getByRole('button', { name: 'Person löschen' }).click();
  await expect(page.locator('#confirm-title')).toHaveText('Person löschen?');
  await page.locator('#confirm-ok').click();
  const toast = page.locator('#toast');
  await expect(toast).toContainText('Person wird gelöscht');
  await toast.hover();
  await page.waitForTimeout(6000);
  expect(writes(requests).filter(r => r.method === 'DELETE'), 'still paused – nothing deleted').toEqual([]);
  await page.mouse.move(5, 5);
  await expect.poll(() => writes(requests).filter(r => r.method === 'DELETE').map(r => r.path), { timeout: 8000 }).toEqual(['/persons/1']);
});

test('view switches share the .segments form; the planner switches follow it in planner colours', async ({ page }) => {
  await arrange(page, 1440, 'dark');
  await open(page, 'dashboard');
  const modes = page.locator('.revenue-modes');
  expect(await modes.evaluate(n => getComputedStyle(n).borderRadius)).toBe('999px');
  expect(await modes.locator('.btn[aria-pressed="true"]').evaluate(n => getComputedStyle(n).borderRadius)).toBe('999px');
  await open(page, 'hall/1');
  // In einem Schritt lesen: der Planer zeichnet seine Leiste bei jedem draw() neu, ein
  // zuvor gegriffenes Element kann beim Auswerten schon ersetzt sein (dann liefert
  // getComputedStyle eine leere Zeichenkette).
  await page.locator('.gp-seg').first().waitFor();
  for (const sel of ['.gp-switch', '.gp-seg']) expect(await page.evaluate(s => getComputedStyle(document.querySelector(s)).borderRadius, sel)).toBe('999px');
});
