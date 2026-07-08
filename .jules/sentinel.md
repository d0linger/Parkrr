## 2026-07-05 - [Throttling Password Verification Endpoints]
*Vulnerability:* Missing rate limiting on secondary password-verification endpoints (ChangePassword, TOTPDisable, TOTPRegenerateBackup) allowed for credential brute-forcing even if the primary Login was protected.
*Learning:* Security boundaries must be consistent. Any endpoint that performs password authentication is a target for brute-force attacks and requires proportional protection.
*Prevention:* Implement a reusable rate-limiting helper for all authentication-sensitive handlers.

## 2026-07-06 - [Securing Passkey Ceremonies]
*Vulnerability:* Passkey registration and login endpoints lacked rate limiting, allowing for brute-forcing of attestation responses and potential DoS through repeated cryptographic verification.
*Learning:* Passwordless authentication flows (WebAuthn) require the same level of protection as traditional password flows. Since the username is unknown during discoverable login, IP-based throttling is necessary.
*Prevention:* Apply the established rate-limiting pattern to all passkey finishing handlers using both IP-based and username-based identifiers.
