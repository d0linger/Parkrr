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
            if (!Number.isFinite(n)) continue; // skip a malformed group value → never emit NaN/Infinity coords
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
        // Routing constraint: when a driveway/gate exists and blocking isn't allowed, accept a candidate
        // only if EVERY vehicle (the candidate + all already placed) still reaches a target (no verparken).
        const routeOn = !o.allowBlocking && o.gates && o.gates.length;
        const routeObs = o.routeObstacles || obstacles || [], routeTol = (o.route && o.route.tol > 0) ? o.route.tol : 0.15, routeCell = o.route && o.route.cell;
        const placedBlocks = [], pre = o.preplaced || []; // pre = vehicles already on the floor (other phases) that must keep exits + act as route obstacles
        const canAllExit = (cand) => {
            if (!routeOn) return true;
            // Check the CANDIDATE first — it fails most often, and rejecting it early skips the O(n)
            // re-check of everything already parked.
            const all = [cand].concat(pre, placedBlocks);
            const exitTol = gap + 0.5; // a car within (gap + 0.5 m) of a driveway exits directly — comb stays valid
            for (const v of all) if (!hasExitPath(v, routeObs.concat(all.filter((x) => x !== v)), o.gates, bounds, { cell: routeCell, clearance: Math.min(v.w, v.h) / 2 + routeTol, exitTol })) return false;
            return true;
        };
        // Free set = shrunk bounds minus every obstacle's AABB (conservative: never offers occupied space).
        const B = { x: bounds.minX + m, y: bounds.minY + m, w: (bounds.maxX - m) - (bounds.minX + m), h: (bounds.maxY - m) - (bounds.minY + m) };
        let free = (B.w > _EPS && B.h > _EPS) ? [B] : [];
        for (const ob of obstacles || []) free = _carve(free, _aabb(ob));

        // Longest edge first, then area: the long vehicles (Camper, Boote) claim the walls before the
        // shorter PKWs fill in, which keeps long uninterrupted perimeter runs available for them.
        const order = items.map((it, i) => ({ it, i })).sort((a, b) =>
            Math.max(b.it.w, b.it.h) - Math.max(a.it.w, a.it.h) || (b.it.w * b.it.h) - (a.it.w * a.it.h) || a.i - b.i);
        // scorePlacement — higher is better. Density (BSSF: small leftover short side) + a gentle
        // top-left bias, PLUS a perpendicular-parking (Kamm) bonus when the candidate sits near a
        // driveway: reward the orientation whose NARROW side faces the driveway and hugging it, both
        // decaying with distance — so vehicles line up perpendicular to the Fahrstraße (Bild 2) instead
        // of the first-fit mix. No driveways ⇒ pure density/hug (unchanged behaviour).
        const driveways = o.driveways || [];
        const scoreOf = (cand, F) => {
            const fa = _aabb(cand), ww = fa.w, hh = fa.h;
            let s = -0.3 * Math.min(F.w - ww, F.h - hh) - 0.002 * (fa.x + fa.y);
            // PERIMETER-FIRST (wall hugging): reward docking onto an outer wall, decaying inward, so the
            // room fills from its edges instead of growing islands mid-floor that fragment the free
            // rectangles and cut exit corridors. Weaker than the comb bonus, so a driveway row still
            // forms — but among equally good comb slots the wall-hugging one wins.
            const dEdge = Math.max(0, Math.min(fa.x - (bounds.minX + m), fa.y - (bounds.minY + m),
                (bounds.maxX - m) - (fa.x + ww), (bounds.maxY - m) - (fa.y + hh)));
            s += 2.5 / (1 + dEdge);
            if (driveways.length) {
                let bd = Infinity, ax = 'h';
                for (const d of driveways) { const a = _aabb(d);
                    const dx = Math.max(a.x - (fa.x + ww), fa.x - (a.x + a.w), 0), dy = Math.max(a.y - (fa.y + hh), fa.y - (a.y + a.h), 0), dist = Math.hypot(dx, dy);
                    if (dist < bd) { bd = dist; ax = a.w >= a.h ? 'h' : 'v'; } // driveway axis: wider ⇒ horizontal
                }
                if (bd < 3) { const perp = ax === 'h' ? (hh >= ww) : (ww >= hh); s += ((perp ? 4 : 0) + 1.2) / (1 + bd); }
            }
            return s;
        };
        // ---- BAY (parking-row) pass ----------------------------------------------------------------
        // A bay is one row along a wall. Its orientation is decided ONCE (perpendicular to that wall =
        // the dense comb/Kamm pattern) and every vehicle in it is laid out by explicit cursor
        // arithmetic — nextCursor = cursor + footprint + gap — so a row is flush and uniform BY
        // CONSTRUCTION instead of emerging from competing score terms (which produced mixed rotations
        // and scattered rows). An obstacle in the row is skipped by advancing the cursor, so a bay
        // survives a column or a driveway crossing the wall. Anything a bay can't take falls through to
        // the MaxRects free-rect placement below, so odd shapes and inner space still get filled.
        const r2 = (v) => Math.round(v * 100) / 100;
        const clearOfPlaced = (cand) => {
            const probe = gap > 0 ? _quad({ x: cand.x - gap, y: cand.y - gap, w: cand.w + 2 * gap, h: cand.h + 2 * gap, rot: cand.rot }) : _quad(cand);
            return !placedBlocks.some((pb) => _sat(probe, _quad(pb)));
        };
        const bays = ['top', 'bottom', 'left', 'right'].map((edge) => ({
            edge, cursor: (edge === 'top' || edge === 'bottom') ? bounds.minX + m : bounds.minY + m, depth: null,
        }));
        const bayPlace = (bay, it) => {
            const horiz = bay.edge === 'top' || bay.edge === 'bottom';
            // Perpendicular to the wall: the vehicle's LONG side runs across the row, the narrow side
            // sits on the wall, which is what fits the most vehicles per metre of wall.
            const rot = horiz ? (it.w >= it.h ? 90 : 0) : (it.w >= it.h ? 0 : 90);
            const fw = rot === 90 ? it.h : it.w, fh = rot === 90 ? it.w : it.h;
            const loX = bounds.minX + m, hiX = bounds.maxX - m, loY = bounds.minY + m, hiY = bounds.maxY - m;
            if (fw > hiX - loX + _EPS || fh > hiY - loY + _EPS) return null; // does not fit the hall at all
            const limit = horiz ? hiX : hiY, span = horiz ? fw : fh;
            // `bounds` is the floor bounding box, which INCLUDES the wall footprint, so the row line is
            // not at the bounding box edge. Probe inward from the wall until the first free depth — that
            // is the inner wall face — and then FIX that depth for the whole bay, so the row stays
            // straight and every vehicle is pushed fully back against the wall. The space that stays
            // free in front of the row is the manoeuvring corridor to the Fahrstraße.
            // Probe only a short way in: the inner wall face is within a wall thickness or two. Searching
            // deeper would stop being "wall hugging" (and costs a lot, since each step runs the checks).
            const depthMax = Math.min(1.5, horiz ? (hiY - loY - fh) : (hiX - loX - fw));
            const at = (c, d) => { globalThis.__at=(globalThis.__at||0)+1;
                const ax = horiz ? c : (bay.edge === 'left' ? loX + d : hiX - fw - d);
                const ay = horiz ? (bay.edge === 'top' ? loY + d : hiY - fh - d) : c;
                if (ax < loX - _EPS || ay < loY - _EPS || ax + fw > hiX + _EPS || ay + fh > hiY + _EPS) return null;
                // anchor→block offset, same convention as the MaxRects path: the FOOTPRINT's top-left
                // must land on (ax, ay), so the block origin shifts by (footprint − item)/2.
                const cand = { x: r2(ax + (fw - it.w) / 2), y: r2(ay + (fh - it.h) / 2), w: it.w, h: it.h, rot };
                // feasible() only covers floor + obstacles; the free-rect carving that normally keeps
                // vehicles apart is bypassed here, so check the placed vehicles explicitly (plus gap).
                return (feasible(cand) && clearOfPlaced(cand) && canAllExit(cand)) ? cand : null;
            };
            for (let c = bay.cursor; c + span <= limit + _EPS; c += 0.25) {
                if (bay.depth == null) { // establish the row line once: first free depth off the wall
                    for (let d = 0; d <= depthMax + _EPS; d += 0.1) {
                        const cand = at(c, d);
                        if (cand) return { cand, next: c + span + gap, depth: d };
                    }
                } else {
                    const cand = at(c, bay.depth);
                    if (cand) return { cand, next: c + span + gap, depth: bay.depth };
                }
            }
            return null;
        };
        const placements = []; let placed = 0, failed = 0;
        for (const { it } of order) {
            let bayHit = null;
            for (const bay of bays) { const r = bayPlace(bay, it); if (r) { bay.cursor = r.next; bay.depth = r.depth; bayHit = r.cand; break; } }
            if (bayHit) {
                placements.push({ id: it.id, x: bayHit.x, y: bayHit.y, rot: bayHit.rot, ok: true });
                placedBlocks.push({ id: it.id, x: bayHit.x, y: bayHit.y, w: it.w, h: it.h, rot: bayHit.rot });
                const fpb = _aabb(bayHit); free = _carve(free, { x: fpb.x - gap, y: fpb.y - gap, w: fpb.w + 2 * gap, h: fpb.h + 2 * gap });
                placed++; continue;
            }
            const oris = Math.abs(it.w - it.h) < 1e-3 ? [0] : [0, 90]; // 180°/270° share a rectangle's footprint
            const cands = [];
            for (const rot of oris) {
                const ww = rot === 90 ? it.h : it.w, hh = rot === 90 ? it.w : it.h;      // footprint (AABB) dims
                const off = { x: (ww - it.w) / 2, y: (hh - it.h) / 2 };                   // anchor→block: keep the footprint's top-left on the anchor
                for (const F of free) {
                    if (F.w + _EPS < ww || F.h + _EPS < hh) continue;
                    // Four corner anchors of F → hug whichever wall/neighbour bounds this rect (never floats).
                    for (const [ax, ay] of [[F.x, F.y], [F.x + F.w - ww, F.y], [F.x, F.y + F.h - hh], [F.x + F.w - ww, F.y + F.h - hh]]) {
                        const cand = { x: Math.round((ax + off.x) * 100) / 100, y: Math.round((ay + off.y) * 100) / 100, w: it.w, h: it.h, rot };
                        if (!feasible(cand)) continue;
                        cands.push({ cand, s: scoreOf(cand, F) });
                    }
                }
            }
            cands.sort((a, b) => b.s - a.s);
            let best = null;
            for (const c of cands) if (canAllExit(c.cand)) { best = c.cand; break; } // best-scoring that keeps every exit path
            if (best) { placements.push({ id: it.id, x: best.x, y: best.y, rot: best.rot, ok: true }); placedBlocks.push({ id: it.id, x: best.x, y: best.y, w: it.w, h: it.h, rot: best.rot });
                const fp = _aabb(best); free = _carve(free, { x: fp.x - gap, y: fp.y - gap, w: fp.w + 2 * gap, h: fp.h + 2 * gap }); placed++; }
            else { placements.push({ id: it.id, ok: false }); failed++; }
        }
        return { placements, placed, failed };
    }

    // hasExitPath(foot, obstacles, targets, bounds, opts?) → can a vehicle at `foot` reach a driveway/
    // gate (`targets`)? Two-stage check, then a BFS fallback:
    //   1) DIRECT (1-step): the footprint already touches a target (within exitTol) → drives straight out.
    //   2) CORRIDOR (multi-step): sweep the footprint's own box along an axis toward a target. If that
    //      corridor is clear of PLACED OBJECTS (other vehicles, walls, columns), the vehicle can drive
    //      over the free parking area onto the driveway. Empty parking space is drivable — it is not an
    //      obstacle — which is what lets a car parked at an outer wall still reach the aisle.
    //   3) Fallback: configuration-space grid BFS (obstacles dilated by `clearance`) for paths that need
    //      to go around a corner. Only ever ADDS reachability, never removes it.
    // No targets ⇒ true (no routing constraint defined). Cost is bounded: stages 1–2 are O(targets ×
    // obstacles) and the BFS grid is capped at ~2500 cells.
    function hasExitPath(foot, obstacles, targets, bounds, opts) { globalThis.__hep=(globalThis.__hep||0)+1;
        if (!targets || !targets.length) return true;
        const o = opts || {};
        const fa = _aabb(foot);
        // BFS fallback clearance defaults to the vehicle's half short side, so the fallback models the
        // real body (a point-robot BFS would let a 2 m car through a 1.5 m gap).
        const clr = o.clearance != null ? o.clearance : Math.min(fa.w, fa.h) / 2;
        const obsA = (obstacles || []).map(_aabb);
        const hits = (c) => obsA.some((a) => a.x < c.x + c.w - _EPS && a.x + a.w > c.x + _EPS && a.y < c.y + c.h - _EPS && a.y + a.h > c.y + _EPS);
        // Stage 1 — DIRECT contact with a driveway/gate.
        const exitTol = o.exitTol != null ? o.exitTol : 0.4;
        for (const t of targets) { const a = _aabb(t);
            const dx = Math.max(a.x - (fa.x + fa.w), fa.x - (a.x + a.w), 0), dy = Math.max(a.y - (fa.y + fa.h), fa.y - (a.y + a.h), 0);
            if (Math.hypot(dx, dy) <= exitTol) return true;
        }
        // Stage 2 — straight manoeuvring CORRIDOR to a target, across free (unoccupied) parking area.
        for (const t of targets) {
            const a = _aabb(t);
            // Moving right/left: the corridor keeps the vehicle's y-span, so the target must cover it.
            if (a.y <= fa.y + _EPS && a.y + a.h >= fa.y + fa.h - _EPS) {
                if (a.x >= fa.x + fa.w && !hits({ x: fa.x + fa.w, y: fa.y, w: a.x - (fa.x + fa.w), h: fa.h })) return true;
                if (a.x + a.w <= fa.x && !hits({ x: a.x + a.w, y: fa.y, w: fa.x - (a.x + a.w), h: fa.h })) return true;
            }
            // Moving down/up: the corridor keeps the vehicle's x-span, so the target must cover it.
            if (a.x <= fa.x + _EPS && a.x + a.w >= fa.x + fa.w - _EPS) {
                if (a.y >= fa.y + fa.h && !hits({ x: fa.x, y: fa.y + fa.h, w: fa.w, h: a.y - (fa.y + fa.h) })) return true;
                if (a.y + a.h <= fa.y && !hits({ x: fa.x, y: a.y + a.h, w: fa.w, h: fa.y - (a.y + a.h) })) return true;
            }
        }
        // Stage 2b — L-SHAPED corridor: drive along one axis to an elbow, then turn 90° onto the target.
        // This is what a vehicle in a room CORNER does: first run parallel to the wall, then turn onto
        // the Fahrstraße. Elbow candidates are sampled across the target's span (bounded), and both legs
        // must be clear of placed objects; free parking area stays drivable.
        const sample = (lo, hi, n) => { const out = []; if (hi < lo) return out; const step = (hi - lo) / Math.max(1, n - 1);
            for (let i = 0; i < n; i++) out.push(lo + step * i); return out; };
        for (const t of targets) {
            const a = _aabb(t);
            // Final approach VERTICAL: slide horizontally to x', then drive up/down onto the target.
            if (a.w + _EPS >= fa.w) {
                for (const x2 of sample(a.x, a.x + a.w - fa.w, 24)) {
                    const legH = { x: Math.min(fa.x, x2), y: fa.y, w: Math.abs(x2 - fa.x) + fa.w, h: fa.h };
                    if (hits(legH)) continue;
                    if (a.y >= fa.y + fa.h && !hits({ x: x2, y: fa.y + fa.h, w: fa.w, h: a.y - (fa.y + fa.h) })) return true;
                    if (a.y + a.h <= fa.y && !hits({ x: x2, y: a.y + a.h, w: fa.w, h: fa.y - (a.y + a.h) })) return true;
                }
            }
            // Final approach HORIZONTAL: slide vertically to y', then drive left/right onto the target.
            if (a.h + _EPS >= fa.h) {
                for (const y2 of sample(a.y, a.y + a.h - fa.h, 24)) {
                    const legV = { x: fa.x, y: Math.min(fa.y, y2), w: fa.w, h: Math.abs(y2 - fa.y) + fa.h };
                    if (hits(legV)) continue;
                    if (a.x >= fa.x + fa.w && !hits({ x: fa.x + fa.w, y: y2, w: a.x - (fa.x + fa.w), h: fa.h })) return true;
                    if (a.x + a.w <= fa.x && !hits({ x: a.x + a.w, y: y2, w: fa.x - (a.x + a.w), h: fa.h })) return true;
                }
            }
        }
        // Stage 3 — BFS fallback (paths that need more than one turn).
        const W = bounds.maxX - bounds.minX, H = bounds.maxY - bounds.minY;
        if (!(W > 0 && H > 0)) return true;
        globalThis.__bfs=(globalThis.__bfs||0)+1; const cell = o.cell > 0 ? o.cell : Math.max(0.2, Math.sqrt((W * H) / 10000));
        const nx = Math.max(1, Math.ceil(W / cell)), ny = Math.max(1, Math.ceil(H / cell));
        const obs = (obstacles || []).map((b) => { const a = _aabb(b); return [a.x - clr, a.y - clr, a.x + a.w + clr, a.y + a.h + clr]; });
        const tgt = targets.map((b) => { const a = _aabb(b); return [a.x, a.y, a.x + a.w, a.y + a.h]; });
        const inR = (px, py, r) => px >= r[0] && px <= r[2] && py >= r[1] && py <= r[3];
        const blocked = new Uint8Array(nx * ny), isT = new Uint8Array(nx * ny), seen = new Uint8Array(nx * ny), q = [];
        // Every cell that is not a wall, restricted zone or placed vehicle is drivable (cost 1). The
        // vehicle's OWN footprint is always a valid start — it legitimately occupies that space — so the
        // neighbours' dilation must not erase its seed cells (that produced "no path" for dense rows).
        for (let j = 0; j < ny; j++) for (let i = 0; i < nx; i++) {
            const px = bounds.minX + (i + 0.5) * cell, py = bounds.minY + (j + 0.5) * cell, k = j * nx + i;
            const own = px >= fa.x && px <= fa.x + fa.w && py >= fa.y && py <= fa.y + fa.h;
            if (!own && obs.some((r) => inR(px, py, r))) blocked[k] = 1;
            if (tgt.some((r) => inR(px, py, r))) isT[k] = 1;
            if (own) { seen[k] = 1; q.push(k); }
        }
        if (!q.length) { // degenerate footprint (smaller than a cell) → seed its centre cell
            const ci = Math.min(nx - 1, Math.max(0, Math.floor((fa.x + fa.w / 2 - bounds.minX) / cell)));
            const cj = Math.min(ny - 1, Math.max(0, Math.floor((fa.y + fa.h / 2 - bounds.minY) / cell)));
            const k = cj * nx + ci; seen[k] = 1; q.push(k);
        }
        for (let hh = 0; hh < q.length; hh++) {
            const k = q[hh]; if (isT[k]) return true; const i = k % nx, j = (k - i) / nx;
            if (i > 0 && !seen[k - 1] && !blocked[k - 1]) { seen[k - 1] = 1; q.push(k - 1); }
            if (i < nx - 1 && !seen[k + 1] && !blocked[k + 1]) { seen[k + 1] = 1; q.push(k + 1); }
            if (j > 0 && !seen[k - nx] && !blocked[k - nx]) { seen[k - nx] = 1; q.push(k - nx); }
            if (j < ny - 1 && !seen[k + nx] && !blocked[k + nx]) { seen[k + nx] = 1; q.push(k + nx); }
        }
        return false;
    }

    const PG = { ringAreaS, pointInPoly, segCross, polySelfIntersects, insetRing, roomAreas, arcPts, parseDXF, rectsCollide, packRects, hasExitPath };
    if (typeof module !== 'undefined' && module.exports) module.exports = PG;
    if (root) root.PG = PG;
})(typeof window !== 'undefined' ? window : null);
