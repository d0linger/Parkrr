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

test('measure tool: arm + two canvas clicks render a ruler with no errors (UX2 + snap)', async ({ page, context }) => {
  const errors = [];
  page.on('pageerror', (e) => errors.push(String(e)));
  await login(page);
  const cookies = await context.cookies();
  const csrf = (cookies.find((c) => c.name === 'parkrr_csrf') || {}).value;
  const post = (url, data) =>
    page.request.post(url, { headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' }, data });
  const g = await (await post('/api/garages', { name: 'E2E MG ' + Date.now() })).json();
  const h = await (await post(`/api/garages/${g.id}/halls`, { name: 'E2E MHalle' })).json();
  await page.goto('/#/hall/' + h.id);
  await expect(page.locator('svg.gp-floor')).toBeVisible({ timeout: 15000 });

  // The planner opens in "Stellplätze" (manage) mode; switch to Garagenplaner (plan) so the
  // drawing toolbar — including the ruler — is shown.
  await page.getByRole('button', { name: 'Garagenplaner' }).click();

  // Arm the ruler via its toolbar button, then click two points on the plan overlay.
  const measureBtn = page.locator('.gp-toolbar button[title^="Messen"]');
  await measureBtn.click();
  await expect(measureBtn).toHaveClass(/\bon\b/); // tool is armed

  const box = await page.locator('svg.gp-floor').boundingBox();
  const ax = box.x + box.width * 0.35, ay = box.y + box.height * 0.5;
  const bx = box.x + box.width * 0.65, by = box.y + box.height * 0.5;
  await page.mouse.move(ax, ay); await page.mouse.click(ax, ay); // point A (runs snapMeasure)
  await page.mouse.move(bx, by); await page.mouse.click(bx, by); // point B → ruler renders
  // A flat horizontal SVG <line> has a 0-height box (Playwright reads it "hidden"), so assert
  // presence, not visibility: the ruler line existing proves snapMeasure + the render path ran.
  await expect(page.locator('.gp-measure')).toHaveCount(1, { timeout: 5000 });
  expect(errors, 'no uncaught errors while measuring').toEqual([]);
});

test('dxf import: PG.parseDXF reads a LINE entity in the browser (FE3)', async ({ page }) => {
  await login(page);
  const n = await page.evaluate(() => {
    const dxf = '0\nSECTION\n2\nENTITIES\n0\nLINE\n10\n0\n20\n0\n11\n5\n21\n0\n0\nENDSEC\n0\nEOF\n';
    return window.PG && window.PG.parseDXF ? window.PG.parseDXF(dxf).polylines.length : -1;
  });
  expect(n, 'parseDXF returns one polyline for a single LINE').toBe(1);
});

test('auto-arrange: PG.packRects runs in-browser and packs overlap-free (FE1)', async ({ page }) => {
  await login(page);
  const r = await page.evaluate(() => {
    if (!window.PG || !window.PG.packRects) return { err: 'no packRects' };
    const dims = { 1: { w: 5, h: 2 }, 2: { w: 4, h: 2 }, 3: { w: 2, h: 1 }, 4: { w: 3, h: 2 } };
    const items = Object.entries(dims).map(([id, d]) => ({ id: +id, w: d.w, h: d.h }));
    const out = window.PG.packRects(items, [], { minX: 0, minY: 0, maxX: 14, maxY: 8 }, null, { step: 0.5, margin: 0.2, gap: 0.15 });
    const ok = out.placements.filter((p) => p.ok).map((p) => ({ ...p, ...dims[p.id] }));
    let overlap = false;
    for (let i = 0; i < ok.length; i++) for (let j = i + 1; j < ok.length; j++) if (window.PG.rectsCollide(ok[i], ok[j])) overlap = true;
    return { placed: out.placed, overlap };
  });
  expect(r.err, r.err).toBeUndefined();
  expect(r.placed, 'all four packed').toBe(4);
  expect(r.overlap, 'no two placements overlap').toBe(false);
});

test('dxf import: uploading a .dxf renders a vector underlay (FE3 vector)', async ({ page, context }) => {
  const errors = [];
  page.on('pageerror', (e) => errors.push(String(e)));
  await login(page);
  const cookies = await context.cookies();
  const csrf = (cookies.find((c) => c.name === 'parkrr_csrf') || {}).value;
  const post = (url, data) =>
    page.request.post(url, { headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/json' }, data });
  const g = await (await post('/api/garages', { name: 'E2E DG ' + Date.now() })).json();
  const h = await (await post(`/api/garages/${g.id}/halls`, { name: 'E2E DHalle' })).json();
  await page.goto('/#/hall/' + h.id);
  await page.getByRole('button', { name: 'Garagenplaner' }).click();

  const dxf = '0\nSECTION\n2\nENTITIES\n'
    + '0\nLINE\n10\n0\n20\n0\n11\n10\n21\n0\n'
    + '0\nLINE\n10\n10\n20\n0\n11\n10\n21\n10\n'
    + '0\nENDSEC\n0\nEOF\n';
  await page.locator('input[accept=".dxf"]').setInputFiles({ name: 'floor.dxf', mimeType: 'application/dxf', buffer: Buffer.from(dxf) });
  await expect(page.locator('.gp-planvec')).toHaveCount(1, { timeout: 5000 }); // vector group rendered
  expect(errors, 'no uncaught errors while importing DXF').toEqual([]);
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
