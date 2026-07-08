## 2026-07-05 - [Throttling Password Verification Endpoints]
*Vulnerability:* Missing rate limiting on secondary password-verification endpoints (ChangePassword, TOTPDisable, TOTPRegenerateBackup) allowed for credential brute-forcing even if the primary Login was protected.
*Learning:* Security boundaries must be consistent. Any endpoint that performs password authentication is a target for brute-force attacks and requires proportional protection.
*Prevention:* Implement a reusable rate-limiting helper for all authentication-sensitive handlers.

## 2026-07-08 - [Throttling Modern Auth (Passkeys)]
*Vulnerability:* Passkey/WebAuthn endpoints were missing rate limiting, allowing for potential registration spam or IP-based brute-forcing of usernameless logins.
*Learning:* Modern authentication methods (WebAuthn, Passkeys) are not immune to automated attacks and must be included in the application's overall rate-limiting strategy.
*Prevention:* Always apply consistent rate-limiting patterns to all endpoints that perform cryptographic verification or handle authentication state changes.
