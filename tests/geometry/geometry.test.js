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
    for (const p of r.placements) { assert.ok(p.x >= 0 && p.y >= 0 && p.x + 4 <= 20 + 1e-9 && p.y + 4 <= 20 + 1e-9, 'placement in bounds: ' + JSON.stringify(p)); } // items are 4×4 squares (rot 0)
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
// A placement's block {x,y} is the UNROTATED rect origin; what is visually placed is its footprint
// AABB, so these assertions are made on the footprint (rotation shifts the block origin by design).
const foot = (p, w, h) => { const W = p.rot === 90 ? h : w, H = p.rot === 90 ? w : h; return { x: p.x + (w - W) / 2, y: p.y + (h - H) / 2, w: W, h: H }; };

test('packRects — first item hugs the margin corner, does not float', () => {
    const r = PG.packRects([{ id: 'a', w: 3, h: 2 }], [], { minX: 0, minY: 0, maxX: 20, maxY: 20 }, null, { margin: 0.2, gap: 0 });
    const p = r.placements[0];
    assert.ok(p.ok);
    const f = foot(p, 3, 2);
    assert.ok(near(f.x, 0.2, 0.01) && near(f.y, 0.2, 0.01), 'footprint flush to the margin corner, not mid-room: ' + f.x + ',' + f.y);
});

test('packRects — second item sits flush against the first, not on its coordinates', () => {
    const items = [{ id: 'a', w: 4, h: 3 }, { id: 'b', w: 4, h: 3 }];
    const r = PG.packRects(items, [], { minX: 0, minY: 0, maxX: 20, maxY: 20 }, null, { margin: 0, gap: 0 });
    const A = r.placements.find((p) => p.id === 'a'), Bp = r.placements.find((p) => p.id === 'b');
    assert.ok(A.ok && Bp.ok);
    assert.ok(!(near(A.x, Bp.x) && near(A.y, Bp.y)), 'B not stacked on A\'s coordinates');
    assert.ok(!PG.rectsCollide({ x: A.x, y: A.y, w: 4, h: 3, rot: A.rot }, { x: Bp.x, y: Bp.y, w: 4, h: 3, rot: Bp.rot }), 'no overlap');
    // flush: their footprints share an edge (touching within a couple cm) on some axis
    const fa = foot(A, 4, 3), fb = foot(Bp, 4, 3);
    const touchX = near(fa.x + fa.w, fb.x, 0.05) || near(fb.x + fb.w, fa.x, 0.05);
    const touchY = near(fa.y + fa.h, fb.y, 0.05) || near(fb.y + fb.h, fa.y, 0.05);
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

// FE3 — a malformed group value must not leak NaN/Infinity into the parsed polylines.
test('parseDXF — malformed coordinate is skipped, output has no NaN', () => {
    const r = PG.parseDXF('0\nSECTION\n2\nENTITIES\n0\nLINE\n10\n1\n20\nBAD\n11\n4\n21\n6\n0\nLINE\n10\n0\n20\n0\n11\n2\n21\n3\n0\nENDSEC\n0\nEOF\n');
    for (const pl of r.polylines) for (const pt of pl) assert.ok(Number.isFinite(pt[0]) && Number.isFinite(pt[1]), 'no NaN/Inf coord: ' + pt);
    for (const k of ['minX', 'minY', 'maxX', 'maxY']) assert.ok(Number.isFinite(r.bbox[k]), 'finite bbox');
});

// FE-Routing — exit-path reachability (Auspark-Logik) + packRects allowBlocking integration.
test('hasExitPath — no target means no routing constraint', () => {
    assert.equal(PG.hasExitPath({ x: 5, y: 5, w: 2, h: 1, rot: 0 }, [], [], { minX: 0, minY: 0, maxX: 10, maxY: 10 }, {}), true);
});

test('hasExitPath — open floor reaches a driveway strip', () => {
    const gate = { x: 0, y: 9, w: 10, h: 1, rot: 0 };
    assert.equal(PG.hasExitPath({ x: 1, y: 1, w: 2, h: 1, rot: 0 }, [], [gate], { minX: 0, minY: 0, maxX: 10, maxY: 10 }, { clearance: 0.6, cell: 0.25 }), true);
});

test('hasExitPath — a boxed-in vehicle cannot reach the gate', () => {
    const walls = [{ x: 3.5, y: 3.5, w: 3, h: 0.4, rot: 0 }, { x: 3.5, y: 6.1, w: 3, h: 0.4, rot: 0 }, { x: 3.5, y: 3.5, w: 0.4, h: 3, rot: 0 }, { x: 6.1, y: 3.5, w: 0.4, h: 3, rot: 0 }];
    const gate = { x: 0, y: 9, w: 10, h: 1, rot: 0 };
    assert.equal(PG.hasExitPath({ x: 4, y: 4, w: 2, h: 2, rot: 0 }, walls, [gate], { minX: 0, minY: 0, maxX: 10, maxY: 10 }, { clearance: 0.3, cell: 0.2 }), false);
});

test('hasExitPath — a gap only passes a vehicle that actually fits through it', () => {
    // The corridor check measures the vehicle's REAL footprint (not a dilated disk), so the question is
    // simply: does the body fit through the 1.5 m gap between the two walls?
    const gate = { x: 8, y: 0, w: 2, h: 10, rot: 0 };
    const walls = [{ x: 5, y: 0, w: 0.4, h: 4.25, rot: 0 }, { x: 5, y: 5.75, w: 0.4, h: 4.25, rot: 0 }]; // gap y4.25..5.75 = 1.5 m
    const bounds = { minX: 0, minY: 0, maxX: 10, maxY: 10 };
    assert.equal(PG.hasExitPath({ x: 1, y: 1, w: 1, h: 1, rot: 0 }, walls, [gate], bounds, { cell: 0.25 }), true, '1 m body fits the 1.5 m gap');
    assert.equal(PG.hasExitPath({ x: 1, y: 1, w: 2, h: 2, rot: 0 }, walls, [gate], bounds, { cell: 0.25 }), false, '2 m body cannot pass a 1.5 m gap');
});

test('packRects routing — allowBlocking gates a "verparken" placement', () => {
    // A dead-end corridor only one car wide: the aisle is at the top, so a second car can only go
    // BEHIND the first and would be boxed in. Exactly the case allowBlocking is meant to decide.
    const bounds = { minX: 0, minY: 0, maxX: 2.5, maxY: 10 };
    const lane = { x: 0, y: 0, w: 2.5, h: 1, rot: 0 }; // driveway across the top; also a no-park obstacle
    const items = [{ id: 'a', w: 4, h: 2 }, { id: 'b', w: 4, h: 2 }];
    const opts = { margin: 0, gap: 0, gates: [lane], routeObstacles: [], route: { cell: 0.25 } };
    const strict = PG.packRects(items, [lane], bounds, null, Object.assign({ allowBlocking: false }, opts));
    const loose = PG.packRects(items, [lane], bounds, null, Object.assign({ allowBlocking: true }, opts));
    assert.equal(loose.placed, 2, 'allowBlocking:true packs both (verparken allowed)');
    assert.equal(strict.placed, 1, 'allowBlocking:false places only the car that keeps an exit');
    assert.ok(strict.placements.some((p) => !p.ok), 'the boxed-in car is marked not-placeable');
});

// FE-Comb — perpendicular (Kamm) parking: cars near a horizontal driveway rotate tall so their
// narrow side faces the Fahrstraße, forming a dense row (Bild 2), instead of a first-fit mix.
test('packRects comb — cars near a horizontal driveway park perpendicular (tall) and line up', () => {
    const bounds = { minX: 0, minY: 0, maxX: 12, maxY: 8 };
    const driveway = { x: 0, y: 6, w: 12, h: 2, rot: 0 }; // Fahrstraße along the bottom (horizontal)
    const items = [{ id: 'a', w: 4, h: 2 }, { id: 'b', w: 4, h: 2 }, { id: 'c', w: 4, h: 2 }];
    const r = PG.packRects(items, [driveway], bounds, null, { margin: 0, gap: 0, driveways: [driveway] });
    const ok = r.placements.filter((p) => p.ok);
    assert.ok(ok.length >= 2, 'cars placed above the driveway');
    for (const p of ok) assert.equal(p.rot, 90, 'car is perpendicular (tall) to the horizontal driveway, not laid across it');
    // and they sit in the same row (all share the bottom edge, flush to the driveway)
    // Footprint bottom via the shared helper: the block origin is NOT the footprint origin for a
    // rotated item, so `p.y + 4` was off by 1 here (it happened not to matter, because the assertion
    // only compares the values to each other — a constant offset cancels).
    const ys = ok.map((p) => { const f = foot(p, 4, 2); return Math.round((f.y + f.h) * 10) / 10; });
    assert.ok(ys.every((y) => Math.abs(y - ys[0]) < 0.3), 'cars form one row along the driveway');
});

// FE-Routing — 1-step exit: a vehicle flush to the driveway exits directly (no lateral clearance),
// so the comb stays valid and allowBlocking:false yields the same layout as true.
test('hasExitPath 1-step — car flush to the driveway exits directly despite tight neighbours', () => {
    const gate = { x: 0, y: 5, w: 10, h: 1, rot: 0 };
    const foot = { x: 4, y: 1, w: 2, h: 4, rot: 0 }; // bottom edge at y5 touches the gate
    const nbL = { x: 2, y: 1, w: 2, h: 4, rot: 0 }, nbR = { x: 6, y: 1, w: 2, h: 4, rot: 0 }; // tight neighbours
    assert.equal(PG.hasExitPath(foot, [nbL, nbR], [gate], { minX: 0, minY: 0, maxX: 10, maxY: 6 }, { clearance: 1.0, exitTol: 0.4 }), true);
});

test('packRects — full comb against the driveway is identical for allowBlocking false and true', () => {
    const bounds = { minX: 0, minY: 0, maxX: 12, maxY: 8 };
    const driveway = { x: 0, y: 5, w: 12, h: 3, rot: 0 };
    const items = []; for (let i = 0; i < 5; i++) items.push({ id: 'c' + i, w: 4, h: 2 });
    const opts = { margin: 0, gap: 0, gates: [driveway], routeObstacles: [], driveways: [driveway] };
    const strict = PG.packRects(items, [driveway], bounds, null, Object.assign({ allowBlocking: false }, opts));
    const loose = PG.packRects(items, [driveway], bounds, null, Object.assign({ allowBlocking: true }, opts));
    assert.equal(strict.placed, loose.placed, 'no car blocks another → both modes place the same count');
    assert.ok(strict.placed >= 5, 'all cars fit the comb');
    for (const p of strict.placements) if (p.ok) assert.equal(p.rot, 90, 'perpendicular comb in strict mode too');
});

// FE-Routing — corridor (multi-step) exit: a car parked away from the aisle still exits by driving
// straight across FREE parking area; only placed objects block that corridor.
test('hasExitPath corridor — car at the outer wall reaches the aisle across free space', () => {
    const bounds = { minX: 0, minY: 0, maxX: 20, maxY: 12 };
    const lane = { x: 0, y: 9, w: 20, h: 3, rot: 0 };          // aisle along the bottom, spans the hall
    const car = { x: 2, y: 0.5, w: 2, h: 4, rot: 0 };           // at the top wall, 4.5 m of free space below
    assert.equal(PG.hasExitPath(car, [], [lane], bounds, { clearance: 1.0 }), true, 'free corridor down to the aisle');
});

test('hasExitPath corridor — blocked when another vehicle sits in the corridor', () => {
    const bounds = { minX: 0, minY: 0, maxX: 20, maxY: 12 };
    const lane = { x: 0, y: 9, w: 20, h: 3, rot: 0 };
    const car = { x: 2, y: 0.5, w: 2, h: 4, rot: 0 };
    const blocker = { x: 1.5, y: 5.5, w: 3, h: 2, rot: 0 };     // parked right in the way, wall to wall around it
    const walls = [{ x: 0, y: 5.5, w: 1.5, h: 2, rot: 0 }, { x: 4.5, y: 5.5, w: 15.5, h: 2, rot: 0 }];
    assert.equal(PG.hasExitPath(car, [blocker].concat(walls), [lane], bounds, { clearance: 1.0, cell: 0.25 }), false, 'corridor and detours blocked');
});

test('hasExitPath corridor — free parking area is drivable, not an obstacle', () => {
    const bounds = { minX: 0, minY: 0, maxX: 20, maxY: 12 };
    const lane = { x: 0, y: 10, w: 20, h: 2, rot: 0 };
    const car = { x: 8, y: 0.5, w: 2, h: 4, rot: 0 };
    // neighbours beside the corridor (not in it) must not block the straight drive down
    const nb = [{ x: 5, y: 0.5, w: 2, h: 4, rot: 0 }, { x: 11, y: 0.5, w: 2, h: 4, rot: 0 }];
    assert.equal(PG.hasExitPath(car, nb, [lane], bounds, { clearance: 1.0 }), true);
});

// FE-Routing — L-shaped exit: a vehicle in a room corner drives parallel to the wall first, then
// turns 90° onto the Fahrstraße. A straight-only check wrongly rejected these.
test('hasExitPath L-shape — corner car exits via horizontal run then turn onto the aisle', () => {
    const bounds = { minX: 0, minY: 0, maxX: 20, maxY: 12 };
    const lane = { x: 14, y: 0, w: 6, h: 12, rot: 0 };            // aisle on the RIGHT
    const car = { x: 0.5, y: 9.5, w: 3, h: 2, rot: 0 };            // bottom-LEFT corner
    // The wall must cover the car's OWN y-span (9.5–11.5), or the straight corridor of stage 2 passes
    // underneath it and stage 2b — the thing this test is named for — never runs. It stops short of the
    // top so the L is still open: slide up the free left edge, then turn right onto the aisle.
    const wall = { x: 5, y: 4, w: 1, h: 8, rot: 0 };
    assert.equal(PG.hasExitPath(car, [wall], [lane], bounds, { clearance: 1.0, cell: 0.25 }), true, 'L-path: vertical leg then turn onto the aisle');
});

test('hasExitPath L-shape — still blocked when the elbow leg is occupied', () => {
    const bounds = { minX: 0, minY: 0, maxX: 20, maxY: 12 };
    const lane = { x: 14, y: 0, w: 6, h: 12, rot: 0 };
    const car = { x: 0.5, y: 9.5, w: 3, h: 2, rot: 0 };
    const wallAll = { x: 4, y: 0, w: 1, h: 12, rot: 0 };           // full-height wall: no way through at all
    assert.equal(PG.hasExitPath(car, [wallAll], [lane], bounds, { clearance: 0.5, cell: 0.25 }), false);
});

// FE-Bay — the bay pass must follow the AISLE, not just the wall it sits on, and must not rescan a
// wall it has already exhausted (that blew the runtime up on large halls).
test('packRects bays — a VERTICAL driveway yields cars facing it (not turned along the far wall)', () => {
    const B = { minX: 0, minY: 0, maxX: 40, maxY: 25 };
    const lane = { x: 18, y: 0, w: 3, h: 25, rot: 0 }; // aisle runs top→bottom
    const walls = [{ x: 0, y: 0, w: 40, h: 0.3, rot: 0 }, { x: 0, y: 24.7, w: 40, h: 0.3, rot: 0 },
        { x: 0, y: 0, w: 0.3, h: 25, rot: 0 }, { x: 39.7, y: 0, w: 0.3, h: 25, rot: 0 }];
    const items = []; for (let i = 0; i < 12; i++) items.push({ id: 'c' + i, w: 4.6, h: 1.9 });
    const r = PG.packRects(items, walls.concat([lane]), B, null,
        { margin: 0, gap: 0, gates: [lane], routeObstacles: walls, driveways: [lane], allowBlocking: false });
    const ok = r.placements.filter((p) => p.ok);
    assert.ok(ok.length >= 10, 'most cars placed, got ' + ok.length);
    for (const p of ok) assert.equal(p.rot || 0, 0, 'long axis points at the vertical aisle (rot 0), not along the far wall');
});

test('packRects bays — a large hall packs in well under a second (no exhausted-bay rescan)', () => {
    const W = 60, H = 40;
    const B = { minX: 0, minY: 0, maxX: W, maxY: H };
    const lane = { x: 0, y: H / 2 - 2, w: W, h: 4, rot: 0 };
    const walls = [{ x: 0, y: 0, w: W, h: 0.3, rot: 0 }, { x: 0, y: H - 0.3, w: W, h: 0.3, rot: 0 },
        { x: 0, y: 0, w: 0.3, h: H, rot: 0 }, { x: W - 0.3, y: 0, w: 0.3, h: H, rot: 0 }];
    const items = []; for (let i = 0; i < 60; i++) items.push({ id: 'c' + i, w: 4.6, h: 1.9 });
    const t0 = Date.now();
    const r = PG.packRects(items, walls.concat([lane]), B, null,
        { margin: 0, gap: 0, gates: [lane], routeObstacles: walls, driveways: [lane], allowBlocking: false });
    const ms = Date.now() - t0;
    assert.equal(r.placed, 60, 'all 60 placed');
    assert.ok(ms < 1500, 'pack finished in ' + ms + ' ms (was minutes when exhausted bays were rescanned)');
});

// FE1 — one unplaceable vehicle must not disable the bay pass for everyone else.
// Closing a bay on its FIRST rejection reverted the whole hall to the scattered
// MaxRects layout as soon as a single oversized item appeared in the list.
test('packRects bays — an item too large for the hall does not break the comb', () => {
    const W = 60, H = 40;
    const B = { minX: 0, minY: 0, maxX: W, maxY: H };
    const lane = { x: 0, y: 18, w: W, h: 4, rot: 0 };
    const walls = [{ x: 0, y: 0, w: W, h: 0.3, rot: 0 }, { x: 0, y: H - 0.3, w: W, h: 0.3, rot: 0 },
        { x: 0, y: 0, w: 0.3, h: H, rot: 0 }, { x: W - 0.3, y: 0, w: 0.3, h: H, rot: 0 }];
    const opts = { margin: 0, gap: 0, gates: [lane], routeObstacles: walls, driveways: [lane], allowBlocking: false };
    const cars = () => { const a = []; for (let i = 0; i < 12; i++) a.push({ id: 'c' + i, w: 4.6, h: 1.9 }); return a; };
    const rowOf = (r) => { const ok = r.placements.filter((p) => p.ok && p.id.startsWith('c'));
        return [...new Set(ok.map((p) => Math.round((p.y + (p.rot === 90 ? (1.9 - 4.6) / 2 : 0)) * 10) / 10))]; };

    const plain = PG.packRects(cars(), walls.concat([lane]), B, null, opts);
    const withOversized = PG.packRects([{ id: 'boat', w: 70, h: 3 }].concat(cars()), walls.concat([lane]), B, null, opts);

    assert.deepEqual(rowOf(withOversized), rowOf(plain),
        'the cars must still form the same wall-hugging row(s) despite an unplaceable item');
    assert.ok(withOversized.placements.find((p) => p.id === 'boat' && !p.ok), 'the oversized item is reported unplaceable');
});

// FE-Routing — degenerate floor bounds must not be reported as "can exit". Stages 1–2b have already
// failed by the time the BFS runs, so if the grid cannot be built reachability is UNPROVEN and the
// honest answer is false; returning true there silently dropped the guaranteeExitPath contract.
test('hasExitPath — degenerate or non-finite bounds report NO exit path, not a free pass', () => {
    // The target is SMALLER than the car on both axes, which is what forces stage 3: stage 2 needs the
    // target to span the car's cross-section and stage 2b needs it at least as wide/tall as the car, so
    // both are skipped and only the BFS can answer. (Stages 1–2b never read `bounds`, so a scenario they
    // can solve would answer before the guard is ever reached and prove nothing.)
    const car = { x: 0.5, y: 0.5, w: 2, h: 4, rot: 0 };
    const tiny = { x: 16, y: 16, w: 1, h: 1, rot: 0 };
    const opts = { clearance: 0.5, cell: 0.25 };
    const good = { minX: 0, minY: 0, maxX: 20, maxY: 20 };
    assert.equal(PG.hasExitPath(car, [], [tiny], good, opts), true, 'valid bounds still route via the BFS');
    for (const [name, bounds] of [
        ['zero width', { minX: 5, minY: 0, maxX: 5, maxY: 20 }],
        ['negative extent', { minX: 10, minY: 0, maxX: 2, maxY: 20 }],
        ['NaN', { minX: 0, minY: 0, maxX: NaN, maxY: 20 }],
        ['Infinity', { minX: 0, minY: 0, maxX: Infinity, maxY: 20 }],
    ]) {
        assert.equal(PG.hasExitPath(car, [], [tiny], bounds, opts), false, name + ' must not claim an exit path');
    }
});
