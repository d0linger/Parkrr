'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../../web/static/js/app.js'), 'utf8');
const start = source.indexOf('    function chargeFormTotal(');
const end = source.indexOf('    function enhanceChargeFields(', start);
assert.ok(start >= 0 && end > start);
const total = vm.runInNewContext(source.slice(start, end) + '\nchargeFormTotal;');

for (const [name, amount, quantity, period, expected] of [
  ['empty amount', '', '1', 'once', null],
  ['blank amount', ' ', '1', 'once', null],
  ['zero amount', '0', '3', 'once', 0],
  ['fractional quantity', '45', '2.5', 'once', 112.5],
  ['empty quantity retains save default', '45', '', 'once', 45],
  ['zero quantity retains save default', '45', '0', 'once', 45],
  ['negative one-off amount', '-20', '2', 'once', -40],
  ['monthly ignores quantity', '20', '3', 'monthly', 20],
  ['yearly ignores unusable quantity', '240', 'Infinity', 'yearly', 240],
  ['invalid amount', 'invalid', '1', 'once', null],
  ['nonfinite amount', 'Infinity', '1', 'once', null],
  ['overflow', '1e308', '3', 'once', null],
  ['unsafe cents', '1e14', '1', 'once', null],
  ['large safe amount', '1000000000', '2', 'once', 2000000000],
  ['currency rounding remains presentation-only', '0.01', '1.5', 'once', 0.015],
]) {
  test('charge preview: ' + name, () => assert.equal(total(amount, quantity, period), expected));
}
