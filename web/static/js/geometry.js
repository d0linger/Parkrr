/* Parkrr planner geometry — pure, dependency-free functions shared by the SPA
   (window.PG) and the node test suite (module.exports). Keeping the area/face
   maths here means it is unit-tested directly instead of buried in the app
   closure. No DOM, no app state: everything is passed in. */
(function (root) {
    'use strict';

    // signed area of a {x,y} ring (CCW positive in screen y-down)
    const ringAreaS = (pts) => { let a = 0; for (let i = 0; i < pts.length; i++) { const j = (i + 1) % pts.length; a += pts[i].x * pts[j].y - pts[j].x * pts[i].y; } return a / 2; };

    // ray-cast point in polygon
    const pointInPoly = (x, y, poly) => { let c = false; for (let i = 0, j = poly.length - 1; i < poly.length; j = i++) { const xi = poly[i].x, yi = poly[i].y, xj = poly[j].x, yj = poly[j].y; if (((yi > y) !== (yj > y)) && x < (xj - xi) * (y - yi) / (yj - yi) + xi) c = !c; } return c; };

    // do segments p1p2 and p3p4 properly cross?
    const segCross = (p1, p2, p3, p4) => { const d = (a, b, c) => (b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x); const d1 = d(p3, p4, p1), d2 = d(p3, p4, p2), d3 = d(p1, p2, p3), d4 = d(p1, p2, p4); return ((d1 > 0) !== (d2 > 0)) && ((d3 > 0) !== (d4 > 0)); };

    // simple-polygon self-intersection test (non-adjacent edge pairs)
    const polySelfIntersects = (poly) => { const k = poly.length; for (let i = 0; i < k; i++) { const a1 = poly[i], a2 = poly[(i + 1) % k]; for (let j = i + 1; j < k; j++) { if (j === i || (j + 1) % k === i || (i + 1) % k === j) continue; if (segCross(a1, a2, poly[j], poly[(j + 1) % k])) return true; } } return false; };

    // offset a CCW ring inward by per-edge distance d[i] (inward = left normal),
    // re-intersecting consecutive edges with a reflex clamp so concave corners can't spike
    function insetRing(poly, d) {
        const k = poly.length;
        const ln = poly.map((a, i) => { const b = poly[(i + 1) % k], dx = b.x - a.x, dy = b.y - a.y, L = Math.hypot(dx, dy) || 1, ux = dx / L, uy = dy / L; return { px: a.x - uy * d[i], py: a.y + ux * d[i], dx: ux, dy: uy, L }; });
        return ln.map((L2, i) => { const L1 = ln[(i - 1 + k) % k], den = L1.dx * L2.dy - L1.dy * L2.dx; if (Math.abs(den) < 1e-9) return { x: L2.px, y: L2.py };
            let t = ((L2.px - L1.px) * L2.dy - (L2.py - L1.py) * L2.dx) / den; const lim = Math.max(L1.L, L2.L); if (t > lim) t = lim; if (t < -lim) t = -lim; return { x: L1.px + L1.dx * t, y: L1.py + L1.dy * t }; });
    }

    // Planar face decomposition of a wall graph → exact interior polygon + area of every
    // enclosed room. Each bounded face (signed area > 0) is a room; each of its edges is
    // inset to the inner face by a distance derived from TOPOLOGY + the Bezug (not from a
    // fragile raster probe): a wall shared with another room is centred → inset thick/2 on
    // each side; a perimeter wall (its twin borders the exterior) is inset per Bezug —
    // inner 0 (drawn line = inner face), axis thick/2, outer thick. This makes the area
    // exact AND invariant to collinear helper points. bezug ∈ 'inner'|'axis'|'outer'.
    function roomAreas(N, E, bezug) {
        if (!E || E.length < 3) return [];
        const half = [];
        E.forEach((e, ei) => { if (!N[e.a] || !N[e.b]) return; half.push({ u: e.a, v: e.b, ei }); half.push({ u: e.b, v: e.a, ei }); });
        if (half.length < 6) return [];
        const out = N.map(() => []);
        half.forEach((h, hi) => { const a = N[h.u], b = N[h.v]; h.ang = Math.atan2(b.y - a.y, b.x - a.x); out[h.u].push(hi); });
        out.forEach((l) => l.sort((p, q) => half[p].ang - half[q].ang));
        const pos = new Map(); out.forEach((l) => l.forEach((hi, i) => pos.set(hi, i)));
        const twin = (hi) => { const h = half[hi]; for (const j of out[h.v]) if (half[j].v === h.u && half[j].ei === h.ei) return j; return -1; };
        const twinOf = half.map((_, hi) => twin(hi));
        const nextH = (hi) => { const t = twinOf[hi]; if (t < 0) return -1; const l = out[half[t].u], i = pos.get(t); return l[(i - 1 + l.length) % l.length]; };
        // 1) label every half-edge with its face; record each face's signed area.
        const faceOf = new Int32Array(half.length).fill(-1), faces = [], seen = new Uint8Array(half.length);
        for (let hi = 0; hi < half.length; hi++) {
            if (seen[hi]) continue; const cyc = []; let c = hi, g = half.length + 4;
            while (c >= 0 && !seen[c] && g-- > 0) { seen[c] = 1; cyc.push(c); faceOf[c] = faces.length; c = nextH(c); }
            const poly = cyc.map((h) => ({ x: N[half[h].u].x, y: N[half[h].u].y }));
            faces.push({ cyc, poly, signed: ringAreaS(poly) });
        }
        // 2) per half-edge inset from the node line: perimeter (twin borders exterior) → Bezug-based;
        // a shared wall (twin borders another room) → centred (th/2) by default.
        const isRoomFace = (f) => f >= 0 && faces[f] && faces[f].signed > 0.01;
        const dirH = half.map((h) => { const a = N[h.u], b = N[h.v], L = Math.hypot(b.x - a.x, b.y - a.y) || 1; return { x: (b.x - a.x) / L, y: (b.y - a.y) / L }; });
        const prevH = new Int32Array(half.length).fill(-1);
        for (let hi = 0; hi < half.length; hi++) { const n = nextH(hi); if (n >= 0) prevH[n] = hi; }
        const insetH = new Float64Array(half.length), solid = new Uint8Array(half.length);
        for (let hi = 0; hi < half.length; hi++) {
            const th = E[half[hi].ei].thick || 0.24, tw = twinOf[hi], part = tw >= 0 && isRoomFace(faceOf[tw]);
            if (part) insetH[hi] = th / 2;
            else { insetH[hi] = (bezug === 'inner' ? 0 : bezug === 'outer' ? th : th / 2); solid[hi] = 1; } // perimeter: definite
        }
        // Collinear inheritance: a partition half-edge that CONTINUES a solid (perimeter/derived) half-edge
        // along the same line — the two are consecutive around a room — adopts its inset; the twin takes
        // th − inset. So a partition prolonging a perimeter wall keeps that room flush to the node line
        // (no area handed to the neighbour room), while a partition that merely SPLITS a room
        // (perpendicular to the perimeter) has no collinear solid neighbour and stays centred.
        // Bound = worst case: a chain listed opposite to its propagation direction advances one hop per
        // pass, so a chain of N half-edges needs N passes. half.length is a safe cap; the no-change
        // break below stops as soon as a fixed point is reached (usually 1–2 passes).
        for (let pass = 0; pass < half.length; pass++) {
            let changed = false;
            for (let hi = 0; hi < half.length; hi++) {
                if (solid[hi]) continue; const tw = twinOf[hi]; if (tw < 0 || !isRoomFace(faceOf[tw])) continue; const th = E[half[hi].ei].thick || 0.24;
                for (const nb of [nextH(hi), prevH[hi]]) {
                    if (nb < 0 || !solid[nb]) continue; if (Math.abs(dirH[hi].x * dirH[nb].x + dirH[hi].y * dirH[nb].y) < 0.999) continue;
                    insetH[hi] = insetH[nb]; insetH[tw] = th - insetH[hi]; solid[hi] = 1; solid[tw] = 1; changed = true; break;
                }
            }
            if (!changed) break;
        }
        // 3) each bounded (signed > 0), simple face is a room — inset per the (possibly inherited) insets.
        const rooms = [];
        for (const face of faces) {
            if (face.signed <= 0.01 || face.poly.length < 3 || polySelfIntersects(face.poly)) continue; // unbounded / degenerate / stub
            const ring = insetRing(face.poly, face.cyc.map((hi) => insetH[hi])), area = Math.abs(ringAreaS(ring));
            let cx = 0, cy = 0; ring.forEach((p) => { cx += p.x; cy += p.y; }); cx /= ring.length; cy /= ring.length;
            rooms.push({ area, ring, cx, cy });
        }
        return rooms;
    }

    // Sample an arc/circle (degrees, CCW, DXF y-up convention) into a polyline of ~6° steps.
    function arcPts(cx, cy, r, a0, a1) {
        let s = a0, e = a1; if (e <= s) e += 360; const pts = [];
        for (let a = s; a < e - 1e-6; a += 6) { const t = a * Math.PI / 180; pts.push([cx + r * Math.cos(t), cy + r * Math.sin(t)]); }
        const te = e * Math.PI / 180; pts.push([cx + r * Math.cos(te), cy + r * Math.sin(te)]);
        return pts;
    }

    // Minimal ASCII-DXF reader (FE3): extract LINE, LWPOLYLINE, legacy POLYLINE/VERTEX,
    // CIRCLE and ARC entities as polylines ([[x,y],…]). Returns { polylines, bbox } in the
    // DXF's own units and y-up orientation; the caller rasterises + calibrates it as a
    // Bauplan underlay. Other entity types (TEXT, INSERT, HATCH, splines…) are ignored.
    function parseDXF(text) {
        const L = String(text).replace(/^﻿/, '').split(/\r\n|\r|\n/);
        const polys = [];
        let type = null, sx = null, sy = null, ex = null, ey = null, cx = null, cy = null, r = null, a0 = null, a1 = null;
        let verts = [], pendX = null, closed = false, vx = null, vy = null;
        let poly = null, polyClosed = false; // legacy POLYLINE/VERTEX accumulator
        const flush = () => {
            if (type === 'LINE') { if (sx != null && sy != null && ex != null && ey != null) polys.push([[sx, sy], [ex, ey]]); }
            else if (type === 'LWPOLYLINE') { if (verts.length > 1) { const p = verts.slice(); if (closed) p.push(p[0].slice()); polys.push(p); } }
            else if (type === 'CIRCLE') { if (cx != null && cy != null && r > 0) polys.push(arcPts(cx, cy, r, 0, 360)); }
            else if (type === 'ARC') { if (cx != null && cy != null && r > 0) polys.push(arcPts(cx, cy, r, a0 == null ? 0 : a0, a1 == null ? 360 : a1)); }
            else if (type === 'VERTEX') { if (poly && vx != null && vy != null) poly.push([vx, vy]); }
            type = null; sx = sy = ex = ey = cx = cy = r = a0 = a1 = vx = vy = null; verts = []; pendX = null; closed = false;
        };
        for (let i = 0; i + 1 < L.length; i += 2) {
            const code = parseInt(L[i].trim(), 10); if (Number.isNaN(code)) continue;
            const val = (L[i + 1] == null ? '' : L[i + 1].trim());
            if (code === 0) {
                flush();
                if (val === 'POLYLINE') { poly = []; polyClosed = false; type = 'POLYLINE'; }
                else if (val === 'SEQEND') { if (poly && poly.length > 1) { const p = poly.slice(); if (polyClosed) p.push(p[0].slice()); polys.push(p); } poly = null; type = null; }
                else if (val === 'VERTEX') { type = 'VERTEX'; }
                else if (val === 'LINE' || val === 'LWPOLYLINE' || val === 'CIRCLE' || val === 'ARC') { type = val; }
                else { type = null; }
                continue;
            }
            const n = parseFloat(val);
            switch (code) {
                case 10: if (type === 'LINE') sx = n; else if (type === 'LWPOLYLINE') pendX = n; else if (type === 'VERTEX') vx = n; else if (type === 'CIRCLE' || type === 'ARC') cx = n; break;
                case 20: if (type === 'LINE') sy = n; else if (type === 'LWPOLYLINE') { if (pendX != null) { verts.push([pendX, n]); pendX = null; } } else if (type === 'VERTEX') vy = n; else if (type === 'CIRCLE' || type === 'ARC') cy = n; break;
                case 11: if (type === 'LINE') ex = n; break;
                case 21: if (type === 'LINE') ey = n; break;
                case 40: if (type === 'CIRCLE' || type === 'ARC') r = n; break;
                case 50: if (type === 'ARC') a0 = n; break;
                case 51: if (type === 'ARC') a1 = n; break;
                case 70: if (type === 'LWPOLYLINE') closed = (n & 1) === 1; else if (type === 'POLYLINE') polyClosed = (n & 1) === 1; break;
            }
        }
        flush();
        let mnx = Infinity, mny = Infinity, mxx = -Infinity, mxy = -Infinity;
        for (const pl of polys) for (const p of pl) { if (p[0] < mnx) mnx = p[0]; if (p[0] > mxx) mxx = p[0]; if (p[1] < mny) mny = p[1]; if (p[1] > mxy) mxy = p[1]; }
        return { polylines: polys, bbox: polys.length ? { minX: mnx, minY: mny, maxX: mxx, maxY: mxy } : { minX: 0, minY: 0, maxX: 0, maxY: 0 } };
    }

    // ---- 2D bin-packing for Auto-Arrange (FE1) ----
    // Pure best-fit-decreasing rectangle packer with 90° rotation and SAT collision. All
    // rectangles use the SAME convention/epsilons as the SPA renderer ({x,y}=top-left, w×h,
    // rot° about the centre; SAT treats a ≤2 cm touch as separated; inside = 4 corners in the
    // floor polygon AND no edge crossing) so a packed placement is valid exactly like a drag.
    const _quad = (b) => { const cx = b.x + b.w / 2, cy = b.y + b.h / 2, r = (b.rot || 0) * Math.PI / 180, c = Math.cos(r), s = Math.sin(r);
        return [[b.x, b.y], [b.x + b.w, b.y], [b.x + b.w, b.y + b.h], [b.x, b.y + b.h]].map(([px, py]) => { const dx = px - cx, dy = py - cy; return [cx + dx * c - dy * s, cy + dx * s + dy * c]; }); };
    const _sat = (A, B) => { const EPS = 0.02; for (const poly of [A, B]) { for (let i = 0; i < poly.length; i++) { const [x1, y1] = poly[i], [x2, y2] = poly[(i + 1) % poly.length];
        let ax = -(y2 - y1), ay = x2 - x1; const L = Math.hypot(ax, ay) || 1; ax /= L; ay /= L; let mnA = Infinity, mxA = -Infinity, mnB = Infinity, mxB = -Infinity;
        for (const [px, py] of A) { const d = px * ax + py * ay; if (d < mnA) mnA = d; if (d > mxA) mxA = d; }
        for (const [px, py] of B) { const d = px * ax + py * ay; if (d < mnB) mnB = d; if (d > mxB) mxB = d; }
        if (Math.min(mxA, mxB) - Math.max(mnA, mnB) < EPS) return false; } } return true; };
    const _pip = (x, y, pts) => { let c = false; for (let i = 0, j = pts.length - 1; i < pts.length; j = i++) { const xi = pts[i][0], yi = pts[i][1], xj = pts[j][0], yj = pts[j][1]; if (((yi > y) !== (yj > y)) && (x < (xj - xi) * (y - yi) / (yj - yi) + xi)) c = !c; } return c; };
    const _segX = (p1, p2, p3, p4) => { const d = (a, b, c) => (b[0] - a[0]) * (c[1] - a[1]) - (b[1] - a[1]) * (c[0] - a[0]); const d1 = d(p3, p4, p1), d2 = d(p3, p4, p2), d3 = d(p1, p2, p3), d4 = d(p1, p2, p4); return ((d1 > 0) !== (d2 > 0)) && ((d3 > 0) !== (d4 > 0)); };
    const _quadInPoly = (q, floor) => { const cx = (q[0][0] + q[1][0] + q[2][0] + q[3][0]) / 4, cy = (q[0][1] + q[1][1] + q[2][1] + q[3][1]) / 4, e = 0.02;
        const qs = q.map(([x, y]) => [x + (cx > x ? e : -e), y + (cy > y ? e : -e)]);
        for (const [x, y] of qs) if (!_pip(x, y, floor)) return false;
        for (let i = 0; i < floor.length; i++) { const p1 = floor[i], p2 = floor[(i + 1) % floor.length]; for (let c = 0; c < 4; c++) if (_segX(p1, p2, qs[c], qs[(c + 1) % 4])) return false; }
        return true; };

    // True if two blocks {x,y,w,h,rot} overlap (SAT, ≤2 cm touch = separate). Public for tests/reuse.
    const rectsCollide = (a, b) => _sat(_quad(a), _quad(b));

    // ---- MaxRects free-rectangle helpers (Jylänki 2010) ----
    const _EPS = 1e-4;
    // AABB of a (possibly rotated) block → {x,y,w,h}.
    function _aabb(b) { const q = _quad(b); let mnx = Infinity, mny = Infinity, mxx = -Infinity, mxy = -Infinity; for (const [x, y] of q) { if (x < mnx) mnx = x; if (x > mxx) mxx = x; if (y < mny) mny = y; if (y > mxy) mxy = y; } return { x: mnx, y: mny, w: mxx - mnx, h: mxy - mny }; }
    const _rIntersect = (a, b) => a.x < b.x + b.w - _EPS && a.x + a.w > b.x + _EPS && a.y < b.y + b.h - _EPS && a.y + a.h > b.y + _EPS;
    const _rContains = (a, b) => a.x <= b.x + _EPS && a.y <= b.y + _EPS && a.x + a.w >= b.x + b.w - _EPS && a.y + a.h >= b.y + b.h - _EPS; // a ⊇ b
    // Drop free rects that are degenerate or fully contained in another (non-maximal).
    function _prune(list) {
        const out = [];
        for (let i = 0; i < list.length; i++) {
            if (list[i].w < _EPS || list[i].h < _EPS) continue;
            let dead = false;
            for (let j = 0; j < list.length && !dead; j++) if (i !== j && _rContains(list[j], list[i]) && !(_rContains(list[i], list[j]) && j > i)) dead = true;
            if (!dead) out.push(list[i]);
        }
        return out;
    }
    // Carve `used` out of every free rect it overlaps → up to 4 border slabs; then prune to maximal set.
    function _carve(list, used) {
        const out = [];
        for (const F of list) {
            if (!_rIntersect(F, used)) { out.push(F); continue; }
            if (used.x > F.x + _EPS) out.push({ x: F.x, y: F.y, w: used.x - F.x, h: F.h });
            if (used.x + used.w < F.x + F.w - _EPS) out.push({ x: used.x + used.w, y: F.y, w: F.x + F.w - (used.x + used.w), h: F.h });
            if (used.y > F.y + _EPS) out.push({ x: F.x, y: F.y, w: F.w, h: used.y - F.y });
            if (used.y + used.h < F.y + F.h - _EPS) out.push({ x: F.x, y: used.y + used.h, w: F.w, h: F.y + F.h - (used.y + used.h) });
        }
        return _prune(out);
    }

    // packRects(items, obstacles, bounds, floor?, opts?) → { placements:[{id,x,y,rot,ok}], placed, failed }.
    //   items     [{ id, w, h }]           vehicle footprints (metres)
    //   obstacles [{ x,y,w,h,rot }]        HARD no-go blocks: walls, columns, lanes, maintenance
    //   bounds    { minX,minY,maxX,maxY }  outer window (usually the floor bbox)
    //   floor     [[x,y],…] | null         polygon a placement must lie fully inside (handles concave/L floors)
    //   opts      { margin, gap }          wall margin, vehicle↔vehicle clearance
    // MaxRects-BSSF: free space is an explicit set of maximal rectangles, initialised to the margin-shrunk
    // bounds with every obstacle AABB carved out — so a placement can NEVER land on a wall/column/lane, and
    // never on another object's coordinates. Vehicles are placed Best-Fit-Decreasing (largest first, keeping
    // big contiguous rects for big vehicles), each tries 0°/90°, and is anchored to a free-rect CORNER
    // (Best-Short-Side-Fit) so it hugs a wall or a neighbour instead of floating mid-room. Every candidate
    // still passes an exact gate: fully inside the floor polygon AND SAT-clear of every obstacle. After a
    // placement the used rect (inflated by `gap`) is carved out and the free set re-maximalised.
    function packRects(items, obstacles, bounds, floor, opts) {
        const o = opts || {}, m = o.margin != null ? o.margin : 0.2, gap = o.gap > 0 ? o.gap : 0;
        const useFloor = floor && floor.length >= 3;
        const obsQ = (obstacles || []).map(_quad);
        const feasible = (cand) => (!useFloor || _quadInPoly(_quad(cand), floor)) && !obsQ.some((ob) => _sat(_quad(cand), ob));
        // Free set = shrunk bounds minus every obstacle's AABB (conservative: never offers occupied space).
        const B = { x: bounds.minX + m, y: bounds.minY + m, w: (bounds.maxX - m) - (bounds.minX + m), h: (bounds.maxY - m) - (bounds.minY + m) };
        let free = (B.w > _EPS && B.h > _EPS) ? [B] : [];
        for (const ob of obstacles || []) free = _carve(free, _aabb(ob));

        const order = items.map((it, i) => ({ it, i })).sort((a, b) =>
            (b.it.w * b.it.h) - (a.it.w * a.it.h) || Math.max(b.it.w, b.it.h) - Math.max(a.it.w, a.it.h) || a.i - b.i);
        const placements = []; let placed = 0, failed = 0;
        for (const { it } of order) {
            const oris = Math.abs(it.w - it.h) < 1e-3 ? [0] : [0, 90]; // 180°/270° share a rectangle's footprint
            let best = null, bestShort = Infinity, bestLong = Infinity;
            for (const rot of oris) {
                const ww = rot === 90 ? it.h : it.w, hh = rot === 90 ? it.w : it.h;      // footprint (AABB) dims
                const off = { x: (ww - it.w) / 2, y: (hh - it.h) / 2 };                   // anchor→block: keep the footprint's top-left on the anchor
                for (const F of free) {
                    if (F.w + _EPS < ww || F.h + _EPS < hh) continue;
                    const shortSide = Math.min(F.w - ww, F.h - hh), longSide = Math.max(F.w - ww, F.h - hh);
                    const better = shortSide < bestShort - _EPS || (Math.abs(shortSide - bestShort) < _EPS && longSide < bestLong - _EPS);
                    if (!better) continue; // strictly tighter fit only → first-best wins ties (deterministic)
                    // Four corner anchors of F → hug whichever wall/neighbour bounds this rect (never floats).
                    for (const [ax, ay] of [[F.x, F.y], [F.x + F.w - ww, F.y], [F.x, F.y + F.h - hh], [F.x + F.w - ww, F.y + F.h - hh]]) {
                        const cand = { x: Math.round((ax + off.x) * 100) / 100, y: Math.round((ay + off.y) * 100) / 100, w: it.w, h: it.h, rot };
                        if (!feasible(cand)) continue;
                        best = cand; bestShort = shortSide; bestLong = longSide; break; // corners of one F tie on score; first (top-left) wins
                    }
                }
            }
            if (best) { placements.push({ id: it.id, x: best.x, y: best.y, rot: best.rot, ok: true });
                const fp = _aabb(best); free = _carve(free, { x: fp.x - gap, y: fp.y - gap, w: fp.w + 2 * gap, h: fp.h + 2 * gap }); placed++; }
            else { placements.push({ id: it.id, ok: false }); failed++; }
        }
        return { placements, placed, failed };
    }

    const PG = { ringAreaS, pointInPoly, segCross, polySelfIntersects, insetRing, roomAreas, arcPts, parseDXF, rectsCollide, packRects };
    if (typeof module !== 'undefined' && module.exports) module.exports = PG;
    if (root) root.PG = PG;
})(typeof window !== 'undefined' ? window : null);
