const { test: base, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

// All requests are intercepted by mockUI; these checks never contact a backend.
// Deliberately no screenshots: the review runner owns the two evidence batches.
const test = base.extend({
  browserErrors: [async ({ page }, use) => {
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('console', message => {
      if (message.type() === 'error') errors.push(message.text());
    });
    await use(errors);
    expect(errors, 'uncaught browser and console errors').toEqual([]);
  }, { auto: true }],
});
test.use({ serviceWorkers: 'block', locale: 'de-DE', timezoneId: 'Europe/Vienna', reducedMotion: 'reduce' });
test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date('2026-09-10T12:00:00Z'));
  await page.addInitScript(() => {
    localStorage.setItem('parkrr-veh', '0');
    localStorage.setItem('parkrr_portal_lang', 'de');
  });
});

const viewports = [[320, 'light'], [390, 'dark'], [768, 'light'], [1440, 'dark']];
const currency = value => new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(value);

async function arrange(page, width, theme, options = {}) {
  await page.setViewportSize({ width, height: 900 });
  await page.emulateMedia({ colorScheme: theme });
  return mockUI(page, { role: 'admin', ...options, overrides: { ...overrides, ...options.overrides } });
}

async function open(page, route) {
  const target = origin + '/#/' + route;
  // Same-hash navigation is not a reload; changed fixtures need a new request.
  if (page.url() === target) await page.reload();
  else await page.goto(target);
  await expect(page.locator(route.startsWith('portal') ? '#portal-view' : '#page')).toHaveAttribute('aria-busy', 'false');
  await expect(page.locator('.route-error')).toHaveCount(0);
  await page.evaluate(async () => {
    await document.fonts.ready;
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
}

async function noPageOverflow(page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'page overflow').toBe(true);
}

async function noWrites(requests) {
  expect(requests.filter(request => !['GET', 'HEAD'].includes(request.method)), 'layout inspection must not write').toEqual([]);
}

for (const [width, theme] of viewports) {
  test(`UI01 vehicle actions precede metadata with intact desktop columns at ${width}px ${theme}`, async ({ page }) => {
    const requests = await arrange(page, width, theme);
    await open(page, 'vehicles/1');
    const overview = page.locator('.vehicle-overview');
    const controls = overview.locator('.record-side');
    const metadata = overview.locator('.record-main');
    await expect(controls.getByRole('heading', { name: 'Status & Zahlung' })).toBeVisible();
    await expect(metadata.getByRole('link', { name: 'Demo Berger 1', exact: true })).toBeVisible();
    // Die Gruppen tragen den Namen des Gefährts (Screenreader: welches Gefährt?).
    await expect(controls.getByRole('radiogroup', { name: /^Lagerstatus · / })).toBeVisible();
    await expect(controls.getByRole('radiogroup', { name: /^Zahlstatus · / })).toBeVisible();
    expect(await controls.evaluate(node => !!(node.compareDocumentPosition(node.parentElement.querySelector('.record-main')) & Node.DOCUMENT_POSITION_FOLLOWING))).toBe(true);
    const [actionsBox, metadataBox] = await Promise.all([controls.boundingBox(), metadata.boundingBox()]);
    if (width < 800) expect(actionsBox.y + actionsBox.height).toBeLessThanOrEqual(metadataBox.y + 1);
    else {
      expect(metadataBox.x + metadataBox.width).toBeLessThanOrEqual(actionsBox.x + 1);
      expect(Math.abs(actionsBox.y - metadataBox.y)).toBeLessThanOrEqual(1);
    }
    // Native Tab must reach the payment control before the metadata person link.
    await controls.getByRole('radio', { name: 'Status: reserviert', exact: true }).focus();
    await page.keyboard.press('Tab');
    await expect(controls.getByRole('radio', { name: 'Bezahlt', exact: true })).toBeFocused();
    await noPageOverflow(page);
    await noWrites(requests);
  });
}

for (const role of ['admin', 'reader']) {
  test(`UI01 preserves ${role} rights for active and archived vehicle details`, async ({ page }) => {
    let archived = false;
    const fetchedStates = [];
    const requests = await arrange(page, 390, 'dark', { role, overrides: {
      '/vehicles': route => {
        fetchedStates.push(archived);
        const vehicle = { ...overrides['/vehicles'][0], archived };
        if (archived) Object.assign(vehicle, { status: 'collected', reserved_from: null, reserved_until: null });
        return route.fulfill({ json: [vehicle] });
      },
    } });
    for (const state of [false, true]) {
      archived = state;
      const previousFetches = fetchedStates.length;
      await open(page, 'vehicles/1');
      expect(fetchedStates.length, 'each state must fetch its current fixture').toBeGreaterThan(previousFetches);
      expect(fetchedStates.at(-1)).toBe(archived);
      await expect(page.locator('#page .balance a[href="#/persons/1"]')).toHaveText('Demo Berger 1');
      if (archived) {
        await expect(page.locator('.record-side').getByRole('heading', { name: 'Archiviert', exact: true })).toBeVisible();
        await expect(page.locator('.record-side').getByRole('radiogroup')).toHaveCount(0);
        const [archiveBox, metadataBox] = await Promise.all([
          page.locator('.record-side').boundingBox(), page.locator('.record-main').boundingBox(),
        ]);
        expect(archiveBox.y + archiveBox.height).toBeLessThanOrEqual(metadataBox.y + 1);
      }
      if (role === 'reader') {
        await expect(page.locator('#page').getByRole('radiogroup')).toHaveCount(0);
        await expect(page.getByRole('button', { name: /^(Bearbeiten|Reaktivieren|Erneut einstellen)$/ })).toHaveCount(0);
      } else if (archived) {
        await expect(page.getByRole('button', { name: 'Reaktivieren', exact: true })).toBeVisible();
      } else {
        await expect(page.locator('.record-side').getByRole('heading', { name: 'Status & Zahlung', exact: true })).toBeVisible();
        await expect(page.locator('.detail-head').getByRole('button', { name: 'Bearbeiten', exact: true })).toBeVisible();
      }
      await noPageOverflow(page);
    }
    await noWrites(requests);
  });
}

for (const [width, theme] of viewports) {
  test(`UI02 invoice overflow cue and document actions stay usable at ${width}px ${theme}`, async ({ page }) => {
    const requests = await arrange(page, width, theme);
    await page.addInitScript(() => { window.printCalls = 0; window.print = () => { window.printCalls++; }; });
    await open(page, 'invoices/1');
    const region = page.getByRole('region', { name: 'Rechnungspositionen', exact: true });
    const hint = page.locator('.invoice-scroll-hint');
    await expect(region).toHaveAttribute('tabindex', '0');
    const overflow = await region.evaluate(node => node.scrollWidth > node.clientWidth + 1);
    if (overflow) {
      await expect(hint).toBeVisible();
      expect(await hint.getAttribute('id')).toBeTruthy();
      await expect(region).toHaveAttribute('aria-describedby', await hint.getAttribute('id'));
      await region.focus();
      await page.keyboard.press('ArrowRight');
      await expect.poll(() => region.evaluate(node => node.scrollLeft)).toBeGreaterThan(0);
    } else {
      await expect(hint).toBeHidden();
      await expect(region).not.toHaveAttribute('aria-describedby');
    }
    const actions = page.locator('.invoice-document-actions');
    const pdf = actions.getByRole('link', { name: 'PDF', exact: true });
    const print = actions.getByRole('button', { name: 'Drucken', exact: true });
    await expect(pdf).toHaveAttribute('href', '/api/invoices/1/pdf');
    await expect(pdf).toHaveAttribute('target', '_blank');
    const [pdfBox, printBox] = await Promise.all([pdf.boundingBox(), print.boundingBox()]);
    expect(Math.abs(pdfBox.y - printBox.y), 'PDF and print wrap as one group').toBeLessThanOrEqual(1);
    expect(pdfBox.x + pdfBox.width).toBeLessThan(printBox.x);
    await print.click();
    expect(await page.evaluate(() => window.printCalls)).toBe(1);
    await expect(region.locator('tbody tr')).toHaveCount(1);
    await expect(region.locator('tbody td').nth(4)).toHaveText(currency(680));
    await noPageOverflow(page);
    // Re-measure after a real viewport change, without rebuilding the route.
    await page.setViewportSize({ width: overflow ? 1440 : 320, height: 900 });
    if (overflow) {
      await expect(hint).toBeHidden();
      await expect(region).not.toHaveAttribute('aria-describedby');
    } else {
      await expect(hint).toBeVisible();
      await expect(region).toHaveAttribute('aria-describedby', await hint.getAttribute('id'));
    }
    await noWrites(requests);
  });
}

test('UI02 print retains all invoice columns and totals without screen-only cues', async ({ page }) => {
  await arrange(page, 1440, 'light');
  await open(page, 'invoices/1');
  const invoice = page.locator('.invoice-doc');
  const originalText = await invoice.locator('.inv-table, .inv-totals').allTextContents();
  await page.emulateMedia({ media: 'print' });
  await expect(invoice).toBeVisible();
  await expect(page.locator('.invoice-document-actions')).toBeHidden();
  await expect(page.locator('.invoice-scroll-hint')).toBeHidden();
  await expect(page.locator('.app-header')).toBeHidden();
  await expect(page.locator('.tabbar')).toBeHidden();
  expect(await invoice.locator('.inv-table, .inv-totals').allTextContents()).toEqual(originalText);
  const geometry = await invoice.locator('.invoice-table-scroll').evaluate(node => ({
    overflow: getComputedStyle(node).overflowX,
    tableMinimum: getComputedStyle(node.querySelector('table')).minWidth,
    clipped: node.querySelector('table').getBoundingClientRect().right > node.getBoundingClientRect().right + 1,
  }));
  expect(geometry).toEqual({ overflow: 'visible', tableMinimum: '0px', clipped: false });
});

for (const role of ['admin', 'editor', 'reader']) {
  test(`UI03 menu and command palette describe billing settings with ${role} access`, async ({ page }) => {
    const requests = await arrange(page, 390, 'light', { role });
    await open(page, 'dashboard');
    await page.getByRole('button', { name: 'Menü', exact: true }).click();
    const menu = page.getByRole('dialog', { name: 'Menü', exact: true });
    const billing = menu.getByRole('button', { name: 'Rechnungs-Einstellungen', exact: true });
    // "Rechnungen" ist seit dem Rechnungslisten-Durchgang die echte Liste (alle Rollen)
    // und darf nie wieder zu den Einstellungen führen.
    await expect(menu.getByRole('button', { name: 'Rechnungen', exact: true })).toHaveCount(1);
    if (role === 'admin') {
      await billing.click();
      await expect(page).toHaveURL(/#\/billing$/);
      await expect(page.locator('.billing-form')).toBeVisible();
    } else {
      await expect(billing).toHaveCount(0);
      await page.keyboard.press('Escape');
    }
    await page.getByRole('button', { name: /^Suchen/ }).click();
    const palette = page.getByRole('dialog', { name: 'Suche', exact: true });
    await palette.getByRole('combobox').fill('Rechnung');
    if (role === 'admin') {
      const result = palette.getByRole('option').filter({ hasText: 'Rechnungs-Einstellungen' });
      await expect(result).toHaveCount(1);
      await result.click();
      await expect(page).toHaveURL(/#\/billing$/);
    } else {
      await expect(palette.getByRole('option').filter({ hasText: 'Rechnungen' })).toHaveCount(1);
      await expect(palette.getByText('Rechnungs-Einstellungen', { exact: true })).toHaveCount(0);
      await page.keyboard.press('Escape');
      await open(page, 'billing');
      await expect(page.getByText('Nur für Administratoren.', { exact: true })).toBeVisible();
      expect(requests.filter(request => request.path === '/billing/settings')).toEqual([]);
    }
    await noWrites(requests);
  });
}

for (const [width, theme] of viewports) {
  test(`UI04 tax currency stays whole and unclipped with large signed values at ${width}px ${theme}`, async ({ page }) => {
    const report = { ...overrides['/reports/tax-year'], total_by_date: 1234567.89,
      expense_total: 2234567.89, depreciation_total: 250000, surplus: -1250000 };
    const requests = await arrange(page, width, theme, { overrides: { '/reports/tax-year': report } });
    await open(page, 'taxyear');
    const values = page.locator('.screen-taxyear > .stat-grid .stat .value');
    await expect(values).toHaveText([currency(1234567.89), currency(2484567.89), currency(-1250000)]);
    for (const fontSize of ['100%', '200%']) {
      await page.evaluate(size => { document.documentElement.style.fontSize = size; }, fontSize);
      await expect.poll(() => values.evaluateAll(nodes => nodes.every(node => {
        const range = document.createRange();
        range.selectNodeContents(node);
        const text = range.getBoundingClientRect();
        const card = node.closest('.stat').getBoundingClientRect();
        const lines = [...range.getClientRects()].map(rect => Math.round(rect.top));
        return new Set(lines).size === 1 && text.left >= card.left - 1 && text.right <= card.right + 1
          && node.scrollWidth <= node.clientWidth + 1;
      })), `whole currency at ${fontSize} font size`).toBe(true);
      await noPageOverflow(page);
    }
    await expect(page.getByRole('button', { name: 'Jahr abschließen', exact: true })).toBeDisabled();
    await noWrites(requests);
  });
}

const exportTargets = ['/api/export/outstanding', '/api/reports/outstanding.pdf', '/api/export/invoices',
  '/api/export/payments', '/api/export/charges', '/api/export/persons', '/api/export/vehicles', '/api/export/occupancy'];

for (const [width, theme] of viewports) {
  test(`UI05 all exports and settings sections retain clear spacing at ${width}px ${theme}`, async ({ page }) => {
    const requests = await arrange(page, width, theme);
    await open(page, 'dashboard');
    const exports = page.locator('.chart-card').filter({ has: page.getByRole('heading', { name: 'Export (CSV)', exact: true }) }).getByRole('link');
    await expect(exports).toHaveCount(8);
    expect(await exports.evaluateAll(nodes => nodes.map(node => node.getAttribute('href')))).toEqual(exportTargets);
    for (const link of await exports.all()) {
      await expect(link).toBeVisible();
      await expect(link).toHaveAttribute('download', '');
    }
    const separated = await exports.evaluateAll(nodes => {
      const boxes = nodes.map(node => node.getBoundingClientRect());
      return boxes.every((box, index) => boxes.slice(index + 1).every(other => {
        const horizontal = Math.max(other.left - box.right, box.left - other.right);
        const vertical = Math.max(other.top - box.bottom, box.top - other.bottom);
        return horizontal >= 6 || vertical >= 6;
      }));
    });
    expect(separated, 'download targets must not touch or overlap').toBe(true);
    await noPageOverflow(page);
    await open(page, 'settings');
    const readiness = page.getByRole('region', { name: 'Betriebsbereitschaft', exact: true });
    const administration = page.locator('.admin-links');
    await expect(readiness).toBeVisible();
    const [readyBox, adminBox] = await Promise.all([readiness.boundingBox(), administration.boundingBox()]);
    expect(adminBox.y - (readyBox.y + readyBox.height), 'settings section gap').toBeGreaterThanOrEqual(8);
    await noPageOverflow(page);
    await noWrites(requests);
  });
}

for (const [width, theme] of viewports) {
  test(`portal identity and controls stay separate at ${width}px ${theme}`, async ({ page }) => {
    const requests = await arrange(page, width, theme, { overrides: {
      '/portal/summary': { ...overrides['/portal/summary'], operator_name: 'Demo Einstellbetrieb Nord mit einem langen Betriebsnamen',
        person_name: 'Alexandra Musterfrau mit einem langen Nachnamen' },
    } });
    await open(page, 'portal/demo');
    const identity = page.locator('.portal-identity');
    const actions = page.locator('.portal-actions');
    await expect(identity).toContainText('Alexandra Musterfrau');
    await expect(actions.getByRole('button', { name: 'Portal verlassen', exact: true })).toBeVisible();
    const [nameBox, actionsBox] = await Promise.all([identity.boundingBox(), actions.boundingBox()]);
    const separated = actionsBox.x >= nameBox.x + nameBox.width - 1 || actionsBox.y >= nameBox.y + nameBox.height - 1;
    expect(separated, 'portal identity and controls do not overlap').toBe(true);
    expect(actionsBox.x).toBeGreaterThanOrEqual(0);
    expect(actionsBox.x + actionsBox.width).toBeLessThanOrEqual(width + 1);
    await actions.getByRole('button', { name: 'EN – Switch to English', exact: true }).click();
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    await expect(page.locator('.portal-actions').getByRole('button', { name: 'Leave portal', exact: true })).toBeVisible();
    await noPageOverflow(page);
    await noWrites(requests);
  });
}

test('single and plural audit and hall counts use the corresponding German noun', async ({ page }) => {
  let count = 1;
  await arrange(page, 390, 'light', { overrides: {
    '/audit': route => route.fulfill({ json: Array.from({ length: count }, (_, i) => ({ ...overrides['/audit'][0], id: i + 1 })) }),
    '/garages/1/halls': route => route.fulfill({ json: [{ ...overrides['/garages/1/halls'][0], spot_count: count }] }),
  } });
  for (const value of [1, 2]) {
    count = value;
    await open(page, 'audit');
    await expect(page.locator('.list-summary')).toHaveText(value === 1 ? '1 Änderung geladen' : '2 Änderungen geladen');
    await open(page, 'garage/1');
    await expect(page.locator('.hall-list .card-meta')).toContainText(value === 1 ? '1 Gefährt' : '2 Gefährte');
    if (value === 1) await expect(page.locator('.hall-list .card-meta')).not.toContainText('1 Gefährte');
  }
});

for (const [width, theme] of [[390, 'light'], [1440, 'dark']]) {
  test(`focused polished surfaces have no serious accessibility violations at ${width}px ${theme}`, async ({ page }) => {
    test.setTimeout(90000);
    await arrange(page, width, theme);
    for (const [route, selector] of [['vehicles/1', '.vehicle-overview'], ['invoices/1', '.screen-invoices'],
      ['taxyear', '.screen-taxyear > .stat-grid'], ['settings', '.set-grid'], ['portal/demo', '.portal-head']]) {
      await open(page, route);
      const result = await new AxeBuilder({ page }).include(selector).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
      expect(result.violations.filter(issue => ['serious', 'critical'].includes(issue.impact)), route).toEqual([]);
    }
  });
}
