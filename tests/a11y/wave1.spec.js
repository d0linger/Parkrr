const { test, expect } = require('@playwright/test');
const { mockUI, origin } = require('./helpers/ui-fixture');

test.use({ serviceWorkers: 'block' });

test('dashboard work list and KPI deep links preserve prepared filters', async ({ page }) => {
  await mockUI(page, { overrides: {
    '/invoices/overdue': [{ id: 91, person_id: 1, person_name: 'Demo Berger 1', number: '2026-0091', days_overdue: 12, open_amount: 240 }],
    '/vehicles/ending-soon': [{ id: 2, label: 'Demo Wohnmobil 2', person_name: 'Demo Gruber 2', days_left: 5 }],
  } });
  await page.goto(origin + '/#/dashboard');
  await expect(page.getByRole('heading', { name: 'Heute' })).toBeVisible();
  await expect(page.locator('.today-row')).toHaveCount(3);
  await page.getByRole('button', { name: 'Offene Salden anzeigen' }).click();
  await expect(page).toHaveURL(origin + '/#/persons?owed=1');
  await expect(page.getByRole('button', { name: 'Nur offen' })).toHaveAttribute('aria-pressed', 'true');
});

test('person form warns about likely duplicates without blocking save', async ({ page }) => {
  const asked = [];
  await mockUI(page, { overrides: {
    '/persons/duplicates': route => {
      asked.push(Object.fromEntries(new URL(route.request().url()).searchParams));
      return route.fulfill({ json: [{ kind: 'person', label: 'Demo Berger', sub: 'gleicher Name · demo0@example.invalid', url: '#/person/1' }] });
    },
  } });
  await page.goto(origin + '/#/persons');
  await page.getByRole('button', { name: '+ Person', exact: true }).click();
  await page.locator('#f_first_name').fill('Demo');
  await page.locator('#f_last_name').fill('Berger');
  await page.locator('#f_last_name').blur();
  await expect(page.getByText('Möglicher Doppeleintrag', { exact: true })).toBeVisible();
  expect(asked.at(-1)).toEqual({ first_name: 'Demo', last_name: 'Berger' }); // neue Person: nichts auszuschließen
  await expect(page.getByRole('button', { name: 'Speichern' })).toBeEnabled();
  await expect(page.locator('.duplicate-warning a')).toHaveAttribute('href', '#/person/1');
});

test('handover template can be inserted and remains editable', async ({ page }) => {
  await mockUI(page, { overrides: {
    '/handover-templates': [{ id: 4, name: 'Standard Einlagerung', applies_to: 'einlagerung', content: '☐ Schlüssel übernommen\n☐ Zustand dokumentiert' }],
    '/vehicles/1/photos': [], '/vehicles/1/history': [], '/vehicles/1/handovers': [],
    '/persons/1/recurring': [],
  } });
  await page.goto(origin + '/#/vehicle/1');
  await page.getByRole('button', { name: '+ Protokoll' }).click();
  await page.locator('#f_template_id').selectOption('4');
  await expect(page.locator('#f_notes')).toHaveValue(/Schlüssel übernommen/);
  await page.locator('#f_notes').fill('Individuell angepasst');
  await expect(page.locator('#f_notes')).toHaveValue('Individuell angepasst');
});

test('admin sees actionable operational readiness in settings', async ({ page }) => {
  await mockUI(page, { role: 'admin', overrides: {
    '/admin/readiness': { state: 'blocked', checked_at: '2026-10-03T12:00:00Z', items: [
      { id: 'backup', label: 'Datensicherung', state: 'blocked', reason: 'Backup-Schlüssel oder Sicherungsziel fehlt.', href: '#/backup', critical: true },
      { id: 'billing', label: 'Rechnungsdaten', state: 'ready', reason: 'Pflichtangaben vollständig.', href: '#/billing', critical: true },
    ] },
  } });
  await page.goto(origin + '/#/settings');
  await expect(page.getByRole('heading', { name: 'Betriebsbereitschaft' })).toBeVisible();
  await expect(page.getByText('Backup-Schlüssel oder Sicherungsziel fehlt.')).toBeVisible();
  await expect(page.getByRole('link', { name: /Datensicherung/ })).toHaveAttribute('href', '#/backup');
});

test('handover templates have a dedicated shared management view', async ({ page }) => {
  await mockUI(page, { overrides: {
    '/handover-templates': [{ id: 4, name: 'Standard Einlagerung', applies_to: 'einlagerung', content: '☐ Schlüssel übernommen' }],
  } });
  await page.goto(origin + '/#/handover-templates');
  await expect(page.getByRole('heading', { name: 'Übergabevorlagen', level: 2 })).toBeVisible();
  await expect(page.getByText('Standard Einlagerung')).toBeVisible();
  await page.getByRole('button', { name: 'Bearbeiten' }).click();
  await expect(page.locator('#f_content')).toHaveValue('☐ Schlüssel übernommen');
});
