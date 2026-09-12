'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// Exercise the real UI predicate, without exporting app internals or a backend.
const source = fs.readFileSync(path.join(__dirname, '../../web/static/js/app.js'), 'utf8');
const start = source.indexOf('    function chargeIsPaid(it) {');
const end = source.indexOf('    function financeRow(it) {', start);
assert.ok(start >= 0 && end > start, 'charge status function exists');
const chargeIsPaid = vm.runInNewContext(source.slice(start, end) + '\nchargeIsPaid;');

const cases = [
  ['standalone unpaid', { paid: false }, false],
  ['standalone paid', { paid: true }, true],
  ['missing optional flags', {}, false],
  ['standalone ignores an unrelated vehicle flag', { vehicle_id: null, vehicle_paid: true }, false],
  ['bound charge follows vehicle payment', { vehicle_id: 1, vehicle_paid: true, paid: false }, true],
  ['bound charge honors its own payment', { vehicle_id: 1, vehicle_paid: false, paid: true }, true],
  ['bound unpaid', { vehicle_id: 1, vehicle_paid: false, paid: false }, false],
  ['standalone invoice paid', { vehicle_id: null, invoiced: true, invoice_open: false, paid: false }, true],
  ['standalone invoice open', { vehicle_id: null, invoiced: true, invoice_open: true, paid: false }, false],
  ['bound invoice paid', { vehicle_id: 1, invoiced: true, invoice_open: false, paid: false }, true],
  ['open invoice is authoritative over stale raw flags', { vehicle_id: 1, invoiced: true, invoice_open: true, paid: true, vehicle_paid: true }, false],
  ['unbilled false invoice_open does not imply paid', { invoice_open: false, paid: false }, false],
];
for (const [name, charge, expected] of cases) {
  test('charge status: ' + name, () => assert.equal(chargeIsPaid(charge), expected));
}
