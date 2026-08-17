// AR2 — unit tests for the planner's pure geometry (web/static/js/geometry.js),
// the module the SPA uses for per-room m². Run: `node --test tests/geometry/`.
// These pin the exact behaviour of the area/face fixes made this cycle.
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const PG = require('../../web/static/js/geometry.js');

const T = 0.24;
const near = (a, b, eps = 1e-3) => Math.abs(a - b) <= eps;

// A rectangle's edges carry a synthetic offset like edgeOffs(): for the perimeter
// of a CCW ring, "inner" pushes each wall to its exterior (away from the room),
// "outer" pushes it to the interior. Axis passes null (no offset).
function rectOffsets(N, E, ref) {
    if (ref === 'axis') return null;
    // ring is CCW-ish; interior is on the left of each directed edge → exterior is right.
    return E.map((e) => {
        const a = N[e.a], b = N[e.b], dx = b.x - a.x, dy = b.y - a.y, L = Math.hypot(dx, dy) || 1;
        const rx = dy / L, ry = -dx / L; // right (exterior) normal
        const m = (ref === 'inner' ? 1 : -1) * (T / 2);
        return { ox: rx * m, oy: ry * m };
    });
}

const rect5 = { N: [{ x: 0, y: 0 }, { x: 5, y: 0 }, { x: 5, y: 5 }, { x: 0, y: 5 }], E: [{ a: 0, b: 1, thick: T }, { a: 1, b: 2, thick: T }, { a: 2, b: 3, thick: T }, { a: 3, b: 0, thick: T }] };

test('5x5 single room — area per Bezug', () => {
    const inner = PG.roomAreas(rect5.N, rect5.E, rectOffsets(rect5.N, rect5.E, 'inner'));
    const axis = PG.roomAreas(rect5.N, rect5.E, null);
    const outer = PG.roomAreas(rect5.N, rect5.E, rectOffsets(rect5.N, rect5.E, 'outer'));
    assert.equal(inner.length, 1); assert.equal(axis.length, 1); assert.equal(outer.length, 1);
    assert.ok(near(inner[0].area, 25.0), 'inner ' + inner[0].area);
    assert.ok(near(axis[0].area, 4.76 * 4.76), 'axis ' + axis[0].area);   // 22.6576
    assert.ok(near(outer[0].area, 4.52 * 4.52), 'outer ' + outer[0].area); // 20.4304
});

test('multi-room — 8x6 split by an interior wall = two equal rooms', () => {
    const N = [{ x: 0, y: 0 }, { x: 4, y: 0 }, { x: 8, y: 0 }, { x: 8, y: 6 }, { x: 4, y: 6 }, { x: 0, y: 6 }];
    const E = [{ a: 0, b: 1 }, { a: 1, b: 2 }, { a: 2, b: 3 }, { a: 3, b: 4 }, { a: 4, b: 5 }, { a: 5, b: 0 }, { a: 1, b: 4 }].map((e) => ({ ...e, thick: T }));
    const rooms = PG.roomAreas(N, E, null);
    assert.equal(rooms.length, 2);
    rooms.forEach((r) => assert.ok(near(r.area, 3.76 * 5.76, 5e-3), 'room ' + r.area)); // 21.6576
    assert.ok(near(rooms[0].area + rooms[1].area, 2 * 3.76 * 5.76, 1e-2));
});

test('robustness — bow-tie / self-intersecting ring yields no room', () => {
    const N = [{ x: 0, y: 0 }, { x: 5, y: 5 }, { x: 5, y: 0 }, { x: 0, y: 5 }];
    const E = [{ a: 0, b: 1 }, { a: 1, b: 2 }, { a: 2, b: 3 }, { a: 3, b: 0 }].map((e) => ({ ...e, thick: T }));
    assert.equal(PG.roomAreas(N, E, null).length, 0);
});

test('robustness — a dangling stub + T-junctions does not throw and finds the room', () => {
    // hall-6-like: perimeter loop with a degree-1 stub and degree-3 junctions
    const N = [{ x: 0, y: 0 }, { x: 6, y: 0 }, { x: 6, y: 4 }, { x: 3, y: 4 }, { x: 3, y: 7 }, { x: 0, y: 7 }, { x: 3, y: 2 }];
    const E = [{ a: 0, b: 1 }, { a: 1, b: 2 }, { a: 2, b: 3 }, { a: 3, b: 4 }, { a: 4, b: 5 }, { a: 5, b: 0 }, { a: 3, b: 6 }].map((e) => ({ ...e, thick: T }));
    const rooms = PG.roomAreas(N, E, null);
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
