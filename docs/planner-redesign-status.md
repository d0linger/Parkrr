# Garagenplaner – Abgleich mit den ursprünglichen Redesign-Anforderungen (Artifact)

Reconciliation of the **original Artifact requirements** against the current Parkrr
implementation. Sources: the sandbox artifact `stellplatz-sandbox.html`, the
integration proposal `garagenplaner-ausbau.html`, and the initial 5-point spec.

Legend: ✅ vollständig · 🟡 teilweise/pragmatisch · ⬜ offen.
Code: everything lives in `web/static/js/app.js` (`buildGP`) unless noted.

---

## A. Ursprüngliche Sandbox-Grundanforderungen (5-Punkte-Spec)

| # | Anforderung | Status | Umsetzung |
|---|---|---|---|
| A1 | Flexibles, config-getriebenes Objekt-Modell (Typen ohne Code erweiterbar) | ✅ | `EXCL`-Katalog + `walls`-Graph; neue Typen = Daten |
| A2 | Sandbox-Mechaniken: verschieben/drehen/skalieren, Snap-Grid (frei·0,1·¼·½·1 m) | ✅ | `resizeBlock`, `snapPos`, `gsnap`, Grid-Seg |
| A3 | Entkoppelte Kollisionsprüfung (rotiertes SAT) + Puffer/Clearance | 🟡 | SAT (`quad`/`satOverlap`) ✅; **Clearance-Puffer um Fahrzeuge nicht portiert** (siehe B4) |
| A4 | Realtime-Feedback (grün/gelb/rot) | ✅ | `statusOf`/`warn`/`collide`, `validVeh` |
| A5 | Refactoring-Garantie: bestehende Funktionen erhalten | ✅ | Additiv; Symbole/Icons/Undo/Autosave/Spots intakt |

## B. Integrations-Proposal (garagenplaner-ausbau.html) – 6 Capabilities

| # | Capability | Status | Umsetzung |
|---|---|---|---|
| B1 | Mauertypen mit Dicke & Material | ✅ | `wall_ext/load/part/fire`, `mat`, **Dicke als Parameter** (Default + je Wand) |
| B2 | Ketten-Wandzeichnen + Auto-Snap | ✅ | `wallClick`/`snapDraw` (Kette, Ortho-Guide, Knoten-Snap) |
| B3 | Echter Wanddurchbruch (Tor/Tür/Fenster) | ✅ | Sauberer Cut (Butt-Caps + Joints) + **eigenständige, editierbare Öffnungs-Objekte** |
| B4 | Clearance-Pufferzonen um Fahrzeuge | ⬜ | **Offen** – im Sandkasten vorhanden, nach Parkrr nicht portiert |
| B5 | Raumflächen (m² je umschlossenem Raum) | 🟡 | **Gesamt-Parkfläche** live (`computeEnclosure`); **kein** m²-Ausweis je einzelnem Raum |
| B6 | Weitere Bauteile per Config (EXCL erweitern) | ✅ | Neue Kinds rein per `EXCL`-Eintrag |
| — | Additiv & ohne Migration (`currentGeometry()` additiv, opaque JSONB) | ✅ | `walls`/`plan`/`mat` additiv serialisiert, 256 KB-Cap |

## C. Sandbox-Zielfeatures (stellplatz-sandbox.html)

| Feature | Status | Umsetzung |
|---|---|---|
| Wände zeichnen (Kette) | ✅ | interaktiver 2-Klick-Editor + Live-Vorschau/Maße |
| Auto-Snap aller Objekte | ✅ | `snapPos` (Objektkanten) + **Innenwand-Face-Snap** für Fahrzeuge/Zonen |
| Winkel-Snap (Ortho H/V) | ✅ | `snapDraw` + gestrichelte Guide-Line |
| Tore/Türen schneiden die Wand | ✅ | Maske-freier Cut; Öffnungen als Objekte |
| Bauplan laden + Kalibrieren | ✅ | Downscale → geometry `plan`, verschieben/skalieren/Deckkraft |
| Stellfläche markieren | ✅ | nicht-sperrende Zone (`stell`), Aufziehen + m² |
| Räume m² | 🟡 | siehe B5 (Gesamt statt je Raum) |
| Speichern/Laden | ✅ | Persistenz via DB-`geometry` + Autosave (statt JSON-Datei; robuster) |
| Ohne Code erweitern | ✅ | siehe A1/B6 |

---

## D. Später ergänzte Optimierungen (alle umgesetzt)

Interaktiver Wand-Editor · Knoten ziehen/teilen/**auflösen** (A–B–C→A–C) · Segment- vs.
Punkt-Löschen · Öffnungen editierbar · Gesamt-Wandlänge trotz Durchbrüchen + lichte
Teilmaße · T-Kreuzungs-Abstände + **Live-Distanzen beim Öffnungs-Hover** · Ortho-Guides ·
**auto-expandierendes, wieder schrumpfendes Canvas mit gleichmäßigem Padding** ·
Wand-/Öffnungs-/Zonen-Popover mit 🗑 · **Lösch-Schutz** (Links-/Rechtsklick ins Leere
löscht nie) · **Rubber-Band-Zonen** · **aktives Werkzeug hervorgehoben**.

## E. Offen / nächste Schritte

- ⬜ **B4 Clearance-Pufferzonen um Fahrzeuge** – Abstand pro Fahrzeug + Prüfung/Visualisierung.
- 🟡 **B5 m² je Raum** – getrennte Räume separat ausweisen (Flood-Fill je Region statt Summe).
- 🟡 **Innenmaße / Wand-Extrusion nach außen** – aktuell Achs-Maß + zentrierte Dicke
      (Standard-CAD); lichtes Maß + einseitige Extrusion offen.
- 🟡 **Zonen-Rotation** im vereinheitlichten Overlay (aktuell nur Move + Eck-Resize).
