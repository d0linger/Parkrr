package handlers

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestPaidFixedPartialSurvivesInvoicing (5th-pass A1-1): a per-period fixed
// partial (Teilbetrag, off-book) must stay credited after its period is invoiced
// — the invoice bills cost − partial, so dropping the credit on lock creates a
// permanent phantom debt equal to the partial.
func TestPaidFixedPartialSurvivesInvoicing(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 100, "monthly", firstOfMonthMonthsAgo(2).Format("2006-01-02"))

	// Record a €30 fixed partial on a completed month.
	key := firstOfMonthMonthsAgo(1).Format("2006-01")
	body, _ := json.Marshal(map[string]any{"period_key": key, "paid": true, "amount": 30})
	req := httptest.NewRequest(http.MethodPost, "/api/agreements/"+strconv.FormatInt(aid, 10)+"/period-paid", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(aid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.SetAgreementPeriodPaid(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("period-paid: %d %s", rec.Code, rec.Body.String())
	}

	ppBefore := personStatsT(t, h, pid).PeriodPaid
	if math.Abs(ppBefore-30) > 0.5 {
		t.Fatalf("period-paid credit before invoicing should be ~30, got %.2f", ppBefore)
	}

	createInvoice(t, h, pid) // bills the completed periods (this one at cost − 30), locks them

	ppAfter := personStatsT(t, h, pid).PeriodPaid
	if math.Abs(ppAfter-30) > 0.5 {
		t.Errorf("fixed partial must stay credited after its period is invoiced: before=%.2f after=%.2f (phantom debt)", ppBefore, ppAfter)
	}
}

// TestStornoCarriesLeistungszeitraum (5th-pass A3-2 / §11 Abs 1 Z 4): a Storno is
// itself a Rechnung and must repeat the mandatory service period of the original.
func TestStornoCarriesLeistungszeitraum(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid)
	if iv.LeistungFrom == nil {
		t.Fatalf("precondition: original invoice should carry a Leistungszeitraum")
	}

	creq := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/cancel", nil)
	creq.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
	crec := httptest.NewRecorder()
	h.CancelInvoice(crec, creq)
	if crec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", crec.Code, crec.Body.String())
	}
	var storno invoice
	if err := json.Unmarshal(crec.Body.Bytes(), &storno); err != nil {
		t.Fatalf("decode storno: %v", err)
	}
	full := getInvoiceT(t, h, storno.ID)
	if full.LeistungFrom == nil || full.LeistungTo == nil {
		t.Errorf("Storno must carry the Leistungszeitraum (§11 Abs 1 Z 4); from=%v to=%v", full.LeistungFrom, full.LeistungTo)
	}
}

// TestReturnToStoredClearsEndDate (5th-pass A2-5): re-opening a collected vehicle
// to "stored" must clear the stale end_date, otherwise accrual stays frozen and
// the active-looking vehicle silently stops billing.
func TestReturnToStoredClearsEndDate(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(3).Format("2006-01-02"))

	// Collect with a PAST date → accrual capped there.
	setStatus(t, h, vid, "collected", firstOfMonthMonthsAgo(1).Format("2006-01-02"))
	collected := personStatsT(t, h, pid).Balance

	// Re-open to stored (no date) → end_date cleared, accrual resumes to today.
	setStatus(t, h, vid, "stored", "")
	stored := personStatsT(t, h, pid).Balance

	if stored <= collected+10 {
		t.Errorf("returning to stored must clear end_date and resume accrual: collected=%.2f stored=%.2f", collected, stored)
	}
}

// TestDeleteInvoicedAgreementBlocked (5th-pass A2-3): an invoiced Pauschale must
// not be deletable — it would orphan the invoice_source lock and sever the
// invoice→source reconstruction trail.
func TestDeleteInvoicedAgreementBlocked(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 100, "monthly", firstOfMonthMonthsAgo(2).Format("2006-01-02"))
	createInvoice(t, h, pid) // locks the agreement's completed periods

	dreq := httptest.NewRequest(http.MethodDelete, "/api/agreements/"+strconv.FormatInt(aid, 10), nil)
	dreq.SetPathValue("id", strconv.FormatInt(aid, 10))
	drec := httptest.NewRecorder()
	h.DeleteAgreement(drec, dreq)
	if drec.Code != http.StatusConflict {
		t.Errorf("deleting an invoiced Pauschale must be blocked (409), got %d %s", drec.Code, drec.Body.String())
	}
}

// setStatus posts a vehicle status change (optional back-dated date).
func setStatus(t *testing.T, h *Handler, vid int64, status, date string) {
	t.Helper()
	payload := map[string]any{"status": status}
	if date != "" {
		payload["date"] = date
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/status", bytes.NewReader(body))
	req.SetPathValue("id", strconv.FormatInt(vid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ChangeVehicleStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %s: %d %s", status, rec.Code, rec.Body.String())
	}
}
