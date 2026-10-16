const { test: base, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');

// Ausbauplan Paket 2 (Bedienung): Hallen-Belegung als Weg in den Planer, App-weite
// Tastenkürzel, Aktionen in der Befehlspalette, kompakte Listen und gespeicherte
// Ansichten. Alles gemockt.
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

const occupancy = { active: 12, placed: 8, trend: [], halls: [
  { hall_id: 3, name: 'Halle A', garage_name: 'Nord', placed: 8 },
  { hall_id: 4, name: 'Halle B', garage_name: 'Nord', placed: 0 },
] };
async function open(page, route) {
  await page.goto(origin + '/#/' + route);
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
}

test('Belegung: jede Halle öffnet ihren Planer, auch eine leere', async ({ page }) => {
  await mockUI(page, { role: 'admin', overrides: { '/occupancy': occupancy } });
  await open(page, 'dashboard');
  const empty = page.getByRole('button', { name: /Halle B \(Nord\) im Planer öffnen, leer/ });
  await expect(empty).toBeVisible();
  await expect(empty).toContainText('leer');
  await page.getByRole('button', { name: /Halle A \(Nord\) im Planer öffnen, 8 platziert/ }).click();
  await expect(page).toHaveURL(/#\/hall\/3$/);
});

test('Tastenkürzel: ? zeigt die Übersicht, / springt ins Suchfeld, N legt an', async ({ page }) => {
  await mockUI(page, { role: 'editor' });
  await open(page, 'vehicles');
  await page.locator('body').press('?');
  const dlg = page.getByRole('dialog', { name: 'Tastaturkürzel' });
  await expect(dlg).toBeVisible();
  await expect(dlg.locator('kbd')).toHaveText([/K$/, '/', 'N', '?', 'Esc']);
  const axe = await new AxeBuilder({ page }).include('#modal').analyze();
  expect(axe.violations.map(v => v.id)).toEqual([]);
  await page.keyboard.press('Escape');
  await expect(dlg).toBeHidden();

  await page.locator('body').press('/');
  await expect(page.getByRole('searchbox', { name: 'In Gefährte suchen' })).toBeFocused();
  // Im Suchfeld ist "n" ein Buchstabe, kein Kürzel.
  await page.keyboard.type('n');
  await expect(page.getByRole('dialog', { name: 'Neues Gefährt' })).toHaveCount(0);
  await page.locator('body').click({ position: { x: 5, y: 5 } });
  await page.locator('body').press('n');
  await expect(page.locator('#modal')).toBeVisible();
  await expect(page.locator('#modal-title')).toHaveText(/Gefährt/);
});

test('Befehlspalette: ohne Eingabe stehen die Aktionen, "anlegen" öffnet den Dialog', async ({ page }) => {
  await mockUI(page, { role: 'editor' });
  await open(page, 'dashboard');
  await page.keyboard.press('Control+k');
  const palette = page.getByRole('dialog', { name: 'Suche', exact: true });
  await expect(palette.getByRole('option')).toHaveText([/Person anlegen/, /Gefährt einlagern/, /Zusatzkosten buchen/, /Tastenkürzel/, /Hell oder dunkel/]);
  await palette.getByRole('combobox').fill('person anl');
  await expect(palette.getByRole('option').first()).toContainText('Person anlegen');
  await page.keyboard.press('Enter');
  await expect(page.locator('#modal-title')).toHaveText('Neue Person');
});

test('Befehlspalette: Leser sehen keine Anlege-Aktionen', async ({ page }) => {
  await mockUI(page, { role: 'reader' });
  await open(page, 'dashboard');
  await page.keyboard.press('Control+k');
  const options = page.getByRole('dialog', { name: 'Suche', exact: true }).getByRole('option');
  await expect(options).toHaveText([/Tastenkürzel/, /Hell oder dunkel/]);
});

test('Kompakt: dichtere Karten, gemerkt über das Neuladen', async ({ page }) => {
  await mockUI(page, { role: 'editor' });
  await page.setViewportSize({ width: 1280, height: 900 });
  await open(page, 'vehicles');
  const first = page.locator('#page .card').first();
  const tall = (await first.boundingBox()).height;
  const toggle = page.getByRole('button', { name: 'Kompakt' });
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-pressed', 'true');
  await expect.poll(async () => (await first.boundingBox()).height).toBeLessThan(tall);
  await expect(page.locator('#page .veh-rate').first()).toBeHidden();
  await page.reload();
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
  await expect(page.getByRole('button', { name: 'Kompakt' })).toHaveAttribute('aria-pressed', 'true');
});

test('Ansichten: mitgelieferte Ansicht setzt die Filter, eigene wird gespeichert und entfernt', async ({ page }) => {
  await mockUI(page, { role: 'editor' });
  await open(page, 'vehicles');
  const views = page.getByRole('combobox', { name: 'Gespeicherte Ansichten' });
  await views.selectOption({ label: 'Reserviert' });
  await expect(page).toHaveURL(/#\/vehicles\?status=reserved$/);
  await expect(page.getByRole('combobox', { name: 'Gespeicherte Ansichten' }).locator('option').first()).toHaveText('Ansicht: Reserviert');

  // Eigene Kombination: Status + Person
  await open(page, 'vehicles?status=stored&person=2');
  await page.getByRole('combobox', { name: 'Gespeicherte Ansichten' }).selectOption({ label: 'Aktuelle Filter speichern …' });
  await page.locator('#modal').getByRole('textbox', { name: 'Name' }).fill('Gruber eingelagert');
  await page.locator('#modal-submit').click();
  await expect(page.getByRole('combobox', { name: 'Gespeicherte Ansichten' }).locator('option').first()).toHaveText('Ansicht: Gruber eingelagert');

  await open(page, 'vehicles');
  await page.getByRole('combobox', { name: 'Gespeicherte Ansichten' }).selectOption({ label: '★ Gruber eingelagert' });
  await expect(page).toHaveURL(/#\/vehicles\?person=2&status=stored$/);
  await page.getByRole('combobox', { name: 'Gespeicherte Ansichten' }).selectOption({ label: '„Gruber eingelagert“ entfernen …' });
  await page.locator('#confirm').getByRole('button', { name: 'Entfernen' }).click();
  await expect(page.getByRole('combobox', { name: 'Gespeicherte Ansichten' }).locator('option', { hasText: 'Gruber eingelagert' })).toHaveCount(0);
  expect(await page.evaluate(() => localStorage.getItem('parkrr-views-vehicles'))).toBe('[]');
});
