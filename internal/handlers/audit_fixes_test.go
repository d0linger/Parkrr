package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// TestPaymentWritesAuditRowInTx (audit C7): the payment and its audit row are
// written in one transaction, so a booked money mutation always carries its
// trail (no orphaned change if the process dies right after the money commit).
func TestPaymentWritesAuditRowInTx(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	pbody, _ := json.Marshal(map[string]any{"amount": 40, "method": "bar"})
	preq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/payments", bytes.NewReader(pbody))
	preq.SetPathValue("id", strconv.FormatInt(pid, 10))
	preq.Header.Set("Content-Type", "application/json")
	prec := httptest.NewRecorder()
	h.CreatePayment(prec, preq)
	if prec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", prec.Code, prec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(prec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	payID := int64(resp["id"].(float64))

	var n int
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action='create' AND entity='payment' AND entity_id=$1`, payID).Scan(&n); err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	if n != 1 {
		t.Errorf("a booked payment must write exactly one audit row (in-tx), got %d", n)
	}
}

// TestCollectedTodayEqualsStoredToday: the accrued balance shown while a vehicle
// is stored (counted through today) must equal the final balance after marking
// it collected today — whole-day counting with an inclusive collection day, so
// the displayed value never jumps when the pickup is recorded.
func TestCollectedTodayEqualsStoredToday(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 35, firstOfMonthMonthsAgo(7).Format("2006-01-02"))

	stored := personStatsT(t, h, pid).Balance

	today := time.Now().Format("2006-01-02")
	body, _ := json.Marshal(map[string]any{"status": "collected", "date": today})
	req := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/status", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(vid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ChangeVehicleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("collect: %d %s", rec.Code, rec.Body.String())
	}

	collected := personStatsT(t, h, pid).Balance
	if math.Abs(stored-collected) > 0.005 {
		t.Errorf("collected-today balance must equal stored-through-today: stored=%.2f collected=%.2f", stored, collected)
	}
}

// TestCancelledVehicleStopsAccrual (audit W1): canceling a vehicle must set an
// end_date so rent stops accruing — otherwise the archived vehicle grows an
// uncollectable receivable forever.
func TestCancelledVehicleStopsAccrual(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(3).Format("2006-01-02"))

	// Cancel effective one month ago → accrual stops there (~2 months, bounded),
	// not the ~3 months it would keep growing to with a NULL end_date.
	cancelDate := firstOfMonthMonthsAgo(1).Format("2006-01-02")
	body, _ := json.Marshal(map[string]any{"status": "cancelled", "date": cancelDate})
	req := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/status", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(vid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ChangeVehicleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	// EndDate is inclusive, so this bills 2 full months (60) plus the cancel day
	// itself (~1) → ~60–61. The regression this guards against is unbounded growth
	// (a NULL end_date would reach ~95 accruing through today).
	if b := personStatsT(t, h, pid).Balance; b < 60 || b > 62 {
		t.Errorf("cancelled vehicle rent must stop at the end date (~2 months, bounded), got %.2f (unbounded growth?)", b)
	}
}

// TestDeletePersonWithPaymentBlocked (audit C2 / BAO §132): a person with a booked
// payment must not be deletable (the payments FK cascades — history would be lost).
func TestDeletePersonWithPaymentBlocked(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	pbody, _ := json.Marshal(map[string]any{"amount": 50, "method": "bar"})
	preq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/payments", bytes.NewReader(pbody))
	preq.SetPathValue("id", strconv.FormatInt(pid, 10))
	preq.Header.Set("Content-Type", "application/json")
	prec := httptest.NewRecorder()
	h.CreatePayment(prec, preq)
	if prec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", prec.Code, prec.Body.String())
	}

	dreq := httptest.NewRequest(http.MethodDelete, "/api/persons/"+strconv.FormatInt(pid, 10), nil)
	dreq.SetPathValue("id", strconv.FormatInt(pid, 10))
	drec := httptest.NewRecorder()
	h.DeletePerson(drec, dreq)
	if drec.Code != http.StatusConflict {
		t.Errorf("deleting a person with a booked payment must be blocked (409), got %d %s", drec.Code, drec.Body.String())
	}
}

// TestInvoiceDocumentIsImmutable (audit C3 / BAO §131): the database itself
// rejects tampering with an issued invoice's document fields and rejects
// deleting it, while the sanctioned settlement update (paid_amount) still works.
func TestInvoiceDocumentIsImmutable(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid)
	ctx := context.Background()

	if _, err := h.Pool.Exec(ctx, `UPDATE invoices SET total = total + 1 WHERE id=$1`, iv.ID); err == nil {
		t.Errorf("immutability trigger must reject changing an invoice's total (BAO §131)")
	}
	if _, err := h.Pool.Exec(ctx, `DELETE FROM invoices WHERE id=$1`, iv.ID); err == nil {
		t.Errorf("immutability trigger must reject deleting an invoice (BAO §132: Storno, not delete)")
	}
	// The sanctioned settlement bookkeeping is still allowed.
	if _, err := h.Pool.Exec(ctx, `UPDATE invoices SET paid_amount = 5 WHERE id=$1`, iv.ID); err != nil {
		t.Errorf("settlement update (paid_amount) must be allowed, got %v", err)
	}
}

// TestPaymentImmutableExceptReversal (audit C3): a booked payment's amount cannot
// be altered and it cannot be deleted, but flagging it reversed is permitted.
func TestPaymentImmutableExceptReversal(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	pbody, _ := json.Marshal(map[string]any{"amount": 50, "method": "bar"})
	preq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/payments", bytes.NewReader(pbody))
	preq.SetPathValue("id", strconv.FormatInt(pid, 10))
	preq.Header.Set("Content-Type", "application/json")
	prec := httptest.NewRecorder()
	h.CreatePayment(prec, preq)
	if prec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", prec.Code, prec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(prec.Body.Bytes(), &resp)
	payID := int64(resp["id"].(float64))
	ctx := context.Background()

	if _, err := h.Pool.Exec(ctx, `UPDATE payments SET amount = amount + 1 WHERE id=$1`, payID); err == nil {
		t.Errorf("immutability trigger must reject changing a payment's amount (BAO §131)")
	}
	if _, err := h.Pool.Exec(ctx, `DELETE FROM payments WHERE id=$1`, payID); err == nil {
		t.Errorf("immutability trigger must reject deleting a payment (reverse it instead)")
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE payments SET reversed=true, reversed_at=now() WHERE id=$1`, payID); err != nil {
		t.Errorf("reversal (reversed flag) must be allowed, got %v", err)
	}
}

// TestInvoiceHasLeistungszeitraum (audit C1 / §11 Abs 1 Z 4): an issued invoice
// must carry a service period (Leistungszeitraum) for the printed document.
func TestInvoiceHasLeistungszeitraum(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid)
	full := getInvoiceT(t, h, iv.ID)
	if full.LeistungFrom == nil || full.LeistungTo == nil {
		t.Errorf("invoice must carry a Leistungszeitraum (§11 Abs 1 Z 4); from=%v to=%v", full.LeistungFrom, full.LeistungTo)
	}
}
