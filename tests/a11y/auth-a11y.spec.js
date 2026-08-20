// @ts-check
// Accessibility checks on AUTHENTICATED views (AR2). Extends the login-only axe
// smoke test to the main app routes so regressions in the app shell are caught.
const { test, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;

const USER = process.env.PARKRR_E2E_USER || 'admin';
const PASS = process.env.PARKRR_E2E_PASS || 'ci-a11y-admin-password';

async function login(page) {
  await page.goto('/');
  await page.waitForSelector('#login-view:not([hidden])', { timeout: 15000 });
  await page.fill('#login-username', USER);
  await page.fill('#login-password', PASS);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('#app-view:not([hidden])', { timeout: 15000 });
}

const views = [
  ['Übersicht', 'dashboard'],
  ['Personen', 'persons'],
  ['Gefährte', 'vehicles'],
  ['Zusatzkosten', 'finance'],
  ['Tarife', 'tariffs'],
];

for (const [label, route] of views) {
  test(`a11y (auth): ${label} has no serious/critical violations`, async ({ page }) => {
    await login(page);
    await page.goto('/#/' + route);
    await page.waitForSelector('#page:not(:empty)', { timeout: 15000 });
    await page.waitForTimeout(400); // settle async list renders

    // Enforces every serious/critical WCAG 2 A/AA rule with no exclusions — the earlier
    // baseline (color-contrast, select-name, aria-prohibited-attr) has been remediated:
    // light-theme AA text inks, aria-label on the filter/sort <select>s, and role="img"
    // on the icon-only "Nicht platziert" span.
    const results = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa'])
      .analyze();
    const serious = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
    if (serious.length) {
      console.log(`\n[${label}] ` + serious.map((v) => `${v.id} (${v.nodes.length})`).join(', '));
      console.log(JSON.stringify(serious.map((v) => ({ id: v.id, help: v.help, nodes: v.nodes.map((n) => n.target) })), null, 2));
    }
    expect(serious, `${label}: serious/critical a11y violations`).toEqual([]);
  });
}

// Die Befehlspalette war bisher in keinem axe-Lauf: sie ist erst nach einem Klick da,
// und die Prüfungen oben sehen nur die Seite darunter. Dieser Test schließt die Lücke.
//
// Was er NICHT leistet, damit sich niemand darauf verlässt: den ungültigen Baum, den
// die Palette vorher hatte — role="listbox" mit einem Hinweistext als Kind statt
// lauter Optionen — meldet axe unter wcag2a/wcag2aa nicht. Nachgemessen: mit dem
// Hinweis zurück im listbox bleibt dieser Test grün. Die Struktur wurde trotzdem
// korrigiert, weil sie der ARIA-Spezifikation widerspricht; belegt ist sie hier aber
// nicht. Was der Test wirklich absichert, sind die Regeln, die axe abdeckt (Kontrast,
// Namen, Rollen) plus die combobox-Verdrahtung unten — die vorher komplett fehlte.
test('a11y (auth): Befehlspalette hat keine serious/critical violations', async ({ page }) => {
  await login(page);
  await page.locator('#search-btn').click();
  await expect(page.locator('#cmdk')).toBeVisible();

  // Einmal ohne Treffer (Hinweiszustand) und einmal mit offener Liste prüfen — die
  // beiden Zustände haben verschiedene Bäume.
  for (const [zustand, eingabe] of [['leer', ''], ['mit Liste', 'Person']]) {
    if (eingabe) {
      await page.locator('.cmdk-input').fill(eingabe);
      await expect(page.locator('.cmdk-row').first()).toBeVisible({ timeout: 10000 });
    }
    const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
    const serious = results.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
    if (serious.length) {
      console.log(`\n[Palette ${zustand}] ` + JSON.stringify(serious.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target) })), null, 2));
    }
    expect(serious, `Palette (${zustand}): serious/critical a11y violations`).toEqual([]);
  }

  // Die Auswahl muss ansagbar sein, nicht nur sichtbar: ohne aria-activedescendant
  // verschieben die Pfeiltasten für Hilfstechnik gar nichts.
  await page.keyboard.press('ArrowDown');
  await expect(page.locator('.cmdk-input')).toHaveAttribute('aria-activedescendant', /^cmdk-opt-/);
  await expect(page.locator('.cmdk-input')).toHaveAttribute('aria-expanded', 'true');
});
