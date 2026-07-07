## 2026-07-05 - [Throttling Password Verification Endpoints]
*Vulnerability:* Missing rate limiting on secondary password-verification endpoints (ChangePassword, TOTPDisable, TOTPRegenerateBackup) allowed for credential brute-forcing even if the primary Login was protected.
*Learning:* Security boundaries must be consistent. Any endpoint that performs password authentication is a target for brute-force attacks and requires proportional protection.
*Prevention:* Implement a reusable rate-limiting helper for all authentication-sensitive handlers.

## 2026-10-27 - [Consistent Rate Limiting on 2FA Enrollment]
*Vulnerability:* Missing rate limiting on 2FA enrollment endpoints (TOTPSetup and TOTPEnable) allowed for brute-forcing 6-digit TOTP codes during the enablement phase and failed to block users already throttled on other endpoints.
*Learning:* Security controls during onboarding/enrollment are just as critical as those during standard login. A lack of throttling in enrollment can be exploited to bypass MFA protections before they are fully active.
*Prevention:* Apply the application's standard `LoginLimiter` to all endpoints that verify MFA codes or secrets, including during enrollment and setup.
