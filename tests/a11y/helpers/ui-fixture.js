// Every request is intercepted. No real credentials or operator data are used.
const fs = require('node:fs/promises');
const path = require('node:path');
const staticRoot = path.resolve(__dirname, '../../..', 'web/static');
const origin = 'http://parkrr.test';
const persons = Array.from({ length: 24 }, (_, i) => ({
  id: i + 1, first_name: 'Demo', last_name: ['Berger', 'Gruber', 'Müller'][i % 3] + ' ' + (i + 1),
  email: 'demo' + i + '@example.invalid', phone: '+43 000 000000',
}));
const categories = [{ id: 1, name: 'Wohnmobil', price_monthly: 85, price_yearly: 1020 }];
const vehicles = persons.slice(0, 12).map((p, i) => ({
  id: i + 1, person_id: p.id, category_id: 1, label: 'Demo Wohnmobil ' + (i + 1),
  license_plate: 'DEMO-' + i, status: i % 3 ? 'stored' : 'reserved', paid: i % 2 === 0,
  billing_period: 'monthly', start_date: '2026-01-01', effective_rate: 85, accrued: 680,
}));
const overview = {
  year: 2026, total_persons: 24, active_vehicles: 12, total_vehicles: 18, total_categories: 1,
  outstanding_total: 2480, accrued_this_year: 12480, paid_total: 10000,
  payments_this_year: 8000, payments_total: 10000,
  revenue_by_month: [900, 950, 980, 1200, 1400, 1300, 1750, 2000, 2000, 0, 0, 0],
  charges_by_month: [40, 80, 50, 90, 100, 110, 160, 130, 170, 0, 0, 0],
  status_counts: { stored: 8, reserved: 4, collected: 6 },
  top_outstanding: persons.slice(0, 3).map(p => ({ person_id: p.id, name: p.first_name + ' ' + p.last_name, outstanding: 680 })),
};

async function mockUI(page, { delay = 0, role = 'editor', loggedIn = true, overrides = {} } = {}) {
  const requests = [];
  const data = {
    '/auth/capabilities': {}, '/auth/me': loggedIn ? { id: 1, username: 'Demo', role, is_admin: role === 'admin' } : null,
    '/persons': persons, '/categories': categories, '/services': [], '/vehicles': vehicles,
    '/persons/outstanding': Object.fromEntries(persons.map(p => [p.id, p.id % 2 ? 680 : 0])),
    '/overview': overview, '/occupancy': { active: 12, placed: 8, halls: [], trend: [] },
    '/charges': [], '/recurring-charges': [], '/agreements': [], '/invoices/overdue': [],
    '/vehicles/ending-soon': [], '/portal-requests': [], '/garages': [], '/halls': [],
    '/backup/health': { enabled: false, targets: {} }, '/backup/status': { enabled: false },
    '/auth/2fa': {}, '/auth/sessions': [], ...overrides,
  };
  await page.route('**/*', async route => {
    const url = new URL(route.request().url());
    if (url.origin !== origin) return route.abort();
    if (url.pathname.startsWith('/api/')) {
      const key = url.pathname.slice(4);
      requests.push({ path: key, method: route.request().method(), at: Date.now() });
      if (delay) await new Promise(resolve => setTimeout(resolve, delay));
      if (typeof data[key] === 'function') return data[key](route);
      const status = key === '/auth/me' && !loggedIn ? 401 : 200;
      return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data[key] ?? []) });
    }
    const file = path.resolve(staticRoot, '.' + (url.pathname === '/' ? '/index.html' : url.pathname));
    if (!file.startsWith(staticRoot + path.sep)) return route.abort();
    const types = { '.html': 'text/html', '.css': 'text/css', '.js': 'application/javascript', '.svg': 'image/svg+xml', '.woff2': 'font/woff2', '.png': 'image/png', '.webmanifest': 'application/manifest+json' };
    try { return await route.fulfill({ contentType: types[path.extname(file)] || 'application/octet-stream', body: await fs.readFile(file) }); }
    catch { return route.fulfill({ status: 404, body: '' }); }
  });
  return requests;
}
module.exports = { mockUI, origin, persons, vehicles };
