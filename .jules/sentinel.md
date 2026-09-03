## 2026-07-05 - [Throttling Password Verification Endpoints]
*Vulnerability:* Missing rate limiting on secondary password-verification endpoints (ChangePassword, TOTPDisable, TOTPRegenerateBackup) allowed for credential brute-forcing even if the primary Login was protected.
*Learning:* Security boundaries must be consistent. Any endpoint that performs password authentication is a target for brute-force attacks and requires proportional protection.
*Prevention:* Implement a reusable rate-limiting helper for all authentication-sensitive handlers.

## 2026-07-10 - [Case-Insensitive Username Rate-Limit Bypass]
*Vulnerability:* The authentication rate-limiter used raw, case-sensitive usernames as part of the throttle key, while the database performed case-insensitive lookups. This allowed an attacker to bypass the lockout by varying the capitalization of the username (e.g., admin, Admin, ADMIN).
*Learning:* Security primitives like rate limiters must match the normalization logic of the data they protect. If a resource is case-insensitive, its throttle must be too.
*Prevention:* Normalize identifiers (e.g., lowercase usernames) before using them as keys in rate limiters or caches.

## 2026-07-12 - [Bcrypt Password Length Truncation and DoS Mitigation]
*Vulnerability:* The application lacked a maximum length limit on passwords. Bcrypt silently truncates input at 72 bytes, meaning any characters beyond that are ignored for authentication, potentially misleading users about their password's strength. Furthermore, hashing extremely long strings can be used for Denial of Service (DoS) attacks.
*Learning:* Always enforce a reasonable upper bound on password lengths at the validation layer when using bcrypt to ensure the entire password is used for hashing and to protect against computational resource exhaustion.
*Prevention:* Validate that passwords are between 8 and 72 bytes before hashing.

## 2026-07-13 - [Identifier Length Validation for DoS and Rate-Limiter Protection]
*Vulnerability:* Authentication identifiers (usernames) and credentials (passwords) lacked early length validation in the Login flow. This exposed the bcrypt comparison to resource exhaustion and allowed an attacker to exert memory pressure on the in-memory rate limiter by sending extremely large username strings used as throttle keys.
*Learning:* Security boundaries must validate input size at the outermost layer. Protecting expensive cryptographic operations and stateful security primitives (like rate limiters) from malformed or over-sized input is a critical part of defense-in-depth.
*Prevention:* Enforce strict maximum lengths on all authentication-related inputs (e.g., 100 chars for usernames, 72 bytes for passwords) before any processing or state lookup occurs.

## 2026-07-14 - [Inconsistent Password Length Validation across Secondary Auth Endpoints]
*Vulnerability:* While password length caps were applied to `Login`, `ChangePassword`, and User Creation/Modification endpoints, secondary endpoints that require re-authentication (`TOTPDisable` and `TOTPRegenerateBackup`) lacked early length limits, allowing potentially expensive bcrypt password comparisons against long payloads on authenticated sessions.
*Learning:* Security validations and length limits must be applied uniformly to every entry point that accepts a password, even secondary re-authentication endpoints.
*Prevention:* Always enforce the standard password length cap (72 bytes for bcrypt) at the request decoding or payload parsing step for every password-handling handler.

## 2026-07-15 - [Bypassing Note Length Limit via Status Transition Endpoint]
*Vulnerability:* Although vehicle notes were restricted on creation/modification, the vehicle status change endpoint accepted an unrestricted `Note` field for logging the transition in the history database. This allowed users with editor permissions to bypass length controls and submit massive string payloads.
*Learning:* Input sanitization and length bounds must be applied to text fields across all mutating endpoints, including transition/history logs, to prevent DB bloat and DoS/memory pressure.
*Prevention:* Enforce the centralized note length validator (`validNoteLength`) on all endpoints receiving text notes or transition details.

## 2026-07-16 - [Expanded CSV Formula Injection Mitigation]
*Vulnerability:* CSV export formula injection protection (`csvSafe`) only checked for `=`, `+`, `-`, `@`, `\t`, and `\r`. Inputs starting with `|` (pipe, used in DDE/cmd execution payloads in older spreadsheet applications) or `%` were left unguarded.
*Learning:* Spreadsheet formula injection (CWE-1236) triggers vary depending on the target software (Excel, LibreOffice, Calc). Sanitization functions must include all known trigger symbols (`=`, `+`, `-`, `@`, `\t`, `\r`, `|`, `%`) and maintain symmetric unguarding logic for round-trip safety.
*Prevention:* Always include `|` and `%` alongside standard operators in CSV cell escaping routines and ensure import unguarding mirrors export escaping.

## 2026-07-17 - [Per-Account Rate-Limiting Bypass on Passkey Registration]
*Vulnerability:* `PasskeyRegisterFinish` recorded failed attestation verifications using `h.Limiter.RecordFailure(key)`, which only updated the per-IP `username|ip` rate limiter. An attacker rotating source IP addresses could bypass the throttle and attempt unlimited passkey registration verifications for an account.
*Learning:* Post-authentication ceremonies that verify credentials or enrolment attestations (such as TOTP setup or Passkey registration) must record failures against both per-IP and per-account (`UserLimiter`) limiters.
*Prevention:* Always use `recordReauthFailure` and `resetReauth` helpers on all secondary auth/re-auth/registration endpoints to ensure per-account limiters are updated across IP rotations.

## 2026-07-18 - [Rate-Limiter Bypass on Passkey Registration Initiation via Step-Up Window]
*Vulnerability:* `PasskeyRegisterBegin` relied solely on `requireStepUp` without explicitly invoking `checkRateLimit`. When an active session was recent (< 10 minutes), `requireStepUp` bypassed rate-limiter evaluation, allowing rate-limited or locked-out accounts to initiate passkey registration ceremonies and persist ceremony state.
*Learning:* Helper functions like `requireStepUp` that shortcut authentication checks for recent sessions must not be treated as a complete substitute for explicit rate limiting on sensitive ceremony initiation endpoints.
*Prevention:* Always invoke `checkRateLimit` explicitly on registration and authentication ceremony endpoints, even if `requireStepUp` is used for step-up authentication.
