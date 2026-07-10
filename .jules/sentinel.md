## 2026-07-05 - [Throttling Password Verification Endpoints]
*Vulnerability:* Missing rate limiting on secondary password-verification endpoints (ChangePassword, TOTPDisable, TOTPRegenerateBackup) allowed for credential brute-forcing even if the primary Login was protected.
*Learning:* Security boundaries must be consistent. Any endpoint that performs password authentication is a target for brute-force attacks and requires proportional protection.
*Prevention:* Implement a reusable rate-limiting helper for all authentication-sensitive handlers.

## 2026-07-10 - [Case-Insensitive Username Rate-Limit Bypass]
*Vulnerability:* The authentication rate-limiter used raw, case-sensitive usernames as part of the throttle key, while the database performed case-insensitive lookups. This allowed an attacker to bypass the lockout by varying the capitalization of the username (e.g., admin, Admin, ADMIN).
*Learning:* Security primitives like rate limiters must match the normalization logic of the data they protect. If a resource is case-insensitive, its throttle must be too.
*Prevention:* Normalize identifiers (e.g., lowercase usernames) before using them as keys in rate limiters or caches.
