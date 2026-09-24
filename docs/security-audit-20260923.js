"use strict";

const findings = [
  {
    id: "INV-01", category: "Data invariants", severity: "High", exploitability: "Trivial",
    title: "Settled charges and vehicles remain mutable or deletable",
    target: "internal/handlers/charges.go:423–551 · internal/handlers/vehicles.go:398–608",
    blast: "Historical balances, settlement allocations, and issued financial evidence",
    summary: "The write guards focus on active invoice sources, but records already settled through direct payments can still be edited, reassigned, or deleted without reversing the ledger entry that paid them.",
    vector: ["Editor settles a vehicle or charge", "A payment and allocation preserve the old amount and owner", "The source record is later edited or deleted", "Live accrual diverges from immutable payment history"],
    impact: "A customer can appear to owe money already paid, or receive credit unsupported by the edited source. Deletion removes the operational explanation for a retained payment and produces accounting state that cannot be reconstructed from the UI.",
    reproduction: "Create a one-off charge, settle it through a payment allocation, then PATCH its amount/person or DELETE it. Compare the person balance, payment attribution, and retained allocation before and after.",
    remediation: ["Treat any source referenced by payment_allocations as financially locked, not only sources on active invoices.", "Route corrections through explicit reversal/replacement records in one transaction; never mutate settled principal fields in place.", "Add database constraints or guarded procedures so alternate callers cannot bypass the handler check."]
  },
  {
    id: "INV-02", category: "Data invariants", severity: "High", exploitability: "Trivial",
    title: "Agreement edits leave historical periodic settlements unreconciled",
    target: "internal/handlers/agreements.go:562–733",
    blast: "Every completed period and payment derived from an edited flat-rate agreement",
    summary: "Changing an agreement's amount, dates, end date, or vehicle membership does not consistently recompute already-created period payments and their auto-ledger entries.",
    vector: ["Settle one or more completed agreement periods", "Edit a billing-defining field", "Existing period rows keep their old monetary meaning", "Balance and payment ledger now use different agreement definitions"],
    impact: "Past payments may underpay or overpay the newly defined agreement, while reports continue to present both states as authoritative. Vehicle membership changes can also move costs between per-vehicle and flat-rate calculations retroactively.",
    reproduction: "Create a monthly agreement with two completed periods, mark them paid, then change its amount or covered vehicle set. Reload person statistics and compare stored period payments with recalculated cost.",
    remediation: ["Freeze every billing-defining field after the first settlement, not only after invoicing.", "If edits are required, post compensating reversal/replacement entries using the old and new snapshots.", "Store an immutable agreement revision identifier on every period payment and allocation."]
  },
  {
    id: "INV-03", category: "Data invariants", severity: "High", exploitability: "Trivial",
    title: "Vehicle ownership transfer splits financial state across customers",
    target: "internal/handlers/vehicles.go:398–513 · internal/handlers/stats.go:383–744",
    blast: "Both the former and new customer's balances, invoices, charges, and agreement coverage",
    summary: "Updating vehicle.person_id moves live rent accrual, while existing payments, bound charges, agreement links, and invoice sources retain their original customer references.",
    vector: ["Vehicle accrues or receives financial activity for customer A", "Editor changes person_id to customer B", "Related rows are not transferred or closed as a unit", "A's ledger and B's live accrual describe one vehicle differently"],
    impact: "Cross-customer balance contamination can create IDOR-like financial disclosure, misapplied credit, duplicate billing, or orphaned charges. The audit trail cannot prove a coherent transfer boundary.",
    reproduction: "Add a vehicle to customer A, accrue rent and a bound charge, record a partial payment, then edit the vehicle owner to customer B. Compare both customer overviews and open-item lists.",
    remediation: ["Disallow owner changes once any financial or agreement reference exists; expose a dedicated transfer workflow instead.", "Close the old ownership period and create a new vehicle/ownership revision for the new customer.", "Validate all related person_id values with deferrable database constraints or a transactional transfer procedure."]
  },
  {
    id: "INV-04", category: "Data invariants", severity: "Medium", exploitability: "Trivial",
    title: "Pay-all during an in-progress period creates no ledger payment",
    target: "internal/handlers/agreements.go:844–967",
    blast: "Current-period revenue, cash totals, and the agreement's apparent settlement state",
    summary: "The master paid flag becomes true, but SetAgreementPaid deliberately records payments only for completed sub-periods. The current partial period is treated as paid without a durable payment row.",
    vector: ["Agreement is inside its current month or year", "Operator marks the whole agreement paid", "Handler skips the incomplete period", "Paid UI state exists without corresponding money-in"],
    impact: "Cash reports understate received money and later period completion can expose an unpaid balance despite the agreement having been marked paid. Restart/recalculation changes what the same action appears to mean.",
    reproduction: "Create an agreement that began in the current month, toggle the master paid control, then inspect payments and total received. Advance the clock beyond period end and recalculate.",
    remediation: ["Reject full settlement while a non-final period exists, or require an explicit advance-payment amount.", "Represent advances as ledger payments/credit, then allocate them when the period becomes final.", "Derive the master paid state from durable allocations rather than storing an independent boolean truth."]
  },
  {
    id: "CNC-01", category: "Concurrency", severity: "High", exploitability: "Complex",
    title: "Agreement overlap check and insert are non-atomic",
    target: "internal/handlers/agreements.go:420–487 · 489–632",
    blast: "Rent calculation for any customer receiving concurrent overlapping agreements",
    summary: "validateAgreement checks for conflicting date windows before the write transaction establishes a serialization point. Two requests can both observe no overlap and both commit.",
    vector: ["Send two concurrent agreement creates for one person", "Both preflight checks read the same empty snapshot", "Each transaction inserts a valid row independently", "Cost logic receives overlapping coverage"],
    impact: "Which agreement covers a vehicle becomes order-dependent. Rent may be omitted or double-accounted, and later settlement/invoicing inherits an ambiguous source of truth.",
    reproduction: "Synchronize two POST requests with overlapping dates for the same person and release them together. Repeat until both return success, then list agreements and calculate rent.",
    remediation: ["Acquire a per-person transaction-scoped advisory lock before checking and writing, or serialize on the person row with SELECT FOR UPDATE.", "Prefer a PostgreSQL exclusion constraint over date ranges where the business rule can be expressed declaratively.", "Map constraint conflicts to deterministic HTTP 409 responses."]
  },
  {
    id: "CNC-02", category: "Concurrency", severity: "High", exploitability: "Complex",
    title: "Agreement extra settlement ignores a lost allocation claim",
    target: "internal/handlers/agreements.go:976–1081",
    blast: "Bound charge settlement, auto-payment totals, and customer credit",
    summary: "The extras path calculates a candidate total before claiming every charge. A concurrent allocator can win a unique allocation, while the agreement path continues with a stale amount or fails to reconcile all side effects.",
    vector: ["Agreement settlement selects open bound charges", "Concurrent payment claims one charge", "Allocation INSERT loses the uniqueness race", "Auto-payment amount and paid flags no longer match claimed rows"],
    impact: "The ledger can overstate cash or produce an orphan auto-payment, and a charge can be visually paid by a settlement that did not own its allocation.",
    reproduction: "Race SetAgreementPaid against CreatePayment allocating the same vehicle-bound charge. Inspect payments, payment_allocations, charge.paid, and person credit after both complete.",
    remediation: ["Claim candidate charges first with locked rows or INSERT … ON CONFLICT … RETURNING, then sum only successful claims.", "Set paid flags from the exact returned allocation set inside the same transaction.", "Treat a lost claim as a normal retry/conflict path, never as a partially successful settlement."]
  },
  {
    id: "CNC-03", category: "Concurrency", severity: "High", exploitability: "Complex",
    title: "Invoice snapshots and source claims straddle the transaction boundary",
    target: "internal/handlers/billing.go:300–542 · 645–819",
    blast: "Immutable invoice contents, gapless numbering, and all billed source positions",
    summary: "CreateInvoice derives open items and buyer data before beginning its transaction. Only the later source-claim inserts arbitrate concurrency, so mutable source values may change between snapshot construction and document issuance.",
    vector: ["Invoice request reads open items outside the transaction", "Concurrent request edits a source or customer", "Invoice transaction snapshots stale values", "Claim succeeds against the same source identity"],
    impact: "A legally immutable invoice can contain an amount, description, owner, or service period that no longer matches the committed source row, even though uniqueness guards prevent simple double billing.",
    reproduction: "Pause CreateInvoice after invoiceLines returns, update an unbilled charge or customer address, then let invoice creation commit. Compare invoice snapshots with committed source state.",
    remediation: ["Move source selection, customer snapshot, compliance checks, and claims into one repeatable-read/serializable transaction.", "Lock source rows before deriving invoice lines, then calculate totals from those locked snapshots.", "Retry serialization failures with a bounded policy and return 409 when the business snapshot changed."]
  },
  {
    id: "FLT-01", category: "Fault tolerance", severity: "High", exploitability: "Trivial",
    title: "Payment creation has no idempotency boundary",
    target: "internal/handlers/payments.go:637–729",
    blast: "Cash ledger, customer credit, invoice settlement, and automatic archival",
    summary: "A POST retry after a lost response always inserts a second payment. Transactional allocation prevents half-writes but cannot distinguish a replay from a distinct payment.",
    vector: ["Client submits a valid payment", "Server commits but response is lost", "Client or proxy retries the POST", "Second payment commits as new money"],
    impact: "Balances can jump into credit and invoices can be overpaid after ordinary network failure. Repair requires identifying and reversing one of two indistinguishable ledger entries.",
    reproduction: "Send CreatePayment with a short client timeout, let the server commit, then resend the identical body. Confirm two payment rows and doubled PaymentsTotal.",
    remediation: ["Require an Idempotency-Key for payment creation and persist it with a unique scope such as operator plus endpoint.", "Store the request fingerprint and original response in the same transaction as the payment.", "Return the original result for matching replays and reject key reuse with a different payload."]
  },
  {
    id: "SEC-01", category: "Authentication", severity: "Critical", exploitability: "Complex",
    title: "Database restore resurrects revoked sessions",
    target: "cmd/parkrr/restore.go:18–66 · internal/auth/sessions.go:40–76",
    blast: "Every account and every session revoked after the restored snapshot",
    summary: "Sessions are stored inside the database backup. Restoring an older snapshot also restores previously valid bearer tokens, undoing logout, password-change revocation, account disablement, and administrative session revocation.",
    vector: ["Attacker retains an old session cookie", "Operator revokes it or changes the password", "A backup predating revocation is restored", "Old token row becomes valid again"],
    impact: "An attacker regains authenticated access without credentials or user interaction. Admin sessions can reappear after an incident response that appeared to contain them.",
    reproduction: "Create session S, take a backup, revoke S, verify S is rejected, restore the backup, then replay S against /api/auth/me.",
    remediation: ["Keep a session-signing epoch or revocation generation outside restored application data and compare it on every request.", "After restore, atomically delete all sessions and force fresh authentication before serving traffic.", "Add a restore-generation marker and test that pre-restore cookies cannot authenticate afterward."]
  },
  {
    id: "SEC-02", category: "Authentication", severity: "High", exploitability: "Complex",
    title: "Split limiter checks admit synchronized login waves",
    target: "internal/handlers/auth.go:98–159 · internal/auth/ratelimit.go:51–109",
    blast: "Password, TOTP, and step-up brute-force resistance",
    summary: "Several authentication paths call Allowed and only record a failure after an expensive verification. Concurrent requests can all pass the pre-check before any of them increments the failure counter.",
    vector: ["Attacker opens many parallel requests for one key", "Every request observes failures below the limit", "All perform credential verification", "Failures are recorded only after the whole wave passed"],
    impact: "The effective attempt rate becomes limit × concurrency, increasing password/TOTP guessing capacity and CPU consumption while logs still show a configured lockout.",
    reproduction: "Hold N wrong-password requests at a barrier immediately after Allowed, then release them together with N much larger than maxFails. Count verifications completed before 429 begins.",
    remediation: ["Use a single Consume/Reserve operation that checks and increments under one lock before verification.", "Refund or reset the reservation on successful authentication instead of recording only failures afterward.", "Apply the atomic primitive consistently to login, TOTP, passkey finish, and step-up paths."]
  },
  {
    id: "SEC-03", category: "Authentication", severity: "High", exploitability: "Complex",
    title: "TOTP setup race can activate an attacker-known secret",
    target: "internal/handlers/twofactor.go:12–130",
    blast: "Second-factor ownership for one authenticated account",
    summary: "Setup overwrites the single pending secret, while enable reads whichever secret is current before a later row lock. Overlapping setup/enable ceremonies are not bound to a nonce or secret version.",
    vector: ["Victim begins setup and receives secret A", "Another authenticated request overwrites pending state with secret B", "Enable reads/validates B without ceremony binding", "Account activates a factor controlled by the second ceremony"],
    impact: "With a stolen authenticated session or confused concurrent tabs, an attacker can determine the TOTP secret that becomes authoritative and lock the user out or preserve persistence.",
    reproduction: "Start two TOTPSetup requests for one user, retain both secrets, and interleave TOTPEnable calls around secret updates. Observe that enablement is tied only to the latest row value, not the initiating ceremony.",
    remediation: ["Store pending enrollment rows keyed by an unguessable ceremony ID and bind enable to that ID plus user.", "Lock and consume the exact pending row during verification; reject superseded or already-consumed ceremonies.", "Require fresh step-up before setup as well as enable and revoke other pending ceremonies after success."]
  },
  {
    id: "SEC-04", category: "Authentication", severity: "Medium", exploitability: "Trivial",
    title: "Public WebAuthn begin permits database write amplification",
    target: "internal/handlers/passkeys.go:301–318 · internal/handlers/passkeys.go:42–93",
    blast: "webauthn_ceremonies table, database write capacity, and login availability",
    summary: "The username-less public begin endpoint persists a new ceremony before proof of possession. Its limiter checks prior failures, but successful begin calls do not consume that failure budget.",
    vector: ["Unauthenticated client calls passkey login begin", "Server creates challenge and database ceremony row", "Client never finishes or fails", "Repeat across IPs until expiry cleanup catches up"],
    impact: "Low-cost requests cause authenticated random generation plus database inserts, index churn, and cleanup load. Distributed callers can exhaust storage or pool capacity and degrade all logins.",
    reproduction: "Issue sustained POSTs to /api/auth/passkey/login/begin without finish requests, rotating source IPs if necessary. Measure ceremony-row growth and DB write latency over the five-minute TTL.",
    remediation: ["Keep anonymous login challenges in encrypted, integrity-protected cookies instead of persistent rows where feasible.", "Otherwise add a dedicated atomic begin-rate budget by IP/subnet and a hard cap on live anonymous ceremonies.", "Delete/replace the prior ceremony for the same cookie rather than appending one row per begin."]
  },
  {
    id: "SEC-05", category: "Authentication", severity: "High", exploitability: "Trivial",
    title: "Passkey-only accounts can delete their last credential",
    target: "internal/handlers/passkeys.go:254–279 · internal/auth/webauthn.go:320–328",
    blast: "Account availability and recovery for passkey-only users",
    summary: "DeletePasskey removes the selected credential without checking that another usable primary authenticator or password remains.",
    vector: ["Account operates without a usable password", "User or hijacked session deletes its only passkey", "Delete succeeds unconditionally", "No primary authentication method remains"],
    impact: "The legitimate user is permanently locked out and requires out-of-band administrative recovery. A session thief can convert temporary access into durable denial of service.",
    reproduction: "Create or configure a user with one passkey and no usable password recovery path, authenticate, delete that passkey, log out, and attempt to sign in again.",
    remediation: ["Within one transaction, lock the user credential set and reject deletion of the last usable primary factor.", "Require recent step-up with a different factor before removing a credential.", "Provide an explicit recovery enrollment path before allowing the final passkey to be removed."]
  },
  {
    id: "SEC-06", category: "Authentication", severity: "High", exploitability: "Complex",
    title: "Password update and session rotation are not atomic",
    target: "internal/handlers/auth.go:385–402 · internal/auth/sessions.go:58–76",
    blast: "All sessions for the user changing a password",
    summary: "ChangePassword commits the new hash before RotateSession separately deletes sessions and creates the replacement. Failure between those operations leaves the credential and session set in an unintended mixed state.",
    vector: ["Password hash UPDATE succeeds", "Session revocation or replacement insert fails", "Handler returns 500", "Old sessions may survive or user has no fresh session despite changed password"],
    impact: "A compromised session can remain valid after a security-driven password change, or the legitimate user can be locked out by an operation reported as failed even though the password changed.",
    reproduction: "Inject a database failure into RotateSession after the password UPDATE. Verify the response is 500, then test the old and new passwords plus every pre-existing session.",
    remediation: ["Move password update, session revocation, new-session insert, and audit event into one database transaction.", "Set the cookie only after commit; on commit failure leave both password and sessions unchanged.", "Add fault-injection tests at each statement boundary."]
  },
  {
    id: "CRY-01", category: "Cryptography", severity: "High", exploitability: "Complex",
    title: "Backup passphrases use a fast, unsalted key derivation",
    target: "internal/backup/backup.go:85–105",
    blast: "Confidentiality of every stolen encrypted backup made with a human passphrase",
    summary: "The AES-256-GCM key is SHA-256(context || passphrase). There is no per-archive salt, memory cost, or work factor, so guesses are cheap and identical passphrases derive identical keys across archives.",
    vector: ["Attacker obtains an encrypted backup", "Archive format exposes nonce and authentication tag", "Attacker hashes passphrase candidates rapidly offline", "A valid GCM tag confirms the correct guess"],
    impact: "Weak or reused operator passphrases can be cracked offline, exposing the complete customer, authentication, billing, and audit database with no further access to Parkrr.",
    reproduction: "Create a backup with a dictionary passphrase and benchmark candidate verification by deriving SHA-256 keys and attempting GCM authentication until the tag validates.",
    remediation: ["Adopt Argon2id or scrypt with a random per-archive salt and versioned parameters stored in an authenticated header.", "Tune memory/time cost for the deployment class and document a migration path that can still read v1 archives.", "Encourage randomly generated high-entropy backup keys and support key rotation."]
  },
  {
    id: "BKP-01", category: "Backup integrity", severity: "High", exploitability: "Complex",
    title: "Cross-replica S3 races can delete a restore point and report healthy",
    target: "internal/backup/schedule.go:188–252 · internal/backup/s3.go:218–269",
    blast: "Shared S3 retention set and the backup health shown by every replica",
    summary: "runMu serializes only one process. Multiple Parkrr replicas can upload and prune the same bucket concurrently, then independently overwrite the singleton S3 health row with whichever result finishes last.",
    vector: ["Two replicas start a scheduled S3 run", "Each lists a different bucket snapshot", "Both upload/prune without a distributed lease", "One deletes a peer's new object while the other records success"],
    impact: "A valid restore point can disappear even though the UI remains green. Retention guarantees become nondeterministic precisely in highly available deployments where operators expect redundancy.",
    reproduction: "Run two instances against the same database and bucket with keep=1, synchronize RunS3 after upload, then let both prune from their respective listings. Compare remaining object and backup_status.",
    remediation: ["Acquire a database advisory lease for the complete upload, verify, prune, and status-update sequence.", "Use collision-resistant object names containing replica/run IDs and prune only from a fresh post-upload listing while holding the lease.", "Track health per target/run, and never let an older completion overwrite a newer result."]
  },
  {
    id: "RST-01", category: "Recovery", severity: "Critical", exploitability: "Complex",
    title: "Online restore does not quiesce application traffic",
    target: "cmd/parkrr/restore.go:18–66 · internal/backup/backup.go:210–236",
    blast: "Entire PostgreSQL schema and every request served during restore",
    summary: "The restore command coordinates with backup creation inside its own process but has no distributed maintenance barrier that stops another running Parkrr instance from reading or writing the target database.",
    vector: ["Operator runs restore against a live database", "Other replicas keep accepting HTTP work", "pg_restore drops/replaces objects in one transaction", "Requests race against old, absent, or newly restored state"],
    impact: "Users can receive inconsistent responses, writes can fail or be lost, and external actions such as email may be emitted for transactions that disappear after restore. Revoked credentials may also become usable before containment runs.",
    reproduction: "Continuously create/update records through a running server while restoring a prior archive into the same database. Observe request errors and determine which acknowledged writes survive.",
    remediation: ["Require an external maintenance mode or distributed database lease that every request checks before restore begins.", "Drain all application replicas, terminate/deny non-restore database sessions, perform restore, then run post-restore invalidation before readiness turns green.", "Document and automate the offline restore runbook; refuse live restore unless an explicit verified maintenance token is present."]
  },
  {
    id: "ARC-01", category: "Resource safety", severity: "High", exploitability: "Complex",
    title: "Backup and restore retain multiple full-size archive copies",
    target: "internal/backup/backup.go:31–59 · 164–236 · internal/backup/schedule.go:83–232",
    blast: "Process memory, container availability, and all in-flight requests during archive work",
    summary: "Dump, encryption, decryption, validation, S3 download, and handler layers pass whole []byte archives. Several stages coexist, multiplying peak memory well beyond the compressed dump size.",
    vector: ["Database grows to a large compressed archive", "pg_dump is buffered fully", "Encryption/decryption allocates another full buffer", "Validation and upload/download add further copies"],
    impact: "A legitimate backup or restore can exceed the container memory limit, trigger OOMKill, interrupt all requests, and leave only partial local artifacts or ambiguous remote status.",
    reproduction: "Seed a database whose custom dump approaches the container memory budget, run a verified backup and restore under a memory limit, and record peak RSS plus termination state.",
    remediation: ["Stream pg_dump through chunked authenticated encryption directly to a temporary file or object upload.", "Validate and restore from bounded readers/files rather than decrypted []byte values.", "Enforce archive-size and free-space budgets before starting, with metrics for peak and expected resource use."]
  },
  {
    id: "DOS-01", category: "Resource safety", severity: "High", exploitability: "Trivial",
    title: "Cleared write deadlines let slow backup clients pin resources",
    target: "internal/handlers/backup.go:21–28 · 272–318",
    blast: "HTTP connections, archive file handles, goroutines, and outbound capacity",
    summary: "Backup download deliberately clears the server WriteTimeout and does not install a replacement deadline or minimum transfer rate. A slow or stalled authenticated client can hold the response indefinitely.",
    vector: ["Authenticated caller starts backup download", "Handler removes the write deadline", "Client reads a few bytes or stops", "Connection and archive resources remain pinned without an upper bound"],
    impact: "A small number of slow clients can exhaust connection or file-descriptor limits and degrade the application. Cancellation depends on transport behavior rather than an explicit response budget.",
    reproduction: "Request a large backup over a client that reads one byte every several minutes. Verify the connection survives beyond all server timeout settings and repeat until resource pressure is visible.",
    remediation: ["Replace the cleared deadline with a backup-specific absolute deadline renewed only while useful progress occurs.", "Enforce per-user concurrency limits and a minimum transfer-rate policy for archive responses.", "Prefer short-lived signed object downloads when S3 is configured, keeping application workers out of the data path."]
  },
  {
    id: "CNC-04", category: "Concurrency", severity: "Medium", exploitability: "Complex",
    title: "Photo and attachment quotas are check-then-insert races",
    target: "internal/handlers/photos.go:90–168 · internal/handlers/attachments.go:88–169",
    blast: "Per-owner object limits and database/blob storage consumption",
    summary: "Both upload paths count existing objects before inserting a new one without locking the owner or enforcing the limit in the database. Concurrent uploads can all observe room under the quota.",
    vector: ["Owner sits one item below quota", "Several uploads run concurrently", "Every COUNT sees the same value", "All inserts commit and exceed the configured limit"],
    impact: "Quota controls are unreliable under normal parallel browser uploads and can be intentionally bypassed to consume storage or inflate backup size.",
    reproduction: "Prepare a vehicle/person at limit−1 and release ten valid upload requests simultaneously. Count committed photos/attachments after all responses complete.",
    remediation: ["Serialize quota check and insert by locking the owner row in the upload transaction.", "For stronger enforcement, maintain an atomic counter with a CHECK constraint or use a trigger/advisory lock.", "Return 409/429 for losing requests and test the exact concurrent boundary."]
  },
  {
    id: "CNC-05", category: "Concurrency", severity: "Medium", exploitability: "Theoretical",
    title: "Global backup mutex waits ignore context cancellation",
    target: "internal/backup/schedule.go:98–101 · 188–191",
    blast: "Queued manual backups, restores, scheduler shutdown, and administrative HTTP requests",
    summary: "RunVolume and RunS3 call sync.Mutex.Lock before doing cancellation-aware work. A request whose context expires while another long backup holds the lock cannot stop waiting.",
    vector: ["Long backup acquires runMu", "Second request queues on Lock", "Second context is canceled or server shuts down", "Goroutine remains blocked until first backup completes"],
    impact: "Canceled clients and shutdowns retain goroutines and request resources. A stuck external command can turn the wait into an unbounded zombie queue.",
    reproduction: "Hold runMu with a deliberately stalled dump, start another backup with a one-second context, cancel it, and observe that the function does not return at the deadline.",
    remediation: ["Use a capacity-one channel/semaphore acquired with select on ctx.Done instead of sync.Mutex.", "Bound the complete queue plus execution lifetime and expose busy as 409/429 rather than silently waiting.", "Separate volume and S3 locks only if their shared dump/restore invariants remain protected."]
  },
  {
    id: "FLT-02", category: "Fault tolerance", severity: "High", exploitability: "Theoretical",
    title: "Migration unlock failure can strand an advisory lock in the pool",
    target: "internal/database/database.go:155–190",
    blast: "Schema migration and startup of every Parkrr replica using the database",
    summary: "The deferred pg_advisory_unlock uses a bounded best-effort call and discards its error before releasing the pooled connection. If unlock fails while the connection remains alive, that session can retain the lock invisibly.",
    vector: ["Migration acquires session advisory lock", "Unlock times out or fails", "Error is ignored", "Connection returns to pool still owning the lock"],
    impact: "Later migration attempts can block indefinitely behind a lock held by an apparently idle pooled connection. Rolling deployments may stall all new replicas with little diagnostic evidence.",
    reproduction: "Fault-inject failure/timeout into pg_advisory_unlock while keeping the connection usable, release it to the pool, then attempt a second migration from another connection.",
    remediation: ["If unlock cannot be confirmed, close/evict the physical connection instead of returning it to the pool.", "Log and surface unlock failure as a startup error with the lock key and backend PID.", "Prefer transaction-scoped advisory locks where migration transaction boundaries permit it."]
  },
  {
    id: "FLT-03", category: "Fault tolerance", severity: "Medium", exploitability: "Theoretical",
    title: "Audit-retention failure is log-only while health stays green",
    target: "internal/server/observability.go:236–277",
    blast: "Audit-table growth, database capacity, and operator confidence",
    summary: "Repeated retention errors are logged and optionally audited into the same database, but readiness/health has no stale-or-failed retention signal.",
    vector: ["Retention prune begins failing", "Scheduler logs warning and continues", "Health endpoints still report ready", "Audit table grows until storage or query performance degrades"],
    impact: "Monitoring based on health checks cannot distinguish compliant retention from a dead maintenance loop. The system may fail later from disk exhaustion while having advertised normal operation.",
    reproduction: "Revoke DELETE permission or force the audit guard to reject retention, wait for a run, then query /health and /ready while observing the unchanged audit row count.",
    remediation: ["Persist last-success, last-attempt, rows-pruned, and last-error for each maintenance job.", "Expose stale/failing maintenance in authenticated metrics and a degraded health detail without leaking sensitive errors.", "Alert after a bounded failure age and stop writing recursive failure audits when capacity is endangered."]
  },
  {
    id: "LFC-01", category: "Lifecycle", severity: "Medium", exploitability: "Theoretical",
    title: "Shutdown signals background jobs but does not join them",
    target: "cmd/parkrr/main.go:244–285",
    blast: "Scheduled backups, expiry cleanup, auto-invoicing, audit retention, and process termination",
    summary: "The process closes cleanupStop and immediately drains HTTP, but it does not wait for worker goroutines to acknowledge cancellation and finish critical sections before run returns.",
    vector: ["Background job is mid-transaction or external I/O", "SIGTERM closes the stop channel", "HTTP shutdown completes first", "Process exits while job cleanup or status/audit write is pending"],
    impact: "Jobs may be cut off after performing external side effects but before recording status, leaving ambiguous backups, emails, or maintenance state. Deploys can repeatedly interrupt long work.",
    reproduction: "Start a slow scheduled backup or auto-invoice run, send SIGTERM, and observe whether main exits before the worker records its final state and releases resources.",
    remediation: ["Run workers under an errgroup or WaitGroup with a shared cancellable context and wait during shutdown.", "Give worker drain a bounded deadline separate from HTTP drain, then report which worker exceeded it.", "Make every job restart-safe with durable run IDs and recovery of incomplete external actions."]
  },
  {
    id: "AUD-01", category: "Audit integrity", severity: "High", exploitability: "Complex",
    title: "Financial mutations and audit evidence are not uniformly atomic",
    target: "internal/handlers/charges.go:386–551 · internal/handlers/vehicles.go:398–608 · internal/handlers/agreements.go:720–724",
    blast: "Forensic completeness and non-repudiation across operational financial records",
    summary: "Several mutations commit through the pool and write their audit event afterward as a separate best-effort operation. Some child vehicle/status events are explicitly post-commit.",
    vector: ["Financial or ownership mutation commits", "Process dies or audit INSERT fails", "Client may receive success or retry", "Authoritative state exists without corresponding evidence"],
    impact: "Investigators cannot reliably reconstruct who changed a billable record. A malicious or accidental high-value edit can land during an audit outage and become indistinguishable from historical data.",
    reproduction: "Inject an audit-table failure or kill the process immediately after a charge/vehicle mutation commit and before its audit call. Compare business state with audit_log.",
    remediation: ["Require every money/ownership mutation to call auditChangeTx inside the same database transaction.", "Make audit failure abort the business transaction for keep-forever entities.", "Add coverage that maps every mutating endpoint to an in-transaction audit write and fault-tests the boundary."]
  },
  {
    id: "ARC-02", category: "Architecture", severity: "Medium", exploitability: "Trivial",
    title: "Overview reconstructs the full financial dataset per request",
    target: "internal/handlers/stats.go:788–1222",
    blast: "Dashboard latency, database pool, CPU, and memory for every operator",
    summary: "Overview loads all vehicles, people names, agreements, charges, payments, recurring data, and invoice state, then performs substantial accrual aggregation in Go for each request.",
    vector: ["Dataset grows across seasons", "Any operator opens or refreshes dashboard", "Handler materializes whole tables and nested maps", "Concurrent viewers repeat identical global work"],
    impact: "Latency and memory scale with total history rather than the requested summary. A few refresh loops can saturate DB connections and CPU, starving transactional workflows.",
    reproduction: "Seed increasing numbers of people, vehicles, agreements, and payments, benchmark /api/overview at concurrency, and plot latency/allocations against total rows.",
    remediation: ["Move set-based totals into targeted SQL aggregates with indexed date predicates and only fetch top-N detail rows.", "Cache or materialize expensive global summaries by year and invalidate them on relevant writes.", "Set response budgets and instrument query count, rows scanned, allocations, and wall time."]
  },
  {
    id: "SEM-01", category: "Input semantics", severity: "Low", exploitability: "Trivial",
    title: "Filename byte truncation can split UTF-8",
    target: "internal/handlers/photos.go:141–142 · internal/handlers/attachments.go:149–151",
    blast: "Uploaded filename display, JSON encoding, exports, and audit readability",
    summary: "Upload handlers truncate filenames by byte slice at 200 bytes. A multibyte rune crossing that boundary becomes invalid UTF-8.",
    vector: ["Upload filename exceeds 200 bytes with a multibyte rune at boundary", "Handler slices raw string bytes", "Invalid UTF-8 is stored", "Later JSON/UI/export layers replace or reject text"],
    impact: "Names render with replacement characters, can fail strict encoders or downstream imports, and no longer round-trip to the user-supplied value.",
    reproduction: "Upload a valid filename composed so a three- or four-byte rune starts at byte 199 or 200, then retrieve metadata and export it.",
    remediation: ["Truncate by rune boundary with utf8.ValidString/utf8.RuneStart or range iteration.", "Apply the limit after normalizing the basename and preserve the extension separately.", "Add boundary tests for 1–4 byte code points and invalid input normalization."]
  },
  {
    id: "SEM-02", category: "Input semantics", severity: "Medium", exploitability: "Trivial",
    title: "Non-positive charge quantity silently becomes one",
    target: "internal/handlers/charges.go:338–356",
    blast: "Charge totals, API client correctness, and invoice amounts",
    summary: "validateCharge rewrites every quantity ≤ 0 to 1 instead of rejecting invalid input. Negative quantities and explicit zero are converted into a positive billable line.",
    vector: ["Client submits quantity 0 or negative", "Validation normalizes it to 1", "Charge is stored successfully", "Customer is billed for an item the caller tried to omit or reverse"],
    impact: "Malformed integrations create silent overcharges. The 201 response hides the caller bug, making reconciliation and dispute diagnosis harder.",
    reproduction: "POST a charge with quantity 0 and again with -2. Verify both succeed and persist quantity 1 with a positive line total.",
    remediation: ["Default quantity only when the JSON field is absent; represent presence with a pointer or custom decoder.", "Reject explicit quantity ≤ 0 with HTTP 400 and a field-specific error.", "Add API tests distinguishing omitted, zero, negative, fractional, and maximum values."]
  },
  {
    id: "INF-01", category: "Deployment", severity: "Medium", exploitability: "Trivial",
    title: "Default Compose exposes unauthenticated metrics",
    target: "docker-compose.yml:88–94 · internal/server/observability.go:127–168",
    blast: "Operational metadata for any network that can reach the default service port",
    summary: "The default configuration permits /metrics without a bearer token unless the operator explicitly sets a token or flips fail-closed authentication.",
    vector: ["Operator deploys the sample Compose configuration", "Service port is reachable beyond a trusted monitoring network", "Metrics token is empty by default", "Remote caller scrapes process and database telemetry"],
    impact: "Attackers gain request-volume, route, latency, error, and pool-pressure intelligence useful for reconnaissance and targeted denial of service. Metric cardinality changes can leak operational behavior.",
    reproduction: "Start the default docker-compose.yml without PARKRR_METRICS_TOKEN and request /metrics from another reachable host; verify a 200 response without Authorization.",
    remediation: ["Set PARKRR_METRICS_REQUIRE_AUTH=true in the default Compose file and fail closed when no token exists.", "Bind metrics to a separate internal listener/network where possible.", "Document an explicit opt-out for trusted single-host setups instead of shipping exposure as the default."]
  },
  {
    id: "INF-02", category: "Information exposure", severity: "High", exploitability: "Trivial",
    title: "Portal bearer token persists in browser history",
    target: "web/static/js/app.js:9299–9340 · portal hash route",
    blast: "Read-only customer portal access to balances, vehicles, invoices, and PDFs",
    summary: "API requests correctly move the token into Authorization, but the bootstrap route remains #/portal/&lt;token&gt;. URL fragments are retained in local browser history and can appear in screenshots, sync, support captures, or shoulder-surfing.",
    vector: ["Customer opens magic link with token in fragment", "Portal keeps the fragment as active route", "Browser stores/syncs the history entry", "Someone with history access replays the bearer token"],
    impact: "Possession of the history entry grants portal access until expiry or revocation. Shared devices and synced browser profiles expand the token's audience beyond the intended recipient.",
    reproduction: "Open a portal link, navigate away or close the tab, then inspect browser history and reopen the entry. Confirm the original token still boots the portal.",
    remediation: ["Read the token once into memory/sessionStorage, then immediately history.replaceState to a token-free route.", "Use a short-lived bootstrap token exchanged for a same-site HttpOnly scoped session where the product flow permits it.", "Keep Referrer-Policy/no-store controls and offer explicit portal sign-out that clears local state."]
  }
];

const severityOrder = { Critical: 0, High: 1, Medium: 2, Low: 3, Info: 4 };
const severityNames = ["Critical", "High", "Medium", "Low"];
const statusLabels = { unreviewed: "Unreviewed", accepted: "Accepted", progress: "In progress", resolved: "Resolved" };
const statusOrder = { progress: 0, accepted: 1, unreviewed: 2, resolved: 3 };
const storageKey = "parkrr-audit-triage-20260923";
const themeKey = "parkrr-audit-theme";
const $ = (selector, root = document) => root.querySelector(selector);

function make(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs)) {
    if (key === "class") node.className = value;
    else if (key === "text") node.textContent = value;
    else if (key === "style") node.setAttribute("style", value);
    else if (key.startsWith("on") && typeof value === "function") node.addEventListener(key.slice(2), value);
    else if (value !== null && value !== undefined) node.setAttribute(key, String(value));
  }
  for (const child of children.flat()) if (child) node.append(child.nodeType ? child : document.createTextNode(String(child)));
  return node;
}

function svgNode(tag, attrs = {}, ...children) {
  const node = document.createElementNS("http://www.w3.org/2000/svg", tag);
  for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, String(value));
  for (const child of children.flat()) if (child) node.append(child);
  return node;
}

function icon(kind) {
  const svg = svgNode("svg", { viewBox: "0 0 24 24", "aria-hidden": "true" });
  const paths = {
    copy: ["M8 8h11v11H8z", "M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"],
    link: ["M10 14a4.5 4.5 0 0 0 6.4.1l2-2a4.5 4.5 0 0 0-6.4-6.4l-1.1 1.1", "M14 10a4.5 4.5 0 0 0-6.4-.1l-2 2a4.5 4.5 0 0 0 6.4 6.4l1.1-1.1"],
    back: ["M19 12H5", "m11 18-6-6 6-6"]
  };
  for (const d of paths[kind]) svg.append(svgNode("path", { d }));
  return svg;
}

function readStored(key, fallback) {
  try { return JSON.parse(localStorage.getItem(key)) || fallback; } catch { return fallback; }
}

const triage = readStored(storageKey, {});
const state = { query: "", severities: new Set(severityNames), category: "all", status: "all", sort: "severity", selected: null };
let visible = [];
let toastTimer;

function statusOf(id) { return triage[id] || "unreviewed"; }
function severityVar(severity) { return `var(--${severity.toLowerCase()})`; }
function searchable(f) { return [f.id, f.category, f.severity, f.exploitability, f.title, f.target, f.blast, f.summary, f.impact, f.reproduction, ...f.vector, ...f.remediation].join(" ").toLowerCase(); }

function renderSummary() {
  const summary = $("#summary");
  summary.replaceChildren(make("div", { class: "risk-summary" }, make("strong", { text: findings.length }), make("span", { text: "Open diagnostic findings" })));
  for (const severity of severityNames) {
    const count = findings.filter((f) => f.severity === severity).length;
    const active = state.severities.size === 1 && state.severities.has(severity);
    const button = make("button", { class: "severity-summary", type: "button", "aria-pressed": active, style: `--severity:${severityVar(severity)}`, onclick: () => {
      state.severities = active ? new Set(severityNames) : new Set([severity]);
      syncSeverityChecks(); render();
    } }, make("strong", { text: count }), make("span", { text: severity }));
    summary.append(button);
  }
}

function buildFilters() {
  const counts = Object.fromEntries(severityNames.map((s) => [s, findings.filter((f) => f.severity === s).length]));
  const host = $("#severity-filters");
  for (const severity of severityNames) {
    const input = make("input", { type: "checkbox", value: severity, checked: "", onchange: () => {
      input.checked ? state.severities.add(severity) : state.severities.delete(severity);
      render();
    } });
    const label = make("label", { class: "check-filter", style: `--severity:${severityVar(severity)}` }, input,
      make("span", { class: "filter-label" }, make("span", { class: "filter-dot", "aria-hidden": "true" }), severity),
      make("span", { class: "filter-count", text: counts[severity] }));
    host.append(label);
  }
  const categories = [...new Set(findings.map((f) => f.category))].sort();
  for (const category of categories) $("#category-filter").append(make("option", { value: category, text: category }));
}

function syncSeverityChecks() {
  document.querySelectorAll("#severity-filters input").forEach((input) => { input.checked = state.severities.has(input.value); });
}

function filtered() {
  const needle = state.query.trim().toLowerCase();
  const rows = findings.filter((f) => state.severities.has(f.severity) && (state.category === "all" || f.category === state.category) && (state.status === "all" || statusOf(f.id) === state.status) && (!needle || searchable(f).includes(needle)));
  rows.sort((a, b) => {
    if (state.sort === "id") return a.id.localeCompare(b.id, undefined, { numeric: true });
    if (state.sort === "category") return a.category.localeCompare(b.category) || severityOrder[a.severity] - severityOrder[b.severity] || a.id.localeCompare(b.id);
    if (state.sort === "status") return statusOrder[statusOf(a.id)] - statusOrder[statusOf(b.id)] || severityOrder[a.severity] - severityOrder[b.severity];
    return severityOrder[a.severity] - severityOrder[b.severity] || a.id.localeCompare(b.id, undefined, { numeric: true });
  });
  return rows;
}

function renderList() {
  visible = filtered();
  const host = $("#finding-list");
  host.replaceChildren();
  $("#result-count").textContent = `${visible.length} of ${findings.length} visible`;
  $("#empty-state").hidden = visible.length !== 0;
  if (visible.length && !visible.some((f) => f.id === state.selected)) state.selected = visible[0].id;
  if (!visible.length) state.selected = null;
  visible.forEach((f) => {
    const status = statusOf(f.id);
    const button = make("button", { class: "finding-row", type: "button", role: "option", "aria-selected": f.id === state.selected, "data-id": f.id, style: `--severity:${severityVar(f.severity)}`, onclick: () => selectFinding(f.id, true) },
      make("span", { class: "finding-id", text: f.id }),
      make("span", { class: "finding-copy" }, make("span", { class: "finding-title", text: f.title }), make("span", { class: "finding-target", text: f.target })),
      make("span", { class: "finding-foot" }, make("span", { class: "triage-mark", "data-status": status, "aria-hidden": "true" }), `${f.category} · ${statusLabels[status]}`));
    host.append(button);
  });
}

function section(title, content) { return make("section", { class: "dossier-section" }, make("h3", { text: title }), content); }

function findingMarkdown(f) {
  return `**[${f.id}] ${f.title}**\n- **Severity & Exploitability:** ${f.severity} | ${f.exploitability}\n- **Target:** \`${f.target}\`\n- **Blast Radius:** ${f.blast}\n- **Vulnerability / Flaw Vector:** ${f.vector.join(" → ")}\n- **Failure Mode & Impact:** ${f.impact}\n- **Reproduction Trigger:** ${f.reproduction}\n- **Remediation Strategy:**\n${f.remediation.map((x) => `  - ${x}`).join("\n")}`;
}

function renderDossier() {
  const host = $("#dossier");
  host.replaceChildren();
  const f = findings.find((item) => item.id === state.selected);
  if (!f) {
    host.append(make("div", { class: "dossier-empty" }, make("div", { class: "dossier-empty-inner" },
      svgNode("svg", { viewBox: "0 0 96 96", "aria-hidden": "true" }, svgNode("path", { d: "M16 76V34l32-18 32 18v42M27 76V42h42v34M37 76V57h22v19M13 76h70" })),
      make("h2", { text: "Select a finding" }), make("p", { text: "Its evidence chain, production impact, reproduction trigger, and remediation plan will appear here." }))));
    return;
  }
  host.style.setProperty("--severity", severityVar(f.severity));
  const back = make("button", { class: "mini-button mobile-back", type: "button", onclick: closeMobileDossier }, icon("back"), "Back to findings");
  const copyButton = make("button", { class: "mini-button", type: "button", "aria-label": `Copy ${f.id} as Markdown`, title: "Copy finding as Markdown", onclick: () => copyText(findingMarkdown(f), `${f.id} copied`) }, icon("copy"), make("span", { text: "Copy" }));
  const linkButton = make("button", { class: "mini-button", type: "button", "aria-label": `Copy deep link to ${f.id}`, title: "Copy deep link", onclick: () => copyText(location.href.split("#")[0] + "#" + f.id, "Deep link copied") }, icon("link"), make("span", { text: "Link" }));
  const head = make("header", { class: "finding-head" },
    make("div", { class: "finding-head-top" }, make("span", { class: "finding-code", text: f.id }), make("div", { class: "finding-head-actions" }, copyButton, linkButton)),
    make("h2", { text: f.title }), make("p", { class: "finding-deck", text: f.summary }),
    make("div", { class: "fact-line" }, make("span", { class: "severity-chip", style: `--severity:${severityVar(f.severity)}`, text: f.severity }), make("span", { class: "fact" }, "Exploitability", make("strong", { text: f.exploitability })), make("span", { class: "fact" }, "Domain", make("strong", { text: f.category }))),
    make("div", { class: "target-block" }, make("code", { text: f.target }), make("button", { class: "mini-button", type: "button", onclick: () => copyText(f.target, "Target copied") }, icon("copy"), "Copy target")));
  const trace = make("ol", { class: "trace", style: `--steps:${f.vector.length}` }, f.vector.map((step) => make("li", { text: step })));
  const remediations = make("ul", { class: "remediation-list" }, f.remediation.map((item) => make("li", { text: item })));
  const triageOptions = make("div", { class: "triage-options" });
  for (const [value, label] of Object.entries(statusLabels)) triageOptions.append(make("button", { class: "triage-option", type: "button", "aria-pressed": statusOf(f.id) === value, onclick: () => setTriage(f.id, value) }, label));
  host.append(back, head, section("Failure path", trace), section("Blast radius", make("p", { text: f.blast })), section("Production impact", make("p", { text: f.impact })), section("Reproduction", make("p", { text: f.reproduction })), section("Remediation", remediations), make("section", { class: "triage-panel" }, make("h3", { text: "Local triage" }), triageOptions));
}

function selectFinding(id, userInitiated = false) {
  if (!findings.some((f) => f.id === id)) return;
  state.selected = id;
  history.replaceState(null, "", `#${id}`);
  renderList(); renderDossier(); renderSummary();
  if (matchMedia("(max-width: 920px)").matches) openMobileDossier();
}

const mobileBackground = [".skip-link", ".topbar", ".intro", ".risk-strip", ".controls", ".register"];
function openMobileDossier() {
  const dossier = $("#dossier");
  dossier.classList.add("is-open");
  dossier.setAttribute("role", "dialog");
  dossier.setAttribute("aria-modal", "true");
  document.body.style.overflow = "hidden";
  mobileBackground.forEach((selector) => $(selector)?.setAttribute("inert", ""));
  $(".mobile-back", dossier)?.focus();
}

function closeMobileDossier() {
  const dossier = $("#dossier");
  dossier.classList.remove("is-open");
  dossier.removeAttribute("role");
  dossier.removeAttribute("aria-modal");
  document.body.style.overflow = "";
  mobileBackground.forEach((selector) => $(selector)?.removeAttribute("inert"));
  document.querySelector(`[data-id="${state.selected}"]`)?.focus();
}

function setTriage(id, value) {
  if (value === "unreviewed") delete triage[id]; else triage[id] = value;
  try { localStorage.setItem(storageKey, JSON.stringify(triage)); } catch { showToast("Status changed for this session only"); }
  render(); showToast(`${id}: ${statusLabels[value]}`);
}

function resetFilters() {
  state.query = ""; state.severities = new Set(severityNames); state.category = "all"; state.status = "all";
  $("#search").value = ""; $("#category-filter").value = "all"; $("#status-filter").value = "all"; syncSeverityChecks(); render();
}

function render() { renderSummary(); renderList(); renderDossier(); }

async function copyText(text, message) {
  try { await navigator.clipboard.writeText(text); }
  catch {
    const area = make("textarea", { class: "sr-only" }); area.value = text; document.body.append(area); area.select(); document.execCommand("copy"); area.remove();
  }
  showToast(message);
}

function showToast(message) {
  const toast = $("#toast"); clearTimeout(toastTimer); toast.textContent = message; toast.hidden = false;
  toastTimer = setTimeout(() => { toast.hidden = true; }, 2600);
}

function exportTriage() {
  const payload = { audit: "Parkrr whole-codebase static audit", commit: "6c7adf9", reviewed: "2026-09-23", exported_at: new Date().toISOString(), findings: findings.map((f) => ({ id: f.id, status: statusOf(f.id), severity: f.severity, category: f.category, title: f.title })) };
  const url = URL.createObjectURL(new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" }));
  const link = make("a", { href: url, download: "parkrr-audit-triage-20260923.json" }); document.body.append(link); link.click(); link.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000); showToast("Triage JSON exported");
}

function moveSelection(delta) {
  if (!visible.length) return;
  const current = Math.max(0, visible.findIndex((f) => f.id === state.selected));
  const next = visible[(current + delta + visible.length) % visible.length]; selectFinding(next.id); document.querySelector(`[data-id="${next.id}"]`)?.focus();
}

buildFilters();
const savedTheme = readStored(themeKey, null);
if (savedTheme === "light" || savedTheme === "dark") document.documentElement.dataset.theme = savedTheme;
const hashID = location.hash.slice(1).toUpperCase();
state.selected = findings.some((f) => f.id === hashID) ? hashID : null;

$("#search").addEventListener("input", (event) => { state.query = event.target.value; render(); });
$("#category-filter").addEventListener("change", (event) => { state.category = event.target.value; render(); });
$("#status-filter").addEventListener("change", (event) => { state.status = event.target.value; render(); });
$("#sort-findings").addEventListener("change", (event) => { state.sort = event.target.value; render(); });
$("#reset-filters").addEventListener("click", resetFilters);
$("#empty-state .reset-button").addEventListener("click", resetFilters);
$("#copy-report").addEventListener("click", () => copyText(filtered().map(findingMarkdown).join("\n\n---\n\n"), `${filtered().length} findings copied`));
$("#export-json").addEventListener("click", exportTriage);
$("#theme-toggle").addEventListener("click", () => {
  const theme = document.documentElement.dataset.theme === "dark" ? "light" : "dark"; document.documentElement.dataset.theme = theme;
  $("#theme-toggle").setAttribute("aria-label", `Switch to ${theme === "dark" ? "light" : "dark"} theme`);
  try { localStorage.setItem(themeKey, JSON.stringify(theme)); } catch { /* preference remains for this page */ }
});
window.addEventListener("hashchange", () => { const id = location.hash.slice(1).toUpperCase(); if (findings.some((f) => f.id === id)) selectFinding(id, true); });
document.addEventListener("keydown", (event) => {
  const typing = /^(INPUT|SELECT|TEXTAREA)$/.test(document.activeElement?.tagName);
  if (event.key === "/" && !typing) { event.preventDefault(); $("#search").focus(); }
  else if (event.key === "Escape") { if ($("#dossier").classList.contains("is-open")) closeMobileDossier(); else if (state.query) { state.query = ""; $("#search").value = ""; render(); } }
  else if (!typing && event.key.toLowerCase() === "j") { event.preventDefault(); moveSelection(1); }
  else if (!typing && event.key.toLowerCase() === "k") { event.preventDefault(); moveSelection(-1); }
  else if (!typing && event.key === "Enter" && document.activeElement?.matches(".finding-row")) { event.preventDefault(); selectFinding(document.activeElement.dataset.id, true); }
});

render();
if (hashID && matchMedia("(max-width: 920px)").matches) openMobileDossier();
