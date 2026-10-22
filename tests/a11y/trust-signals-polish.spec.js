const { test: base, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides, invoice } = require('./helpers/overhaul-fixture');

// Polish 2026-10-14: danger signal, money words, location state, the global invoice
// list, planner accessibility and small consistency fixes. Every request is
// intercepted by mockUI; nothing here contacts a backend or writes.
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
  await page.clock.setFixedTime(new Date('2026-09-10T12:00:00Z'));
  await page.addInitScript(() => {
    localStorage.setItem('parkrr-veh', '0');
    localStorage.setItem('parkrr_portal_lang', 'de');
  });
});

// Five documents covering every list filter: one open and not yet due, one open and
// overdue, one paid, and a canceled original with its storno counter-document.
const invoices = [
  { ...invoice, id: 1, number: '2026-0012', status: 'offen', issued_on: '2026-09-01', due_on: '2026-09-15', person_name: 'Demo Berger 1' },
  { ...invoice, id: 2, number: '2026-0011', status: 'teilbezahlt', issued_on: '2026-08-10', due_on: '2026-09-01', open_amount: 180, paid_amount: 500, person_id: 2, person_name: 'Demo Gruber 2' },
  { ...invoice, id: 3, number: '2026-0010', status: 'bezahlt', issued_on: '2026-07-01', due_on: '2026-07-15', open_amount: 0, paid_amount: 680, person_name: 'Demo Berger 1' },
  { ...invoice, id: 4, number: '2026-0009', status: 'storniert', canceled: true, issued_on: '2026-06-01', due_on: '2026-06-15', person_name: 'Demo Müller 3' },
  { ...invoice, id: 5, number: '2026-0013', status: 'storno', cancels_id: 4, total: -680, issued_on: '2026-06-02', due_on: null, person_name: 'Demo Müller 3' },
];

async function arrange(page, width, theme, options = {}) {
  await page.setViewportSize({ width, height: 900 });
  await page.emulateMedia({ colorScheme: theme });
  return mockUI(page, { role: 'admin', ...options, overrides: { ...overrides, '/invoices': invoices, ...options.overrides } });
}

async function open(page, route) {
  const target = origin + '/#/' + route;
  if (page.url() === target) await page.reload();
  else await page.goto(target);
  await expect(page.locator(route.startsWith('portal') ? '#portal-view' : '#page')).toHaveAttribute('aria-busy', 'false');
  await expect(page.locator('.route-error')).toHaveCount(0);
}

const noWrites = requests => expect(requests.filter(r => !['GET', 'HEAD'].includes(r.method)), 'must not write').toEqual([]);
const noOverflow = async page => expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'page overflow').toBe(true);
const seriousAxe = async (page, include) => {
  const builder = new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21aa']);
  if (include) builder.include(include);
  const result = await builder.analyze();
  return result.violations.filter(v => ['serious', 'critical'].includes(v.impact)).map(v => v.id + ': ' + v.nodes.map(n => n.target.join(' ')).join(', '));
};

for (const [width, theme] of [[390, 'dark'], [1440, 'light']]) {
  test(`invoice list filters open, overdue and paid in one tap at ${width}px ${theme}`, async ({ page }) => {
    const requests = await arrange(page, width, theme);
    await open(page, 'invoices');
    await expect(page.getByRole('heading', { level: 2, name: 'Rechnungen' })).toBeVisible();
    const rows = page.locator('#page a.inv-link');
    await expect(rows).toHaveCount(5);
    await expect(rows.first()).toContainText('Demo Berger 1');
    const filter = page.getByRole('group', { name: 'Rechnungen filtern' });
    await expect(filter.getByRole('button', { name: 'Alle', exact: true })).toHaveAttribute('aria-pressed', 'true');

    await filter.getByRole('button', { name: 'Überfällig', exact: true }).click();
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText('2026-0011');
    await expect(rows.first().locator('.is-overdue')).toHaveText('überfällig seit 1.9.2026');
    await expect(filter.getByRole('button', { name: 'Überfällig', exact: true })).toHaveAttribute('aria-pressed', 'true');

    await filter.getByRole('button', { name: 'Offen', exact: true }).click();
    await expect(rows).toHaveCount(2);
    await expect(page.locator('#page').getByText('fällig am 15.9.2026', { exact: true })).toBeVisible();
    await filter.getByRole('button', { name: 'Bezahlt', exact: true }).click();
    await expect(rows).toHaveCount(1);
    await expect(rows.first()).toContainText('2026-0010');

    // Location: the page lives in the menu, so ☰ is marked and so is its entry.
    await expect(page.locator('#menu-btn')).toHaveClass(/is-current/);
    await expect(page.locator('.tab[aria-current="page"]')).toHaveCount(0);
    await page.getByRole('button', { name: 'Menü', exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'Menü' }).getByRole('button', { name: 'Rechnungen', exact: true })).toHaveAttribute('aria-current', 'page');
    await page.keyboard.press('Escape');

    expect(await seriousAxe(page)).toEqual([]);
    await noOverflow(page);
    await rows.first().click();
    await expect(page).toHaveURL(/#\/invoices\/3$/);
    await noWrites(requests);
  });
}

test('an empty invoice list explains where invoices come from', async ({ page }) => {
  await arrange(page, 390, 'light', { overrides: { '/invoices': [] } });
  await open(page, 'invoices');
  await expect(page.getByText('Noch keine Rechnungen. Eine Rechnung erstellst du auf der Personenseite mit „+ Rechnung“.')).toBeVisible();
});

test('person danger zone lives only in the edit dialog and keeps confirmation and undo', async ({ page }) => {
  const requests = await arrange(page, 390, 'light');
  await open(page, 'persons');
  const list = page.locator('#page');
  await expect(list.getByRole('button', { name: /anonymisieren$/ })).toHaveCount(0);
  await expect(list.getByRole('button', { name: /löschen$/ })).toHaveCount(0);
  await expect(list.getByRole('button', { name: /bearbeiten$/ }).first()).toBeVisible();

  await list.getByRole('button', { name: /bearbeiten$/ }).first().click();
  const edit = page.getByRole('dialog', { name: 'Person bearbeiten', exact: true });
  await expect(edit.locator('.person-danger')).toBeVisible();
  await page.keyboard.press('Escape');
  await open(page, 'persons/1');
  await expect(page.locator('#page .person-danger')).toHaveCount(0);
  await page.getByRole('button', { name: 'Bearbeiten', exact: true }).click();
  const zone = edit.locator('.person-danger');
  await expect(zone.locator('.tf-danger-label')).toHaveText('Gefahrenzone');
  await zone.getByRole('button', { name: 'Anonymisieren (DSGVO)' }).click();
  await expect(page.locator('#confirm')).toHaveClass(/modal-danger/);
  await expect(page.locator('#confirm-ok')).toHaveClass(/btn-danger/);
  await page.keyboard.press('Escape');
  await expect(edit).toBeVisible();
  // Löschen fragt immer nach, und Rückgängig im Hinweis nimmt es danach noch zurück,
  // bevor irgendetwas den Server erreicht.
  await zone.getByRole('button', { name: 'Person löschen' }).click();
  await expect(page.locator('#confirm-title')).toHaveText('Person löschen?');
  await page.keyboard.press('Escape');
  await expect(edit).toBeVisible();
  await zone.getByRole('button', { name: 'Person löschen' }).click();
  await page.locator('#confirm-ok').click();
  await expect(edit).toBeHidden();
  await page.locator('#toast').getByRole('button', { name: 'Rückgängig' }).click();
  await expect(page.locator('#toast')).toContainText('Abgebrochen');
  await noWrites(requests);
});

test('creating an invoice is a neutral confirmation; storno is the destructive one', async ({ page }) => {
  const requests = await arrange(page, 1440, 'light');
  await open(page, 'persons/1');
  await page.getByRole('button', { name: '+ Rechnung', exact: true }).click();
  const dialog = page.locator('#confirm');
  await expect(page.locator('#confirm-title')).toHaveText('Rechnung erstellen?');
  await expect(dialog).not.toHaveClass(/modal-danger/);
  await expect(page.locator('#confirm-ok')).toHaveClass(/btn-primary/);
  await expect(page.locator('#confirm-ok')).not.toHaveClass(/btn-danger/);
  await expect(dialog.locator('.confirm-ic')).toBeHidden();
  await page.keyboard.press('Escape');

  await open(page, 'invoices/1');
  const head = page.locator('.detail-head');
  const storno = head.getByRole('button', { name: 'Storno', exact: true });
  await expect(storno).toHaveClass(/btn-ghost-danger/);
  // Last action of the one action block, after the document group — not next to
  // "Bezahlen", and never wrapped alone under the back button.
  expect(await head.locator('.invoice-actions').evaluate(node => node.lastElementChild.textContent.trim())).toBe('Storno');
  const [title, stornoBox] = await Promise.all([head.locator('h2').boundingBox(), storno.boundingBox()]);
  expect(stornoBox.x, 'storno sits on the right, not under the back button').toBeGreaterThan(title.x + title.width / 2);
  await storno.click();
  await expect(dialog).toHaveClass(/modal-danger/);
  await expect(dialog.locator('.confirm-ic')).toBeVisible();
  await expect(page.locator('#confirm-title')).toHaveText('Rechnung 2026-0012 stornieren?');
  await expect(page.locator('#confirm-body')).toContainText(/Gesamtbetrag 680,00\s*€/);
  await page.keyboard.press('Escape');
  await noWrites(requests);
});

test('money labels keep the paid flag apart from received payments', async ({ page }) => {
  await arrange(page, 1440, 'dark');
  await open(page, 'dashboard');
  const flagged = page.locator('.stat').filter({ has: page.locator('.label', { hasText: 'Als bezahlt markiert' }) });
  await expect(flagged).toHaveCount(1);
  await expect(flagged.locator('.stat-note')).toHaveText('Markierung auf den Karten · kein Zahlungseingang');
  await expect(page.locator('.stat .label').getByText('Bezahlt', { exact: true })).toHaveCount(0);

  await open(page, 'persons/1');
  await expect(page.locator('.bal-hero-sub')).toHaveText('Stand heute · Aufgelaufen − Eingegangen');
  await expect(page.locator('.bal-figs .fig-l').getByText('Eingegangen', { exact: true })).toHaveCount(1);
  await expect(page.locator('.bal-figs .fig-l').getByText('Bezahlt', { exact: true })).toHaveCount(0);
  // The in-card eyebrow ("Pauschalen") no longer inherits the 18px card heading.
  const eyebrow = page.locator('.card .sec-eyebrow', { hasText: 'Pauschalen' });
  expect(await eyebrow.evaluate(node => getComputedStyle(node).fontSize)).toBe('11px');
});

test('planner controls expose names and state, and the guide is legible on light', async ({ page }) => {
  await arrange(page, 1440, 'light');
  await open(page, 'hall/1');
  const plan = page.getByRole('region', { name: 'Hallenplan' });
  await expect(plan).toHaveAttribute('tabindex', '0');
  const raster = page.locator('.gp-seg').filter({ has: page.getByRole('button', { name: 'Raster frei' }) });
  await expect(raster.locator('button[aria-pressed="true"]')).toHaveCount(1);
  for (const name of ['Darstellung Symbol', 'Darstellung Foto', 'Darstellung Rechteck']) {
    await expect(page.getByRole('button', { name, exact: true })).toHaveAttribute('aria-pressed', /true|false/);
  }
  await expect(page.locator('.planner-guide-hint')).toHaveText('Gültige Platzierungen speichern sofort · Grundriss mit „Speichern“');
  expect(await seriousAxe(page, '.gp')).toEqual([]);
});

test('small consistency fixes: pressed bulk toggle, empty status bar, amber reservations, portal PDF cue', async ({ page }) => {
  await arrange(page, 1440, 'light');
  await open(page, 'vehicles');
  const bulk = page.getByRole('button', { name: 'Mehrfachauswahl', exact: true });
  await bulk.click();
  await expect(bulk).toHaveAttribute('aria-pressed', 'true');
  await expect(bulk).toHaveClass(/btn-primary/);

  await open(page, 'dashboard');
  const cancelled = page.locator('.status-row').filter({ hasText: 'storniert' }).locator('.bar-fill');
  await expect(cancelled).toHaveClass(/is-zero/);
  // The width is applied in a requestAnimationFrame after render.
  await expect.poll(() => cancelled.evaluate(node => node.getBoundingClientRect().width)).toBe(0);

  await open(page, 'calendar');
  const reserve = page.locator('.cal-legend .cal-reserve');
  expect(await reserve.evaluate(node => getComputedStyle(node).backgroundColor)).toBe('rgb(184, 119, 0)');

  await open(page, 'portal/demo');
  await expect(page.locator('.portal-row .portal-pdf').first()).toHaveText('PDF');
});
