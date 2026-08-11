# Design: Freies Resizen + Fahrzeug-Darstellung (Symbole/Fotos)

Stand: 2026-08-11 · Bezug: Garagenplaner/Stellplätze · Mockup: interaktives Artifact (Resize + Symbol/Foto/Rechteck).
Grundsatz: baut auf Vorhandenem auf (Maße-Endpoint, Kategorien, Fahrzeug-Fotos, Clamp/Snap/Kollision).

## Feature 1 — Resizen von allen Seiten

8 Griffe je selektiertem Block: 4 Ecken (`nw ne se sw`) + 4 Kanten (`n e s w`). Die **gegenüberliegende
Seite/Ecke bleibt verankert**; nur die gezogene Kante/Ecke bewegt sich.

- **Fahrzeug:** Ziehen bearbeitet die echten Maße. Achsen-Zuordnung im lokalen Blockrahmen:
  lokale Breite (x-Ausdehnung) → `length_m`, lokale Höhe (y) → `width_m`. Der Block bleibt
  maßstabsgetreu; ein **Maße-Chip** („L×B m") zeigt live mit. `height_m/weight_t` unberührt.
  **Persistenz beim Loslassen** über den vorhandenen `PUT /vehicles/:id/dimensions` (ein Aufruf).
- **Ausgenommene:** Ziehen ändert `w/h` frei (in `hall.geometry.excl`), Persistenz wie bisher (`commitGeom`).
- **Rotationsbewusst:** Resize rechnet im **lokalen** Blockrahmen. Verfahren:
  1. Ankerpunkt (Gegenecke bzw. Gegenkanten-Mitte) in lokalen Koordinaten bestimmen (konstant).
  2. Anker-**Weltposition** aus dem aktuellen Block berechnen: `world = center + R(rot)·(local − (w/2,h/2))`.
  3. Zeiger → lokal transformieren (`R(−rot)`), bewegte Kante = lokale Zeiger-Koordinate, auf Min geklemmt.
  4. Neue `w/h`; `b.x,b.y` so lösen, dass der Anker **weltfest** bleibt:
     `center = world − R(rot)·(anchorLocalNeu − (w/2,h/2))`, dann `b.x=center.x−w/2`, `b.y=center.y−h/2`.
- **Constraints:** Min 0,5 m je Achse; Raster-/Rand-Anrasten der bewegten Kante (bestehendes `snapPos`
  auf die resultierende Position); Kollision/außerhalb → roter Rahmen, Rücksprung auf Ausgangsmaße beim Loslassen.
- **Ecke vs. Kante:** Eckgriff ändert beide Achsen (Gegenecke fest), Kantengriff eine Achse (Gegenkante fest).

**Validierung:** Unit-Tests (Node) für rot ∈ {0,45,90} × 8 Griffe: (a) Ankerpunkt bleibt weltfest (±1e-6),
(b) Zielmaß stimmt, (c) Min-Clamp greift. GUI-Abnahme durch Tester.

## Feature 2 — Fahrzeuge als Symbol / Foto / Rechteck

**Fallback-Kette** je Fahrzeug: eigenes Bild → Foto → Kategorie-Symbol → Rechteck. Status bleibt in
**jedem** Modus über Farbe **und** ●◑⇄-Badge erkennbar (a11y, nie nur Farbe).

- **Kategorie-Symbole:** SVG-Bibliothek (Draufsicht), 11 Kategorien — PKW, Motorrad, Transporter,
  Wohnmobil, Wohnwagen, Anhänger, Boot/Trailer, Traktor, Ladewagen, Rückewagen, Kipper. Getönt in
  Status-Farbe, maßstäblich im Block. Mapping über Kategoriename (`CATSYM`), Default = PKW.
- **Ansichts-Umschalter** in der Toolbar (Symbol / Foto / Rechteck). Auswahl **client-seitig** gemerkt
  (`localStorage: gp.render`). Kein Backend für den Umschalter.
- **Foto-Modus:** vorhandenes Fahrzeug-Foto als Blockbild (dunkler Scrim + Label lesbar, Status-Ring).
  Benötigt Foto-Referenz im Plan-Query (Backend, Phase 3).
- **Pro-Fahrzeug-Override:** `vehicles.planner_symbol` (nullable) hält entweder einen
  Built-in-Symbol-Key **oder** `custom:<id>` für ein hochgeladenes Icon (Tabelle
  `planner_icons`, Migration 042). Kein separates `planner_image`-Feld — Bilder laufen
  ebenfalls über `planner_symbol=custom:<id>`. Überschreibt die Ansicht nur für dieses Fahrzeug.
- **Declutter:** sehr kleiner Block → Symbol vereinfacht / nur Farbe (analog Label-Declutter heute).

## Punkt 1 — Breite/Tiefe = Info

Die Stepper regenerieren heute via `gpShape` den Grundriss und **überschreiben** Handzeichnungen.
Neu: **Breite/Tiefe werden ein Live-Info-Readout** = Bounding-Box des tatsächlichen `P.floor`-Polygons
(aktualisiert beim Kantenziehen). **Nicht editierbar.** Exakte Größe kommt aus **Form bearbeiten**
(Kante anklicken → Meter-Feld, existiert). `P.Wm/P.Hm` bleiben intern als Canvas-Grenze; abgeleitet aus
der bbox. Preset-Picker (Rechteck/L/…) bleibt und füllt beim Wählen die aktuelle Fläche/Default.

## Punkt 2 — Auto-Match + neue Inputs

Automatisch aus der Fahrzeug-Akte (nichts doppelt pflegen): Kategorie → Symbol, L×B → Größe,
Höhe → Tor-Check, Gewicht → Bodenlast, Foto → Bild, Kennzeichen/Name → Label.

Neue Inputs (sparsam), alle auto-gematcht:
- **Kategorie-Default: Symbol + Standard-Footprint** — Gefährt ohne Maße bekommt typische Größe/Symbol
  statt generisch 4,5×2 (`CATDEF` Mapping; Frontend, Phase 2).
- **Ladebedarf (Strom) — Häkchen am Fahrzeug** → ⚡-Badge am Block (Backend-Feld `needs_power`, Migration; Phase später).
- **Gespann/Zugfahrzeug, Wartung-fällig, Fahrzeugfarbe** — spätere, optionale Phasen.

## Reihenfolge / Wellen
1. **Feature 1 — 8-Seiten-Resize** (Frontend, vorhandene Endpoints; unit-getestet).
2. **Feature 2a — Symbole + Umschalter + Fallback (Rechteck/Symbol) + Kategorie-Defaults** (Frontend).
3. **Punkt 1 — Breite/Tiefe info-only** (Frontend).
4. **Feature 2b — Foto im Plan-Query + Pro-Fahrzeug-Override** (Backend + UI).
5. **Punkt 2 — Ladebedarf-Flag + ⚡-Badge** (Backend + UI).

## Randfälle
- Resize eines Fahrzeugs ändert echte Maße → Undo/Redo greift; ein `/dimensions`-Write beim Loslassen.
- Foto ohne Draufsicht → Scrim + Label; Status als Ring/Badge.
- Sehr kleiner Block → Symbol/Beschriftung ausdünnen wie heute.
- Farbblind: ●◑⇄ bleibt in allen Modi.
- Kein Foto/Override vorhanden → Fallback greift lautlos (Symbol → Rechteck).
