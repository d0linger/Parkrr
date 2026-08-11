# Testmatrix — Garagenplaner & Stellplätze

Stand: 2026-08-11 · Feature: Hallen-/Stellplatzverwaltung (2-Modus-Planer)
Umgebung: lokale Dev-Instanz `http://localhost:8099` · Rolle: Editor/Admin (Schreiben), Reader (nur Lesen)

Legende Testkategorie: **HP** = Happy Path · **EC** = Edge Case · **NT** = Negative Test ·
**GUI** = GUI-Stabilität/State · **DLG** = Dialog-Management · **PERF** = Performance · **A11Y** = Barrierefreiheit/Responsive

Vorbedingung „Basis": Angemeldet als Editor, ≥1 Garage → ≥1 Halle angelegt, Planer der Halle geöffnet. Einige Gefährte existieren; einige mit gepflegten Maßen (L/B/H/Gewicht), einige ohne.

---

## 1 · Interaktiver Lageplan (Status, Klick-Aktion)

| ID | Test-Szenario | Vorbedingung | Testschritte | Erwartetes Ergebnis (visuell + funktional) | Kat. |
|----|----|----|----|----|----|
| LP-01 | Status-Farbcodierung sichtbar | Basis, ≥1 platziertes Gefährt | Detailpanel öffnen → Status nacheinander auf Belegt/Reserviert/Ein-Aus stellen | Block wechselt Farbe (Belegt rot, Reserviert gelb, Ein-Aus blau); Symbol ●/◑/⇄ im Code-Chip wechselt mit | HP |
| LP-02 | Klick öffnet Auswahl/Detail | Basis, platziertes Gefährt | Auf einen belegten Block klicken (kein Ziehen) | Block wird selektiert (weißer Rahmen), rechtes Panel zeigt Fahrzeug, Maße, Position, Prüfung | HP |
| LP-03 | Klick-to-Action → Mieterdaten | Gefährt gehört einer Person | Im Detailpanel „Mieterdaten öffnen →" klicken | Navigiert zur Personenseite des Mieters | HP |
| LP-04 | Belegungs-Metriken korrekt | Basis, 2 Gefährte platziert | Metrik-Leiste + Belegungsbalken prüfen | „Belegt · 2", Frei-m² = Halle − Ausgenommen − Belegt; Balken-Breite ∝ belegte Fläche | HP |
| LP-05 | Occupancy-Bar bei 0 | Halle ohne Platzierung | Planer öffnen | „0 Gefährte", Balken leer, keine JS-Fehler | EC |
| LP-06 | Reader darf nicht ändern | Als Reader angemeldet | Planer öffnen, Block anklicken/ziehen | Auswahl/Anzeige ok; keine Bearbeitungs-Buttons, Ziehen bewegt nichts, keine 403-Toaster-Flut | NT |

## 2 · Geometrie- & Dimensionen-Matching

| ID | Test-Szenario | Vorbedingung | Testschritte | Erwartetes Ergebnis | Kat. |
|----|----|----|----|----|----|
| DM-01 | Höhe ≤ Torhöhe = ok | Torhöhe 3,0 m; Gefährt Höhe 2,5 m | Gefährt platzieren, Detailpanel „Prüfung" | Zeile „Höhe 2,5 m · Tor 3,0 m" = ok; kein Warn-Badge | HP |
| DM-02 | Höhe > Torhöhe = Warnung | Torhöhe 3,0 m; Gefährt 3,3 m | Gefährt platzieren | Warn-Badge „⚠ Maß" am Block; Prüfzeile Höhe = „!"; Toast „Höhe/Gewicht" beim Platzieren | HP |
| DM-03 | Gewicht > Bodenlast = Warnung | Bodenlast 5 t; Gefährt 6,2 t | Gefährt platzieren | Warn-Badge, Prüfzeile Gewicht rot „!" | HP |
| DM-04 | Gefährt exakt = Torhöhe | Torhöhe 3,0; Gefährt 3,0 | Platzieren | Grenzwert gilt als ok (≤, kein Warnung) | EC |
| DM-05 | Gefährt ohne Maße | Gefährt ohne L/B/H/Gewicht | Platzieren | Kein Höhen-/Gewichts-Warnung (unbekannt); Footprint mit Default (≈4,5×2 m); „Maße offen" im Label | EC |
| DM-06 | Footprint aus Maßen | Gefährt 7,4×2,3 m | Platzieren | Blockgröße entspricht 7,4×2,3 m im Raster | HP |
| DM-07 | Torhöhe ändern re-prüft | Platziertes Gefährt 2,9 m, Tor 3,0 | Im Garagenplaner Torhöhe auf 2,5 senken, zurück zu Stellplätze | Gefährt zeigt jetzt Höhen-Warnung | EC |

## 3 · Drag-&-Drop / Umbuchung + Konfliktprüfung

| ID | Test-Szenario | Vorbedingung | Testschritte | Erwartetes Ergebnis | Kat. |
|----|----|----|----|----|----|
| DD-01 | Platzieren aus Palette | Modus Stellplätze, freies Gefährt in „Nicht platziert" | Gefährt-Karte auf freie Fläche ziehen & loslassen | Vorschau grün während Ziehen; Block wird angelegt; Karte verschwindet aus Palette; Toast „platziert" | HP |
| DD-02 | Umbuchung (verschieben) | Platziertes Gefährt | Block auf andere freie Fläche ziehen | Block folgt, grün, an neuer Position; „Verschoben" Toast; bleibt nach Reload | HP |
| DD-03 | Kollision blockiert | Zwei Gefährte | Eines auf das andere ziehen & loslassen | Vorschau/Block rot; beim Loslassen Rücksprung auf alte Position; Toast „Kollision" | NT |
| DD-04 | Außerhalb Fläche blockiert | L-Form-Halle | Gefährt auf einen ausgeschnittenen Bereich ziehen | Vorschau „außerhalb der Fläche" rot; Drop verworfen mit Toast | NT |
| DD-05 | Drop auf Ausgenommene | Fahrstraße/Säule vorhanden | Gefährt auf Fahrstraße ziehen | Rot „belegt/ausgenommen"; Drop verworfen | NT |
| DD-06 | Drop neben Canvas | — | Palette-Karte loslassen außerhalb des Plans | Kein Block; Toast „Auf die Fläche ziehen"; Palette unverändert | NT |
| DD-07 | Drehen (90°) | Selektiertes Gefährt | „⟳ Drehen" | Breite/Höhe getauscht, sofern Platz frei; sonst „Drehen nicht möglich" | EC |
| DD-08 | Größe ändern (Ausgenommene) | Ausgewählte Fahrstraße | Eck-Handle ziehen | Größe ändert sich; überlappt sie ein Gefährt → Rücksprung, Toast | EC |
| DD-09 | Entfernen gibt frei | Platziertes Gefährt | Detailpanel „Entfernen" | Block weg; Gefährt erscheint wieder in „Nicht platziert"; im Backend spot_id=NULL | HP |
| DD-10 | Raster vs. Frei | — | Umschalter Raster/Frei, dann verschieben | Raster: ganzzahlige m-Schritte; Frei: stufenlos (0,01 m) | EC |

## 4 · Garagenplaner (Form, Maße, Kanten, Ausgenommene)

| ID | Test-Szenario | Vorbedingung | Testschritte | Erwartetes Ergebnis | Kat. |
|----|----|----|----|----|----|
| GP-01 | Form wechseln | Modus Garagenplaner | Rechteck/L-Form/Schräg/Stufe wählen | Grundriss ändert sich; außenliegende Gefährte werden gezählt/gewarnt | HP |
| GP-02 | Maße ändern | — | Breite/Tiefe ±; Torhöhe/Bodenlast ± | Werte ändern sich, Grundriss skaliert, Blöcke werden geklemmt | HP |
| GP-03 | Ecke ziehen | „◇ Form bearbeiten" aktiv | Ecke ziehen | Polygon folgt; Ortho-Snap bei 90°/Shift; Fläche/Umfang aktualisiert | HP |
| GP-04 | Kantenlänge editieren | Form bearbeiten | Linie klicken → Längenfeld erscheint → Wert (Dezimal) eintragen | Kante nimmt exakte Länge an (2 Dezimalstellen) | HP |
| GP-05 | Punkt einfügen/löschen | Form bearbeiten | Doppelklick auf Kante (einfügen), Doppelklick Ecke (löschen) | Ecke wird ein-/ausgefügt; Minimum 3 Ecken erzwungen | EC |
| GP-06 | Ganze Linie verschieben | Form bearbeiten | Kante greifen und ziehen | Beide Endpunkte bewegen sich, in der Fläche geklemmt | HP |
| GP-07 | Ausgenommene hinzufügen | — | „+ Fahrstraße/Wartung/Säule/Notausgang" | Fläche erscheint an erster freier Position; in Liste; auswählbar | HP |
| GP-08 | Ausgenommene löschen | ≥1 Fläche | „×" in der Liste | Fläche entfernt, Metriken aktualisiert | HP |
| GP-09 | Speichern persistiert | Geänderter Grundriss | „● Speichern"; Seite neu laden | Form/Maße/Torhöhe/Bodenlast/Ausgenommene bleiben erhalten | HP |
| GP-10 | Fläche/Umfang korrekt | Nicht-rechteckige Form | Metriken lesen | m² (Shoelace) und Umfang plausibel und live aktualisiert | EC |

## 5 · GUI-Stabilität & State-Management

| ID | Test-Szenario | Vorbedingung | Testschritte | Erwartetes Ergebnis | Kat. |
|----|----|----|----|----|----|
| ST-01 | Maximize/Minimize | Basis | „⛶" → Vollbild; „⛶/⤢" → zurück; Esc | Planer überlagert die App; Esc schließt; Layout rechnet neu; keine doppelten Scrollbalken | GUI |
| ST-02 | DnD im Maximize | Vollbild aktiv | Gefährt platzieren/verschieben im Vollbild | Funktioniert identisch; Vorschau/Kollision korrekt | GUI |
| ST-03 | Zustand bei Modus-Wechsel | Auswahl aktiv | Zwischen Garagenplaner/Stellplätze wechseln | Auswahl/Editiermodus wird zurückgesetzt; keine Reste; korrekte Rail | GUI |
| ST-04 | Undo/Redo | Mehrere Änderungen | Strg+Z / Strg+Umschalt+Z (und ↶/↷) | Grundriss + Platz-Positionen springen korrekt; Buttons disabled an Grenzen | GUI |
| ST-05 | Verlassen räumt auf | Planer offen | Zu anderem Menü navigieren, zurück | Keine hängenden keydown/resize-Listener; kein doppeltes Rendern; Speicher stabil | GUI |
| ST-06 | Reload stellt her | Platzierungen + gespeicherter Grundriss | Browser-Reload | Platzierungen sofort da (persistiert); Grundriss nach „Speichern" da | GUI |
| ST-07 | Nebenläufige Nutzung | 2 Tabs, gleiche Halle | In Tab A verschieben; Tab B neu laden | Tab B zeigt die Änderung (server-persistiert) | EC |

## 6 · Dialog-Management (keine überlappenden Dialoge)

| ID | Test-Szenario | Vorbedingung | Testschritte | Erwartetes Ergebnis | Kat. |
|----|----|----|----|----|----|
| DG-01 | Kein überlappender Dialog | — | „+ Garage"/„+ Halle"-Formular öffnen, dann erneut auslösen | Immer nur EIN Modal sichtbar; kein Stapeln | DLG |
| DG-02 | Haupt-UI blockiert | Modal offen | Hinter dem Modal klicken/navigieren | Interaktion blockiert (dialog modal); Fokus im Dialog | DLG |
| DG-03 | Esc & Klick-außerhalb | Modal offen | Esc drücken; Backdrop klicken | Dialog schließt sauber, ohne Datenverlust an anderer Stelle | DLG |
| DG-04 | Keine Editor-Verschachtelung | Kanten-Längenfeld offen | Anderen Editor/Dialog auslösen | Längenfeld schließt/verliert Fokus sauber; kein verschachtelter Editor-Zustand | DLG |
| DG-05 | Bestätigung bei Löschen | Halle/Garage | „Löschen" | Bestätigungsdialog erscheint; Abbrechen lässt alles unverändert | DLG |

## 7 · Performance & Reaktion

| ID | Test-Szenario | Vorbedingung | Testschritte | Erwartetes Ergebnis | Kat. |
|----|----|----|----|----|----|
| PF-01 | Viele Stellplätze | Halle mit 60–100 Platzierungen | Planer öffnen, scrollen, verschieben | Initiales Rendern < ~300 ms; flüssiges Ziehen (kein spürbares Ruckeln) | PERF |
| PF-02 | DnD-Latenz | Große Halle, viele Blöcke | Block zügig ziehen | Vorschau folgt ohne merkliche Verzögerung; Kollisionsprüfung hält Schritt | PERF |
| PF-03 | Häufige Metrik-Updates | Ecke schnell ziehen | Grundriss editieren | Fläche/Umfang aktualisieren ohne Einfrieren | PERF |
| PF-04 | Reduzierte Bewegung | OS „Reduce Motion" an | Interagieren | Keine störenden Animationen (prefers-reduced-motion beachtet) | A11Y |

## 8 · Barrierefreiheit & Responsive

| ID | Test-Szenario | Vorbedingung | Testschritte | Erwartetes Ergebnis | Kat. |
|----|----|----|----|----|----|
| AC-01 | Status ohne Farbe erkennbar | Platzierte Gefährte versch. Status | Farbblind-Simulation (z. B. Deuteranopie) | Status weiterhin unterscheidbar über Symbol ●/◑/⇄ + Textlabel im Detail | A11Y |
| AC-02 | Warnungen nicht nur Farbe | Gefährt mit Höhen-/Kollisionsproblem | Ansehen | Text-Badge „⚠ außerhalb"/„⚠ Maß" zusätzlich zur Farbe | A11Y |
| AC-03 | Tablet-Layout | Viewport ~900 px | Planer öffnen | Rail rückt unter den Canvas (1-Spalten-Grid); nichts abgeschnitten | A11Y |
| AC-04 | Mobile-Layout | Viewport ~390 px | Planer öffnen, Block antippen | Bedienbar; horizontaler Scroll nur im Canvas, nicht der Seite; Touch-Drag (touch-action:none) funktioniert | A11Y |
| AC-05 | Tastatur/Fokus | — | Durch Buttons tabben | Sichtbarer Fokus; Buttons erreichbar; Längen-Eingabe per Tastatur editierbar | A11Y |
| AC-06 | Kontrast | — | Text/Chips im Plan lesen | Ausreichender Kontrast auf dunklem Panel (Labels, Metriken, Warnungen) | A11Y |

---

## Prüfergebnis (Stand 2026-08-11)

Legende: **✅** = an Quelle verifiziert (Code / DB / Geometrie-Test) · **👤** = Logik im Code vorhanden, visuelle/Interaktions-Abnahme im Browser durch Tester (mit Strg+Shift+R). Keine offenen ⚠️-Befunde.

| ID | Status | Beleg / Notiz |
|----|----|----|
| LP-01 | ✅ | Symbole `●/◑/⇄` (`GPSTAT`, app.js:3769) + Statusfarben `.gp-block.veh.busy/resv/move` (style.css:1319–1321) |
| LP-02/03 | 👤 | Auswahl + Detailpanel + „Mieterdaten öffnen"-Nav vorhanden |
| LP-04 | ✅ | Frei = Halle − Ausgenommen − Belegt (app.js:4190); Balken ∝ Fläche (4081); `polyArea` Shoelace (3742) |
| LP-05 | ✅ | 0 Gefährte; `Math.max(1,tot)` verhindert Div/0 |
| LP-06 | ✅ | `canManageNow` (3868) sperrt Verschieben (4519); Edit-Buttons an `canManage()` gebunden |
| DM-01/04 | ✅ | `heightOK` = `H ≤ Tor+0.001` (3871) → Grenzwert gilt als ok |
| DM-02/03 | ✅ | `⚠ Maß`-Badge (4153) + Prüfzeilen (4347–4348); `weightOK` (3872) |
| DM-05 | ✅ | `H==null`→ok; „Maße offen" (3881); Default-Footprint 4,5×2 |
| DM-06 | ✅ | Maßstabsgetreue Skalierung `sp.w=L, sp.h=W` (Geometrie-Test bestätigt) |
| DM-07 | ✅/👤 | `heightOK` nutzt `P.tor` live; Re-Render nach Torhöhen-Änderung |
| DD-02/09 | ✅ | Persistenz via `/spots`; Entfernen (`api.del /spots`, 4442) → Backend `spot_id=NULL` (stellplatz.go:578) |
| DD-03/05 | ✅ | `collide`+`satOverlap` (3875/3756) inkl. Ausgenommene; Rücksprung + Toast (4561) |
| DD-04 | ✅ | `rectInPoly`/`inside`; Vorschau „außerhalb der Fläche" (4598) |
| DD-06 | ✅ | Drop neben Canvas → Toast „Auf die Fläche ziehen" (4603) |
| DD-01/07/08 | 👤 | Platzieren/Drehen/Resize-Logik inkl. Validierung vorhanden |
| DD-10 | ✅ | `snapV`: Raster (¼/½/1 m) vs. Frei (0,01 m) (3937); „Frei"-Button (4208) |
| GP-01 | ✅ | 5 Formen rect/l/u/trap/step (4364); `setShape` zählt Außenliegende (4420) |
| GP-04/06/10 | ✅ | `setEdgeLen` exakte Länge (3968); Linie-Verschieben geklemmt (4446); `polyArea`/`edgeLen` |
| GP-02/03/05/07/08/09 | 👤 | Maße/Ecke/Punkt/Ausgenommene/Speichern-Logik vorhanden |
| ST-04 | ✅ | `pushUndo`/`doUndo`/`doRedo` (3989–3999), Historie-Cap 80, Grenzen-Guard |
| ST-05 | ✅ | `keydown`/`keyup`/`resize`-Listener + ResizeObserver räumen bei `!root.isConnected` auf (4002/4010/4037) |
| ST-06/07 | ✅ | Plan lädt server-seitig (3845); Positionen persistiert |
| ST-01/02/03 | 👤 | Maximize/Esc (4008), DnD-Vollbild, Modus-Reset vorhanden |
| DG-01…05 | ✅/👤 | Natives `<dialog>.showModal()` (213/380/422) = genau ein Modal, Backdrop + Fokusfalle vom Browser |
| PF-04 | ✅ | `prefers-reduced-motion`-Media-Queries vorhanden (style.css mehrfach) |
| PF-01/02/03 | 👤 | Performance/Latenz — im Browser mit vielen Blöcken zu messen |
| AC-01/02 | ✅ | Status-Symbole + Text; `⚠ außerhalb`/`⚠ Maß`-Text-Badges (4153) |
| AC-03/04 | ✅ | `@media(max-width:900px)` Rail unter Canvas (1277); `touch-action:none` (1300/1350) |
| AC-05/06 | 👤 | Tastatur-Fokus & Kontrast — visuell/Screenreader zu prüfen |

**Zusätzlich per Geometrie-Test abgesichert** (nicht in obiger Matrix): Rotationsbewusstes Clamping, magnetisches Rand-Anrasten und Innen/Außen-Prüfung wurden end-to-end gegen alle 5 Formen (rect/l/trap/step/u) × Ränder/Ecken/Stufe × Rotation (0/30/90°) × Raster (Frei/¼/½/1) validiert: **0 Snap-verursachte Außerhalb-Fälle**, bündiges Anrasten an allen achsparallelen Rändern erreicht.

---

## Rand-Anrasten, Clamping & Geometrie (Verhalten)
- **Bezug ist der ECHTE Grundriss**, nicht die Canvas-Fläche `0..Wm/0..Hm`. Snap/Clamp hängen an der Bounding-Box des Polygons **und** an allen Vertex-Koordinaten (Außenränder + Innenstufen). Ein eingerückter Grundriss (z. B. Oberkante y=1,2, rechte Kante x=36,1) rastet korrekt an der grünen Linie, nicht am Canvas-Rand.
- **Magnet-Reichweite = eine Rasterzelle (+ε)**: `(P.snap ? gridStep : 0,25) + 0,05`. „Zum Rand ziehen" landet immer bündig, statt eine Zelle davor zu stoppen.
- **Selbst-validierend** (`both → nur X → nur Y → gar nicht`): bei diagonalen (`trap`) oder Notch-Formen (`u`) rastet der Magnet nur, wenn das Ergebnis im Grundriss liegt — er schiebt **nie** einen Block nach außen (kein Reset).
- **Rotationsbewusst**: geklemmt/gerastet wird die gedrehte Bounding-Box (AABB-Halbausdehnung), damit auch 90°-Blöcke bündig an den Rand kommen.
- **Bündig ist gültig**: der T-Kreuzungs-Fehlalarm in `rectInPoly`/`quadInPoly` ist behoben (geschrumpftes Polygon für Ecken- und Kantentest), sodass eine Kante exakt auf dem Rand nicht als „außerhalb" gilt.

## Bekannte, bewusste Abweichungen vom Prototyp (kein Bug)
- **Persistenz statt localStorage:** Platzierungen liegen als Backend-Datensätze (Spot + `vehicle.spot_id`), Grundriss in `hall.geometry`. Undo/Redo umfasst Geometrie/Positionen, **nicht** das Anlegen/Löschen von Platzierungen (diese persistieren sofort).
- **Palette = real nicht zugewiesene Gefährte** (`spot_id IS NULL`), nicht die 4 Demo-Fahrzeuge des Prototyps.
- **Immer dunkles Planer-Design** (bewusst, wie der Prototyp), unabhängig vom App-Theme.

## Navigation (Kürzel)
- **Strg + Mausrad** = Zoom zum Cursor · **mittlere Maustaste** ODER **Leertaste + Ziehen** = Pan · **F** = Einpassen · **⛶/Esc** = Vollbild ein/aus · **Strg+Z / Strg+Umschalt+Z** = Undo/Redo. (F/Leertaste werden in Eingabefeldern & `<select>` nicht abgefangen.)

## Offene Punkte / später
- **Grundriss-Hintergrundbild + Maßstab-Kalibrierung** (Prototyp-Wave 2) — noch nicht portiert. *(einziger echt offener Feature-Punkt)*
- ~~Fahrzeug-Maße-Pflege direkt im Planer~~ — **erledigt**: `dimForm` (app.js:4256), Buttons „Maße festlegen →" / „✎ Maße".
- **👤-Zeilen oben** noch im Browser abnehmen (reine Interaktions-/Sicht-/Performance-Prüfungen).
