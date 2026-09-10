# Security audit remediation — 2026-09-10

Scope: the 11 actionable findings from the audit of dev at 2554174, plus the
informational database-administrator trust boundary. Implementation is local to
dev. Nothing has been pushed, deployed, or applied to a production database.

## Implemented changes

| Finding | Change |
|---|---|
| A01 — required MFA | Migration 067 records factor proof per session. Password login grants proof only after TOTP/recovery verification; verified WebAuthn grants proof. Middleware independently checks enrollment and session proof. Unverified enrolled sessions cannot change factors. Rotation preserves assurance without upgrading it. The UI returns these sessions to sign-in. |
| A02 — clone suspension | The verified-credential policy returns a distinct failure after suspension. The handler records the event and returns before issuing a session. Warning-only mode remains unchanged. |
| A03 — metrics growth | HTTP methods use a fixed label allow-list plus other, including requests rejected by the rate limiter. |
| A04 — duplicate charge claims | Migration 069 serializes competing invoice/payment claims for one-off charges and rejects the losing transaction. Payment, credit, charge/vehicle sliders, and agreement extras translate the conflict into 409. Existing financial records are never deleted to make the migration pass. |
| A05 — period reversal | Generic reversal locks the payment and rejects period-managed settlements before mutation. The existing period control remains the supported reversal path. The UI suppresses the generic reversal button for managed settlements. |
| A06 — Grafana | The optional monitoring stack pins Grafana 13.2.1 by verified multi-platform digest and enables CSP. |
| A07 — handover deletion | Migration 068 changes the handover-to-vehicle FK to RESTRICT. Parent deletes return 409; the existing admin-only explicit handover deletion remains available. |
| A08 — anonymization | Reminder recipients are anonymized through invoice ownership even when the current email is blank. |
| A09 — backup staging | Archive listing and single-transaction restoration stream the already-decrypted buffer to pg_restore stdin instead of writing a whole dump to /tmp. |
| A10 — monitoring exposure | Prometheus and Grafana publish only on loopback. Remote access requires an administrative tunnel or a separately configured protected TLS proxy. |
| A11 — publication scan gate | Publication builds a candidate digest, scans both shipped architectures, and only then promotes release tags. Both CI and publication quality gates also run the large-backup restore regression. |

The A04 guard covers the reproduced one-off charge invariant, including vehicle
and agreement extras. It is not a general rewrite of periodic billing or concurrent
repricing. Existing completed-period billing rules are unchanged. Its post-lock
check uses READ COMMITTED; other isolation levels deliberately fail closed.
Multi-source deadlocks roll back the whole transaction, never partial money state.

The Grafana update addresses [CVE-2025-4123](https://grafana.com/security/security-advisories/cve-2025-4123/).
This is not a certification that every package in every image is vulnerability-free.

## Verification

- Full ordinary Go suite passed against a fresh disposable PostgreSQL 16 database.
- Authentication regressions cover enrollment versus proof, successful TOTP and
  recovery login, missing proof, session rotation, last-factor removal, and clone
  warning versus suspension.
- Financial regressions cover the actual concurrent invoice/payment handlers and
  both overlapping claim-insertion directions. Reversal and anonymization
  regressions pass.
- Parent vehicle/person deletion preserves the signed handover and returns 409.
- A real archive larger than 32 MiB validates and restores with a 32 MiB /tmp and
  256 MiB memory limit. Wrong-key rejection and rollback after a real SQL failure
  also pass. The test refuses any database not named parkrr_test_backup.
- go vet, go build, JavaScript syntax checks, workflow actionlint, shell syntax,
  and rendered Compose validation passed. Rendered monitoring ports are loopback
  only; no Compose up command was used.
- Full Linux race-enabled suite passed against a separate fresh PostgreSQL 16
  database, with the checkout mounted read-only and CPU/memory bounded.

Tests used synthetic records only. No production backup, customer data, live
credentials, registry publication, external message, or service restart was used.

## Deployment is a separate, operator-controlled step

1. Review the changes on dev before syncing or merging. Do not deploy candidate
   image tags from the publishing workflow.
2. Take and verify backups using the existing operational process. Rehearse
   migrations and restore on a separate copy with production-like volume.
3. Before migration 069, have an authorized operator run this read-only check:

   ```sql
   SELECT s.ref_id AS charge_id, s.invoice_id, a.payment_id
   FROM invoice_source s
   JOIN invoices i ON i.id = s.invoice_id
   JOIN payment_allocations a ON a.kind = s.kind AND a.ref_id = s.ref_id
   WHERE s.kind = 'charge' AND s.period_key = '' AND NOT i.canceled;
   ```

   Any result requires financial reconciliation. Do not delete payment or invoice
   evidence automatically. Migration 069 refuses conflicting data rather than
   silently changing it; an unreviewed rollout could therefore fail startup.
4. Migration 067 defaults existing sessions to unverified. Sites with
   PARKRR_REQUIRE_2FA=true must announce reauthentication and ensure valid
   TOTP/recovery/passkey access. Initial enrollment is followed by fresh sign-in.
   Avoid mixing old MFA middleware and the new binary during rollout.
5. Migration 068 intentionally changes parent deletion: remove handovers only
   through the explicitly authorized admin workflow, or retain/archive the parent.
6. Stage Grafana against a copy of its volume. Validate provisioning, dashboards,
   plugins and CSP before switching. A downgrade needs its matching volume backup;
   do not run an old binary against a database already migrated by a newer release.
7. Verify any remote monitoring tunnel/proxy before changing operators' access.
   Loopback bindings do not isolate other containers already on parkrr-net.
8. Large backups still occupy memory during encryption/decryption. The stdin fix
   removes the temporary-disk limit, not the need for archive-size/memory planning.

## I01 — informational, operational work still required

The runtime currently owns database objects and applies migrations. In-database
triggers cannot provide tamper resistance against compromise of that administrator.
Changing this trust boundary is not a safe automatic configuration toggle.

Before claiming independent audit integrity:

- Provision separate runtime, migration/restore, and retention identities.
- Move migration execution out of the long-running runtime before reducing its
  privileges. Inventory bootstrap, sequences, maintenance jobs, anonymization,
  backups and restore; do not blindly revoke rights required by those paths.
- Keep the runtime from owning tables or schemas, acquiring an owner role,
  updating/deleting/truncating audit history, or changing its protection.
- Export security events to independently administered, retention-protected
  storage. Keep its deletion authority out of the application process and host.
- Monitor delivery failures and periodically verify exported chains and recovery.

These identities and the independent destination have not been provisioned.
They require operator review and a separately authorized deployment. NOSUPERUSER
alone, or another trigger owned by the same runtime, does not close this item.
