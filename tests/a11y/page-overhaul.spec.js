const { test, expect } = require('@playwright/test');
const path = require('node:path');
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');
const AxeBuilder = require('@axe-core/playwright').default;
test.use({ serviceWorkers: 'block' });
const routes = ['dashboard', 'persons', 'persons/1', 'vehicles', 'vehicles/1', 'finance', 'tariffs', 'users',
  'billing', 'invoices/1', 'backup', 'audit', 'settings', 'calendar', 'garages', 'garage/1', 'hall/1'];
const captureDir = path.resolve(__dirname, '../../.impeccable/review/page-overhaul');
test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date('2026-09-10T12:00:00Z'));
  await page.addInitScript(() => { localStorage.setItem('parkrr-veh', '0'); localStorage.setItem('parkrr_portal_lang', 'de'); });
});
for (const [theme, width] of [['light', 1440], ['dark', 390]]) {
  test(`all pages ${theme} ${width}`, async ({ page }) => {
    test.setTimeout(120000);
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
    await mockUI(page, { role: 'admin', overrides });
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    for (const route of routes) {
      await page.goto(origin + '/#/' + route);
      await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
      await expect(page.locator('.route-error')).toHaveCount(0);
      await expect(page.locator('.route-view h2').first()).toBeVisible();
      await page.evaluate(async () => { await document.fonts.ready; await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))); });
      await page.screenshot({ path: path.join(captureDir, `${theme}-${width}-${route.replace('/', '-')}.png`), fullPage: true });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), route + ' overflow').toBe(true);
    }
    await page.goto(origin + '/#/portal/demo');
    await expect(page.locator('#portal-view')).toHaveAttribute('aria-busy', 'false');
    await page.screenshot({ path: path.join(captureDir, `${theme}-${width}-portal.png`), fullPage: true });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'portal overflow').toBe(true);
    expect(errors).toEqual([]);
  });
}
test('targeted page actions, filters, section navigation and billing validation', async ({ page }) => {
  await mockUI(page, { role: 'admin', overrides });
  await page.goto(origin + '/#/finance');
  await page.getByRole('combobox', { name: 'Zahlstatus filtern' }).selectOption('paid');
  await expect(page.locator('.charge-card')).toHaveCount(1);
  await page.getByRole('combobox', { name: 'Zahlstatus filtern' }).selectOption('open');
  await expect(page.locator('.charge-card')).toHaveCount(2);
  await page.goto(origin + '/#/users');
  await page.getByRole('combobox', { name: 'Rolle filtern' }).selectOption('reader');
  await expect(page.locator('.list-summary')).toContainText('1–1');
  await page.goto(origin + '/#/persons/1');
  await page.getByRole('navigation', { name: 'Abschnitte dieser Seite' }).getByRole('link', { name: 'Rechnungen' }).click();
  await expect(page).toHaveURL(/#\/persons\/1$/);
  await expect(page.getByRole('heading', { name: 'Rechnungen', exact: true })).toBeFocused();
  await page.goto(origin + '/#/billing');
  await page.getByLabel('Nächste Nummer', { exact: true }).fill('-1');
  await page.getByRole('button', { name: 'Einstellungen speichern' }).click();
  await expect(page.getByLabel('Nächste Nummer', { exact: true })).toBeFocused();
  await page.getByLabel('Nächste Nummer', { exact: true }).fill('14');
  await page.getByRole('button', { name: 'Einstellungen speichern' }).click();
  await expect(page.locator('.save-feedback')).toHaveText('Einstellungen gespeichert.');
});
test('calendar agenda and planner modes work on narrow screens', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mockUI(page, { role: 'admin', overrides });
  await page.goto(origin + '/#/calendar');
  await expect(page.locator('.cal-agenda')).toBeVisible();
  await page.getByRole('button', { name: 'Monat', exact: true }).click();
  await expect(page.locator('.cal-grid')).toBeVisible();
  await page.goto(origin + '/#/hall/1');
  await page.getByRole('button', { name: 'Garagenplaner', exact: true }).click();
  await expect(page.locator('.gp')).toHaveAttribute('data-mode', 'plan');
  await expect(page.getByRole('button', { name: 'Garagenplaner', exact: true })).toHaveAttribute('aria-pressed', 'true');
});
test('admin failures stay visible with retry', async ({ page }) => {
  let auditFailed = true;
  await mockUI(page, { role: 'admin', overrides: { ...overrides,
    '/backup/status': r => r.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"Temporär nicht verfügbar"}' }),
    '/audit': r => r.fulfill({ status: auditFailed ? 503 : 200, contentType: 'application/json', body: auditFailed ? '{"error":"Temporär nicht verfügbar"}' : '[]' }),
    '/auth/sessions': r => r.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"Temporär nicht verfügbar"}' }),
  } });
  await page.goto(origin + '/#/backup');
  await expect(page.getByRole('button', { name: 'Erneut versuchen' })).toBeVisible();
  await expect(page.getByText('Nicht aktiviert', { exact: true })).toHaveCount(0);
  await page.goto(origin + '/#/audit');
  await expect(page.getByRole('button', { name: 'Erneut versuchen' })).toBeVisible();
  auditFailed = false;
  await page.getByRole('button', { name: 'Erneut versuchen' }).click();
  await expect(page.getByText('Keine Änderungen für diese Auswahl.')).toBeVisible();
  await page.goto(origin + '/#/settings');
  await expect(page.getByRole('heading', { name: 'Dein Konto' })).toBeVisible();
  await expect(page.getByText('Sitzungen konnten nicht geladen werden.')).toBeVisible();
});
test('portal validates requests and does not prefetch payment QR images', async ({ page }) => {
  const requests = await mockUI(page, { overrides });
  await page.goto(origin + '/#/portal/demo');
  await expect(page.locator('#portal-view')).toHaveAttribute('aria-busy', 'false');
  expect(requests.filter(r => r.path.endsWith('/pay-qr'))).toHaveLength(0);
  const field = page.locator('.portal-form input').first();
  const box = await field.boundingBox();
  expect(box.height).toBeGreaterThanOrEqual(40);
  expect(box.height).toBeLessThanOrEqual(60);
  await page.locator('.portal-form').first().getByRole('button').click();
  await expect(page.getByText('Bitte mindestens eine Kontaktangabe eintragen.')).toBeVisible();
  expect(requests.filter(r => r.path === '/portal/requests')).toHaveLength(0);
});
test('billing, calendar, settings and portal accessibility', async ({ page }) => {
  await mockUI(page, { role: 'admin', overrides });
  for (const route of ['billing', 'calendar', 'settings', 'portal/demo']) {
    await page.goto(origin + '/#/' + route);
    await expect(page.locator(route.startsWith('portal') ? '#portal-view' : '#page')).toHaveAttribute('aria-busy', 'false');
    const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
    expect(result.violations.filter(v => ['serious', 'critical'].includes(v.impact)), route).toEqual([]);
  }
});
test('login help and both appearance previews', async ({ page }) => {
  await mockUI(page, { loggedIn: false });
  for (const [theme, width] of [['light', 1440], ['dark', 390]]) {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
    await page.goto(origin);
    await expect(page.locator('#login-form')).toBeVisible();
    await page.screenshot({ path: path.join(captureDir, `${theme}-${width}-login.png`), fullPage: true });
  }
  await page.locator('.login-help summary').click();
  await expect(page.locator('.login-help p')).toBeVisible();
});
test('portal temporary failures recover; expired links explain the next step', async ({ page }) => {
  let status = 503;
  await mockUI(page, { overrides: { ...overrides, '/portal/summary': r => r.fulfill({ status,
    contentType: 'application/json', body: JSON.stringify(status === 200 ? overrides['/portal/summary'] : { error: 'unavailable' }) }) } });
  await page.goto(origin + '/#/portal/demo');
  await expect(page.getByRole('button', { name: 'Erneut versuchen' })).toBeVisible();
  status = 200;
  await page.getByRole('button', { name: 'Erneut versuchen' }).click();
  await expect(page.getByRole('heading', { name: 'Rechnungen', exact: true })).toBeVisible();
  status = 401;
  await page.reload();
  await expect(page.getByText('Dieser Link ist ungültig oder abgelaufen. Bitte fordern Sie einen neuen an.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Erneut versuchen' })).toHaveCount(0);
});
test('all operator pages reflow at the intermediate breakpoint', async ({ page }) => {
  test.setTimeout(60000);
  await page.setViewportSize({ width: 768, height: 900 });
  await mockUI(page, { role: 'admin', overrides });
  for (const route of routes) {
    await page.goto(origin + '/#/' + route);
    await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
    await expect(page.locator('.route-error')).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), route).toBe(true);
  }
});
test('review regressions: invoice scrolling, audit insets and calendar placeholders', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 900 });
  await mockUI(page, { role: 'admin', overrides });
  await page.goto(origin + '/#/invoices/1');
  const scroll = page.getByRole('region', { name: 'Rechnungspositionen' });
  await expect(scroll).toBeVisible();
  expect(await scroll.evaluate(n => n.scrollWidth > n.clientWidth)).toBe(true);
  expect(await page.locator('.inv-table th').first().evaluate(n => n.getBoundingClientRect().height)).toBeLessThan(40);
  await page.goto(origin + '/#/audit');
  await expect(page.locator('.audit-filters')).toBeVisible();
  const insets = await page.locator('.audit-filters').evaluate(card => {
    const outer = card.getBoundingClientRect();
    return [...card.querySelectorAll('input, select')].map(input => {
      const inner = input.getBoundingClientRect(); return Math.min(inner.left - outer.left, outer.right - inner.right);
    });
  });
  expect(Math.min(...insets)).toBeGreaterThanOrEqual(12);
  await page.goto(origin + '/#/calendar');
  await page.getByRole('button', { name: 'Monat', exact: true }).click();
  expect(await page.locator('.cal-placeholder').count()).toBeGreaterThan(0);
  expect(await page.locator('.cal-placeholder').first().evaluate(n => getComputedStyle(n).boxShadow)).toBe('none');
});
