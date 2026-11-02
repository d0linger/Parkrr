const { test, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

// Auswahllisten mit Suche (comboBox) statt <datalist>: das native datalist filterte beim
// Öffnen nach dem Text im Feld, mit „Anhänger“ im Feld sah man nur die Anhänger.
test.use({ serviceWorkers: 'block', reducedMotion: 'reduce' });

const categories = [
  { id: 1, name: 'Anhänger', default_monthly_cost: 30, default_yearly_cost: 360 },
  { id: 2, name: 'Auto', default_monthly_cost: 40, default_yearly_cost: 480 },
  { id: 3, name: 'Pferdeanhänger', default_monthly_cost: 40, default_yearly_cost: 480 },
  { id: 4, name: 'Wohnmobil', default_monthly_cost: 85, default_yearly_cost: 1020 },
  { id: 5, name: 'Altes Boot', default_monthly_cost: 20, default_yearly_cost: 200, archived: true },
];

async function openVehicle(page) {
  await mockUI(page, { overrides: { ...overrides, '/categories': categories } });
  await page.goto(origin + '/#/vehicles');
  await page.getByRole('button', { name: '+ Gefährt', exact: true }).click();
  await expect(page.locator('#modal')).toBeVisible();
  return page.getByRole('combobox', { name: /Tarif \/ Typ/ });
}
const options = (page) => page.getByRole('listbox', { name: 'Tarife' }).getByRole('option');

test('Tarif: Öffnen zeigt alle Tarife, der gewählte ist markiert; erst Tippen filtert', async ({ page }) => {
  const field = await openVehicle(page);
  await expect(field).toHaveValue('Anhänger');
  await field.click();
  await expect(field).toHaveAttribute('aria-expanded', 'true');
  // Die archivierten stehen im neuen Gefährt nicht zur Wahl (wie bisher).
  await expect(options(page)).toHaveText([/^Anhänger/, /^Auto/, /^Pferdeanhänger/, /^Wohnmobil/]);
  await expect(options(page).filter({ hasText: /^Anhänger/ })).toHaveAttribute('aria-selected', 'true');
  expect((await new AxeBuilder({ page }).include('#modal').analyze()).violations.map((v) => v.id)).toEqual([]);

  await field.fill('pferd');
  await expect(options(page)).toHaveText([/^Pferdeanhänger/]);
  await expect(page.locator('.combo-label mark')).toHaveText('Pferd');
  await field.fill('xyz');
  await expect(options(page)).toHaveText(['Kein Treffer']);
});

test('Tarif: Tastatur wählt, Escape schließt nur die Liste, der Preis zieht nach', async ({ page }) => {
  const field = await openVehicle(page);
  await expect(page.locator('#f_rate')).toHaveValue('30');
  await field.focus();
  await page.keyboard.press('ArrowDown');
  await expect(options(page)).toHaveCount(4);
  await page.keyboard.press('ArrowDown'); // vom gewählten Anhänger eins weiter: Auto
  await page.keyboard.press('Enter');
  await expect(field).toHaveValue('Auto');
  await expect(field).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('#f_category_id')).toHaveValue('2');
  await expect(page.locator('#f_rate')).toHaveValue('40');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Escape');
  await expect(field).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('#modal')).toBeVisible();
  // Leeren zeigt alles; ein Klick auf einen Eintrag wählt ihn.
  await page.getByRole('button', { name: 'Eingabe leeren' }).click();
  await expect(options(page)).toHaveCount(4);
  await options(page).filter({ hasText: /^Wohnmobil/ }).click();
  await expect(page.locator('#f_rate')).toHaveValue('85');
  await expect(page.locator('#err_category_lookup')).toBeHidden();
});

test('Zusatzkosten: Dienste-Liste zeigt alle Dienste trotz Text im Feld, freier Text bleibt erlaubt', async ({ page }) => {
  await mockUI(page, { overrides: { ...overrides } });
  await page.goto(origin + '/#/finance');
  await page.getByRole('button', { name: '+ Position', exact: true }).click();
  const field = page.getByRole('combobox', { name: /^Bezeichnung/ });
  await field.fill('Individuelle Reinigung');
  await page.getByRole('button', { name: 'Dienste anzeigen', exact: true }).click();
  await expect(page.getByRole('listbox', { name: 'Dienste' }).getByRole('option')).toHaveCount(2);
  await page.getByRole('listbox', { name: 'Dienste' }).getByRole('option', { name: /^Stromanschluss/ }).click();
  await expect(field).toHaveValue('Stromanschluss');
  await expect(page.locator('#f_amount')).toHaveValue('20');
  await field.fill('Sonderreinigung');
  await field.press('Tab');
  await expect(field).toHaveValue('Sonderreinigung');
});
