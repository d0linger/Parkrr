-- Ausbauplan, offener Punkt O3 (Entscheidung vom 22.10.2026): beim Saisonwechsel
-- nennt „Platz vorschlagen“ den Platz vom Vorjahr zuerst, wenn er frei ist und passt.
--
-- „Erneut einstellen“ (einzeln oder im Saisonwechsel) legt ein NEUES Gefährt an. Bisher
-- wusste die Kopie nicht, von welchem Gefährt sie stammt; ihr Vorjahresplatz stand zwar
-- in spot_occupancy_history, aber unter der Nummer des Vorgängers. copied_from führt
-- dorthin.
--
-- Bewusst OHNE Fremdschlüssel, wie die Belegungshistorie selbst: der Verweis ist
-- Herkunft, keine Abhängigkeit. Ein Fremdschlüssel sperrte beim Kopieren die Zeile des
-- Vorgängers (FOR KEY SHARE); ändert jemand den Vorgänger gleichzeitig, bräche die Kopie
-- unter REPEATABLE READ mit einem Serialisierungsfehler ab. Wird der Vorgänger gelöscht,
-- bleibt die Nummer stehen — seine Einträge in der Historie überleben ihn ohnehin.
ALTER TABLE vehicles ADD COLUMN IF NOT EXISTS copied_from BIGINT;

-- Frühere Kopien: das Änderungsprotokoll nennt den Vorgänger seit jeher im selben
-- Wortlaut („re-stored vehicle from #12“, DuplicateVehicle).
UPDATE vehicles v
   SET copied_from = src.id
  FROM audit_log a
  JOIN vehicles src ON src.id = substring(a.summary FROM '^re-stored vehicle from #([0-9]+)$')::bigint
 WHERE a.entity = 'vehicle' AND a.action = 'create' AND a.entity_id = v.id
   AND a.summary LIKE 're-stored vehicle from #%'
   AND v.copied_from IS NULL AND src.id <> v.id;
