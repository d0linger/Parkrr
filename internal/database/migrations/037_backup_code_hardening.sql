-- Backup-code hardening: legacy codes were 8 characters from a ~31-character
-- alphabet (~40 bits) stored as unsalted SHA-256 hashes, making them cheap to
-- brute-force from a database leak. Codes are now 12 characters stored as
-- salted bcrypt hashes, which can never match the old SHA-256 values, so all
-- existing codes are invalidated here. Users must regenerate their recovery
-- codes (2FA settings -> regenerate).
DELETE FROM totp_backup_codes;
