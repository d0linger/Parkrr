const { test: base, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

// Ausbauplan Paket 8: Rollen Buchhaltung und Hallendienst. Die Rechte prüft der
// Server (Go-Test über alle Routen); hier geht es darum, dass die Oberfläche
// ausblendet, was die Rolle nicht darf, und dass der Hallendienst keine Beträge sieht.
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
// Wie der Server: Beträge für den Hallendienst gibt es nicht (403).
const forbidden = route => route.fulfill({ status: 403, contentType: 'application/json', body: JSON.stringify({ error: 'insufficient permissions' }) });
const hallOverrides = { ...overrides, '/overview': forbidden, '/persons/outstanding': forbidden, '/invoices/overdue': forbidden,
  '/charges': forbidden, '/persons/1/stats': forbidden, '/persons/1/recurring': forbidden };

test('Hallendienst: eigene Startseite, kein Tarife-Reiter, keine Rechnungen im Menü', async ({ page }) => {
  const asked = [];
  page.on('request', (req) => { const u = new URL(req.url()); if (u.pathname.startsWith('/api/')) asked.push(u.pathname); });
  await mockUI(page, { role: 'hallendienst', overrides: { ...hallOverrides,
    '/service-tasks/due': { overdue: 1, power_without_task: 0, items: [{ task_id: 3, title: 'Batterie laden', vehicle_id: 1, vehicle_label: 'Demo Wohnmobil 1', due_on: '2026-10-14T00:00:00Z' }] } } });
  await open(page, 'dashboard');
  await expect(page.locator('#page').getByRole('heading', { name: 'Heute in der Halle' })).toBeVisible();
  await expect(page.getByText('1 Serviceaufgabe fällig')).toBeVisible();
  expect(asked).not.toContain('/api/overview');
  await expect(page.locator('.tab[data-route="tariffs"]')).toBeHidden();
  await page.locator('#menu-btn').click();
  const menu = page.locator('#menu');
  await expect(menu.getByRole('button', { name: 'Serviceaufgaben' })).toBeVisible();
  await expect(menu.getByRole('button', { name: 'Rechnungen' })).toHaveCount(0);
  await expect(menu.getByRole('button', { name: 'Steuerjahr' })).toHaveCount(0);
  expect((await new AxeBuilder({ page }).include('#page').analyze()).violations.map(v => v.id)).toEqual([]);
});

test('Hallendienst: Gefährt ohne Preis und Saldo, Übergabe und Fotos aber möglich', async ({ page }) => {
  await mockUI(page, { role: 'hallendienst', overrides: hallOverrides });
  await open(page, 'vehicle/1');
  const info = page.locator('#page .card').first();
  await expect(info).toContainText('Kennzeichen');
  await expect(info).not.toContainText('Preis');
  await expect(info).not.toContainText('Aufgelaufen');
  await expect(page.getByRole('button', { name: '+ Protokoll' })).toBeVisible();
  await expect(page.getByRole('button', { name: '+ Foto' })).toBeVisible();
  await expect(page.locator('#page')).not.toContainText('€');
  await open(page, 'person/1');
  await expect(page.getByRole('heading', { name: 'Gefährte' })).toBeVisible();
  await expect(page.locator('#page')).not.toContainText('€');
});

test('Buchhaltung: Steuerjahr im Menü, aber keine Bearbeiten-Knöpfe', async ({ page }) => {
  await mockUI(page, { role: 'buchhaltung', overrides });
  await open(page, 'vehicles');
  await expect(page.getByRole('button', { name: /\+ Gefährt/ })).toHaveCount(0);
  await page.locator('#menu-btn').click();
  const menu = page.locator('#menu');
  await expect(menu.getByRole('button', { name: 'Steuerjahr' })).toBeVisible();
  await expect(menu.getByRole('button', { name: 'Rechnungen' })).toBeVisible();
  await expect(menu.getByRole('button', { name: 'Benutzer' })).toHaveCount(0);
});

test('Hallendienst: Planer platziert Gefährte, den Grundriss gibt es nicht', async ({ page }) => {
  await mockUI(page, { role: 'hallendienst', overrides: hallOverrides });
  await open(page, 'hall/1');
  const modes = page.getByRole('group', { name: 'Planermodus' });
  await expect(modes.getByRole('button', { name: 'Stellplätze' })).toBeVisible();
  await expect(modes.getByRole('button', { name: 'Garagenplaner' })).toHaveCount(0);
  await expect(page.locator('.gp-vehc .gp-vpick').first()).toBeVisible();
});
