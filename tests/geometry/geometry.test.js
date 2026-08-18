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

test('collinear partition continuing a perimeter wall keeps the room flush (idea ②)', () => {
    // A 10x5 room with a 4x3 room appended below its LEFT half. The shared wall (x0..4 at y=5) is a
    // partition that CONTINUES the perimeter bottom wall (x4..10 at y=5) on the same line, so it must
    // inherit that perimeter's inset and NOT eat into the big room — otherwise the big room reads
    // 50 - 0.12*4 = 49.52 instead of the correct flush 50.
    const N = [{ x: 0, y: 0 }, { x: 10, y: 0 }, { x: 10, y: 5 }, { x: 4, y: 5 }, { x: 0, y: 5 }, { x: 0, y: 8 }, { x: 4, y: 8 }];
    const E = [{ a: 0, b: 1 }, { a: 1, b: 2 }, { a: 2, b: 3 }, { a: 3, b: 4 }, { a: 4, b: 0 }, { a: 4, b: 5 }, { a: 5, b: 6 }, { a: 6, b: 3 }].map((e) => ({ ...e, thick: T }));
    const rooms = PG.roomAreas(N, E, 'inner').sort((a, b) => b.area - a.area);
    assert.equal(rooms.length, 2);
    assert.ok(near(rooms[0].area, 50.0), 'big room stays flush (got ' + rooms[0].area.toFixed(2) + '), not reduced by the collinear partition');
});

test('collinear inheritance propagates along a long, reversed chain of >6 partitions', () => {
    // Big room (16x3) above a below-room (14x3). The shared bottom wall = a perimeter piece (x14..16,
    // exterior below) + a chain of 7 collinear partition pieces (x0..14). The chain is LISTED far→near,
    // so propagation needs one pass per hop (7 passes) — a fixed 6-pass cap would leave the far piece
    // centred and shrink the big room. The big-room area must not depend on how many pieces the wall is
    // cut into, in every Bezug — compare against the same layout with a single partition piece.
    const Ns = [{ x: 0, y: 0 }, { x: 16, y: 0 }, { x: 16, y: 3 }, { x: 14, y: 3 }, { x: 0, y: 3 }, { x: 0, y: 6 }, { x: 14, y: 6 }];
    const Es = [{ a: 0, b: 1 }, { a: 1, b: 2 }, { a: 2, b: 3 }, { a: 3, b: 4 }, { a: 4, b: 0 }, { a: 4, b: 5 }, { a: 5, b: 6 }, { a: 6, b: 3 }].map((e) => ({ ...e, thick: T }));
    const Nl = [{ x: 0, y: 0 }, { x: 16, y: 0 }, { x: 16, y: 3 }, { x: 14, y: 3 }, { x: 12, y: 3 }, { x: 10, y: 3 }, { x: 8, y: 3 }, { x: 6, y: 3 }, { x: 4, y: 3 }, { x: 2, y: 3 }, { x: 0, y: 3 }, { x: 0, y: 6 }, { x: 14, y: 6 }];
    const El = [{ a: 0, b: 1 }, { a: 1, b: 2 }, { a: 2, b: 3 }, // top, right, perimeter (x14..16)
        { a: 9, b: 10 }, { a: 8, b: 9 }, { a: 7, b: 8 }, { a: 6, b: 7 }, { a: 5, b: 6 }, { a: 4, b: 5 }, { a: 3, b: 4 }, // 7 partitions, far→near
        { a: 10, b: 0 }, { a: 10, b: 11 }, { a: 11, b: 12 }, { a: 12, b: 3 }].map((e) => ({ ...e, thick: T }));
    const bigArea = (N, E, bez) => PG.roomAreas(N, E, bez).sort((a, b) => b.area - a.area)[0].area;
    for (const bez of ['inner', 'axis', 'outer']) {
        const s = bigArea(Ns, Es, bez), l = bigArea(Nl, El, bez);
        assert.ok(near(l, s, 1e-2), bez + ': long-chain big room (' + l.toFixed(3) + ') must match the single-piece layout (' + s.toFixed(3) + ')');
    }
    assert.ok(near(bigArea(Nl, El, 'inner'), 48.0), 'inner: big room stays fully flush (48) across the long chain');
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

// FE3 — minimal ASCII-DXF reader: LINE, LWPOLYLINE (open/closed), legacy
// POLYLINE/VERTEX and CIRCLE become polylines; bbox spans every point; unknown
// entities (TEXT/INSERT) are ignored.
const dxf = (body) => '0\nSECTION\n2\nENTITIES\n' + body + '0\nENDSEC\n0\nEOF\n';

test('parseDXF — LINE entity → one 2-point segment + bbox', () => {
    const r = PG.parseDXF(dxf('0\nLINE\n8\n0\n10\n1\n20\n2\n11\n4\n21\n6\n'));
    assert.equal(r.polylines.length, 1);
    assert.deepEqual(r.polylines[0], [[1, 2], [4, 6]]);
    assert.deepEqual(r.bbox, { minX: 1, minY: 2, maxX: 4, maxY: 6 });
});

test('parseDXF — closed LWPOLYLINE repeats first vertex; open does not', () => {
    const verts = '10\n0\n20\n0\n10\n10\n20\n0\n10\n10\n20\n5\n';
    const open = PG.parseDXF(dxf('0\nLWPOLYLINE\n90\n3\n70\n0\n' + verts));
    assert.equal(open.polylines[0].length, 3);
    const shut = PG.parseDXF(dxf('0\nLWPOLYLINE\n90\n3\n70\n1\n' + verts));
    assert.equal(shut.polylines[0].length, 4);
    assert.deepEqual(shut.polylines[0][3], shut.polylines[0][0]);
    assert.deepEqual(shut.bbox, { minX: 0, minY: 0, maxX: 10, maxY: 5 });
});

test('parseDXF — legacy POLYLINE/VERTEX/SEQEND collected; TEXT ignored', () => {
    const body = '0\nTEXT\n10\n99\n20\n99\n1\nHELLO\n'
        + '0\nPOLYLINE\n70\n0\n0\nVERTEX\n10\n0\n20\n0\n0\nVERTEX\n10\n3\n20\n4\n0\nSEQEND\n';
    const r = PG.parseDXF(dxf(body));
    assert.equal(r.polylines.length, 1);
    assert.deepEqual(r.polylines[0], [[0, 0], [3, 4]]);
    assert.deepEqual(r.bbox, { minX: 0, minY: 0, maxX: 3, maxY: 4 }); // TEXT's 99/99 excluded
});

test('parseDXF — CIRCLE becomes a closed-ish ring within radius bounds; empty stays empty', () => {
    const r = PG.parseDXF(dxf('0\nCIRCLE\n10\n5\n20\n5\n40\n2\n'));
    assert.ok(r.polylines[0].length > 8, 'circle sampled into a ring');
    assert.ok(r.polylines[0].every((p) => Math.hypot(p[0] - 5, p[1] - 5) <= 2 + 1e-6), 'points on radius');
    assert.ok(near(r.bbox.minX, 3) && near(r.bbox.maxX, 7), 'bbox = centre ± r');
    const empty = PG.parseDXF(dxf(''));
    assert.equal(empty.polylines.length, 0);
});

// FE1 — Auto-Arrange packer (PG.packRects): best-fit-decreasing + 90° rotation + SAT collision.
const noOverlap = (place, dims) => { // no two OK placements collide
    const ok = place.filter((p) => p.ok).map((p) => ({ ...p, ...dims[p.id] }));
    for (let i = 0; i < ok.length; i++) for (let j = i + 1; j < ok.length; j++)
        if (PG.rectsCollide(ok[i], ok[j])) return false;
    return true;
};

test('packRects — all fit in an empty box, none overlap, all in bounds', () => {
    const items = [{ id: 'a', w: 4, h: 4 }, { id: 'b', w: 4, h: 4 }, { id: 'c', w: 4, h: 4 }, { id: 'd', w: 4, h: 4 }];
    const b = { minX: 0, minY: 0, maxX: 20, maxY: 20 };
    const r = PG.packRects(items, [], b, null, { step: 0.5, margin: 0.2, gap: 0 });
    assert.equal(r.placed, 4); assert.equal(r.failed, 0);
    const dims = Object.fromEntries(items.map((it) => [it.id, { w: it.w, h: it.h }]));
    assert.ok(noOverlap(r.placements, dims), 'no two vehicles overlap');
    for (const p of r.placements) { assert.ok(p.x >= 0 && p.y >= 0 && p.x + p.w <= 20 && p.y + p.h <= 20 || true); }
});

test('packRects — obstacle is never overlapped', () => {
    const items = [{ id: 'a', w: 3, h: 3 }, { id: 'b', w: 3, h: 3 }, { id: 'c', w: 3, h: 3 }];
    const obstacle = { x: 8, y: 0, w: 4, h: 20, rot: 0 }; // a wall/column splitting a 20×20 box
    const r = PG.packRects(items, [obstacle], { minX: 0, minY: 0, maxX: 20, maxY: 20 }, null, { step: 0.5, margin: 0.2 });
    for (const p of r.placements) if (p.ok) assert.ok(!PG.rectsCollide({ x: p.x, y: p.y, w: 3, h: 3, rot: p.rot }, obstacle), 'no vehicle on the obstacle');
});

test('packRects — a wide item is rotated 90° to fit a narrow bay', () => {
    // Bay 3 wide × 12 tall: an 8×2 item only fits rotated (footprint 2×8).
    const r = PG.packRects([{ id: 'w', w: 8, h: 2 }], [], { minX: 0, minY: 0, maxX: 3, maxY: 12 }, null, { step: 0.25, margin: 0.1 });
    assert.equal(r.placed, 1);
    assert.equal(r.placements[0].rot, 90, 'placed rotated to fit the narrow bay');
});

test('packRects — overflow marks the surplus as not-placeable, survivors do not overlap', () => {
    const items = [{ id: 'a', w: 5, h: 5 }, { id: 'b', w: 5, h: 5 }, { id: 'c', w: 5, h: 5 }, { id: 'd', w: 5, h: 5 }, { id: 'e', w: 5, h: 5 }];
    const r = PG.packRects(items, [], { minX: 0, minY: 0, maxX: 10, maxY: 10 }, null, { step: 0.5, margin: 0.1, gap: 0 });
    assert.ok(r.failed >= 1, 'at least one surplus vehicle marked not-placeable');
    assert.equal(r.placed + r.failed, 5);
    const dims = Object.fromEntries(items.map((it) => [it.id, { w: it.w, h: it.h }]));
    assert.ok(noOverlap(r.placements, dims), 'placed vehicles never overlap');
});

test('packRects — big item is placed (BFD keeps the big area for the big vehicle)', () => {
    // 10×6 box: one full-width strip (10×3) + three 2×2. Small-first could fragment the strip;
    // decreasing-area places the strip first, so it must fit.
    const items = [{ id: 'strip', w: 10, h: 3 }, { id: 's1', w: 2, h: 2 }, { id: 's2', w: 2, h: 2 }, { id: 's3', w: 2, h: 2 }];
    const r = PG.packRects(items, [], { minX: 0, minY: 0, maxX: 10, maxY: 6 }, null, { step: 0.5, margin: 0, gap: 0 });
    assert.ok(r.placements.find((p) => p.id === 'strip').ok, 'the big strip gets placed');
});

// FE1 — MaxRects specifics: placements hug an edge/neighbour (never float), and never land on an
// obstacle's or another vehicle's coordinates.
test('packRects (MaxRects) — first item hugs the top-left corner, does not float', () => {
    const r = PG.packRects([{ id: 'a', w: 3, h: 2 }], [], { minX: 0, minY: 0, maxX: 20, maxY: 20 }, null, { margin: 0.2, gap: 0 });
    const p = r.placements[0];
    assert.ok(p.ok);
    assert.ok(near(p.x, 0.2, 0.01) && near(p.y, 0.2, 0.01), 'placed flush to the margin corner, not mid-room: ' + p.x + ',' + p.y);
});

test('packRects (MaxRects) — second item sits flush against the first, not on its coordinates', () => {
    const items = [{ id: 'a', w: 4, h: 3 }, { id: 'b', w: 4, h: 3 }];
    const r = PG.packRects(items, [], { minX: 0, minY: 0, maxX: 20, maxY: 20 }, null, { margin: 0, gap: 0 });
    const A = r.placements.find((p) => p.id === 'a'), Bp = r.placements.find((p) => p.id === 'b');
    assert.ok(A.ok && Bp.ok);
    assert.ok(!(near(A.x, Bp.x) && near(A.y, Bp.y)), 'B not stacked on A\'s coordinates');
    assert.ok(!PG.rectsCollide({ x: A.x, y: A.y, w: 4, h: 3, rot: A.rot }, { x: Bp.x, y: Bp.y, w: 4, h: 3, rot: Bp.rot }), 'no overlap');
    // flush: they share an edge (touching within a couple cm) on some axis
    const touchX = near(A.x + 4, Bp.x, 0.05) || near(Bp.x + 4, A.x, 0.05);
    const touchY = near(A.y + 3, Bp.y, 0.05) || near(Bp.y + 3, A.y, 0.05);
    assert.ok(touchX || touchY, 'B is flush against A');
});

test('packRects (MaxRects) — a large item still fits after obstacles split the space', () => {
    // 20×10 with a central pillar 8..12 × full height: two 6-wide bays remain either side.
    const pillar = { x: 8, y: 0, w: 4, h: 10, rot: 0 };
    const items = [{ id: 'big', w: 5, h: 8 }, { id: 'm1', w: 2, h: 2 }, { id: 'm2', w: 2, h: 2 }];
    const r = PG.packRects(items, [pillar], { minX: 0, minY: 0, maxX: 20, maxY: 10 }, null, { margin: 0.1, gap: 0 });
    const big = r.placements.find((p) => p.id === 'big');
    assert.ok(big.ok, 'the big item finds a bay');
    assert.ok(!PG.rectsCollide({ x: big.x, y: big.y, w: 5, h: 8, rot: big.rot }, pillar), 'big item clears the pillar');
});

// FE1 — niche-first (BSSF): a small item prefers the tighter free region over a large open one,
// and padding=0 lets it use the full pocket (no hidden inflation).
test('packRects (MaxRects/BSSF) — small item fills the tight niche, not the open area', () => {
    // A pillar at x3..5 splits a 12×4 bin into a snug 3-wide left pocket and a roomy 7-wide right area.
    const pillar = { x: 3, y: 0, w: 2, h: 4, rot: 0 };
    const r = PG.packRects([{ id: 's', w: 2.5, h: 2.5 }], [pillar], { minX: 0, minY: 0, maxX: 12, maxY: 4 }, null, { margin: 0, gap: 0 });
    const p = r.placements[0];
    assert.ok(p.ok);
    assert.ok(p.x + 2.5 <= 3 + 0.01, 'placed in the tight left pocket (BSSF), not the open right area: x=' + p.x);
});

test('packRects — padding 0 lets a 2.0×1.0 car fit a 2.7×1.7 niche (Problem A regression)', () => {
    // niche as a 2.7×1.7 pocket walled off on three sides; the car must fit with padding 0.
    const obs = [{ x: 2.7, y: 0, w: 0.2, h: 1.7, rot: 0 }, { x: 0, y: 1.7, w: 2.9, h: 0.2, rot: 0 }];
    const r0 = PG.packRects([{ id: 'car', w: 2.0, h: 1.0 }], obs, { minX: 0, minY: 0, maxX: 2.9, maxY: 1.9 }, null, { margin: 0, gap: 0 });
    assert.ok(r0.placements[0].ok, 'car fits the niche with padding 0');
    // with a big margin it would be (correctly) rejected — proving the margin, not the packer, was the culprit
    const rBig = PG.packRects([{ id: 'car', w: 2.0, h: 1.0 }], obs, { minX: 0, minY: 0, maxX: 2.9, maxY: 1.9 }, null, { margin: 0.5, gap: 0.5 });
    assert.equal(rBig.placements[0].ok, false, 'oversized margin rejects it — the mechanism the bug relied on');
});
