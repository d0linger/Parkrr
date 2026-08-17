// @ts-check
// Planner smoke E2E (AR1): log in, seed a garage + hall via the API, open the
// Garagenplaner and assert the editor shell mounts without uncaught errors.
// Self-contained so it works on a fresh CI instance (no pre-existing halls).
// The geometry maths themselves are covered by the node:test suite in
// tests/geometry/ (CI job "geometry"); this guards the login→planner integration
// so a "blank/crashed planner" regression fails the build.
const { test, expect } = require('@playwright/test');

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

async function login(page) {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
}

test('planner: login → seed a hall via API → Garagenplaner shell renders cleanly', async ({ page, context }) => {
  const errors = [];
  page.on('pageerror', (e) => errors.push(String(e)));

  await login(page);

  // CSRF token the server set at login (double-submit cookie).
  const cookies = await context.cookies();
  const csrf = (cookies.find((c) => c.name === 'parkrr_csrf') || {}).value;
  expect(csrf, 'CSRF cookie present after login').toBeTruthy();
  const post = (url, data) =>
    page.request.post(url, { headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' }, data });

  const gRes = await post('/api/garages', { name: 'E2E Garage ' + Date.now() });
  expect(gRes.ok(), 'create garage: ' + gRes.status()).toBeTruthy();
  const garage = await gRes.json();

  const hRes = await post(`/api/garages/${garage.id}/halls`, { name: 'E2E Halle' });
  expect(hRes.ok(), 'create hall: ' + hRes.status()).toBeTruthy();
  const hall = await hRes.json();

  // Open the planner for that hall.
  await page.goto('/#/hall/' + hall.id);

  // Core editor UI must mount: the mode switch, the toolbar and the plan SVG.
  await expect(page.getByRole('button', { name: 'Garagenplaner' })).toBeVisible({ timeout: 15000 });
  await expect(page.locator('.gp-toolbar')).toBeVisible();
  await expect(page.locator('svg.gp-floor')).toBeVisible();

  expect(errors, 'no uncaught page errors while the planner loads').toEqual([]);
});

test('export: occupancy CSV downloads with the expected header (FE2)', async ({ page }) => {
  await login(page);
  const res = await page.request.get('/api/export/occupancy');
  expect(res.ok(), 'occupancy CSV status ' + res.status()).toBeTruthy();
  expect(res.headers()['content-type'] || '').toContain('text/csv');
  expect(await res.text()).toContain('garage;halle;stellplaetze');
});

test('occupancy: response carries a daily trend array (FE4)', async ({ page }) => {
  await login(page);
  const res = await page.request.get('/api/occupancy');
  expect(res.ok(), 'occupancy status ' + res.status()).toBeTruthy();
  const j = await res.json();
  expect(Array.isArray(j.trend), 'occupancy.trend is an array').toBeTruthy();
});

test('wall templates: create → list → delete round-trip (AR3)', async ({ page, context }) => {
  await login(page);
  const cookies = await context.cookies();
  const hdr = { 'X-CSRF-Token': (cookies.find((c) => c.name === 'parkrr_csrf') || {}).value, 'Content-Type': 'application/json' };
  const name = 'E2E TPL ' + Date.now();
  const cr = await page.request.post('/api/wall-templates', { headers: hdr, data: { name, walls: { nodes: [{ x: 0, y: 0 }], edges: [] } } });
  expect(cr.ok(), 'create template ' + cr.status()).toBeTruthy();
  const tpl = await cr.json();
  const list = await (await page.request.get('/api/wall-templates')).json();
  expect(list.some((t) => t.id === tpl.id && t.name === name), 'template appears in the list').toBeTruthy();
  const del = await page.request.delete('/api/wall-templates/' + tpl.id, { headers: hdr });
  expect(del.ok(), 'delete template ' + del.status()).toBeTruthy();
});
