const { test, expect } = require('@playwright/test');
const { mockUI, origin, persons, vehicles } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

// Current repository assets, fully intercepted requests, and synthetic records only.
test.use({ serviceWorkers: 'block' });
test.beforeEach(async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
});

for (const failure of ['server', 'network']) {
  test(`handover template ${failure} deletion failure is visible and can be retried`, async ({ page }) => {
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    let templates = [{ id: 7, name: 'Synthetische Vorlage', applies_to: 'beide', content: 'Testtext' }];
    let attempts = 0;
    await mockUI(page, { overrides: {
      '/handover-templates': route => route.fulfill({ json: templates }),
      '/handover-templates/7': async route => {
        expect(route.request().method()).toBe('DELETE');
        attempts++;
        if (attempts === 1) {
          if (failure === 'network') return route.abort('failed');
          return route.fulfill({ status: 503, json: { error: 'Vorlage vorübergehend nicht löschbar. Bitte erneut versuchen.' } });
        }
        templates = [];
        return route.fulfill({ json: { status: 'deleted' } });
      },
    } });
    await page.goto(origin + '/#/handover-templates');
    const row = page.locator('.template-row', { hasText: 'Synthetische Vorlage' });
    const remove = async () => {
      await row.getByRole('button', { name: 'Löschen', exact: true }).click();
      await page.getByRole('dialog', { name: 'Übergabevorlage löschen?', exact: true })
        .getByRole('button', { name: 'Löschen', exact: true }).click();
    };
    await remove();
    await expect(page.locator('.toast.error')).toBeVisible();
    await expect(row).toBeVisible();
    await expect(page.locator('.toast', { hasText: 'Übergabevorlage gelöscht' })).toHaveCount(0);
    expect(attempts).toBe(1);
    expect(errors).toEqual([]);
    await remove();
    await expect(row).toHaveCount(0);
    await expect(page.locator('.toast', { hasText: 'Übergabevorlage gelöscht' })).toBeVisible();
    expect(attempts).toBe(2);
    expect(errors).toEqual([]);
  });
}

for (const kind of ['person', 'vehicle']) {
  test(`${kind} duplicate warning asks the exact duplicate check and excludes the edited record`, async ({ page }) => {
    // The fuzzy palette search reported "Wolfgang Atzmüller" while editing "Günter
    // Kitzmüller"; the dialog now asks /duplicates, which compares exactly on the
    // server and leaves out the record being edited (exclude).
    let duplicates = [];
    const asked = [];
    await mockUI(page, { overrides: {
      '/persons': [persons[0]], '/vehicles': [vehicles[0]],
      [`/${kind}s/duplicates`]: route => {
        asked.push(Object.fromEntries(new URL(route.request().url()).searchParams));
        return route.fulfill({ json: duplicates });
      },
      '/search': route => route.fulfill({ json: [{ kind, label: 'Unscharfer Treffer', url: `#/${kind}/9` }] }),
    } });
    await page.goto(origin + '/#/' + (kind === 'person' ? 'persons' : 'vehicles'));
    await page.getByRole('button', {
      name: kind === 'person' ? 'Demo Berger 1 bearbeiten' : 'Demo Wohnmobil 1 bearbeiten', exact: true,
    }).click();
    const modal = page.locator('#modal');
    const probe = modal.getByLabel(kind === 'person' ? 'E-Mail' : 'Kennzeichen', { exact: true });
    const check = async () => {
      const before = asked.length;
      await probe.focus();
      await probe.press('Tab');
      await expect.poll(() => asked.length).toBeGreaterThan(before);
    };
    await check();
    expect(asked.at(-1).exclude).toBe('1');
    expect(asked.at(-1)[kind === 'person' ? 'email' : 'license_plate']).toBe(await probe.inputValue());
    await expect(modal.locator('.duplicate-warning')).toBeHidden();
    duplicates = [{ kind, label: 'Anderer Eintrag', sub: 'gleiche E-Mail', url: `#/${kind}/2` }];
    await check();
    await expect(modal.locator('.duplicate-links a')).toHaveCount(1);
    await expect(modal.locator('.duplicate-links a')).toHaveAttribute('href', `#/${kind}/2`);
    await expect(modal.locator('.duplicate-warning')).not.toContainText('Unscharfer Treffer');
    await expect(modal.getByRole('button', { name: 'Speichern', exact: true })).toBeEnabled();
  });
}

for (const failure of [401, 403, 503, 'network']) {
  test(`unconfirmed logout ${failure} keeps the privacy lock until a successful retry`, async ({ page }) => {
    let attempts = 0;
    await mockUI(page, { overrides: { '/auth/logout': route => {
      attempts++;
      if (attempts === 1) {
        if (failure === 'network') return route.abort('failed');
        return route.fulfill({ status: failure, json: { error: failure === 403 ? 'invalid CSRF token' : 'not authenticated' } });
      }
      return route.fulfill({ json: { status: 'ok' } });
    } } });
    await page.goto(origin + '/#/persons');
    await page.getByRole('button', { name: 'Menü', exact: true }).click();
    await page.getByRole('button', { name: 'Abmelden', exact: true }).click();
    await expect(page.locator('#logout-pending')).toBeVisible();
    await expect(page.locator('#app-view')).toBeHidden();
    await expect(page.locator('#login-form')).toBeHidden();
    expect(await page.evaluate(() => sessionStorage.getItem('parkrr.logoutPending'))).toBe('1');
    expect(attempts).toBe(1);
    await page.getByRole('button', { name: 'Abmeldung erneut versuchen', exact: true }).click();
    await expect(page.locator('#logout-pending')).toHaveCount(0);
    await expect(page.locator('#login-form')).toBeVisible();
    expect(await page.evaluate(() => sessionStorage.getItem('parkrr.logoutPending'))).toBeNull();
    expect(attempts).toBe(2);
  });
}

for (const duplicate of [false, true]) {
  test(`UTF-8 BOM import preserves raw JSON${duplicate ? ' including duplicate keys' : ' and embedded BOM characters'}`, async ({ page }) => {
    const raw = '{\r\n "version":1,"year":2025,' + (duplicate ? '"year":2026,' : '') +
      '"property_id":1,"source":"Prüfung \uFEFF €","reviewer":"Testperson",\r\n' +
      '"entries":[{"external_id":"test-1","kind":"income_adjustment","amount_eur":"1.00","description":"Synthetischer Wert"}]\r\n}\n';
    let received;
    const requests = await mockUI(page, { role: 'admin', overrides: {
      ...overrides,
      '/reports/tax-year': { ...overrides['/reports/tax-year'], year: 2025,
        write_enabled: false, locked: true, properties: [{ id: 1, name: 'Synthetisches Steuerobjekt' }] },
      '/reports/tax-import/preview': route => {
        received = route.request().postData();
        if (duplicate) return route.fulfill({ status: 400, json: { error: 'Doppeltes JSON-Feld: year.' } });
        return route.fulfill({ json: { version: 1, year: 2025, property_id: 1,
          preview: true, persisted: false, verified: false, source: 'Prüfung \uFEFF €', reviewer: 'Testperson',
          entries: [{ external_id: 'test-1', kind: 'income_adjustment', amount_cents: 100, description: 'Synthetischer Wert' }],
          totals_cents: { income_adjustment: 100, expense_adjustment: 0, external_depreciation: 0 },
        } });
      },
    } });
    await page.goto(origin + '/#/taxyear');
    const section = page.locator('.tax-import');
    await section.locator('summary').first().click();
    await section.getByLabel('JSON-Datei mit Jahreswerten').setInputFiles({ name: 'synthetic-bom.json',
      mimeType: 'application/json', buffer: Buffer.concat([Buffer.from([0xEF, 0xBB, 0xBF]), Buffer.from(raw, 'utf8')]) });
    await section.getByRole('button', { name: 'Datei prüfen', exact: true }).click();
    if (duplicate) {
      await expect(section.getByRole('alert')).toContainText('Doppeltes JSON-Feld: year.');
      await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeDisabled();
    } else {
      await expect(section.getByRole('status')).toContainText('nicht gespeichert');
      await expect(section.getByRole('button', { name: 'Vorschau herunterladen' })).toBeEnabled();
    }
    expect(received).toBe(raw);
    expect(requests.filter(request => request.method !== 'GET').map(({ path, method }) => ({ path, method })))
      .toEqual([{ path: '/reports/tax-import/preview', method: 'POST' }]);
  });
}
