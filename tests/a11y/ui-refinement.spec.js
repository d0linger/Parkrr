const { test, expect } = require('@playwright/test');
const { mockUI, origin } = require('./helpers/ui-fixture');
const AxeBuilder = require('@axe-core/playwright').default;
test.use({ serviceWorkers: 'block' });
// Keep the branded glyph stable between before/after captures.
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem('parkrr-veh', '0');
  });
});

for (const theme of ['light', 'dark']) {
  for (const width of [390, 768, 1440]) {
    test(`UI preview ${theme} ${width}`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
      const errors = [];
      page.on('pageerror', error => errors.push(error.message));
      const requests = await mockUI(page, { delay: 120 });
      await page.goto(origin + '/#/dashboard');
      await expect(page.getByRole('heading', { name: 'Export (CSV)' })).toBeVisible();
      await page.evaluate(() => document.fonts.ready);
      const firstOverview = requests.find(r => r.path === '/overview');
      console.log(JSON.stringify({ theme, width, dashboardMs: Date.now() - firstOverview.at,
        requestStarts: requests.filter(r => ['/overview', '/occupancy', '/invoices/overdue', '/vehicles/ending-soon', '/portal-requests'].includes(r.path)).map(r => ({ path: r.path, ms: r.at - firstOverview.at })) }));
      await page.screenshot({ path: testInfo.outputPath('dashboard.png'), fullPage: true });
      for (const route of ['persons', 'vehicles', 'finance', 'tariffs', 'garages', 'settings']) {
        await page.goto(origin + '/#/' + route);
        await expect(page.locator('.page-head h2, .detail-head h2').first()).toBeVisible();
        await page.evaluate(() => document.fonts.ready);
        await page.screenshot({ path: testInfo.outputPath(route + '.png'), fullPage: true });
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), route + ' overflows').toBe(true);
      }
      expect(errors).toEqual([]);
    });
  }
}

test('partial dashboard failure is visible and null optional lists are safe', async ({ page }) => {
  await mockUI(page, { overrides: {
    '/occupancy': route => route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }),
    '/invoices/overdue': route => route.fulfill({ contentType: 'application/json', body: 'null' }),
    '/vehicles/ending-soon': route => route.fulfill({ contentType: 'application/json', body: 'null' }),
  } });
  await page.goto(origin + '/#/dashboard');
  await expect(page.locator('.load-notice')).toContainText('Belegung');
  await expect(page.locator('.load-notice button')).toHaveText('Erneut versuchen');
  await expect(page.getByRole('heading', { name: 'Export (CSV)' })).toBeVisible();
});

test('late failed route leaves the new page and its busy state intact', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  const requests = await mockUI(page, { overrides: {
    '/vehicles': async route => { await gate; return route.fulfill({ status: 503, contentType: 'application/json', body: '{}' }); },
  } });
  await page.goto(origin + '/#/vehicles');
  try {
    await expect.poll(() => requests.some(r => r.path === '/vehicles')).toBe(true);
    await page.evaluate(() => { location.hash = '#/persons'; });
    await expect(page.locator('.page-head h2')).toHaveText('Personen');
    await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
    const response = page.waitForResponse('**/api/vehicles');
    release();
    await response;
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    await expect(page.locator('.page-head h2')).toHaveText('Personen');
    await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
  } finally { release(); }
});

test('short viewport keeps form actions reachable', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 568, height: 320 });
  await mockUI(page);
  await page.goto(origin + '/#/persons');
  await page.getByRole('button', { name: '+ Neu', exact: true }).click();
  // Wait for the sheet's entrance to settle before judging the final position.
  await expect.poll(async () => {
    const submit = await page.locator('#modal-submit').boundingBox();
    return submit.y + submit.height;
  }).toBeLessThanOrEqual(320);
  await expect(page.locator('#modal-body')).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('short-dialog.png') });
});

test('sliders retain alignment and keyboard behavior after resize', async ({ page }, testInfo) => {
  await mockUI(page);
  await page.goto(origin + '/#/vehicles');
  const seg = page.getByRole('radiogroup', { name: 'Lagerstatus' }).first();
  await expect(seg).toBeVisible();
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await expect.poll(() => seg.evaluate(node => {
      const thumb = node.querySelector('.seg-thumb').getBoundingClientRect();
      const active = node.querySelector('button.active').getBoundingClientRect();
      return Math.abs(thumb.left - active.left) < 1 && Math.abs(thumb.width - active.width) < 1;
    })).toBe(true);
  }
  await seg.locator('button.active').focus();
  await page.keyboard.press('ArrowRight');
  await expect(seg.locator('button:focus')).toHaveCount(1);
  await page.emulateMedia({ forcedColors: 'active', reducedMotion: 'reduce' });
  expect(await seg.locator('button.active').evaluate(node => getComputedStyle(node).outlineStyle)).not.toBe('none');
  await page.screenshot({ path: testInfo.outputPath('forced-colors.png') });
});

test('dashboard starts independent requests together', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  const requests = await mockUI(page, { overrides: {
    '/overview': async route => { await gate; await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ year: 2026, total_persons: 0 }) }); },
  } });
  await page.goto(origin + '/#/dashboard');
  try {
    await expect.poll(() => requests.filter(r => ['/occupancy', '/invoices/overdue', '/vehicles/ending-soon', '/portal-requests'].includes(r.path)).length).toBe(4);
    await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'true');
  } finally { release(); }
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
});

test('list recovery, pagination focus and skip link keep the current route', async ({ page }) => {
  await mockUI(page);
  await page.goto(origin + '/#/persons');
  const search = page.getByRole('searchbox');
  await search.fill('does-not-exist');
  await expect(page.getByText('Keine passenden Einträge.')).toBeVisible();
  await page.getByRole('button', { name: 'Suche zurücksetzen' }).click();
  await expect(search).toBeFocused();
  await expect(page.locator('.list-summary')).toHaveText('1–10 von 24 Einträgen');
  await page.getByRole('button', { name: 'Nächste Seite' }).click();
  await expect(page.locator('.list-summary')).toBeFocused();
  await expect(page.locator('.list-summary')).toHaveText('11–20 von 24 Einträgen');
  await page.locator('.skip-link').focus();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(origin + '/#/persons');
  await expect(page.locator('#page')).toBeFocused();
});

test('failed route can be retried', async ({ page }) => {
  let fails = true;
  await mockUI(page, { overrides: {
    '/persons': route => route.fulfill({ status: fails ? 503 : 200, contentType: 'application/json', body: fails ? JSON.stringify({ error: 'Verbindung unterbrochen' }) : '[]' }),
  } });
  await page.goto(origin + '/#/persons');
  await expect(page.getByRole('heading', { name: 'Ansicht nicht geladen', level: 2 })).toBeVisible();
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
  fails = false;
  await page.getByRole('button', { name: 'Erneut versuchen' }).click();
  await expect(page.locator('.page-head h2')).toHaveText('Personen');
  await expect(page.getByText('Noch keine Personen.')).toBeVisible();
});

test('login rejects duplicate submissions and restores the button on error', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  const requests = await mockUI(page, { loggedIn: false, overrides: {
    '/auth/login': async route => { await gate; await route.fulfill({ status: 401, contentType: 'application/json', body: JSON.stringify({ error: 'Anmeldung fehlgeschlagen' }) }); },
  } });
  await page.goto(origin + '/');
  await page.locator('#login-username').fill('Demo');
  await page.locator('#login-password').fill('synthetic-password');
  await page.locator('#login-form').evaluate(form => { form.requestSubmit(); form.requestSubmit(); });
  try {
    await expect.poll(() => requests.filter(r => r.path === '/auth/login').length).toBe(1);
    await expect(page.locator('#login-form button[type=submit]')).toBeDisabled();
  } finally { release(); }
  await expect(page.locator('#login-error')).toBeVisible();
  await expect(page.locator('#login-form button[type=submit]')).toBeEnabled();
  await expect(page.locator('#login-password')).toHaveValue('synthetic-password');
});

test('saving form blocks duplicate submit and Escape, preserves input on failure', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  const { persons } = require('./helpers/ui-fixture');
  const requests = await mockUI(page, { overrides: {
    '/persons': async route => {
      if (route.request().method() === 'GET') return route.fulfill({ contentType: 'application/json', body: JSON.stringify(persons) });
      await gate;
      return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'Speichern fehlgeschlagen' }) });
    },
  } });
  await page.goto(origin + '/#/persons');
  await page.getByRole('button', { name: '+ Neu', exact: true }).click();
  await page.locator('#f_first_name').fill('Test');
  await page.locator('#f_last_name').fill('Entwurf');
  await page.locator('#modal-form').evaluate(form => { form.requestSubmit(); form.requestSubmit(); });
  try {
    await expect.poll(() => requests.filter(r => r.path === '/persons' && r.method === 'POST').length).toBe(1);
    await page.keyboard.press('Escape');
    await expect(page.locator('#modal')).toBeVisible();
    await expect(page.locator('#modal-submit')).toHaveAttribute('aria-busy', 'true');
  } finally { release(); }
  await expect(page.locator('#modal-form-error')).toBeVisible();
  await expect(page.locator('#f_last_name')).toHaveValue('Entwurf');
  await expect(page.locator('#modal-submit')).toBeEnabled();
  await page.keyboard.press('Escape');
  await expect(page.locator('#modal')).not.toBeVisible();
});

for (const theme of ['light', 'dark']) {
  test(`shared UI accessibility and long content ${theme}`, async ({ page }) => {
    const { persons } = require('./helpers/ui-fixture');
    await page.setViewportSize({ width: 320, height: 740 });
    await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
    await mockUI(page, { overrides: {
      '/persons': [{ ...persons[0], first_name: 'Alexandra'.repeat(12), last_name: '長い名前 Müller', email: 'a'.repeat(120) + '@example.invalid' }],
    } });
    await page.goto(origin + '/#/persons');
    await expect(page.locator('.pcard')).toBeVisible();
    const clipped = await page.locator('.pcard').evaluate(card => {
      const box = card.getBoundingClientRect();
      return Array.from(card.querySelectorAll('button')).some(button => {
        const rect = button.getBoundingClientRect();
        return rect.right > box.right || rect.left < box.left;
      });
    });
    expect(clipped).toBe(false);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const audit = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    expect(audit.violations.filter(v => ['serious', 'critical'].includes(v.impact))).toEqual([]);
  });
}
