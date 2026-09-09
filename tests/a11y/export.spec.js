// @ts-check
// Der CSV-Export der Belege (Hundert 19): die beiden neuen Ziele muessen in der
// Oberflaeche verlinkt sein UND echte Daten liefern — ein Link, der 404 liefert,
// faellt sonst erst dem Steuerberater auf.
const { test, expect } = require('@playwright/test');

// Diese Pruefungen laufen gegen DENSELBEN Backend wie die uebrigen Worker und
// oeffnen schwere Seiten (Finanzuebersicht mit Diagrammen, Benutzerliste). Unter
// paralleler Last reicht das Standardbudget von 30 s nicht: Login und Seitenaufbau
// zusammen liegen dann darueber. Kein Retry-Pflaster, sondern ein Budget, das zur
// tatsaechlichen Arbeit passt.
test.setTimeout(60000);

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

test('Export: Rechnungen und Zusatzkosten sind verlinkt und liefern CSV', async ({ page }) => {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });

  // Die Übersicht klappt bei LEERER Datenbank auf einen Leerzustand zusammen
  // ("Noch keine Daten…") und rendert dann KEINE Export-Karte — die frische CI-DB
  // ist genau dieser Fall, weshalb diese Prüfung in CI nie bestand. Eine eigene
  // Person säen, damit die Übersicht ihren vollen Inhalt aufbaut.
  await page.evaluate(async () => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    await fetch('/api/persons', { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: JSON.stringify({ first_name: 'Export', last_name: 'SeedDash' }) });
  });

  // Die Export-Karte steht auf der ÜBERSICHT (routes.dashboard), nicht unter
  // #/finance. Dass diese Prüfung je unter #/finance grün war, lag am inzwischen
  // behobenen Render-Wettlauf: der noch laufende Dashboard-Aufbau überschrieb die
  // Finanzseite samt seiner Export-Links — der Test fand sie auf der falschen Route.
  await page.reload();
  await page.waitForSelector('#page:not(:empty)', { timeout: 30000 });
  await page.waitForSelector('a[href="/api/export/invoices"]', { timeout: 30000 });
  await expect(page.locator('a[href="/api/export/charges"]')).toHaveCount(1);

  for (const entity of ['invoices', 'charges']) {
    const res = await page.evaluate(async (e) => {
      const r = await fetch('/api/export/' + e, { credentials: 'same-origin' });
      return { status: r.status, ct: r.headers.get('content-type'), head: (await r.text()).slice(0, 120) };
    }, entity);
    expect(res.status, entity).toBe(200);
    expect(res.ct, entity).toContain('text/csv');
    // Semikolon-getrennte Kopfzeile mit BOM davor.
    expect(res.head, entity).toContain(';');
  }
  // Aufräumen: nur die eigene Saat-Person.
  await page.evaluate(async () => {
    const csrf = document.cookie.split('; ').find((c) => c.startsWith('parkrr_csrf='))?.split('=')[1];
    const list = await (await fetch('/api/persons?limit=1000', { credentials: 'same-origin' })).json();
    for (const p of (Array.isArray(list) ? list : []).filter((x) => x.last_name === 'SeedDash')) {
      await fetch('/api/persons/' + p.id, { method: 'DELETE', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf } });
    }
  });

});
