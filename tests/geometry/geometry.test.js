// AR2 — unit tests for the planner's pure geometry (web/static/js/geometry.js),
// the module the SPA uses for per-room m². Run: `node --test tests/geometry/`.
// These pin the exact behaviour of the area/face fixes: areas are derived from
// topology + Bezug (no fragile raster probe), so they're exact AND invariant to
// collinear helper points on a straight wall.
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const PG = require('../../web/static/js/geometry.js');

const T = 0.24;
const near = (a, b, eps = 1e-3) => Math.abs(a - b) <= eps;
const areas = (rooms) => rooms.map((r) => r.area);

const rect5 = { N: [{ x: 0, y: 0 }, { x: 5, y: 0 }, { x: 5, y: 5 }, { x: 0, y: 5 }], E: [{ a: 0, b: 1, thick: T }, { a: 1, b: 2, thick: T }, { a: 2, b: 3, thick: T }, { a: 3, b: 0, thick: T }] };

test('5x5 single room — area per Bezug', () => {
    const inner = PG.roomAreas(rect5.N, rect5.E, 'inner');
    const axis = PG.roomAreas(rect5.N, rect5.E, 'axis');
    const outer = PG.roomAreas(rect5.N, rect5.E, 'outer');
    assert.equal(inner.length, 1); assert.equal(axis.length, 1); assert.equal(outer.length, 1);
    assert.ok(near(inner[0].area, 25.0), 'inner ' + inner[0].area);          // drawn = inner face
    assert.ok(near(axis[0].area, 4.76 * 4.76), 'axis ' + axis[0].area);      // 22.6576
    assert.ok(near(outer[0].area, 4.52 * 4.52), 'outer ' + outer[0].area);   // 20.4304
});

test('exact + STABLE: a collinear helper point on a straight wall does not change the area', () => {
    const t = 0.30;
    const clean = { N: [{ x: 0, y: 0 }, { x: 36.1, y: 0 }, { x: 36.1, y: 12.8 }, { x: 0, y: 12.8 }], E: [{ a: 0, b: 1 }, { a: 1, b: 2 }, { a: 2, b: 3 }, { a: 3, b: 0 }].map((e) => ({ ...e, thick: t })) };
    const split = { N: [{ x: 0, y: 0 }, { x: 18.05, y: 0 }, { x: 36.1, y: 0 }, { x: 36.1, y: 12.8 }, { x: 0, y: 12.8 }], E: [{ a: 0, b: 1 }, { a: 1, b: 2 }, { a: 2, b: 3 }, { a: 3, b: 4 }, { a: 4, b: 0 }].map((e) => ({ ...e, thick: t })) };
    const a1 = PG.roomAreas(clean.N, clean.E, 'inner')[0].area;
    const a2 = PG.roomAreas(split.N, split.E, 'inner')[0].area;
    assert.ok(near(a1, 36.1 * 12.8), 'clean inner = 462.08, got ' + a1);
    assert.ok(near(a1, a2, 1e-6), 'collinear point changed the area: ' + a1 + ' vs ' + a2);
});

test('multi-room — 8x6 split by a shared interior wall = two equal rooms (½-thick each side)', () => {
    const N = [{ x: 0, y: 0 }, { x: 4, y: 0 }, { x: 8, y: 0 }, { x: 8, y: 6 }, { x: 4, y: 6 }, { x: 0, y: 6 }];
    const E = [{ a: 0, b: 1 }, { a: 1, b: 2 }, { a: 2, b: 3 }, { a: 3, b: 4 }, { a: 4, b: 5 }, { a: 5, b: 0 }, { a: 1, b: 4 }].map((e) => ({ ...e, thick: T }));
    const rooms = PG.roomAreas(N, E, 'axis');
    assert.equal(rooms.length, 2);
    rooms.forEach((r) => assert.ok(near(r.area, 3.76 * 5.76, 5e-3), 'room ' + r.area)); // 21.6576
});

test('robustness — bow-tie / self-intersecting ring yields no room', () => {
    const N = [{ x: 0, y: 0 }, { x: 5, y: 5 }, { x: 5, y: 0 }, { x: 0, y: 5 }];
    const E = [{ a: 0, b: 1 }, { a: 1, b: 2 }, { a: 2, b: 3 }, { a: 3, b: 0 }].map((e) => ({ ...e, thick: T }));
    assert.equal(PG.roomAreas(N, E, 'inner').length, 0);
});

test('robustness — a dangling stub + T-junctions does not throw and finds the room', () => {
    const N = [{ x: 0, y: 0 }, { x: 6, y: 0 }, { x: 6, y: 4 }, { x: 3, y: 4 }, { x: 3, y: 7 }, { x: 0, y: 7 }, { x: 3, y: 2 }];
    const E = [{ a: 0, b: 1 }, { a: 1, b: 2 }, { a: 2, b: 3 }, { a: 3, b: 4 }, { a: 4, b: 5 }, { a: 5, b: 0 }, { a: 3, b: 6 }].map((e) => ({ ...e, thick: T }));
    const rooms = PG.roomAreas(N, E, 'axis');
    assert.ok(rooms.length >= 1);
    rooms.forEach((r) => { assert.ok(Number.isFinite(r.area) && r.area > 0); assert.ok(Number.isFinite(r.cx) && Number.isFinite(r.cy)); });
});

test('insetRing — concave (reflex) corner does not spike out of bounds', () => {
    const L = [{ x: 0, y: 0 }, { x: 6, y: 0 }, { x: 6, y: 3 }, { x: 3, y: 3 }, { x: 3, y: 6 }, { x: 0, y: 6 }];
    const ring = PG.insetRing(L, L.map(() => 0.12));
    assert.ok(ring.every((p) => Math.abs(p.x) <= 8 && Math.abs(p.y) <= 8), 'no runaway vertex');
});

test('pointInPoly + ringAreaS basics', () => {
    assert.equal(PG.pointInPoly(2.5, 2.5, rect5.N), true);
    assert.equal(PG.pointInPoly(-1, 2.5, rect5.N), false);
    assert.ok(near(Math.abs(PG.ringAreaS(rect5.N)), 25.0));
});
