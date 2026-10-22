const { test, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const path = require('node:path');
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

// Mock-only: every network request is intercepted; no real operator data or DB.
test.use({ serviceWorkers: 'block' });
test.beforeEach(async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
});

const endpoint = '/reports/tax-import/preview';
const report = overrides['/reports/tax-year'];
const draft = {
  version: 1, year: 2025, property_id: 1, source: 'Synthetische Jahreswerte – Prüfung €', reviewer: 'Testperson',
  entries: [
    { external_id: 'income-1', kind: 'income_adjustment', amount_eur: '-20.00', description: 'Synthetische Einnahmenkorrektur' },
    { external_id: 'expense-1', kind: 'expense_adjustment', amount_eur: '25.50', description: 'Synthetische Werbungskostenkorrektur' },
    { external_id: 'depreciation-1', kind: 'external_depreciation', amount_eur: '100.00', description: 'Synthetische externe AfA' },
  ],
};
const reply = {
  version: 1, year: 2025, property_id: 1, source: draft.source, reviewer: draft.reviewer,
  preview: true, persisted: false, verified: false,
  entries: draft.entries.map(({ amount_eur, ...entry }) => ({ ...entry, amount_cents: Math.round(Number(amount_eur) * 100) })),
  totals_cents: { income_adjustment: -2000, expense_adjustment: 2550, external_depreciation: 10000 },
  notes: ['Synthetischer Prüffall; kein Abgleich mit Bestandsdaten.'],
};

async function openImport(page, { handler = reply, properties = report.properties.slice(0, 1), year = 2025, ...changes } = {}) {
  const requests = await mockUI(page, { role: 'admin', overrides: {
    ...overrides,
    '/reports/tax-year': { ...report, write_enabled: false, locked: true, year, properties, ...changes },
    [endpoint]: handler,
  } });
  await page.goto(origin + '/#/taxyear');
  const section = page.locator('.tax-import');
  await expect(section).not.toHaveAttribute('open', '');
  await expect(section.locator('summary').first()).toHaveText('Externe Jahreswerte prüfen · Importvorschau');
  await section.locator('summary').first().click();
  return { section, requests };
}

async function selectFile(section, content = JSON.stringify(draft), name = 'synthetische-jahreswerte.json') {
  await section.getByLabel('JSON-Datei mit Jahreswerten').setInputFiles({ name, mimeType: 'application/json',
    buffer: Buffer.isBuffer(content) ? content : Buffer.from(content, 'utf8') });
}

async function preview(section) {
  await section.getByRole('button', { name: 'Datei prüfen', exact: true }).click();
}

function expectPreviewOnly(requests, count) {
  expect(requests.filter(request => request.method !== 'GET').map(({ path, method }) => ({ path, method })))
    .toEqual(Array.from({ length: count }, () => ({ path: endpoint, method: 'POST' })));
}

async function downloadJSON(page, button, filename) {
  const pending = page.waitForEvent('download');
  await button.click();
  const download = await pending;
  expect(download.suggestedFilename()).toBe(filename);
  const chunks = [];
  for await (const chunk of await download.createReadStream()) chunks.push(chunk);
  return JSON.parse(Buffer.concat(chunks).toString('utf8'));
}

for (const width of [320, 390, 1440]) {
  test(`readonly annual import preview is accessible at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const { section, requests } = await openImport(page);
    const originalTotals = await page.locator('.stat-grid, .tax-property-summary').allTextContents();
    await expect(page.getByRole('button', { name: 'Jahr entsperren', exact: true })).toBeDisabled();
    await expect(section.getByLabel('Steuerobjekt für die Vorschau')).toHaveValue('1');
    await expect(section.getByLabel('JSON-Datei mit Jahreswerten')).toHaveAttribute('accept', '.json,application/json');
    await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeDisabled();
    await selectFile(section);
    expectPreviewOnly(requests, 0); // Selecting a local file must not upload it.
    await preview(section);
    await expect(section.getByRole('status')).toHaveText('3 Einträge geprüft · nicht gespeichert · nicht fachlich bestätigt.');
    await section.getByText('Einträge ansehen · 3', { exact: true }).click();
    await expect(section.locator('.tax-import-totals dt')).toHaveText(['Einnahmenkorrekturen', 'Werbungskostenkorrekturen', 'Externe AfA']);
    await expect(section.locator('.tax-import-totals dd').nth(0)).toContainText(/-20,00\s*€/);
    await expect(section.locator('.tax-import-totals dd').nth(1)).toContainText(/25,50\s*€/);
    await expect(section.locator('.tax-import-totals dd').nth(2)).toContainText(/100,00\s*€/);
    await expect(section.locator('.tax-import-result')).not.toContainText(/Gesamtsumme|Überschuss|Steuerbetrag/);
    await expect(section.getByRole('button', { name: /übernehmen|speichern|buchen/i })).toHaveCount(0);
    await expect(page.getByText('Überschuss 2025', { exact: true })).toBeVisible();
    expect(await page.locator('.stat-grid, .tax-property-summary').allTextContents()).toEqual(originalTotals);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    for (const theme of ['light', 'dark']) {
      await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme);
      const result = await new AxeBuilder({ page }).include('.tax-import').withTags(['wcag22a', 'wcag22aa']).analyze();
      expect(result.violations.filter(v => ['serious', 'critical'].includes(v.impact))).toEqual([]);
      if (process.env.PARKRR_CAPTURE_DIR) {
        await section.screenshot({ path: path.join(process.env.PARKRR_CAPTURE_DIR, `tax-import-${theme}-${width}.png`),
          style: '.app-header, .tabbar { visibility: hidden !important; }' });
      }
    }
    if (width === 390) {
      await page.evaluate(() => document.documentElement.style.fontSize = '32px');
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    }
    expectPreviewOnly(requests, 1);
    expect(errors).toEqual([]);
  });
}

test('example and preview downloads are explicit non-authoritative drafts; discard clears the file', async ({ page }) => {
  const { section, requests } = await openImport(page);
  const example = await downloadJSON(page, section.getByRole('button', { name: 'Beispieldatei herunterladen' }),
    'parkrr-jahreswerte-beispiel-2025.json');
  expect(example).toMatchObject({ version: 1, year: 2025, property_id: 1 });
  expect(example.source).toContain('SYNTHETISCHES BEISPIEL');
  expect(example.entries).toHaveLength(3);
  expect(example.entries.every(entry => typeof entry.amount_eur === 'string')).toBe(true);
  expectPreviewOnly(requests, 0);
  await selectFile(section);
  await preview(section);
  const downloaded = await downloadJSON(page, section.getByRole('button', { name: 'Vorschau herunterladen' }),
    'parkrr-importvorschau-2025.json');
  expect(downloaded).toEqual({ format: 'parkrr-tax-import-preview', version: 1, result: reply });
  await section.getByRole('button', { name: 'Auswahl verwerfen' }).click();
  await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeDisabled();
  await expect(section.getByLabel('JSON-Datei mit Jahreswerten')).toHaveValue('');
  await expect(section.getByLabel('JSON-Datei mit Jahreswerten')).toBeFocused();
  await expect(section.getByRole('status', { includeHidden: true })).toBeEmpty();
  await expect(section.locator('.tax-import-result')).toBeEmpty();
  expectPreviewOnly(requests, 1);
});

test('multiple properties require a choice, and changing it invalidates a completed preview', async ({ page }) => {
  const { section, requests } = await openImport(page, { properties: report.properties });
  const property = section.getByLabel('Steuerobjekt für die Vorschau');
  await expect(property).toHaveValue('');
  await expect(section.getByRole('button', { name: 'Beispieldatei herunterladen' })).toBeDisabled();
  await selectFile(section);
  await preview(section);
  expectPreviewOnly(requests, 0);
  await property.selectOption('1');
  await preview(section);
  await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeEnabled();
  await property.selectOption('2');
  await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeDisabled();
  await expect(section.getByRole('status', { includeHidden: true })).toBeEmpty();
  await expect(section.locator('.tax-import-result')).toBeEmpty();
  const example = await downloadJSON(page, section.getByRole('button', { name: 'Beispieldatei herunterladen' }),
    'parkrr-jahreswerte-beispiel-2025.json');
  expect(example.property_id).toBe(2);
  expectPreviewOnly(requests, 1);
});

test('file replacement invalidates the previous result before another upload', async ({ page }) => {
  const { section, requests } = await openImport(page);
  await selectFile(section);
  await preview(section);
  await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeEnabled();
  await selectFile(section, JSON.stringify({ ...draft, source: 'Anderer Entwurf' }), 'anderer-entwurf.json');
  await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeDisabled();
  await expect(section.getByRole('status', { includeHidden: true })).toBeEmpty();
  await expect(section.locator('.tax-import-result')).toBeEmpty();
  expectPreviewOnly(requests, 1);
});

test('duplicate JSON keys reach server validation unchanged rather than being silently collapsed', async ({ page }) => {
  const raw = '{\r\n "version":1,"year":2025,"year":2026,"property_id":1,"source":"Prüfung €",\r\n "reviewer":"Test","entries":[]\r\n}\n';
  let received;
  const { section, requests } = await openImport(page, { handler: async route => {
    received = route.request().postData();
    expect(route.request().headers()['content-type']).toBe('application/json');
    await route.fulfill({ status: 400, json: { error: 'Doppeltes JSON-Feld: year.' } });
  } });
  await selectFile(section, raw);
  await preview(section);
  await expect(section.getByRole('alert')).toContainText('Doppeltes JSON-Feld: year.');
  expect(received).toBe(raw);
  await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeDisabled();
  expectPreviewOnly(requests, 1);
});

test('server rejection retains the selected file and permits an explicit retry', async ({ page }) => {
  const bodies = [];
  const { section, requests } = await openImport(page, { handler: async route => {
    bodies.push(route.request().postData());
    await route.fulfill({ status: bodies.length === 1 ? 503 : 200,
      json: bodies.length === 1 ? { error: 'Prüfung vorübergehend nicht verfügbar.' } : reply });
  } });
  await selectFile(section);
  await preview(section);
  await expect(section.getByRole('alert')).toContainText('Die Auswahl bleibt erhalten; keine Daten wurden übernommen.');
  await expect(section.getByLabel('JSON-Datei mit Jahreswerten')).not.toHaveValue('');
  await expect(section.getByRole('button', { name: 'Datei prüfen', exact: true })).toBeEnabled();
  await preview(section);
  await expect(section.getByRole('status')).toContainText('nicht gespeichert');
  await expect(section.getByRole('alert')).toBeHidden();
  expect(bodies).toEqual([JSON.stringify(draft), JSON.stringify(draft)]);
  expectPreviewOnly(requests, 2);
});

for (const changed of ['property', 'file', 'discard']) {
  test(`a late response cannot validate a draft after ${changed} changes`, async ({ page }) => {
    let release, started;
    const pending = new Promise(resolve => { release = resolve; });
    const received = new Promise(resolve => { started = resolve; });
    const { section, requests } = await openImport(page, { properties: report.properties, handler: async route => {
      started();
      await pending;
      await route.fulfill({ json: reply });
    } });
    await section.getByLabel('Steuerobjekt für die Vorschau').selectOption('1');
    await selectFile(section);
    await preview(section);
    await received;
    await expect(section.getByRole('form')).toHaveAttribute('aria-busy', 'true');
    try {
      if (changed === 'property') await section.getByLabel('Steuerobjekt für die Vorschau').selectOption('2');
      if (changed === 'file') await selectFile(section, JSON.stringify(draft), 'neue-datei.json');
      if (changed === 'discard') await section.getByRole('button', { name: 'Auswahl verwerfen' }).click();
    } finally {
      release();
    }
    await expect(section.getByRole('button', { name: 'Datei prüfen', exact: true })).toBeEnabled();
    await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeDisabled();
    await expect(section.getByRole('status', { includeHidden: true })).toBeEmpty();
    await expect(section.locator('.tax-import-result')).toBeEmpty();
    expectPreviewOnly(requests, 1);
  });
}

for (const [label, response] of [
  ['wrong year', { ...reply, year: 2026 }],
  ['wrong property', { ...reply, property_id: 2 }],
  ['persisted response', { ...reply, persisted: true }],
  ['verified response', { ...reply, verified: true }],
  ['non-preview response', { ...reply, preview: false }],
  ['non-integer money', { ...reply, totals_cents: { ...reply.totals_cents, external_depreciation: 1.5 } }],
]) {
  test(`rejects a ${label} without showing an accepted result`, async ({ page }) => {
    const { section, requests } = await openImport(page, { handler: response });
    await selectFile(section);
    await preview(section);
    await expect(section.getByRole('alert')).toBeVisible();
    await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeDisabled();
    await expect(section.locator('.tax-import-result')).toBeEmpty();
    expectPreviewOnly(requests, 1);
  });
}

for (const [label, content, message] of [
  ['empty', Buffer.alloc(0), 'höchstens 64 KiB'],
  ['over 64 KiB', Buffer.alloc(64 * 1024 + 1, ' '), 'höchstens 64 KiB'],
  ['invalid UTF-8', Buffer.from([0x7b, 0x22, 0xc3, 0x28, 0x22, 0x7d]), 'nicht als UTF-8 lesbar'],
]) {
  test(`${label} files fail locally without an upload`, async ({ page }) => {
    const { section, requests } = await openImport(page);
    await selectFile(section, content);
    await preview(section);
    await expect(section.getByRole('alert')).toContainText(message);
    await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeDisabled();
    expectPreviewOnly(requests, 0);
  });
}

test('response descriptions render as text, not imported markup', async ({ page }) => {
  const description = '<img src=x onerror="window.importInjected=true">';
  const { section, requests } = await openImport(page, { handler: { ...reply,
    entries: [{ ...reply.entries[0], description }, ...reply.entries.slice(1)] } });
  await selectFile(section);
  await preview(section);
  await section.getByText('Einträge ansehen · 3', { exact: true }).click();
  await expect(section.getByText(description, { exact: true })).toBeVisible();
  await expect(section.locator('.tax-import-result img')).toHaveCount(0);
  expect(await page.evaluate(() => window.importInjected)).toBeUndefined();
  expectPreviewOnly(requests, 1);
});

test('exactly 64 KiB is accepted and forwarded intact', async ({ page }) => {
  const body = Buffer.from(JSON.stringify(draft), 'utf8');
  const content = Buffer.concat([body, Buffer.alloc(64 * 1024 - body.length, ' ')]);
  let received;
  const { section, requests } = await openImport(page, { handler: async route => {
    received = route.request().postDataBuffer();
    await route.fulfill({ json: reply });
  } });
  await selectFile(section, content);
  await preview(section);
  await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeEnabled();
  expect(received.equals(content)).toBe(true);
  expectPreviewOnly(requests, 1);
});

for (const role of ['reader', 'editor']) {
  test(`${role} does not get the admin import UI or fetch a tax report`, async ({ page }) => {
    const requests = await mockUI(page, { role, overrides });
    await page.goto(origin + '/#/taxyear');
    await expect(page.getByText('Nur für Administratoren und die Buchhaltung.', { exact: true })).toBeVisible();
    await expect(page.locator('.tax-import')).toHaveCount(0);
    expect(requests.filter(request => request.path.startsWith('/reports/tax-'))).toEqual([]);
    expectPreviewOnly(requests, 0);
  });
}

test('unsupported years never offer implicit creation or import', async ({ page }) => {
  const { section, requests } = await openImport(page, { year: 2024 });
  await expect(section).toContainText('Das Dateiformat unterstützt derzeit die Jahre 2025 und 2026.');
  await expect(section.getByRole('form')).toHaveCount(0);
  expectPreviewOnly(requests, 0);
});

test('missing properties explain the prerequisite without creating an object', async ({ page }) => {
  const { section, requests } = await openImport(page, { properties: [] });
  await expect(section).toContainText('Im Lesemodus wird kein Objekt angelegt.');
  await expect(section.getByRole('form')).toHaveCount(0);
  expectPreviewOnly(requests, 0);
});

test('the 2026 template uses the displayed report year, not the computer year', async ({ page }) => {
  const { section, requests } = await openImport(page, { year: 2026 });
  const example = await downloadJSON(page, section.getByRole('button', { name: 'Beispieldatei herunterladen' }),
    'parkrr-jahreswerte-beispiel-2026.json');
  expect(example).toMatchObject({ year: 2026, property_id: 1 });
  expectPreviewOnly(requests, 0);
});
