package database

import (
	"slices"
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
