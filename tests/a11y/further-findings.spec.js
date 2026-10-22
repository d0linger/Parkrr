const fs = require('node:fs');
const path = require('node:path');
const { test: base, expect } = require('@playwright/test');
const AxeBuilder = require('@axe-core/playwright').default;
const { mockUI, origin } = require('./helpers/ui-fixture');
const { overrides } = require('./helpers/overhaul-fixture');

// Die weiteren Funde aus der Kritik vom 14.10.2026: Hilfe und Handbuch, geführte
// Wiederherstellung und Steuer-Aktivierung, Kennzahlen eingeklappt, Belegung je Halle,
// "Im Plan platzieren", Mehrfachauswahl für Zusatzkosten, einheitliches Öffnen,
// Pflichtfelder, Kalender, Jänner, Planer-Symbole und die Reiterleiste bei 200 %.
// Alles gemockt; das Handbuch kommt aus docs/, wie in der App.
const handbook = fs.readFileSync(path.resolve(__dirname, '../../docs/betreiber-handbuch.md'), 'utf8');
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
  await page.addInitScript(() => { localStorage.setItem('parkrr-veh', '0'); });
});

async function arrange(page, width, theme, options = {}) {
  await page.setViewportSize({ width, height: 900 });
  await page.emulateMedia({ colorScheme: theme });
  return mockUI(page, { role: 'admin', ...options,
    overrides: { ...overrides, '/help/handbook': { markdown: handbook }, ...options.overrides } });
}
async function open(page, route) {
  await page.goto(origin + '/#/' + route);
  await expect(page.locator('#page')).toHaveAttribute('aria-busy', 'false');
}
const writes = requests => requests.filter(r => !['GET', 'HEAD'].includes(r.method));
async function axe(page, include) {
  const result = await new AxeBuilder({ page }).include(include).withTags(['wcag2a', 'wcag2aa']).analyze();
  expect(result.violations.filter(v => ['serious', 'critical'].includes(v.impact)).map(v => v.id)).toEqual([]);
}

test('the handbook is a page: menu entry, sections, deep links from backup and tax year', async ({ page }) => {
  const requests = await arrange(page, 1440, 'light');
  await open(page, 'dashboard');
  await page.getByRole('button', { name: 'Menü', exact: true }).click();
  await page.locator('#menu').getByRole('button', { name: 'Hilfe & Handbuch' }).click();
  await expect(page).toHaveURL(origin + '/#/help');
  await expect(page.getByRole('heading', { level: 2, name: 'Hilfe & Handbuch' })).toBeVisible();
  await expect(page.getByRole('heading', { level: 3, name: '2. Backups: einrichten, prüfen, wiederherstellen' })).toBeVisible();
  // Der Titel (#) steht nicht doppelt; Tabellen und Code kommen als echte Elemente.
  await expect(page.locator('.md h2')).toHaveCount(0);
  await expect(page.locator('.md table').first()).toBeVisible();
  await expect(page.locator('.md-pre code')).toContainText('parkrr restore');
  await expect(page.locator('.section-index a', { hasText: '4. Die Schalter' })).toBeVisible();
  await page.getByRole('button', { name: 'Menü', exact: true }).click();
  await expect(page.locator('#menu .menu-item[aria-current="page"]')).toHaveText('Hilfe & Handbuch');
  await page.keyboard.press('Escape');
  await axe(page, '.route-view');

  await open(page, 'backup');
  const restore = page.locator('.restore-section');
  await expect(restore.locator('.restore-steps li')).toHaveCount(4);
  await expect(restore.locator('.copy-cmd code')).toHaveText(['docker compose stop app',
    'docker compose run --rm app restore /backups/<datei.dump.enc>', 'docker compose start app']);
  await expect(restore.getByRole('button', { name: 'Kopieren: docker compose stop app' })).toBeVisible();
  await restore.getByLabel(/Alle Parkrr-App-Instanzen stoppen/).check();
  await restore.getByRole('link', { name: /Handbuch: Wiederherstellen/ }).click();
  const target = page.locator('#h-2-backups-einrichten-prufen-wiederherstellen');
  await expect(target).toBeFocused();
  await expect.poll(() => target.evaluate(n => Math.round(n.getBoundingClientRect().top))).toBeLessThan(200);

  await open(page, 'taxyear');
  await page.getByText('Später aktivieren', { exact: true }).click();
  await expect(page.locator('.tax-mode .cmd-steps li')).toHaveCount(3);
  await expect(page.locator('.tax-mode .copy-cmd code')).toHaveText(['PARKRR_TAX_WRITES_ENABLED=true', 'docker compose up -d app']);
  await page.locator('.tax-mode').getByRole('link', { name: /Handbuch: Steuerbereich aktivieren/ }).click();
  await expect(page.locator('#h-steuerbereich-zuerst-nur-lesen-spater-aktivieren')).toBeFocused();
  expect(writes(requests)).toEqual([]);
});

test('the handbook is for admins only', async ({ page }) => {
  const requests = await arrange(page, 1440, 'light', { role: 'editor' });
  await open(page, 'dashboard');
  await page.getByRole('button', { name: 'Menü', exact: true }).click();
  await expect(page.locator('#menu').getByRole('button', { name: 'Hilfe & Handbuch' })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await open(page, 'help');
  await expect(page.getByText('Nur für Administratoren.', { exact: true })).toBeVisible();
  expect(requests.filter(r => r.path === '/help/handbook')).toEqual([]);
});

test('dashboard: the key figures start open, fold on request and remember it', async ({ page }) => {
  await arrange(page, 1440, 'light');
  await open(page, 'dashboard');
  const kpis = page.locator('.dash-kpis');
  await expect(kpis).toHaveAttribute('open', '');
  await expect(kpis.locator('summary')).toContainText('Kennzahlen 2026');
  await expect(kpis.locator('summary')).toContainText('Umsatz 12.480,00 €');
  await expect(kpis.locator('summary')).toContainText('24 Personen · 12 aktiv');
  await expect(page.locator('.dash-money')).toBeVisible();
  await kpis.locator('summary').click();
  await expect(page.locator('.dash-money')).toBeHidden();
  // Gespeichert wird im toggle-Ereignis, das der Browser NACH dem Klick als eigene
  // Aufgabe feuert. Ohne dieses Warten lud der langsamere CI-Runner neu, bevor der
  // Zustand geschrieben war.
  await expect.poll(() => page.evaluate(() => localStorage.getItem('parkrr-open-dash-kpis'))).toBe('0');
  await page.reload();
  await expect(page.locator('.dash-kpis')).not.toHaveAttribute('open', '');
  await expect(page.locator('.dash-hero')).toBeVisible();
  // Kupferner Balken mit Legende; Statusbalken wachsen per transform, nicht per width.
  await expect(page.locator('.chart-card', { hasText: 'Zusatzkosten pro Monat' }).locator('.legend'))
    .toContainText('laufender Monat · noch nicht abgeschlossen');
  expect(await page.locator('.bars .bar-fill').first().evaluate(n => getComputedStyle(n).transitionProperty)).not.toContain('width');
});

test('garages show occupancy per hall and a way into the planner; rows open as a whole', async ({ page }) => {
  await arrange(page, 1440, 'light');
  await open(page, 'garages');
  await expect(page.getByRole('link', { name: 'Hallen öffnen' })).toHaveCount(0);
  const cta = page.locator('.unplaced-cta');
  await expect(cta).toContainText('11 Gefährte sind noch nicht platziert.');
  const minis = page.locator('.hall-mini');
  await expect(minis).toHaveCount(2);
  await expect(minis.first()).toHaveAttribute('href', '#/hall/1');
  await expect(minis.first().locator('.hall-mini-veh')).toHaveCount(1);
  await expect(page.locator('.hall-minis-empty')).toHaveText('Noch keine Halle angelegt.');
  await axe(page, '.route-view');
  // Ein Klick auf die Kartenfläche (nicht auf den Namen) öffnet die Garage.
  const box = await page.locator('.storage-list > .card').first().boundingBox();
  await page.mouse.click(box.x + 8, box.y + box.height / 2);
  await expect(page).toHaveURL(origin + '/#/garage/1');
  await expect(page.getByRole('button', { name: 'Planer ›' })).toHaveCount(0);
  await expect(page.locator('.hall-list .card-chev')).toHaveCount(2);

  await open(page, 'persons');
  await expect(page.getByRole('button', { name: /öffnen$/ })).toHaveCount(0);
  await expect(page.locator('.pcard .card-chev').first()).toBeVisible();
  const person = page.locator('.pcard').first();
  const pbox = await person.boundingBox();
  await page.mouse.click(pbox.x + pbox.width * 0.55, pbox.y + pbox.height / 2);
  await expect(page).toHaveURL(origin + '/#/persons/1');
});

test('an unplaced vehicle offers “Im Plan platzieren” and arrives armed in the planner', async ({ page }) => {
  const requests = await arrange(page, 1440, 'light', { overrides: {
    '/garages': [{ id: 1, name: 'Standort Nord', hall_count: 1 }],
    '/garages/1/halls': [{ id: 1, garage_id: 1, name: 'Halle Nord', spot_count: 1, geometry: { Wm: 14, Hm: 9, floor: [[0, 0], [14, 0], [14, 9], [0, 9]] } }],
  } });
  await open(page, 'vehicles');
  const card = page.locator('#page .card', { hasText: 'Demo Wohnmobil 2' }).first();
  await expect(card.getByRole('radiogroup', { name: 'Lagerstatus · Demo Wohnmobil 2' })).toBeVisible();
  await expect(card.locator('.veh-loc-ic.empty')).toHaveCount(0);
  await card.getByRole('button', { name: 'Im Plan platzieren' }).click();
  await expect(page).toHaveURL(origin + '/#/hall/1');
  await expect(page.locator('.gp-armbar')).toBeVisible();
  await expect(page.locator('.gp-vehc.is-armed')).toContainText('Demo Wohnmobil 2');
  expect(writes(requests)).toEqual([]);
});

test('with several halls, “Im Plan platzieren” opens the last hall without asking', async ({ page }) => {
  await arrange(page, 1440, 'light', { overrides: {
    '/halls/2/plan': { hall: { ...overrides['/halls/1/plan'].hall, id: 2, name: 'Halle Süd' }, garage_name: 'Standort Nord', spots: [] },
  } });
  await page.addInitScript(() => { localStorage.setItem('parkrr-last-hall', '2'); });
  await open(page, 'vehicles');
  await page.locator('#page .card', { hasText: 'Demo Wohnmobil 2' }).first().getByRole('button', { name: 'Im Plan platzieren' }).click();
  await expect(page).toHaveURL(origin + '/#/hall/2');
  await expect(page.locator('#modal')).not.toHaveAttribute('open', '');
  await expect(page.locator('.gp-vehc.is-armed')).toContainText('Demo Wohnmobil 2');
});

test('several open extra costs are marked paid at once and can be taken back', async ({ page }) => {
  const posts = [];
  const paid = (id) => async route => { posts.push([id, route.request().postDataJSON().paid]); return route.fulfill({ json: { status: 'ok' } }); };
  await arrange(page, 1440, 'light', { overrides: {
    '/charges': [
      { id: 1, person_id: 1, person_name: 'Demo Berger 1', description: 'Innenreinigung', amount: 45, quantity: 1, total: 45, charged_on: '2026-09-02', paid: false },
      { id: 4, person_id: 2, person_name: 'Demo Gruber 2', description: 'Batterie laden', amount: 15, quantity: 1, total: 15, charged_on: '2026-09-03', paid: false },
      { id: 3, person_id: 1, person_name: 'Demo Berger 1', vehicle_id: 1, vehicle_label: 'Demo Wohnmobil 1', description: 'Batterieservice', amount: 30, quantity: 1, total: 30, charged_on: '2026-08-30', paid: false },
      { id: 5, person_id: 2, person_name: 'Demo Gruber 2', description: 'Stromanschluss', amount: 20, quantity: 1, total: 20, charged_on: '2026-08-28', paid: true },
    ],
    '/charges/1/paid': paid(1), '/charges/4/paid': paid(4), '/charges/5/paid': paid(5),
  } });
  await open(page, 'finance');
  await expect(page.locator('.page-context')).toHaveText('Einmalige und wiederkehrende Zusatzkosten prüfen und begleichen.');
  await page.getByRole('button', { name: 'Mehrfachauswahl' }).click();
  // Gebundene Posten folgen ihrem Gefährt: keine Auswahl. Freie bezahlte sind wählbar.
  await expect(page.locator('.bulk-cb')).toHaveCount(3);
  await page.getByRole('checkbox', { name: /Innenreinigung/ }).check();
  await page.getByRole('checkbox', { name: /Batterie laden/ }).check();
  await expect(page.locator('.bulk-bar')).toContainText('2 ausgewählt');
  await page.locator('.bulk-bar').getByRole('button', { name: 'Als bezahlt markieren' }).click();
  await expect(page.locator('#toast')).toContainText('2 als bezahlt markiert');
  expect(posts).toEqual([[1, true], [4, true]]);
  await page.locator('#toast').getByRole('button', { name: 'Rückgängig' }).click();
  await expect.poll(() => posts.length).toBe(4);
  expect(posts.slice(2)).toEqual([[1, false], [4, false]]);
  // Zurück auf offen: nur der bezahlte Posten ändert sich, der offene bleibt unberührt.
  await page.getByRole('button', { name: 'Mehrfachauswahl' }).click();
  await page.getByRole('checkbox', { name: /Stromanschluss/ }).check();
  await page.getByRole('checkbox', { name: /Innenreinigung/ }).check();
  await page.locator('.bulk-bar').getByRole('button', { name: 'Als offen markieren' }).click();
  await expect(page.locator('#toast')).toContainText('1 als offen markiert');
  expect(posts.slice(4)).toEqual([[5, false]]);
});

test('storno names its legal basis', async ({ page }) => {
  const requests = await arrange(page, 1440, 'light');
  await open(page, 'invoices/1');
  await page.getByRole('button', { name: 'Storno', exact: true }).click();
  await expect(page.locator('#confirm-body')).toContainText('§ 131 BAO');
  await page.keyboard.press('Escape');
  expect(writes(requests)).toEqual([]);
});

test('menu sheet closes with its own button; a list without entries shows one action', async ({ page }) => {
  await arrange(page, 390, 'light', { overrides: { '/vehicles': [], '/handover-templates': [] } });
  await open(page, 'vehicles');
  await expect(page.locator('.toolbar')).toBeHidden();
  await expect(page.locator('.list-summary')).toBeHidden();
  await expect(page.locator('#page .empty')).toContainText('Noch keine Gefährte.');
  await expect(page.locator('#page .empty').getByRole('button', { name: '+ Erstes Gefährt' })).toBeVisible();
  await open(page, 'handover-templates');
  await expect(page.locator('#page .empty').getByRole('button', { name: '+ Erste Vorlage' })).toBeVisible();

  await page.getByRole('button', { name: 'Menü', exact: true }).click();
  await expect(page.locator('#menu')).toHaveAttribute('open', '');
  await expect(page.locator('#menu .menu-item').first()).toBeFocused();
  await page.getByRole('button', { name: 'Menü schließen' }).click();
  await expect(page.locator('#menu')).not.toHaveAttribute('open', '');
});

test('lists default to 25 per page and remember the chosen size', async ({ page }) => {
  await arrange(page, 1440, 'light');
  await open(page, 'persons');
  await expect(page.locator('.list-summary')).toHaveText('1–24 von 24 Einträgen');
  await page.getByRole('combobox', { name: 'Einträge pro Seite' }).selectOption('10');
  await expect(page.locator('.list-summary')).toHaveText('1–10 von 24 Einträgen');
  await page.reload();
  await expect(page.locator('.list-summary')).toHaveText('1–10 von 24 Einträgen');
  await expect(page.getByRole('combobox', { name: 'Einträge pro Seite' })).toHaveValue('10');
});

test('the person form says that first or last name is enough and checks it before saving', async ({ page }) => {
  const requests = await arrange(page, 1440, 'light');
  await open(page, 'persons');
  await page.locator('.page-head').getByRole('button', { name: '+ Person' }).click();
  const modal = page.locator('#modal');
  await expect(modal.locator('.form-required-note')).toHaveText('* Vor- oder Nachname: eines davon genügt.');
  await expect(modal.locator('label[for="f_first_name"]')).toContainText('*');
  await modal.getByRole('button', { name: 'Speichern' }).click();
  await expect(modal.locator('#err_first_name')).toHaveText('Vor- oder Nachname angeben');
  await expect(page.getByRole('textbox', { name: 'Vorname', exact: true })).toBeFocused();
  expect(writes(requests)).toEqual([]);
});

test('calendar chips lead with the name; months are Austrian; the person page folds history and statistics', async ({ page }) => {
  await arrange(page, 1440, 'light');
  await open(page, 'calendar');
  const chip = page.locator('.cal-cell .cal-ev.cal-pickup').first();
  await expect(chip).toHaveAttribute('title', 'Abholung: Demo Wohnmobil 1');
  await expect(chip.locator('svg')).toHaveCount(1);
  expect(await chip.evaluate(n => [...n.childNodes].filter(c => c.nodeType === 3).map(c => c.textContent).join(''))).toBe('Demo Wohnmobil 1');
  await expect(chip.locator('.sr-only')).toHaveText('Abholung: ');
  for (let i = 0; i < 8; i++) await page.getByRole('button', { name: 'Voriger Monat' }).click();
  await expect(page.locator('.calendar-head h2')).toHaveText('Kalender · Jänner 2026');

  await open(page, 'persons/1');
  const folds = page.locator('.person-fold');
  await expect(folds).toHaveCount(2);
  await expect(folds.nth(0)).not.toHaveAttribute('open', '');
  await expect(folds.nth(0).locator('summary')).toContainText('1 Ereignis');
  await expect(folds.nth(1).locator('summary')).toContainText('Kostenstatistik');
  await page.locator('.section-index a', { hasText: 'Verlauf' }).click();
  await expect(folds.nth(0)).toHaveAttribute('open', '');
  await expect(folds.nth(0)).toContainText('Innenreinigung erfasst');
});

test('planner tools use the app icon set; no font-dependent glyphs remain', async ({ page }) => {
  await arrange(page, 1440, 'light');
  await open(page, 'hall/1');
  const toolbar = page.locator('.gp-toolbar');
  for (const name of ['Rückgängig (Strg+Z)', 'Wiederholen (Strg+Umschalt+Z)', 'Darstellung Symbol', 'Darstellung Foto', 'Darstellung Rechteck', 'Pufferzonen zwischen Fahrzeugen (Taste P)']) {
    await expect(toolbar.getByRole('button', { name, exact: true }).locator('svg')).toHaveCount(1);
  }
  const text = await toolbar.evaluate(n => n.textContent);
  for (const glyph of ['↶', '↷', '◈', '▣', '▭', '⤢', '⭳', '▤', '⊾']) expect(text).not.toContain(glyph);
});

test('a 320 px phone shows short tab labels; the full name stays the accessible name', async ({ page }) => {
  await arrange(page, 320, 'light');
  await open(page, 'dashboard');
  await expect(page.locator('.tab[data-route="garages"] .tab-short')).toBeVisible();
  await expect(page.locator('.tab[data-route="garages"] .tab-short')).toHaveText('Stellpl.');
  await expect(page.locator('.tab[data-route="vehicles"] .tab-label')).toBeVisible();
  // Der volle Name bleibt der zugängliche Name, auch wenn die Kurzform sichtbar ist.
  for (const name of ['Übersicht', 'Personen', 'Gefährte', 'Tarife', 'Stellplätze']) {
    await expect(page.locator('.tabbar').getByRole('button', { name, exact: true })).toBeVisible();
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('.tab[data-route="garages"] .tab-short')).toBeHidden();
  await expect(page.locator('.tab[data-route="garages"] .tab-full')).toBeVisible();
});

test('status bars stay readable at 320 px and 200 % text: name and number on top, bar below', async ({ page }) => {
  await arrange(page, 320, 'light');
  await open(page, 'dashboard');
  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  const row = page.locator('.bars .status-row').first();
  await expect.poll(() => row.locator('.bar-track').evaluate(n => Math.round(n.getBoundingClientRect().width))).toBeGreaterThan(100);
  const [name, track, val] = await Promise.all(['.status-name', '.bar-track', '.bar-val'].map(s => row.locator(s).boundingBox()));
  expect(track.y).toBeGreaterThan(name.y + name.height - 1);
  expect(val.x + val.width).toBeLessThanOrEqual(320);
});

test('tab labels never break mid-word; at 200 % text the bar shows icons and keeps the names', async ({ page }) => {
  await arrange(page, 390, 'light');
  await open(page, 'dashboard');
  const label = page.locator('.tab[data-route="garages"] > span:last-child');
  const lineHeight = await label.evaluate(n => parseFloat(getComputedStyle(n).lineHeight) || parseFloat(getComputedStyle(n).fontSize) * 1.3);
  expect((await label.boundingBox()).height).toBeLessThan(lineHeight * 1.6);
  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  await expect.poll(() => label.evaluate(n => n.getBoundingClientRect().width)).toBeLessThanOrEqual(1);
  for (const name of ['Übersicht', 'Personen', 'Gefährte', 'Tarife', 'Stellplätze']) {
    await expect(page.locator('.tabbar').getByRole('button', { name, exact: true })).toBeVisible();
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
});

test('desktop header shows the search field with its shortcut; icons are distinct', async ({ page }) => {
  await arrange(page, 1440, 'light');
  await open(page, 'dashboard');
  const search = page.getByRole('button', { name: /^Suchen/ });
  await expect(search.locator('.search-hint')).toBeVisible();
  // Kürzel im Feld und im Tooltip passen zur Plattform (Chromium im Test: kein Mac).
  const mac = await page.evaluate(() => /Mac|iPhone|iPad/i.test(navigator.platform || navigator.userAgent || ''));
  await expect(search.locator('.search-kbd')).toHaveText(mac ? '⌘K' : 'Strg K');
  await expect(search).toHaveAttribute('title', mac ? 'Suche · ⌘K' : 'Suche · Strg K');
  await page.getByRole('button', { name: 'Menü', exact: true }).click();
  const icon = (label) => page.locator('#menu .menu-item', { hasText: new RegExp('^' + label + '$') }).locator('svg').innerHTML();
  expect(await icon('Rechnungs-Einstellungen')).not.toBe(await icon('Einstellungen'));
  await page.keyboard.press('Escape');
  // Audit-Log: Aktionen rechts wie in allen Seitenköpfen.
  await open(page, 'audit');
  const head = page.locator('.route-view > .detail-head');
  const [h2, action] = await Promise.all([head.locator('h2').boundingBox(), head.getByRole('link', { name: /Revisionsexport/ }).boundingBox()]);
  expect(action.x).toBeGreaterThan(h2.x + h2.width / 2);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(search.locator('.search-hint')).toBeHidden();
});
