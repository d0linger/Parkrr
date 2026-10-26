const { test, expect } = require('@playwright/test');
const { mockUI, origin } = require('./helpers/ui-fixture');

// Die Stellplatz-Buchten hinter der Login-Karte glimmen auch unter erzwungenem
// „weniger Bewegung“ (Remote-Desktop, Windows ohne Animationen): ausdrücklicher Wunsch
// des Betreibers. Die Lade-Spirale dreht dort weiterhin, nur ruhiger.
for (const reducedMotion of ['reduce', 'no-preference']) {
  test('Login: die Buchten glimmen auf (' + reducedMotion + ')', async ({ page }) => {
    await page.emulateMedia({ reducedMotion });
    await mockUI(page, { loggedIn: false });
    await page.goto(origin + '/');
    await expect(page.locator('#login-view')).toBeVisible();
    const cells = page.locator('#login-bays i');
    await expect.poll(() => cells.count()).toBeGreaterThan(4);
    expect(await cells.first().evaluate((n) => {
      const cs = getComputedStyle(n);
      return [cs.animationName, cs.animationDuration, cs.animationIterationCount];
    })).toEqual(['lv-bay', '20s', 'infinite']);
    const glow = () => page.evaluate(() => [...document.querySelectorAll('#login-bays i')]
      .reduce((sum, n) => sum + Number(getComputedStyle(n).opacity), 0));
    const before = await glow();
    await expect.poll(async () => Math.abs((await glow()) - before), { timeout: 4000 }).toBeGreaterThan(0.01);
    const spin = await page.evaluate(() => {
      const s = document.createElement('span');
      s.className = 'spin';
      document.body.append(s);
      return getComputedStyle(s).animationIterationCount;
    });
    expect(spin).toBe('infinite');
  });
}
