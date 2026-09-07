-- Die Aufbewahrung war bisher reine ANZAHL ("die neuesten 7"). Die Anzahl sagt aber
-- nichts über den abgedeckten ZEITRAUM: wer aus irgendeinem Anlass sieben Läufe an
-- einem Nachmittag anstößt, hat danach sieben Sicherungen von heute und keine
-- einzige von gestern — und merkt es erst, wenn er eine braucht (Hundert 09).
--
-- keep_days ist ein BODEN, keine Obergrenze: gelöscht wird nur, was sowohl jenseits
-- der Anzahl liegt ALS AUCH älter als diese Tagesgrenze ist. Damit kann die neue
-- Einstellung nur dazu führen, dass mehr aufbewahrt wird, nie weniger.
--
-- Default 0 = keine Altersgrenze, also exakt das bisherige Verhalten. Die Migration
-- ist damit verhaltensneutral; wer eine Mindest-Historie will, stellt sie im
-- Backup-Reiter ein.
ALTER TABLE backup_settings ADD COLUMN IF NOT EXISTS volume_keep_days INT NOT NULL DEFAULT 0;
ALTER TABLE backup_settings ADD COLUMN IF NOT EXISTS s3_keep_days     INT NOT NULL DEFAULT 0;

-- Negative Werte hätten keine sinnvolle Bedeutung und würden in prunableS3/
-- prunableFiles als "Grenze in der Zukunft" ausgelegt: dann wäre NICHTS je alt
-- genug und das Aufräumen stünde still. Die Anwendung klemmt sie bereits ab; der
-- CHECK hält die Regel auch für jeden Schreibweg an ihr vorbei.
ALTER TABLE backup_settings DROP CONSTRAINT IF EXISTS backup_settings_keep_days_nonneg;
ALTER TABLE backup_settings ADD CONSTRAINT backup_settings_keep_days_nonneg
    CHECK (volume_keep_days >= 0 AND s3_keep_days >= 0);
