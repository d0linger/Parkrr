package handlers

import (
	"testing"
	"time"

	"github.com/preining/parkrr/internal/models"
)

// TestAgreementInvoicePaidPeriodsPopulated: after a Pauschale's periods are billed on
// an invoice and that invoice is paid, the agreement payload lists those periods as
// invoice-paid — so the progress bar reads "bezahlt" instead of "0 % bezahlt".
func TestAgreementInvoicePaidPeriodsPopulated(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 30, "monthly", firstOfMonthMonthsAgo(3).Format("2006-01-02"))

	iv := createInvoice(t, h, pid) // bills the 3 completed months

	// Before payment: invoiced but the invoice is open → not yet invoice-paid.
	agsBefore, err := h.loadAgreements(t.Context(), pid, time.Now())
	if err != nil {
		t.Fatalf("load agreements: %v", err)
	}
	if got := agreementByID(agsBefore, aid); len(got.InvoicePaidPeriods) != 0 {
		t.Errorf("before paying the invoice, no period should be invoice-paid, got %v", got.InvoicePaidPeriods)
	}

	full := getInvoiceT(t, h, iv.ID)
	payInvoices(t, h, pid, map[string]any{"amount": full.Total, "auto": true})

	agsAfter, err := h.loadAgreements(t.Context(), pid, time.Now())
	if err != nil {
		t.Fatalf("load agreements: %v", err)
	}
	if got := agreementByID(agsAfter, aid); len(got.InvoicePaidPeriods) != 3 {
		t.Errorf("after paying the invoice, the 3 completed periods must be invoice-paid, got %v", got.InvoicePaidPeriods)
	}
}

func agreementByID(ags []models.FlatRatePeriod, id int64) models.FlatRatePeriod {
	for i := range ags {
		if ags[i].ID == id {
			return ags[i]
		}
	}
	return models.FlatRatePeriod{}
}
