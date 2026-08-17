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

    const PG = { ringAreaS, pointInPoly, segCross, polySelfIntersects, insetRing, roomAreas, arcPts, parseDXF };
    if (typeof module !== 'undefined' && module.exports) module.exports = PG;
    if (root) root.PG = PG;
})(typeof window !== 'undefined' ? window : null);
