//go:build integration

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestDepositRetentionFailureRollsBackMoneyAndAudit(t *testing.T) {
	h := testHandler(t)
	pid, _, _ := seasonFixture(t, h, "")
	withAddress(t, h, pid)
	iv := mustInvoice(t, h, pid)
	person := strconv.FormatInt(pid, 10)
	depositIntegrityBook(t, h, pid, `{"kind":"erhalten","amount":200}`)
	auditBefore := integrityCount(t, h, `SELECT count(*) FROM audit_log`)
	removeFailure := integrityFailPaymentLink(t, h, "deposits", fmt.Sprintf("NEW.person_id = %d", pid))

	body := `{"kind":"einbehalten","amount":50,"note":"Correction regression"}`
	w := callJSONRaw(h, h.CreateDeposit, http.MethodPost, "/api/persons/x/deposits", person, body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("injected link failure: %d %s", w.Code, w.Body.String())
	}
	if got := integrityCount(t, h, `SELECT count(*) FROM deposits WHERE person_id=$1 AND kind='einbehalten'`, pid); got != 0 {
		t.Errorf("failed retention left %d deposit debits", got)
	}
	integrityAssertInvoiceMoney(t, h, pid, iv.ID, 0, 0)
	if got := integrityCount(t, h, `SELECT count(*) FROM audit_log`); got != auditBefore {
		t.Errorf("failed retention changed audit count: got %d, want %d", got, auditBefore)
	}
	depositIntegrityBalance(t, h, pid, 200)

	removeFailure()
	retained := depositIntegrityBook(t, h, pid, body)
	if retained.PaymentID == nil {
		t.Fatal("successful retry must link its payment")
	}
	var linked int64
	if err := h.Pool.QueryRow(t.Context(), `SELECT payment_id FROM deposits WHERE id=$1`, retained.ID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != *retained.PaymentID {
		t.Fatalf("saved payment link = %d, response = %d", linked, *retained.PaymentID)
	}
	integrityAssertInvoiceMoney(t, h, pid, iv.ID, 1, 50)
	depositIntegrityBalance(t, h, pid, 150)
	if got := integrityCount(t, h, `SELECT count(*) FROM audit_log WHERE entity='deposit' AND entity_id=$1 AND action='create'`, retained.ID); got != 1 {
		t.Errorf("successful retry deposit audit count = %d, want 1", got)
	}
}

func TestDepositRetentionRejectsLockedTaxYear(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	pinClock(h, bilDay(t, "2095-01-02"))
	depositIntegrityBook(t, h, pid, `{"kind":"erhalten","amount":200,"booked_on":"2094-06-01"}`)
	integrityLockTaxYear(t, h, 2094)
	auditBefore := integrityCount(t, h, `SELECT count(*) FROM audit_log`)
	w := callJSONRaw(h, h.CreateDeposit, http.MethodPost, "/api/persons/x/deposits", strconv.FormatInt(pid, 10),
		`{"kind":"einbehalten","amount":50,"booked_on":"2094-06-01","note":"Locked year retention"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("retention in locked year: %d %s", w.Code, w.Body.String())
	}
	if got := integrityCount(t, h, `SELECT count(*) FROM deposits WHERE person_id=$1 AND kind='einbehalten'`, pid); got != 0 {
		t.Errorf("locked-year rejection left %d retention rows", got)
	}
	if got := integrityCount(t, h, `SELECT count(*) FROM payments WHERE person_id=$1`, pid); got != 0 {
		t.Errorf("locked-year rejection left %d payments", got)
	}
	if got := integrityCount(t, h, `SELECT count(*) FROM audit_log`); got != auditBefore {
		t.Errorf("locked-year rejection changed audit count: got %d, want %d", got, auditBefore)
	}
	depositIntegrityBalance(t, h, pid, 200)
}

func TestDepositPaymentReversalRestoresBalanceAndKeepsHistory(t *testing.T) {
	h := testHandler(t)
	pid, _, _ := seasonFixture(t, h, "")
	withAddress(t, h, pid)
	iv := mustInvoice(t, h, pid)
	depositIntegrityBook(t, h, pid, `{"kind":"erhalten","amount":200}`)
	retained := depositIntegrityBook(t, h, pid, `{"kind":"einbehalten","amount":50,"note":"Correct this retention"}`)
	if retained.PaymentID == nil {
		t.Fatal("retention did not record a payment")
	}
	depositIntegrityBalance(t, h, pid, 150)
	integrityAssertInvoiceMoney(t, h, pid, iv.ID, 1, 50)
	depositID, paymentID := strconv.FormatInt(retained.ID, 10), strconv.FormatInt(*retained.PaymentID, 10)
	for _, phase := range []string{"active", "reversed"} {
		if phase == "reversed" {
			w := callJSON(h, h.DeletePayment, http.MethodDelete, "/api/payments/x", paymentID, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("reverse retention payment: %d %s", w.Code, w.Body.String())
			}
		}
		w := callJSON(h, h.DeleteDeposit, http.MethodDelete, "/api/deposits/x", depositID, nil)
		if w.Code != http.StatusConflict {
			t.Errorf("delete %s linked retention = %d, want 409: %s", phase, w.Code, w.Body.String())
		}
	}

	entries := depositIntegrityBalance(t, h, pid, 200)
	var found bool
	for _, entry := range entries {
		if entry.ID == retained.ID {
			found = true
			if !entry.Reversed || entry.Amount != 50 || entry.PaymentID == nil || *entry.PaymentID != *retained.PaymentID {
				t.Errorf("reversed retention history changed: %+v", entry)
			}
		}
	}
	if !found {
		t.Error("reversed retention disappeared from deposit history")
	}
	integrityAssertInvoiceMoney(t, h, pid, iv.ID, 1, 0)
	var amount float64
	var reversed bool
	if err := h.Pool.QueryRow(t.Context(), `SELECT amount,reversed FROM payments WHERE id=$1`, *retained.PaymentID).Scan(&amount, &reversed); err != nil {
		t.Fatal(err)
	}
	if amount != 50 || !reversed {
		t.Errorf("original payment changed or was not reversed: amount=%v reversed=%v", amount, reversed)
	}
	if w := callJSON(h, h.DeletePayment, http.MethodDelete, "/api/payments/x", paymentID, nil); w.Code != http.StatusNotFound {
		t.Errorf("second reversal = %d, want 404: %s", w.Code, w.Body.String())
	}
	depositIntegrityBalance(t, h, pid, 200)
	depositIntegrityBook(t, h, pid, `{"kind":"zurueck","amount":200,"method":"bar"}`)
	depositIntegrityBalance(t, h, pid, 0)
	if got := integrityCount(t, h, `SELECT count(*) FROM payments WHERE person_id=$1 AND NOT reversed`, pid); got != 0 {
		t.Errorf("refund after reversal created %d active revenue payments", got)
	}
}

type depositIntegrityEntry struct {
	ID        int64   `json:"id"`
	Amount    float64 `json:"amount"`
	PaymentID *int64  `json:"payment_id"`
	Reversed  bool    `json:"reversed"`
}

func depositIntegrityBook(t *testing.T, h *Handler, pid int64, body string) depositIntegrityEntry {
	t.Helper()
	w := callJSONRaw(h, h.CreateDeposit, http.MethodPost, "/api/persons/x/deposits", strconv.FormatInt(pid, 10), body)
	if w.Code != http.StatusCreated {
		t.Fatalf("book deposit: %d %s", w.Code, w.Body.String())
	}
	var out depositIntegrityEntry
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ID == 0 {
		t.Fatal("deposit response has no ID")
	}
	return out
}

func depositIntegrityBalance(t *testing.T, h *Handler, pid int64, want float64) []depositIntegrityEntry {
	t.Helper()
	w := callJSON(h, h.ListDeposits, http.MethodGet, "/api/persons/x/deposits", strconv.FormatInt(pid, 10), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list deposits: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Balance float64                 `json:"balance"`
		Entries []depositIntegrityEntry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Balance != want {
		t.Errorf("deposit balance = %v, want %v", out.Balance, want)
	}
	return out.Entries
}

func integrityLockTaxYear(t *testing.T, h *Handler, year int) {
	t.Helper()
	if _, err := h.Pool.Exec(t.Context(), `INSERT INTO tax_year_locks(year) VALUES ($1)`, year); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := h.Pool.Exec(context.Background(), `DELETE FROM tax_year_locks WHERE year=$1`, year); err != nil {
			t.Errorf("cleanup year lock: %v", err)
		}
	})
}

func integrityCount(t *testing.T, h *Handler, query string, args ...any) int {
	t.Helper()
	var count int
	if err := h.Pool.QueryRow(t.Context(), query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func integrityAssertInvoiceMoney(t *testing.T, h *Handler, personID, invoiceID int64, payments int, wantPaid float64) {
	t.Helper()
	if got := integrityCount(t, h, `SELECT count(*) FROM payments WHERE person_id=$1`, personID); got != payments {
		t.Errorf("payment records = %d, want %d", got, payments)
	}
	var paid, allocated float64
	if err := h.Pool.QueryRow(t.Context(), `SELECT paid_amount,
		COALESCE((SELECT sum(amount) FROM invoice_payments WHERE invoice_id=$1),0)
		FROM invoices WHERE id=$1`, invoiceID).Scan(&paid, &allocated); err != nil {
		t.Fatal(err)
	}
	if paid != wantPaid || allocated != wantPaid {
		t.Errorf("invoice paid=%v allocated=%v, want both %v", paid, allocated, wantPaid)
	}
}

// Fail the final link write after the payment and invoice work have run. The
// temporary trigger is scoped to the fixture row and removed before retrying.
func integrityFailPaymentLink(t *testing.T, h *Handler, table, condition string) func() {
	t.Helper()
	name := pgx.Identifier{"integrity_link_failure_" + strconv.FormatInt(time.Now().UnixNano(), 10)}.Sanitize()
	tableName := pgx.Identifier{table}.Sanitize()
	if _, err := h.Pool.Exec(t.Context(), `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected payment link failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := h.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS `+name+` ON `+tableName+`; DROP FUNCTION IF EXISTS `+name+`()`); err != nil {
			t.Errorf("remove payment link failure: %v", err)
		}
	}
	t.Cleanup(cleanup)
	if _, err := h.Pool.Exec(t.Context(), `CREATE TRIGGER `+name+` BEFORE UPDATE OF payment_id ON `+tableName+
		` FOR EACH ROW WHEN (`+condition+`) EXECUTE FUNCTION `+name+`() `); err != nil {
		t.Fatal(err)
	}
	return cleanup
}
