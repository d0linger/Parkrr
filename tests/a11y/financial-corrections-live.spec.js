const { test, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;

const isolated = process.env.PARKRR_E2E_ISOLATED === '1' &&
  ['http://localhost:18099', 'http://localhost:18101'].includes(process.env.PARKRR_BASE_URL);
if (!isolated && process.env.PARKRR_E2E_REQUIRE_LIVE === '1') {
  throw new Error('Financial correction tests require an explicitly disposable E2E server.');
}
test.skip(!isolated, 'Financial corrections require an explicitly disposable E2E server.');
test.setTimeout(90000);

async function login(page, context) {
  await page.goto('/');
  await page.locator('#login-username').fill(process.env.PARKRR_E2E_USER || 'admin');
  await page.locator('#login-password').fill(process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password');
  await page.locator('#login-form button[type="submit"]').click();
  await expect(page.locator('#app-view')).toBeVisible();
  const csrf = (await context.cookies()).find(cookie => cookie.name === 'parkrr_csrf');
  expect(csrf).toBeDefined();
  const headers = { 'X-CSRF-Token': csrf.value };
  const get = async path => {
    const response = await page.request.get('/api' + path);
    expect(response.ok(), path + ': ' + await response.text()).toBe(true);
    return response.json();
  };
  const post = async (path, data) => {
    const response = await page.request.post('/api' + path, { headers, data });
    expect(response.ok(), path + ': ' + await response.text()).toBe(true);
    return response.json();
  };
  return { headers, get, post };
}

async function reversePayment(page, paymentId, row) {
  await row.getByRole('button', { name: 'Zahlung stornieren', exact: true }).click();
  const reversed = page.waitForResponse(response =>
    new URL(response.url()).pathname === '/api/payments/' + paymentId && response.request().method() === 'DELETE');
  await page.locator('#confirm-ok').click();
  // Let the real undo window expire without hovering or focusing its pause controls.
  await page.mouse.move(0, 0);
  expect((await reversed).ok()).toBe(true);
}

for (const [width, theme] of [[390, 'dark'], [1440, 'light']]) {
  test(`reversing a retained deposit restores the full refundable balance at ${width}px ${theme}`, async ({ page, context }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const { headers, get, post } = await login(page, context);
    const tag = 'Kautionskorrektur-' + width + '-' + Date.now();
    const person = await post('/persons', { first_name: 'GUI', last_name: tag });
    try {
      await page.goto('/#/persons/' + person.id);
      const deposit = page.locator('.person-deposit');
      const book = async (button, amount, note) => {
        await deposit.getByRole('button', { name: button, exact: true }).click();
        const modal = page.locator('#modal');
        await modal.getByRole('spinbutton', { name: 'Betrag (€)', exact: true }).fill(amount);
        if (note) await modal.getByRole('textbox', { name: 'Grund', exact: true }).fill(note);
        const booked = page.waitForResponse(response =>
          new URL(response.url()).pathname === '/api/persons/' + person.id + '/deposits' && response.request().method() === 'POST');
        await modal.getByRole('button', { name: note ? 'Einbehalten' : 'Buchen', exact: true }).click();
        const response = await booked;
        expect(response.status(), await response.text()).toBe(201);
        await expect(modal).toBeHidden();
        return response.json();
      };
      await book('+ Erhalten', '200');
      await expect(deposit.locator('.amt')).toHaveText(/offen 200,00\s*€/);
      const retained = await book('Einbehalten', '50', tag);
      expect(retained.payment_id).toBeGreaterThan(0);
      await expect(deposit.locator('.amt')).toHaveText(/offen 150,00\s*€/);
      const payment = page.locator('.pay-card').filter({ hasText: tag });
      // Retaining a deposit refreshes the payment section without a manual reload.
      await expect(payment).toBeVisible();
      expect((await get('/persons/' + person.id + '/stats')).payments_total).toBe(50);

      await reversePayment(page, retained.payment_id, payment);
      await expect(deposit.locator('.amt')).toHaveText(/offen 200,00\s*€/);
      const history = deposit.locator('.balance').filter({ hasText: tag });
      await expect(history).toContainText('Einbehalten · storniert');
      await expect(history).toContainText(/50,00\s*€/);
      await expect(history.getByRole('button')).toHaveCount(0);
      expect(await history.evaluate(node => getComputedStyle(node).opacity)).toBe('1');
      const restored = await get('/persons/' + person.id + '/deposits');
      expect(restored.balance).toBe(200);
      expect(restored.entries.find(entry => entry.id === retained.id)).toMatchObject({
        amount: 50, payment_id: retained.payment_id, reversed: true,
      });
      expect((await get('/persons/' + person.id + '/stats')).payments_total).toBe(0);
      const axe = await new AxeBuilder({ page }).include('.person-deposit').withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
      expect(axe.violations).toEqual([]);
      await deposit.screenshot({ path: testInfo.outputPath('reversed-deposit.png') });

      await book('Zurückzahlen', '200');
      await expect(deposit.locator('.amt')).toHaveText('ausgeglichen');
      await page.reload();
      await expect(deposit.locator('.amt')).toHaveText('ausgeglichen');
      await expect(deposit.getByRole('button', { name: 'Zurückzahlen', exact: true })).toHaveCount(0);
      const refunded = await get('/persons/' + person.id + '/deposits');
      expect(refunded.balance).toBe(0);
      expect(refunded.entries.find(entry => entry.kind === 'zurueck')).toMatchObject({ amount: 200 });
      expect(refunded.entries).toHaveLength(3);
      expect((await get('/persons/' + person.id + '/stats')).payments_total).toBe(0);
      expect(errors).toEqual([]);
      await testInfo.attach('deposit-correction-values', {
        body: JSON.stringify({ restored, refunded }, null, 2), contentType: 'application/json',
      });
    } finally {
      await page.request.delete('/api/persons/' + person.id, { headers }).catch(() => {});
    }
  });
}

test('reversed bank assignment reopens the receipt and can be corrected to another person', async ({ page, context }, testInfo) => {
  const { headers, get, post } = await login(page, context);
  const tag = 'Bankkorrektur-' + Date.now();
  const first = await post('/persons', { first_name: 'GUI Alt', last_name: tag });
  const corrected = await post('/persons', { first_name: 'GUI Richtig', last_name: tag });
  try {
    const xml = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.08"><BkToCstmrStmt><Stmt>
<Acct><Id><IBAN>AT611904300234573201</IBAN></Id></Acct>
<Ntry><Amt Ccy="EUR">17.23</Amt><CdtDbtInd>CRDT</CdtDbtInd><Sts><Cd>BOOK</Cd></Sts>
<BookgDt><Dt>2026-10-10</Dt></BookgDt><AcctSvcrRef>${tag}</AcctSvcrRef>
<NtryDtls><TxDtls><RmtInf><Ustrd>${tag}</Ustrd></RmtInf></TxDtls></NtryDtls></Ntry>
</Stmt></BkToCstmrStmt></Document>`;
    await page.goto('/#/bank');
    const imported = page.waitForResponse(response =>
      new URL(response.url()).pathname === '/api/bank-import' && response.request().method() === 'POST');
    await page.locator('#page input[type="file"]').setInputFiles({
      name: 'correction.xml', mimeType: 'application/xml', buffer: Buffer.from(xml),
    });
    expect(await (await imported).json()).toMatchObject({ imported: 1, duplicates: 0 });
    const row = page.locator('.bank-row').filter({ hasText: tag });
    await expect(row).toBeVisible();
    const receipt = (await get('/bank-transactions?status=offen')).items.find(item => item.remittance === tag);
    expect(receipt).toBeDefined();
    const assign = async personId => {
      await row.getByRole('button', { name: 'Zuordnen …', exact: true }).click();
      const modal = page.locator('#modal');
      await modal.getByRole('combobox', { name: 'oder als Guthaben bei', exact: true }).selectOption(String(personId));
      const assigned = page.waitForResponse(response =>
        new URL(response.url()).pathname === '/api/bank-transactions/' + receipt.id + '/assign' && response.request().method() === 'POST');
      await modal.getByRole('button', { name: 'Zuordnen', exact: true }).click();
      const response = await assigned;
      expect(response.ok(), await response.text()).toBe(true);
      await expect(modal).toBeHidden();
      await expect(row).toHaveCount(0);
      return response.json();
    };
    const original = await assign(first.id);
    expect((await get('/persons/' + first.id + '/stats')).payments_total).toBe(17.23);
    await page.goto('/#/persons/' + first.id);
    await reversePayment(page, original.payment_id, page.locator('.pay-card').filter({ hasText: tag }));
    expect((await get('/persons/' + first.id + '/stats')).payments_total).toBe(0);
    await page.goto('/#/bank');
    await expect(row).toBeVisible();
    const reopened = (await get('/bank-transactions?status=offen')).items.find(item => item.id === receipt.id);
    expect(reopened).toMatchObject({ status: 'offen', payment_id: original.payment_id, person_id: null, amount: 17.23 });
    const replacement = await assign(corrected.id);
    expect(replacement.payment_id).not.toBe(original.payment_id);
    expect((await get('/persons/' + corrected.id + '/stats')).payments_total).toBe(17.23);
    const oldPayments = await get('/persons/' + first.id + '/payments');
    const newPayments = await get('/persons/' + corrected.id + '/payments');
    expect(oldPayments).toHaveLength(1);
    expect(oldPayments[0]).toMatchObject({ id: original.payment_id, amount: 17.23, reversed: true });
    expect(newPayments).toHaveLength(1);
    expect(newPayments[0]).toMatchObject({ id: replacement.payment_id, amount: 17.23, reversed: false });
    await page.getByRole('group', { name: 'Status', exact: true }).getByRole('button', { name: 'Zugeordnet', exact: true }).click();
    await expect(row.getByRole('link', { name: 'Zur Person', exact: true })).toHaveAttribute('href', '#/person/' + corrected.id);
    await testInfo.attach('bank-correction-values', {
      body: JSON.stringify({ receipt, reopened, original, replacement, oldPayments, newPayments }, null, 2),
      contentType: 'application/json',
    });
  } finally {
    for (const person of [first, corrected]) {
      await page.request.delete('/api/persons/' + person.id, { headers }).catch(() => {});
    }
  }
});
