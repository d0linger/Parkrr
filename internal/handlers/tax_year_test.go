package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTaxRuleYear(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	cases := []struct {
		paid, kind, period string
		want               int
	}{
		{"2032-01-08", "recurring", "2031-12", 2031}, // December rent paid in the window → prior year
		{"2032-01-16", "recurring", "2031-12", 2032}, // one day outside the window
		{"2031-12-20", "agreement", "2032-01", 2032}, // January rent prepaid in late December
		{"2031-12-16", "agreement", "2032-01", 2031}, // before the window
		{"2031-12-28", "recurring", "2032", 2032},    // yearly period prepaid
		{"2032-01-05", "recurring", "2031-11", 2032}, // not the adjacent month
		{"2032-01-05", "charge", "2031-12", 2032},    // one-off charges are not recurring
		{"2032-01-05", "", "", 2032},                 // no period at all
	}
	for _, c := range cases {
		if got := taxRuleYear(day(c.paid), c.kind, c.period); got != c.want {
			t.Errorf("taxRuleYear(%s, %q, %q) = %d, want %d", c.paid, c.kind, c.period, got, c.want)
		}
	}
}

// The year report counts by payment date, excludes Stornos, splits by what was
// settled, and offers the 15-day rule as an alternative in both directions.
func TestTaxYearReport(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	var pid int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name) VALUES ('Steuerjahr', 'Integration') RETURNING id`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	ref := time.Now().UnixNano() // unique settlement refs per run
	insert := func(paidOn string, amount float64, method string, auto bool, kind, period string, reversed bool) int64 {
		t.Helper()
		var id int64
		var k, per any
		if kind != "" {
			k, per = kind, period
		}
		var sref any
		if kind != "" {
			ref++
			sref = ref
		}
		if err := h.Pool.QueryRow(ctx,
			`INSERT INTO payments (person_id, amount, paid_on, method, auto, settles_kind, settles_ref, settles_period,
			                       reversed, reversed_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, CASE WHEN $9 THEN now() END) RETURNING id`,
			pid, amount, paidOn, method, auto, k, sref, per, reversed).Scan(&id); err != nil {
			t.Fatalf("insert payment %s: %v", paidOn, err)
		}
		return id
	}
	p1 := insert("2031-06-10", 100, "ueberweisung", false, "", "", false)
	ref++
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, kind, ref_id, amount) VALUES ($1, 'vehicle', $2, 60)`, p1, ref); err != nil {
		t.Fatal(err)
	}
	insert("2031-03-05", 50, "bar", false, "", "", true)                  // storniert
	insert("2032-01-08", 80, "", true, "recurring", "2031-12", false)     // → 2031 by rule
	insert("2031-12-20", 70, "", true, "agreement", "2032-01", false)     // → 2032 by rule
	insert("2031-01-10", 40, "bar", false, "recurring", "2030-12", false) // → 2030 by rule

	req := httptest.NewRequest(http.MethodGet, "/api/reports/tax-year?year=2031", nil)
	rec := httptest.NewRecorder()
	h.TaxYear(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var rep taxYearReport
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.TotalByDate != 210 || rep.Count != 3 {
		t.Errorf("by date: total %.2f / count %d, want 210 / 3", rep.TotalByDate, rep.Count)
	}
	if rep.TotalWithRule != 180 {
		t.Errorf("with 15-day rule: %.2f, want 180 (100 + 80 shifted in; 70 and 40 shifted out)", rep.TotalWithRule)
	}
	if rep.ReversedCount != 1 || rep.ReversedAmount != 50 {
		t.Errorf("reversed: %d / %.2f, want 1 / 50", rep.ReversedCount, rep.ReversedAmount)
	}
	if rep.SliderCount != 1 || rep.SliderAmount != 70 {
		t.Errorf("slider: %d / %.2f, want 1 / 70", rep.SliderCount, rep.SliderAmount)
	}
	if len(rep.ShiftedIn) != 1 || rep.ShiftedIn[0].Amount != 80 || len(rep.ShiftedOut) != 2 {
		t.Errorf("shifted in %v, out %v; want one in (80) and two out", rep.ShiftedIn, rep.ShiftedOut)
	}
	if rep.ByMonth[0] != 40 || rep.ByMonth[5] != 100 || rep.ByMonth[11] != 70 {
		t.Errorf("by month: %v", rep.ByMonth)
	}
	kinds := map[string]float64{}
	for _, g := range rep.ByKind {
		kinds[g.Key] = g.Amount
	}
	want := map[string]float64{"vehicle": 60, "credit": 40, "agreement": 70, "recurring": 40}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("by kind %s = %.2f, want %.2f (all: %v)", k, kinds[k], v, kinds)
		}
	}

	// The report CSV lists every row the year touches, with both year columns.
	rec = httptest.NewRecorder()
	h.TaxYearCSV(rec, httptest.NewRequest(http.MethodGet, "/api/reports/tax-year.csv?year=2031", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "jahr_nach_15_tage_regel") ||
		!strings.Contains(body, "2032-01-08") || strings.Contains(body, "2031-03-05") {
		t.Errorf("tax-year CSV: status %d, body:\n%s", rec.Code, body)
	}

	// The plain payments export honors ?year= (by payment date, Stornos included
	// and flagged, as before).
	creq := httptest.NewRequest(http.MethodGet, "/api/export/payments?year=2031", nil)
	creq.SetPathValue("entity", "payments")
	rec = httptest.NewRecorder()
	h.ExportCSV(rec, creq)
	body = rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "2031-06-10") || strings.Contains(body, "2032-01-08") {
		t.Errorf("payments CSV ?year=2031: status %d, body:\n%s", rec.Code, body)
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "zahlungen-2031") {
		t.Errorf("file name should carry the year: %q", rec.Header().Get("Content-Disposition"))
	}

	// The PDF renders.
	rec = httptest.NewRecorder()
	h.TaxYearPDF(rec, httptest.NewRequest(http.MethodGet, "/api/reports/tax-year.pdf?year=2031&rule=1", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "%PDF") {
		t.Errorf("tax-year PDF: status %d, starts %q", rec.Code, rec.Body.String()[:min(8, rec.Body.Len())])
	}
}
