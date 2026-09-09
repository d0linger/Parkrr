-- Laufende Belegungen in die Historie übernehmen (Nachtrag zu 061).
--
-- Migration 061 legt spot_occupancy_history an und füllt sie über einen TRIGGER auf
-- vehicles.spot_id. Ein Trigger sieht aber nur ÄNDERUNGEN: jedes Gefährt, das im
-- Moment der Migration bereits auf einem Platz stand, hat nie einen ausgelöst und
-- steht deshalb mit KEINER offenen Zeile in der Historie. Zwei Folgen, beide still:
--
--   * Die Historie meldet "frei" für Plätze, die heute belegt sind.
--   * Die erste Umplatzierung eines solchen Gefährts schliesst nichts — die
--     vorangegangene Belegung taucht nirgends auf, auch nicht rückwirkend.
--
-- Warum als EIGENE Migration und nicht als Ergänzung in 061: eine angewandte
-- Migration wird nie geändert (die Prüfsumme beim Start würde den Dienst mit
-- "wurde nach dem Anwenden verändert" anhalten, und der Rat "eine NEUE Migration"
-- ist genau dieser hier).
--
-- Zum Zeitpunkt: started_at = now() ist bewusst gewählt und bewusst NICHT der
-- wahre Beginn — wann ein Gefährt auf seinen Platz kam, ist nirgends aufgezeichnet,
-- das war ja der Anlass für 061. Die Zeile sagt also "ab hier nachweislich belegt",
-- nicht "seit hier belegt". Eine erfundene Vergangenheit wäre schlimmer als eine
-- ehrliche Lücke.
--
-- ON CONFLICT DO NOTHING trägt den partiellen Unique-Index uq_spot_hist_open: ein
-- zweiter Lauf (oder ein Gefährt, das inzwischen doch eine offene Zeile hat) legt
-- nichts doppelt an.
INSERT INTO spot_occupancy_history
       (spot_id, spot_label, hall_id, vehicle_id, vehicle_label, person_id, started_at)
SELECT v.spot_id,
       COALESCE(s.label, ''),
       s.hall_id,
       v.id,
       COALESCE(NULLIF(v.label, ''), NULLIF(v.license_plate, ''), 'Gefährt'),
       v.person_id,
       now()
  FROM vehicles v
  LEFT JOIN spots s ON s.id = v.spot_id
 WHERE v.spot_id IS NOT NULL
ON CONFLICT DO NOTHING;
