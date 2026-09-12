const { test, expect } = require('@playwright/test');

// This test creates financial records. Opt in only for a disposable test backend.
test.skip(process.env.PARKRR_E2E_ISOLATED !== '1', 'Requires an explicitly disposable backend');
test.setTimeout(60000);

test('person creation/edit, detail navigation and additional-cost payment survive real backend reloads', async ({ page, context }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/');
  await page.locator('#login-username').fill(process.env.PARKRR_E2E_USER || 'admin');
  await page.locator('#login-password').fill(process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password');
  await page.locator('#login-form button[type=submit]').click();
  await expect(page.locator('#app-view')).toBeVisible();
  const csrf = (await context.cookies()).find(cookie => cookie.name === 'parkrr_csrf').value;
  const headers = { 'X-CSRF-Token': csrf };
  let personId, chargeId;
  const tag = Date.now();
  const lastName = 'Funktionsprüfung-' + tag;
  try {
    await page.goto('/#/persons');
    await page.getByRole('button', { name: '+ Neu', exact: true }).click();
    const modal = page.locator('#modal');
    await modal.getByLabel('Vorname', { exact: true }).fill('UI');
    await modal.getByLabel('Nachname', { exact: true }).fill(lastName);
    await modal.getByLabel('E-Mail', { exact: true }).fill(`workflow-${tag}@example.invalid`);
    const personResponse = page.waitForResponse(res => new URL(res.url()).pathname === '/api/persons' && res.request().method() === 'POST');
    await modal.getByRole('button', { name: 'Speichern', exact: true }).click();
    const created = await personResponse;
    expect(created.status()).toBe(201);
    personId = (await created.json()).id;
    await expect(modal).toBeHidden();
    await page.getByRole('link', { name: 'UI ' + lastName, exact: true }).click();
    await expect(page).toHaveURL(new RegExp('#/persons/' + personId + '$'));
    await page.getByRole('navigation', { name: 'Abschnitte dieser Seite' }).getByRole('link', { name: 'Rechnungen', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Rechnungen', exact: true })).toBeFocused();
    await page.goto('/#/persons');
    await page.getByRole('button', { name: 'UI ' + lastName + ' bearbeiten', exact: true }).click();
    await modal.getByLabel('Telefon', { exact: true }).fill('+43 123 456789');
    const editResponse = page.waitForResponse(res => new URL(res.url()).pathname === '/api/persons/' + personId && res.request().method() === 'PUT');
    await modal.getByRole('button', { name: 'Speichern', exact: true }).click();
    expect((await editResponse).ok()).toBeTruthy();
    await expect(modal).toBeHidden();
    await page.reload();
    const people = await (await page.request.get('/api/persons')).json();
    expect(people.find(person => person.id === personId).phone).toBe('+43 123 456789');

    await page.goto('/#/finance');
    await page.getByRole('button', { name: '+ Neu', exact: true }).click();
    await modal.getByRole('combobox', { name: 'Person', exact: true }).selectOption(String(personId));
    await modal.getByRole('textbox', { name: 'Bezeichnung', exact: true }).fill(lastName);
    await modal.getByRole('spinbutton', { name: 'Betrag (€)', exact: true }).fill('12.50');
    await modal.getByLabel('Menge', { exact: true }).fill('2');
    const chargeResponse = page.waitForResponse(res => new URL(res.url()).pathname === '/api/charges' && res.request().method() === 'POST');
    await modal.getByRole('button', { name: 'Speichern', exact: true }).click();
    const charge = await chargeResponse;
    expect(charge.status()).toBe(201);
    chargeId = (await charge.json()).id;
    await expect(modal).toBeHidden();
    const row = page.locator('.charge-card', { hasText: lastName });
    await expect(row).toContainText('25,00');
    for (const paid of [true, false]) {
      const changed = page.waitForResponse(res => new URL(res.url()).pathname === `/api/charges/${chargeId}/paid` && res.request().method() === 'POST');
      await row.getByRole('radio', { name: paid ? 'Bezahlt' : 'Zahlung offen', exact: true }).click();
      const response = await changed;
      expect(response.ok()).toBeTruthy();
      expect(response.request().postDataJSON()).toEqual({ paid });
      await expect(row).toHaveClass(paid ? /is-paid/ : /is-open/);
      await page.reload();
      await page.getByRole('combobox', { name: 'Person filtern' }).selectOption(String(personId));
      await page.getByRole('combobox', { name: 'Zahlstatus filtern' }).selectOption(paid ? 'paid' : 'open');
      await expect(row).toBeVisible();
      const rows = await (await page.request.get('/api/charges')).json();
      expect(rows.find(entry => entry.id === chargeId)).toMatchObject({ paid, amount: 12.5, quantity: 2, total: 25 });
      // Show both states before the next toggle so persistence of the filter is intentional.
      await page.getByRole('combobox', { name: 'Zahlstatus filtern' }).selectOption('');
    }
    expect(errors).toEqual([]);
  } finally {
    // Cleanup must not replace the original assertion or timeout with a teardown error.
    if (chargeId) await page.request.delete('/api/charges/' + chargeId, { headers }).catch(() => {});
    if (personId) await page.request.delete('/api/persons/' + personId, { headers }).catch(() => {});
  }
});
