//go:build integration

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

func TestBankAssignmentFailureRollsBackMoneyAndAudit(t *testing.T) {
	h := testHandler(t)
	pid, _, _ := seasonFixture(t, h, "")
	withAddress(t, h, pid)
	iv := mustInvoice(t, h, pid)
	bankID := bankIntegrityFixture(t, h)
	body := fmt.Sprintf(`{"invoice_id":%d}`, iv.ID)
	request := func() *httptest.ResponseRecorder {
		return callJSONRaw(h, h.AssignBankTransaction, http.MethodPost, "/api/bank-transactions/x/assign", strconv.FormatInt(bankID, 10), body)
	}
	auditBefore := integrityCount(t, h, `SELECT count(*) FROM audit_log`)
	removeFailure := integrityFailPaymentLink(t, h, "bank_transactions", fmt.Sprintf("NEW.id = %d", bankID))
	if w := request(); w.Code != http.StatusInternalServerError {
		t.Fatalf("injected bank link failure: %d %s", w.Code, w.Body.String())
	}
	integrityAssertInvoiceMoney(t, h, pid, iv.ID, 0, 0)
	if got := integrityCount(t, h, `SELECT count(*) FROM audit_log`); got != auditBefore {
		t.Errorf("failed assignment changed audit count: got %d, want %d", got, auditBefore)
	}
	var status string
	var paymentID, personID *int64
	if err := h.Pool.QueryRow(t.Context(), `SELECT status,payment_id,person_id FROM bank_transactions WHERE id=$1`, bankID).
		Scan(&status, &paymentID, &personID); err != nil {
		t.Fatal(err)
	}
	if status != "offen" || paymentID != nil || personID != nil {
		t.Errorf("failed assignment changed bank receipt: status=%q payment=%v person=%v", status, paymentID, personID)
	}

	removeFailure()
	w := request()
	if w.Code != http.StatusOK {
		t.Fatalf("retry assignment: %d %s", w.Code, w.Body.String())
	}
	newPaymentID := bankIntegrityPaymentID(t, w)
	integrityAssertInvoiceMoney(t, h, pid, iv.ID, 1, 50)
	if err := h.Pool.QueryRow(t.Context(), `SELECT status,payment_id,person_id FROM bank_transactions WHERE id=$1`, bankID).
		Scan(&status, &paymentID, &personID); err != nil {
		t.Fatal(err)
	}
	if status != "zugeordnet" || paymentID == nil || *paymentID != newPaymentID || personID == nil || *personID != pid {
		t.Errorf("successful retry did not save its bank link: status=%q payment=%v person=%v", status, paymentID, personID)
	}
	if w := request(); w.Code != http.StatusConflict {
		t.Errorf("duplicate assignment = %d, want 409: %s", w.Code, w.Body.String())
	}
	integrityAssertInvoiceMoney(t, h, pid, iv.ID, 1, 50)
}

func TestBankPaymentReversalConflictPreservesMoney(t *testing.T) {
	h := testHandler(t)
	pid, _, _ := seasonFixture(t, h, "")
	withAddress(t, h, pid)
	iv := mustInvoice(t, h, pid)
	bankID := bankIntegrityFixture(t, h)
	w := callJSONRaw(h, h.AssignBankTransaction, http.MethodPost, "/api/bank-transactions/x/assign", strconv.FormatInt(bankID, 10),
		fmt.Sprintf(`{"invoice_id":%d}`, iv.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("initial assignment: %d %s", w.Code, w.Body.String())
	}
	paymentID := bankIntegrityPaymentID(t, w)
	tx, err := h.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := lockBankTransactionTx(t.Context(), tx, bankID); err != nil {
		t.Fatal(err)
	}
	auditBefore := integrityCount(t, h, `SELECT count(*) FROM audit_log`)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	r := httptest.NewRequest(http.MethodDelete, "/api/payments/x", nil).WithContext(ctx)
	r.SetPathValue("id", strconv.FormatInt(paymentID, 10))
	w = httptest.NewRecorder()
	h.DeletePayment(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("busy bank reversal: %d %s", w.Code, w.Body.String())
	}
	if ctx.Err() != nil {
		t.Fatal("busy bank reversal waited for its request deadline")
	}
	integrityAssertInvoiceMoney(t, h, pid, iv.ID, 1, 50)
	if got := integrityCount(t, h, `SELECT count(*) FROM payments WHERE id=$1 AND NOT reversed`, paymentID); got != 1 {
		t.Error("conflicted reversal changed the payment")
	}
	if got := integrityCount(t, h, `SELECT count(*) FROM bank_transactions WHERE id=$1 AND status='zugeordnet' AND payment_id=$2`, bankID, paymentID); got != 1 {
		t.Error("conflicted reversal changed the bank receipt")
	}
	if got := integrityCount(t, h, `SELECT count(*) FROM audit_log`); got != auditBefore {
		t.Errorf("conflicted reversal changed audit count: got %d, want %d", got, auditBefore)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	w = callJSON(h, h.DeletePayment, http.MethodDelete, "/api/payments/x", strconv.FormatInt(paymentID, 10), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("retry reversal after releasing lock: %d %s", w.Code, w.Body.String())
	}
	integrityAssertInvoiceMoney(t, h, pid, iv.ID, 1, 0)
}

func TestBankAssignmentRejectsLockedTaxYear(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	bankID := bankIntegrityFixture(t, h)
	if _, err := h.Pool.Exec(t.Context(), `UPDATE bank_transactions SET booked_on='2094-06-01' WHERE id=$1`, bankID); err != nil {
		t.Fatal(err)
	}
	integrityLockTaxYear(t, h, 2094)
	auditBefore := integrityCount(t, h, `SELECT count(*) FROM audit_log`)
	w := callJSONRaw(h, h.AssignBankTransaction, http.MethodPost, "/api/bank-transactions/x/assign", strconv.FormatInt(bankID, 10),
		fmt.Sprintf(`{"person_id":%d}`, pid))
	if w.Code != http.StatusConflict {
		t.Fatalf("assignment in locked year: %d %s", w.Code, w.Body.String())
	}
	if got := integrityCount(t, h, `SELECT count(*) FROM payments WHERE person_id=$1`, pid); got != 0 {
		t.Errorf("locked-year rejection left %d payments", got)
	}
	if got := integrityCount(t, h, `SELECT count(*) FROM bank_transactions WHERE id=$1 AND status='offen' AND payment_id IS NULL AND person_id IS NULL`, bankID); got != 1 {
		t.Error("locked-year rejection changed the open bank receipt")
	}
	if got := integrityCount(t, h, `SELECT count(*) FROM audit_log`); got != auditBefore {
		t.Errorf("locked-year rejection changed audit count: got %d, want %d", got, auditBefore)
	}
}

func TestBankLegacyReversedAssignmentCanBeCorrected(t *testing.T) {
	for _, tc := range []struct {
		name         string
		ignoreReopen bool
	}{
		{name: "reassign directly"},
		{name: "ignore and reopen before reassignment", ignoreReopen: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler(t)
			firstPerson := createIntegrationPerson(t, h)
			otherPerson := createIntegrationPerson(t, h)
			actor := &models.User{ID: mkUser(t, h, "bank-legacy-"+strconv.FormatInt(time.Now().UnixNano(), 10), false),
				Username: "bank-legacy", Role: models.RoleEditor}
			bankID := bankIntegrityFixture(t, h)
			assign := func(pid int64) *httptest.ResponseRecorder {
				return bankIntegrityRequest(h.AssignBankTransaction, actor, http.MethodPost,
					"/api/bank-transactions/x/assign", bankID, fmt.Sprintf(`{"person_id":%d}`, pid))
			}
			w := assign(firstPerson)
			if w.Code != http.StatusOK {
				t.Fatalf("initial assignment: %d %s", w.Code, w.Body.String())
			}
			oldPaymentID := bankIntegrityPaymentID(t, w)
			if w := bankIntegrityRequest(h.DeletePayment, actor, http.MethodDelete, "/api/payments/x", oldPaymentID, ""); w.Code != http.StatusOK {
				t.Fatalf("reverse initial payment: %d %s", w.Code, w.Body.String())
			}
			// Older versions left the stored status assigned after reversal. Reading
			// and correcting that history must work without rewriting original money.
			if _, err := h.Pool.Exec(t.Context(), `UPDATE bank_transactions SET status='zugeordnet' WHERE id=$1`, bankID); err != nil {
				t.Fatal(err)
			}
			checkList := func(status string, want bool) {
				t.Helper()
				w := callJSON(h, h.ListBankTransactions, http.MethodGet, "/api/bank-transactions?status="+status, "", nil)
				if w.Code != http.StatusOK {
					t.Fatalf("list %s receipts: %d %s", status, w.Code, w.Body.String())
				}
				var out struct {
					Items []bankTxOut `json:"items"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, item := range out.Items {
					if item.ID == bankID {
						found = true
						if item.Status != status || item.PaymentID == nil || *item.PaymentID != oldPaymentID {
							t.Errorf("legacy receipt lost its status or payment history: %+v", item)
						}
					}
				}
				if found != want {
					t.Errorf("legacy receipt in %s list = %v, want %v", status, found, want)
				}
			}
			checkList("offen", true)
			checkList("zugeordnet", false)
			if got := integrityCount(t, h, `SELECT count(*) FROM bank_transactions WHERE id=$1 AND status='zugeordnet'`, bankID); got != 1 {
				t.Error("listing rewrote the stored legacy status")
			}
			if tc.ignoreReopen {
				for _, ignored := range []bool{true, false} {
					w := bankIntegrityRequest(h.SetBankTransactionIgnored, actor, http.MethodPost,
						"/api/bank-transactions/x/ignore", bankID, fmt.Sprintf(`{"ignored":%t}`, ignored))
					if w.Code != http.StatusOK {
						t.Fatalf("set ignored=%v: %d %s", ignored, w.Code, w.Body.String())
					}
					checkList("ignoriert", ignored)
					checkList("offen", !ignored)
				}
			}
			w = assign(otherPerson)
			if w.Code != http.StatusOK {
				t.Fatalf("correct legacy assignment: %d %s", w.Code, w.Body.String())
			}
			if newPaymentID := bankIntegrityPaymentID(t, w); newPaymentID == oldPaymentID {
				t.Fatal("legacy correction replayed the reversed payment")
			}
			if got := integrityCount(t, h, `SELECT count(*) FROM payments WHERE id=$1 AND person_id=$2 AND amount=50 AND reversed`, oldPaymentID, firstPerson); got != 1 {
				t.Error("legacy correction changed the original payment")
			}
			if got := integrityCount(t, h, `SELECT count(*) FROM payments WHERE person_id IN ($1,$2)`, firstPerson, otherPerson); got != 2 {
				t.Errorf("legacy correction has %d payments, want original plus correction", got)
			}
			if got := integrityCount(t, h, `SELECT count(*) FROM payments WHERE person_id=$1 AND amount=50 AND NOT reversed`, otherPerson); got != 1 {
				t.Error("legacy correction did not book exactly one payment to corrected customer")
			}
		})
	}
}

func TestBankPaymentReversalAllowsRepeatedCorrection(t *testing.T) {
	for _, tc := range []struct {
		name           string
		changeActor    bool
		changeCustomer bool
	}{
		{name: "same actor and customer"},
		{name: "same actor and corrected customer", changeCustomer: true},
		{name: "different actor and corrected customer", changeActor: true, changeCustomer: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := testHandler(t)
			firstPerson, _, _ := seasonFixture(t, h, "")
			otherPerson, _, _ := seasonFixture(t, h, "")
			tag := strconv.FormatInt(time.Now().UnixNano(), 10)
			actor := &models.User{ID: mkUser(t, h, "bank-integrity-a-"+tag, false), Username: "bank-integrity-a", Role: models.RoleEditor}
			correctingActor := actor
			if tc.changeActor {
				correctingActor = &models.User{ID: mkUser(t, h, "bank-integrity-b-"+tag, false), Username: "bank-integrity-b", Role: models.RoleEditor}
			}
			bankID := bankIntegrityFixture(t, h)
			assign := func(who *models.User, pid int64) *httptest.ResponseRecorder {
				return bankIntegrityRequest(h.AssignBankTransaction, who, http.MethodPost,
					"/api/bank-transactions/x/assign", bankID, fmt.Sprintf(`{"person_id":%d}`, pid))
			}
			w := assign(actor, firstPerson)
			if w.Code != http.StatusOK {
				t.Fatalf("initial assignment: %d %s", w.Code, w.Body.String())
			}
			current := bankIntegrityPaymentID(t, w)
			paymentIDs := []int64{current}
			currentPerson := firstPerson
			assignedBy := actor.ID
			for cycle := 0; cycle < 3; cycle++ {
				w := bankIntegrityRequest(h.DeletePayment, correctingActor, http.MethodDelete, "/api/payments/x", current, "")
				if w.Code != http.StatusOK {
					t.Fatalf("cycle %d reverse: %d %s", cycle, w.Code, w.Body.String())
				}
				var status string
				var previousPaymentID *int64
				if err := h.Pool.QueryRow(t.Context(), `SELECT status,payment_id FROM bank_transactions WHERE id=$1`, bankID).
					Scan(&status, &previousPaymentID); err != nil {
					t.Fatal(err)
				}
				if status != "offen" || previousPaymentID == nil || *previousPaymentID != current {
					t.Fatalf("cycle %d bank row after reversal: status=%q previous payment=%v", cycle, status, previousPaymentID)
				}
				var changes map[string]struct {
					Old any `json:"old"`
					New any `json:"new"`
				}
				var rawChanges []byte
				if err := h.Pool.QueryRow(t.Context(), `SELECT changes FROM audit_log
					WHERE entity='bank_transaction' AND entity_id=$1 AND action='update' ORDER BY id DESC LIMIT 1`, bankID).
					Scan(&rawChanges); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(rawChanges, &changes); err != nil {
					t.Fatal(err)
				}
				for field, old := range map[string]any{
					"status": "zugeordnet", "person_id": float64(currentPerson), "resolved_by": float64(assignedBy),
				} {
					var wantNew any
					if field == "status" {
						wantNew = "offen"
					}
					change, ok := changes[field]
					if !ok || change.Old != old || change.New != wantNew {
						t.Errorf("cycle %d bank reversal audit %s = %+v, want old=%v new=%v", cycle, field, change, old, wantNew)
					}
				}
				if change, ok := changes["resolved_at"]; !ok || change.Old == nil || change.New != nil {
					t.Errorf("cycle %d bank reversal lost resolved timestamp diff: %+v", cycle, change)
				}
				var amount float64
				var reversed bool
				var savedPerson int64
				if err := h.Pool.QueryRow(t.Context(), `SELECT amount,reversed,person_id FROM payments WHERE id=$1`, current).
					Scan(&amount, &reversed, &savedPerson); err != nil {
					t.Fatal(err)
				}
				if amount != 50 || !reversed || savedPerson != currentPerson {
					t.Fatalf("cycle %d original payment changed: amount=%v reversed=%v person=%d", cycle, amount, reversed, savedPerson)
				}
				if w := bankIntegrityRequest(h.DeletePayment, correctingActor, http.MethodDelete, "/api/payments/x", current, ""); w.Code != http.StatusNotFound {
					t.Fatalf("cycle %d repeated reversal = %d, want 404: %s", cycle, w.Code, w.Body.String())
				}
				currentPerson = firstPerson
				if tc.changeCustomer && cycle%2 == 0 {
					currentPerson = otherPerson
				}
				w = assign(correctingActor, currentPerson)
				if w.Code != http.StatusOK {
					t.Fatalf("cycle %d corrected assignment: %d %s", cycle, w.Code, w.Body.String())
				}
				current = bankIntegrityPaymentID(t, w)
				assignedBy = correctingActor.ID
				for _, previous := range paymentIDs {
					if current == previous {
						t.Fatalf("cycle %d replayed reversed payment %d", cycle, current)
					}
				}
				paymentIDs = append(paymentIDs, current)
				if w := assign(actor, currentPerson); w.Code != http.StatusConflict {
					t.Fatalf("cycle %d duplicate assignment = %d, want 409: %s", cycle, w.Code, w.Body.String())
				}
			}
			var total, reversed int
			var activeAmount float64
			if err := h.Pool.QueryRow(t.Context(), `SELECT count(*),count(*) FILTER (WHERE reversed),
				COALESCE(sum(amount) FILTER (WHERE NOT reversed),0) FROM payments WHERE person_id IN ($1,$2)`, firstPerson, otherPerson).
				Scan(&total, &reversed, &activeAmount); err != nil {
				t.Fatal(err)
			}
			if total != 4 || reversed != 3 || activeAmount != 50 {
				t.Errorf("correction history: payments=%d reversed=%d active amount=%v, want 4/3/50", total, reversed, activeAmount)
			}
			if got := integrityCount(t, h, `SELECT count(*) FROM audit_log WHERE entity='bank_transaction' AND entity_id=$1`, bankID); got < 7 {
				t.Errorf("bank history has %d audit entries, want at least 4 assignments and 3 reversals", got)
			}
		})
	}
}

func bankIntegrityFixture(t *testing.T, h *Handler) int64 {
	t.Helper()
	var id int64
	if err := h.Pool.QueryRow(t.Context(), `INSERT INTO bank_transactions (ref,booked_on,amount)
		VALUES ($1,'2026-10-10',50) RETURNING id`, "integrity-"+strconv.FormatInt(time.Now().UnixNano(), 10)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := h.Pool.Exec(context.Background(), `DELETE FROM bank_transactions WHERE id=$1`, id); err != nil {
			t.Errorf("cleanup bank fixture: %v", err)
		}
	})
	return id
}

func bankIntegrityPaymentID(t *testing.T, w *httptest.ResponseRecorder) int64 {
	t.Helper()
	var result struct {
		PaymentID int64 `json:"payment_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.PaymentID == 0 {
		t.Fatal("assignment response has no payment ID")
	}
	return result.PaymentID
}

func bankIntegrityRequest(hf http.HandlerFunc, actor *models.User, method, path string, id int64, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", strconv.FormatInt(id, 10))
	r = r.WithContext(auth.ContextWithUser(r.Context(), actor))
	w := httptest.NewRecorder()
	hf(w, r)
	return w
}
