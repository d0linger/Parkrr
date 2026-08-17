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

    // Planar face decomposition of a wall graph → exact interior polygon + area of
    // every enclosed room. Each bounded face (signed area > 0) is a room; its edges
    // are inset to the inner face by (offset·inward-normal + thick/2), honouring the
    // Bezug via `offsets` ({ox,oy} per edge, or null for axis). Returns [{area, ring, cx, cy}].
    function roomAreas(N, E, offsets) {
        if (!E || E.length < 3) return [];
        const offs = offsets || null, half = [];
        E.forEach((e, ei) => { if (!N[e.a] || !N[e.b]) return; half.push({ u: e.a, v: e.b, ei }); half.push({ u: e.b, v: e.a, ei }); });
        if (half.length < 6) return [];
        const out = N.map(() => []);
        half.forEach((h, hi) => { const a = N[h.u], b = N[h.v]; h.ang = Math.atan2(b.y - a.y, b.x - a.x); out[h.u].push(hi); });
        out.forEach((l) => l.sort((p, q) => half[p].ang - half[q].ang));
        const pos = new Map(); out.forEach((l) => l.forEach((hi, i) => pos.set(hi, i)));
        const twin = (hi) => { const h = half[hi]; for (const j of out[h.v]) if (half[j].v === h.u && half[j].ei === h.ei) return j; return -1; };
        const nextH = (hi) => { const t = twin(hi); if (t < 0) return -1; const l = out[half[t].u], i = pos.get(t); return l[(i - 1 + l.length) % l.length]; };
        const seen = new Uint8Array(half.length), rooms = [];
        for (let hi = 0; hi < half.length; hi++) {
            if (seen[hi]) continue; const cyc = []; let c = hi, g = half.length + 4;
            while (c >= 0 && !seen[c] && g-- > 0) { seen[c] = 1; cyc.push(c); c = nextH(c); }
            if (cyc.length < 3) continue;
            const poly = cyc.map((h) => ({ x: N[half[h].u].x, y: N[half[h].u].y }));
            if (ringAreaS(poly) <= 0.01 || polySelfIntersects(poly)) continue; // unbounded face (−) or degenerate/stub loop
            const k = poly.length, d = [];
            for (let i = 0; i < k; i++) { const ei = half[cyc[i]].ei, a = poly[i], b = poly[(i + 1) % k];
                const dx = b.x - a.x, dy = b.y - a.y, L = Math.hypot(dx, dy) || 1, nlx = -dy / L, nly = dx / L;
                const o = (offs && offs[ei]) || { ox: 0, oy: 0 }; d.push((o.ox * nlx + o.oy * nly) + (E[ei].thick || 0.24) / 2); }
            const ring = insetRing(poly, d), area = Math.abs(ringAreaS(ring));
            let cx = 0, cy = 0; ring.forEach((p) => { cx += p.x; cy += p.y; }); cx /= ring.length; cy /= ring.length;
            rooms.push({ area, ring, cx, cy });
        }
        return rooms;
    }

    const PG = { ringAreaS, pointInPoly, segCross, polySelfIntersects, insetRing, roomAreas };
    if (typeof module !== 'undefined' && module.exports) module.exports = PG;
    if (root) root.PG = PG;
})(typeof window !== 'undefined' ? window : null);
