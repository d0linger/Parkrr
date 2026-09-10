-- Existing sessions have no recorded factor proof and must reauthenticate when
-- required MFA is enabled. Enrollment alone must never upgrade their assurance.
ALTER TABLE sessions ADD COLUMN factor_verified BOOLEAN NOT NULL DEFAULT false;
