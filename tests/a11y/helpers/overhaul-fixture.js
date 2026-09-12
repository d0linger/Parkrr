const { persons, vehicles } = require('./ui-fixture');
// Synthetic operational records, never copied from the operator database.
const fleet = vehicles.map((v, i) => ({ ...v, person_name: 'Demo Berger ' + (i + 1), category_name: 'Wohnmobil',
  accrued_cost: 680, accrued: 680, length_m: 6, width_m: 2.4, height_m: 2.7, weight_t: 3,
  end_date: i < 3 ? '2026-09-' + (10 + i) : null, reserved_from: i === 0 ? '2026-09-04' : null,
}));
const invoice = { id: 1, person_id: 1, number: '2026-0012', status: 'offen', issued_on: '2026-09-01', due_on: '2026-09-15',
  person_name: 'Demo Berger 1', total: 680, subtotal: 680, tax_amount: 0, paid_amount: 0, open_amount: 680, kleinunternehmer: true,
  seller: { name: 'Demo Einstellbetrieb', address: 'Musterstraße 12\n0000 Musterort' }, buyer: { name: 'Demo Berger 1', address: 'Beispielweg 4\n0000 Musterort' },
  items: [{ pos: 1, description: 'Einstellplatz Wohnmobil · Januar bis August', quantity: 8, unit_amount: 85, line_total: 680 }],
};
const charges = [
  { id: 1, person_id: 1, person_name: 'Demo Berger 1', description: 'Innenreinigung', amount: 45, quantity: 1, total: 45, charged_on: '2026-09-02', paid: false },
  { id: 2, person_id: 2, person_name: 'Demo Gruber 2', description: 'Stromanschluss', amount: 20, quantity: 2, total: 40, charged_on: '2026-09-01', paid: true },
  { id: 3, person_id: 1, person_name: 'Demo Berger 1', vehicle_id: 1, vehicle_label: 'Demo Wohnmobil 1', description: 'Batterieservice', amount: 30, quantity: 1, total: 30, charged_on: '2026-08-30', paid: false },
];
const hall = { id: 1, garage_id: 1, name: 'Halle Nord', spot_count: 1, geometry: { Wm: 14, Hm: 9, shape: 'rect', tor: 3, load: 5,
  floor: [[0, 0], [14, 0], [14, 9], [0, 9]], excl: [] } };
const overrides = {
  '/categories': [{ id: 1, name: 'Wohnmobil', default_monthly_cost: 85, default_yearly_cost: 1020 }],
  '/persons': persons, '/vehicles': fleet, '/vehicles/unassigned': fleet.slice(1), '/charges': charges,
  '/services': [{ id: 1, name: 'Innenreinigung', default_amount: 45 }, { id: 2, name: 'Stromanschluss', default_amount: 20 }],
  '/users': [{ id: 1, username: 'Demo Admin', role: 'admin', email: 'admin@example.invalid', totp_enabled: true },
    { id: 2, username: 'Demo Team', role: 'editor', email: 'team@example.invalid', totp_enabled: false },
    { id: 3, username: 'Demo Lesekonto', role: 'reader', email: 'lesen@example.invalid', totp_enabled: true }],
  '/persons/1/stats': { person_name: 'Demo Berger 1', year: 2026, balance: 755, total_accrued: 680, total_charges: 75, total_paid: 0,
    payments_year: 0, payments_total: 0, agreements: [], recurring_charges: [], years: [{ year: 2025, cost: 600 }, { year: 2026, cost: 755 }],
    monthly_accrued: [85, 85, 85, 85, 85, 85, 85, 85, 75, 0, 0, 0] },
  '/persons/1/payments': [], '/persons/1/invoices': [invoice], '/persons/1/recurring': [], '/persons/1/attachments': [],
  '/persons/1/timeline': [{ kind: 'charge', text: 'Innenreinigung erfasst', at: '2026-09-02T10:00:00Z' }],
  '/vehicles/1/photos': [], '/vehicles/1/history': [], '/vehicles/1/handovers': [], '/vehicles/1/attachments': [],
  '/invoices/1': invoice,
  '/invoices/overdue': [{ ...invoice, days_overdue: 0, open_amount: 680 }],
  '/billing/settings': { seller_name: 'Demo Einstellbetrieb', seller_address: 'Musterstraße 12', kleinunternehmer: true,
    invoice_prefix: '2026-', next_invoice_no: 13, number_pad: 4, payment_terms_days: 14 },
  '/auth/sessions': [{ token: 'fixture-current', user_agent: 'Chrome', ip: '192.0.2.1', current: true, last_seen: '2026-09-10T09:00:00Z' },
    { token: 'fixture-other', user_agent: 'Firefox', ip: '192.0.2.2', current: false, last_seen: '2026-09-09T17:00:00Z' }],
  '/backup/status': { enabled: true, scheduled: true, s3: false, dir: '/backups', schema_version: 'demo', settings: {},
    status: { last_volume_at: '2026-09-10T03:00:00Z', last_volume_ok: true, last_volume_size: 2048000, restore_tested_at: '2026-09-10T03:01:00Z' },
    files: [{ name: 'parkrr-demo-20260910.dump.enc', modified: '2026-09-10T03:00:00Z', size: 2048000 }], s3_files: [] },
  '/audit': [{ id: 1, created_at: '2026-09-10T09:00:00Z', username: 'Demo Admin', action: 'create', entity: 'person', entity_id: 1, description: 'Demo Berger 1 angelegt', details: {} }],
  '/garages': [{ id: 1, name: 'Standort Nord', hall_count: 2 }, { id: 2, name: 'Standort Süd', hall_count: 0 }],
  '/garages/1/halls': [hall, { ...hall, id: 2, name: 'Halle Süd', spot_count: 0 }],
  '/halls/1/plan': { hall, garage_name: 'Standort Nord', spots: [{ id: 1, vehicle_id: 1, vehicle_label: 'Demo Wohnmobil 1', vehicle_type: 'Wohnmobil',
    person_id: 1, person_name: 'Demo Berger 1', length_m: 6, width_m: 2.4, height_m: 2.7, geometry: { x: 1, y: 1, w: 6, h: 2.4 } }] },
  '/planner-icons': [],
  '/portal/summary': { person_name: 'Demo Berger 1', open_total: 680, vehicles: [{ label: 'Demo Wohnmobil 1', status: 'stored' }],
    invoices: [{ ...invoice, open: 680 }], handovers: [] },
};
module.exports = { overrides, invoice, charges, hall };
