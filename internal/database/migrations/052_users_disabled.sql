-- Ein ausgeschiedener Mitarbeiter liess sich bisher nur per DeleteUser stoppen — und
-- das nullt über ON DELETE SET NULL die Urheberschaft auf Aufzeichnungen, die nach
-- BAO §131/§132 gerade NICHT nachträglich verändert werden sollen: invoices.created_by,
-- payments.created_by, payments.reversed_by, handover_protocols.created_by und
-- self_service_tokens.created_by (Hundert API-31).
--
-- (Das Änderungsprotokoll selbst ist NICHT betroffen: audit_log.user_id trägt gar
-- keinen Fremdschlüssel und führt den Benutzernamen zusätzlich als Text mit.)
--
-- Ein Deaktiviert-Flag hält diese Verweise intakt und sperrt trotzdem jeden Zugang.
--
-- Default false: Bestandskonten bleiben unverändert aktiv, die Migration ist damit
-- verhaltensneutral.
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled BOOLEAN NOT NULL DEFAULT false;

-- Teilindex auf die (seltenen) deaktivierten Konten, damit die Benutzerliste sie
-- ohne Full Scan kennzeichnen kann.
CREATE INDEX IF NOT EXISTS idx_users_disabled ON users (id) WHERE disabled;
