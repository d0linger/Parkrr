package database

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Retention categorisation guard.
//
// The audit trail has three tiers and the boundaries are a policy decision, not an
// implementation detail — so they are pinned here rather than left to whoever edits
// the slices next. The rule that matters most: the short tier is an ALLOW-LIST, so
// an action introduced later ages out on the LONG window by default. Losing the
// record of a change is far worse than keeping some noise.

func TestAuditRetentionTiersAreDisjoint(t *testing.T) {
	for _, a := range auditShortLivedActions {
		if slices.Contains(auditKeepForeverEntities, a) {
			t.Errorf("%q appears in both the short-lived actions and the keep-forever entities", a)
		}
	}
}

func TestKeepForeverCoversEveryRecordOfAccount(t *testing.T) {
	// Entities whose audit rows are (or explain) records of account under BAO §132.
	// For a person-level charge the audit row is the only record that a period was
	// paid, so dropping one of these silently destroys a payment proof.
	for _, want := range []string{"invoice", "payment", "billing", "flatrate", "recurring_charge"} {
		if !slices.Contains(auditKeepForeverEntities, want) {
			t.Errorf("entity %q must never be pruned but is missing from auditKeepForeverEntities", want)
		}
	}
}

func TestShortTierIsOnlyAuthAndOpsNoise(t *testing.T) {
	// Every short-lived action must be justifiable as "no business transaction".
	allowed := map[string]string{
		"login":  "authentication event",
		"logout": "authentication event",
		"backup": "operational task",
		"remind": "outgoing mail, the invoice itself is the record",
		"import": "bulk load; the created rows carry their own create entries",
	}
	for _, a := range auditShortLivedActions {
		if _, ok := allowed[a]; !ok {
			t.Errorf("action %q was added to the short window without a documented reason — "+
				"business changes must stay on the long window", a)
		}
	}
}

func TestSecurityRelevantActionsStayOnTheLongWindow(t *testing.T) {
	// These look like auth/ops at a glance but are exactly what an investigation
	// needs years later, so they must NOT be in the short tier.
	for _, a := range []string{
		"restore",                    // a database restore is the most invasive admin action there is
		"security",                   // passkey clone warning — an indication of compromise
		"create", "update", "delete", // the actual "who changed what" trail
		"revoke", // portal-link revocation
	} {
		if slices.Contains(auditShortLivedActions, a) {
			t.Errorf("action %q must stay on the long retention window", a)
		}
	}
}

// The short pass filters by ACTION only (see PruneAuditLog), so the allow-list is
// the sole thing standing between the retention job and a record of account. This
// pins that no action which can constitute one ever enters it.
func TestShortTierCannotReachRecordsOfAccount(t *testing.T) {
	for _, a := range []string{"create", "update", "delete", "restore", "revoke", "security"} {
		if slices.Contains(auditShortLivedActions, a) {
			t.Fatalf("action %q can carry a record of account (invoice/payment/billing/flatrate/"+
				"recurring_charge) and must never be short-lived — the short pass is not "+
				"entity-filtered", a)
		}
	}
}

// TestAuditDeletesStayBatched pins the shape that makes retention converge.
//
// audit_log carries a FOR EACH ROW plpgsql trigger and every pooled connection runs
// under statement_timeout=10s, so an unbounded DELETE has a cliff — and past it the
// failure mode is "never", not "slow": the statement is cancelled, the transaction
// rolls back, and ZERO rows are removed, so the next run faces the same backlog and
// fails identically. Measured on this deployment's Postgres against a clone of the
// table with the same trigger: 200k rows drained in 515 ms, while a single
// unbounded DELETE of 4M rows removed nothing at all, twice running. The batched
// form drained the same 4M rows completely.
//
// A reviewer cannot see any of that by reading a DELETE, hence this guard.
func TestAuditDeletesStayBatched(t *testing.T) {
	src, err := os.ReadFile("database.go")
	if err != nil {
		t.Fatalf("read database.go: %v", err)
	}
	// Match to the end of the raw-string SQL literal, not to the first ")" — the
	// first paren closes `ALL($2)`, well before the LIMIT this is looking for.
	stmts := regexp.MustCompile("(?s)DELETE FROM audit_log.*?`").FindAllString(string(src), -1)
	if len(stmts) == 0 {
		t.Fatal("no DELETE against audit_log found — has PruneAuditLog been renamed or moved?")
	}
	for _, s := range stmts {
		if !strings.Contains(s, "LIMIT") {
			t.Errorf("unbounded DELETE against audit_log — it must delete in batches or it will\n"+
				"silently stop making ANY progress once the backlog exceeds statement_timeout:\n%s", s)
		}
	}
}

// The batch must stay far enough inside statement_timeout that a slower disk, a
// colder cache or a bulkier `changes` payload cannot push one statement over it.
func TestAuditPruneBatchStaysWellInsideStatementTimeout(t *testing.T) {
	// ~390k rows/s measured => 20k rows is ~50 ms against a 10 s cap. Allow generous
	// headroom for a machine an order of magnitude slower than the one measured.
	if auditPruneBatch < 1000 || auditPruneBatch > 100000 {
		t.Fatalf("auditPruneBatch = %d is outside the range that keeps one DELETE safely inside "+
			"the 10s statement_timeout while still draining a backlog in reasonable time",
			auditPruneBatch)
	}
}

// TestBackupFailureActionKeepsTheLongWindow pins the split the backup scheduler
// relies on: a routine successful run is ops noise and may age out early, but a
// failed or unverified run must survive the short window.
//
// Without this, adding "backup_failed" to auditShortLivedActions — or renaming the
// action in internal/backup — would silently delete the answer to "when did the
// nightly backups stop?" after one year, while everything still compiles.
func TestBackupFailureActionKeepsTheLongWindow(t *testing.T) {
	if !slices.Contains(auditShortLivedActions, "backup") {
		t.Error(`"backup" should stay short-lived: a successful nightly run a year ago is noise`)
	}
	if slices.Contains(auditShortLivedActions, "backup_failed") {
		t.Fatal(`"backup_failed" must NOT be short-lived — a failed or unverified backup is ` +
			`exactly what an investigation needs years later`)
	}
	// The literal must match internal/backup.actionBackupFailed. The packages cannot
	// import each other, so the value is pinned on both sides instead.
	const actionBackupFailed = "backup_failed"
	if slices.Contains(auditShortLivedActions, actionBackupFailed) {
		t.Fatalf("%q is short-lived", actionBackupFailed)
	}
}
