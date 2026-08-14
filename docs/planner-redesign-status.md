# Garagenplaner – Redesign status (audit)

Status of the wall-based planner redesign. Legend: ✅ done · 🟡 partial/pragmatic · ⬜ open.

## Core model & mechanics
- ✅ Config-driven entity catalogue (`EXCL`: wall types, openings, zones) — new types are data, not code.
- ✅ Sandbox mechanics: drag / resize / rotate, snap-to-grid (frei · 0,1 · ¼ · ½ · 1 m).
- ✅ Decoupled rotated-SAT collision (`quad` / `satOverlap`) + realtime valid/invalid feedback.
- ✅ Opaque persistence (`hall.geometry` incl. `walls` graph) + undo/redo + autosave. No migration.
- ✅ Vehicle symbol/icon system preserved (built-in + custom planner icons).

## Wall editor
- ✅ Interactive chained wall drawing (2-click, live preview + length, close/ESC/right-click/Doppelklick).
- ✅ Node graph: drag node, split segment (Doppelklick / mid-wall Anbau), **dissolve point** (A–B–C → A–C).
- ✅ Wall types (Außen/Trag/Trenn/Brand) + material tag + **thickness parameter** (default for new walls, per-wall via popover).
- ✅ Openings (Tor/Tür/Fenster) as **clean cut-outs** and **editable objects** (select / move along wall / resize handles / delete → wall re-closes).
- ✅ Wall totals: full node-to-node length shown offset outside the wall + clear (lichte) sub-segment lengths.
- ✅ Quick-edit popover for wall (Länge + Dicke), opening (Breite), node (auflösen), zone (löschen) — all with on-object 🗑.

## Layout / measurement feedback
- ✅ "Wände sind die Grenze": floor outline auto-derived from the wall enclosure; Parkfläche m² live.
- ✅ Orthogonal (H/V) snapping with dashed guide-line; ⊾90°/Shift hard-lock.
- ✅ T-junction anchor distances + **live left/right distances on hover** when placing openings/walls on a wall.
- ✅ Auto-expanding canvas (scale is a stable base; growth no longer rescales) + **equal padding on all 4 sides**.
- 🟡 Inner vs. axis dimensions: displayed length is the **axis (centerline)** measure; thickness is a parameter and is
     drawn centered on the axis. True *inner-clear* dimension chains and one-sided (outward) wall extrusion are **not**
     implemented — the centerline+centered-thickness model is the current (standard-CAD) choice.

## Objects & zones
- ✅ Rubber-band zone creation (Fahrstraße/Wartung/Notausgang/Stellfläche): arm tool → click-drag → edit state.
- ✅ On-canvas zone delete (🗑 popover) — no side-menu detour needed.
- ✅ Vehicles **and** zones snap flush to the **inner face** of nearby axis-aligned walls.
- 🟡 Zone rotation is not exposed in the unified overlay (move + corner-resize only).

## UX
- ✅ Delete-safety: left-click empty only deselects; right-click/ESC cancel the tool / deselect — **never delete**.
- ✅ Active tool clearly highlighted (filled state) in the rail.
- ✅ Grid shrinks back to content on discard/delete/Passen (no permanently inflated grid).

## Open / nice-to-have
- ⬜ Inner-clear dimension chains + outward wall extrusion (see 🟡 above).
- ⬜ Zone rotation in the overlay.
- ⬜ Bauplan underlay for very large images (currently downscaled + size-capped to fit the geometry blob).
