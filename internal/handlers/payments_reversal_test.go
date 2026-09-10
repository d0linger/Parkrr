package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestDeletePaymentRejectsPeriodManagedSettlement(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 30, "monthly", firstOfMonthMonthsAgo(3).Format("2006-01-02"))
	key := firstOfMonthMonthsAgo(3).Format("2006-01")
	setAgreementPeriodPaidT(t, h, aid, key, true)
	var payID int64
	if err := h.Pool.QueryRow(t.Context(), `SELECT id FROM payments WHERE person_id=$1 AND settles_kind='agreement' AND settles_ref=$2 AND settles_period=$3`, pid, aid, key).Scan(&payID); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodDelete, "/api/payments/"+strconv.FormatInt(payID, 10), nil)
	r.SetPathValue("id", strconv.FormatInt(payID, 10))
	rec := httptest.NewRecorder()
	h.DeletePayment(rec, r)
	if rec.Code != http.StatusConflict {
		t.Fatalf("reverse: %d %s", rec.Code, rec.Body.String())
	}
	var reversed, paid bool
	if err := h.Pool.QueryRow(t.Context(), `SELECT reversed,EXISTS(SELECT 1 FROM flat_rate_period_payments WHERE period_id=$2 AND period_key=$3) FROM payments WHERE id=$1`, payID, aid, key).Scan(&reversed, &paid); err != nil {
		t.Fatal(err)
	}
	if reversed || !paid {
		t.Fatalf("unexpected state reversed=%v period_paid=%v", reversed, paid)
	}
	lines, err := h.invoiceLines(httptest.NewRequest(http.MethodGet, "/", nil), pid)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		if line.Kind == "agreement" && line.ID == aid && line.Period == key {
			t.Fatal("reversed period unexpectedly eligible for invoicing")
		}
	}
	setAgreementPeriodPaidT(t, h, aid, key, false)
	var remains bool
	if err := h.Pool.QueryRow(t.Context(),
		`SELECT EXISTS(SELECT 1 FROM flat_rate_period_payments WHERE period_id=$1 AND period_key=$2)`,
		aid, key).Scan(&remains); err != nil {
		t.Fatal(err)
	}
	if remains {
		t.Fatal("supported period toggle did not reopen the period")
	}
}
