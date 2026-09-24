package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/preining/parkrr/internal/models"
)

// Regression tests for the billing audit findings BIL-01..08 and the server side
// of WEB-01/WEB-02 (Idempotency-Key on pay-invoices, charges, recurring).

func bilDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// pinClock pins the handler's clock to noon of the given day.
func pinClock(h *Handler, day time.Time) {
	h.Now = func() time.Time { return day.Add(12 * time.Hour) }
}

func postJSON(h *Handler, hf http.HandlerFunc, path, id string, body any, headers map[string]string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	if id != "" {
		req.SetPathValue("id", id)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	hf(rec, req)
	return rec
}

func bilInvoiceRec(h *Handler, pid int64) *httptest.ResponseRecorder {
	id := strconv.FormatInt(pid, 10)
	return postJSON(h, h.CreateInvoice, "/api/persons/"+id+"/invoices", id, map[string]any{}, nil)
}

func mustInvoice(t *testing.T, h *Handler, pid int64) invoice {
	t.Helper()
	rec := bilInvoiceRec(h, pid)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invoice: %d %s", rec.Code, rec.Body.String())
	}
	var iv invoice
	if err := json.Unmarshal(rec.Body.Bytes(), &iv); err != nil {
		t.Fatal(err)
	}
	return getInvoiceT(t, h, iv.ID)
}

func recurringByID(t *testing.T, h *Handler, pid, rid int64) models.RecurringCharge {
	t.Helper()
	list, err := h.loadRecurringCharges(t.Context(), pid, h.now())
	if err != nil {
		t.Fatal(err)
	}
	for _, rc := range list {
		if rc.ID == rid {
			return rc
		}
	}
	t.Fatalf("recurring %d not found", rid)
	return models.RecurringCharge{}
}

// BIL-01: the Nebenkosten master slider settles only the periods complete at that
// moment. The running period and later periods stay owed and are invoiced.
func TestRecurringMasterPaidSettlesOnlyCompletedPeriods(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pinClock(h, bilDay(t, "2026-05-15"))
	pid := createIntegrationPerson(t, h)
	rid := mkRecurring(t, h, pid, 20, "2026-02-01") // open-ended

	setRecurringMasterPaid(t, h, rid, true)
	rc := recurringByID(t, h, pid, rid)
	if !rc.Paid {
		t.Error("derived paid must read true right after settling every completed period")
	}
	got := map[string]bool{}
	for _, k := range rc.PaidPeriods {
		got[k] = true
	}
	if len(got) != 3 || !got["2026-02"] || !got["2026-03"] || !got["2026-04"] || got["2026-05"] {
		t.Fatalf("slider must settle exactly Feb–Apr (complete), not the running May: %v", rc.PaidPeriods)
	}
	var stored bool
	if err := h.Pool.QueryRow(t.Context(), `SELECT paid FROM recurring_charges WHERE id=$1`, rid).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored {
		t.Error("no sticky master flag may be stored")
	}
	// The running month is still owed (balance = May 1–15 accrual).
	if s := personStatsT(t, h, pid); s.Balance < 5 {
		t.Errorf("running month must stay owed after the slider, balance %.2f", s.Balance)
	}

	// Two months later May and June are complete and unpaid → invoiced, owed.
	pinClock(h, bilDay(t, "2026-07-15"))
	if rc := recurringByID(t, h, pid, rid); rc.Paid {
		t.Error("derived paid must turn open once a later period completes unpaid")
	}
	iv := mustInvoice(t, h, pid)
	if math.Abs(iv.Subtotal-40) > 0.005 || len(iv.Items) != 2 {
		t.Fatalf("invoice must bill May+June (40), got %.2f %+v", iv.Subtotal, iv.Items)
	}
	// BIL-08: the Leistungszeitraum ends with the last billed period, not the issue date.
	if iv.LeistungFrom == nil || iv.LeistungFrom.Format(dateLayout) != "2026-05-01" ||
		iv.LeistungTo == nil || iv.LeistungTo.Format(dateLayout) != "2026-06-30" {
		t.Errorf("Leistungszeitraum want 2026-05-01..2026-06-30, got %v..%v", iv.LeistungFrom, iv.LeistungTo)
	}
}

// BIL-01: a charge with no completed period can't be "paid" by the master slider.
func TestRecurringMasterPaidRejectsWithoutCompletedPeriod(t *testing.T) {
	h := testHandler(t)
	pinClock(h, bilDay(t, "2026-05-15"))
	pid := createIntegrationPerson(t, h)
	rid := mkRecurring(t, h, pid, 20, "2026-05-01")
	id := strconv.FormatInt(rid, 10)
	rec := postJSON(h, h.SetRecurringChargePaid, "/api/recurring/"+id+"/paid", id, map[string]any{"paid": true}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("no completed period: want 409, got %d %s", rec.Code, rec.Body.String())
	}
}

// BIL-02: a vehicle settled once still bills the rent that accrues afterwards —
// through an allocated payment and through the slider alike.
func TestSettledVehicleStillBillsLaterRent(t *testing.T) {
	for _, via := range []string{"payment", "slider"} {
		t.Run(via, func(t *testing.T) {
			h := testHandler(t)
			compliantSeller(t, h)
			pinClock(h, bilDay(t, "2026-05-15"))
			pid := createIntegrationPerson(t, h)
			vid := mkStoredVehicle(t, h, pid, 30, "2026-03-01")
			v := getVehicleT(t, h, pid, vid)
			accrued := v.AccruedCost // Mar + Apr + May 1–15
			if via == "payment" {
				rec := postPayment(t, h, pid, map[string]any{"amount": accrued, "allocate": true})
				if rec.Code != http.StatusCreated {
					t.Fatalf("payment: %d %s", rec.Code, rec.Body.String())
				}
			} else {
				id := strconv.FormatInt(vid, 10)
				if rec := postJSON(h, h.MarkPaid, "/api/vehicles/"+id+"/paid", id, map[string]any{"paid": true}, nil); rec.Code != http.StatusOK {
					t.Fatalf("slider: %d %s", rec.Code, rec.Body.String())
				}
			}
			v = getVehicleT(t, h, pid, vid)
			if !v.Paid || v.PaidThrough == nil || v.PaidThrough.Format(dateLayout) != "2026-05-15" {
				t.Fatalf("settlement must record paid_through=2026-05-15, got paid=%v through=%v", v.Paid, v.PaidThrough)
			}
			// Nothing complete is unpaid yet: Mar/Apr are covered, May is running.
			if rec := bilInvoiceRec(h, pid); rec.Code != http.StatusBadRequest {
				t.Fatalf("nothing to bill yet: want 400, got %d %s", rec.Code, rec.Body.String())
			}

			// July 10: May's remaining days (16–31) and June are owed and invoiced.
			pinClock(h, bilDay(t, "2026-07-10"))
			iv := mustInvoice(t, h, pid)
			mayPaid := math.Round(3000.0*15/31) / 100 // 14.52
			want := math.Round((30-mayPaid+30)*100) / 100
			if math.Abs(iv.Subtotal-want) > 0.005 || len(iv.Items) != 2 {
				t.Fatalf("invoice must bill May rest + June = %.2f, got %.2f %+v", want, iv.Subtotal, iv.Items)
			}
			if iv.LeistungTo == nil || iv.LeistungTo.Format(dateLayout) != "2026-06-30" {
				t.Errorf("Leistungszeitraum must end 2026-06-30, got %v", iv.LeistungTo)
			}
			// Balance and invoice agree: what is owed is exactly the invoice plus July.
			s := personStatsT(t, h, pid)
			july := math.Round(3000.0*10/31) / 100
			if math.Abs(s.Balance-(want+july)) > 0.05 {
				t.Errorf("balance %.2f must equal invoiced %.2f + running July %.2f", s.Balance, want, july)
			}
		})
	}
}

// BIL-03: a charge claimed by a payment allocation but flagged open (legacy
// toggle-off state) is neither billed nor able to block the person's invoices.
func TestAllocatedChargeWithOpenFlagDoesNotBlockInvoicing(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	cid := mkChargeP(t, h, pid, "Bezahlt", 50, "2026-05-01")
	mkChargeP(t, h, pid, "Offen", 30, "2026-05-02")
	rec := postPayment(t, h, pid, map[string]any{"amount": 50, "allocations": []map[string]any{{"kind": "charge", "id": cid}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := h.Pool.Exec(t.Context(), `UPDATE charges SET paid=false WHERE id=$1`, cid); err != nil {
		t.Fatal(err)
	}
	iv := mustInvoice(t, h, pid)
	if math.Abs(iv.Subtotal-30) > 0.005 {
		t.Fatalf("invoice must bill only the open charge (30), got %.2f", iv.Subtotal)
	}
}

// BIL-03: a claim conflict is reported as such, never as the harmless "raced".
func TestChargeClaimConflictIsNotRaced(t *testing.T) {
	if isChargeClaimConflict(errors.New("x")) {
		t.Fatal("plain error is not a claim conflict")
	}
	err := fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "23505", ConstraintName: "charge_claim_exclusive"})
	if !isChargeClaimConflict(err) {
		t.Fatal("charge_claim_exclusive must be detected")
	}
	if isChargeClaimConflict(&pgconn.PgError{Code: "23505", ConstraintName: "uq_invoice_source_ref_period"}) {
		t.Fatal("a real uq race is not a claim conflict")
	}
}

// BIL-04: a bound Zusatzkosten on an active invoice no longer blocks the vehicle
// slider; the slider pays only the vehicle's own rent.
func TestVehicleSliderIgnoresInvoicedBoundCharge(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pinClock(h, bilDay(t, "2026-05-15"))
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 31, "2026-05-10") // no completed period yet
	var cid int64
	if err := h.Pool.QueryRow(t.Context(),
		`INSERT INTO charges (person_id, vehicle_id, description, amount, quantity, charged_on)
		 VALUES ($1,$2,'Reifenwechsel',50,1,'2026-05-12') RETURNING id`, pid, vid).Scan(&cid); err != nil {
		t.Fatal(err)
	}
	iv := mustInvoice(t, h, pid)
	if math.Abs(iv.Subtotal-50) > 0.005 {
		t.Fatalf("invoice must bill the bound charge (50), got %.2f", iv.Subtotal)
	}
	id := strconv.FormatInt(vid, 10)
	rec := postJSON(h, h.MarkPaid, "/api/vehicles/"+id+"/paid", id, map[string]any{"paid": true}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("vehicle slider must succeed, got %d %s", rec.Code, rec.Body.String())
	}
	var paid float64
	if err := h.Pool.QueryRow(t.Context(), `SELECT COALESCE(sum(amount),0) FROM payments WHERE person_id=$1`, pid).Scan(&paid); err != nil {
		t.Fatal(err)
	}
	if math.Abs(paid-6) > 0.005 { // May 10–15 at 1 €/day, the invoiced charge excluded
		t.Errorf("slider must pay only the vehicle rent (6.00), booked %.2f", paid)
	}
}

// waitForLockWait blocks until some backend waits on a lock whose query matches.
func waitForLockWait(ctx context.Context, t *testing.T, h *Handler, like string) {
	t.Helper()
	for {
		var blocked bool
		if err := h.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
			   AND wait_event_type='Lock' AND query LIKE $1)`, like).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// BIL-05: a vehicle slider racing an invoice that claims the vehicle waits for the
// invoice's per-person lock and then refuses, instead of paying invoiced rent.
func TestVehicleSliderWaitsForConcurrentInvoice(t *testing.T) {
	h := testHandler(t)
	pinClock(h, bilDay(t, "2026-05-15"))
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, "2026-03-01")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Stand in for CreateInvoice mid-transaction: holds the person lock and has
	// claimed the vehicle's March period, not yet committed.
	blocker, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := blocker.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", err)
		}
	}()
	if err := lockInvoicePersonTx(ctx, blocker, pid); err != nil {
		t.Fatal(err)
	}
	var invID int64
	if err := blocker.QueryRow(ctx,
		`INSERT INTO invoices (number, person_id, subtotal, total) VALUES ($1,$2,30,30) RETURNING id`,
		"BIL05-"+strconv.FormatInt(time.Now().UnixNano(), 10), pid).Scan(&invID); err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx,
		`INSERT INTO invoice_source (invoice_id, kind, ref_id, period_key) VALUES ($1,'vehicle',$2,'2026-03')`, invID, vid); err != nil {
		t.Fatal(err)
	}

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		id := strconv.FormatInt(vid, 10)
		b, _ := json.Marshal(map[string]any{"paid": true})
		req := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+id+"/paid", bytes.NewReader(b)).WithContext(ctx)
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		h.MarkPaid(rec, req)
		done <- rec
	}()
	waitForLockWait(ctx, t, h, "%pg_advisory_xact_lock%")
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("slider after a concurrent invoice must be 409, got %d %s", rec.Code, rec.Body.String())
	}
	var payments int
	var paid bool
	if err := h.Pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM payments WHERE person_id=$1), (SELECT paid FROM vehicles WHERE id=$2)`, pid, vid).
		Scan(&payments, &paid); err != nil {
		t.Fatal(err)
	}
	if payments != 0 || paid {
		t.Errorf("invoiced vehicle must not also be paid: payments=%d paid=%v", payments, paid)
	}
}

// BIL-07: a payment reversal takes the invoice lock BEFORE touching the invoice's
// payment links — the order a Storno uses — so the two can't deadlock.
func TestDeletePaymentLocksInvoiceBeforeLinks(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 40)
	iv := createInvoice(t, h, pid)
	pr := payInvoices(t, h, pid, map[string]any{"amount": 40, "auto": true})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	blocker, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := blocker.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", err)
		}
	}()
	// Hold the invoice row like a Storno in progress.
	if _, err := blocker.Exec(ctx, `SELECT id FROM invoices WHERE id=$1 FOR UPDATE`, iv.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		id := strconv.FormatInt(pr.PaymentID, 10)
		req := httptest.NewRequest(http.MethodDelete, "/api/payments/"+id, nil).WithContext(ctx)
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		h.DeletePayment(rec, req)
		done <- rec
	}()
	waitForLockWait(ctx, t, h, "%FROM invoices WHERE id IN (SELECT invoice_id FROM invoice_payments%")
	// The reversal waits on the invoice and has NOT locked the links yet: the Storno
	// (still holding the invoice) can take them without waiting.
	if _, err := blocker.Exec(ctx,
		`SELECT 1 FROM invoice_payments WHERE invoice_id=$1 FOR UPDATE NOWAIT`, iv.ID); err != nil {
		t.Fatalf("reversal must not hold invoice_payments while waiting for the invoice: %v", err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("reversal: %d %s", rec.Code, rec.Body.String())
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if got := getInvoiceT(t, h, iv.ID); got.PaidAmount > 0.005 {
		t.Errorf("reversal must give the amount back to the invoice, paid_amount=%.2f", got.PaidAmount)
	}
}

// BIL-08: a monthly Pauschale invoiced mid-month states the completed months as
// Leistungszeitraum, not a range running to the issue date.
func TestInvoiceLeistungToIsLastBilledDay(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pinClock(h, bilDay(t, "2026-04-15"))
	pid := createIntegrationPerson(t, h)
	mkAgreement(t, h, pid, 30, "monthly", "2026-02-01")
	iv := mustInvoice(t, h, pid)
	if iv.LeistungFrom == nil || iv.LeistungFrom.Format(dateLayout) != "2026-02-01" ||
		iv.LeistungTo == nil || iv.LeistungTo.Format(dateLayout) != "2026-03-31" {
		t.Fatalf("Leistungszeitraum want 2026-02-01..2026-03-31, got %v..%v", iv.LeistungFrom, iv.LeistungTo)
	}
}

func uniqueKey(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// WEB-01: pay-invoices honors an optional Idempotency-Key.
func TestPayInvoicesIdempotency(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 40)
	createInvoice(t, h, pid)
	id := strconv.FormatInt(pid, 10)
	path := "/api/persons/" + id + "/pay-invoices"
	key := uniqueKey("payinv")
	body := map[string]any{"amount": 50, "auto": true, "paid_on": "2026-08-02"}

	first := postJSON(h, h.PayInvoices, path, id, body, map[string]string{"Idempotency-Key": key})
	second := postJSON(h, h.PayInvoices, path, id, body, map[string]string{"Idempotency-Key": key})
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("pay-invoices: %d %s / %d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	if second.Header().Get("Idempotent-Replayed") != "true" {
		t.Error("retry must be marked as a replay")
	}
	var a, b payResult
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	_ = json.Unmarshal(second.Body.Bytes(), &b)
	if a != b || a.PaymentID == 0 || a.Allocated != 40 || a.Unallocated != 10 || a.Invoices != 1 {
		t.Fatalf("replay must return the first result: %+v vs %+v", a, b)
	}
	var n int
	if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM payments WHERE person_id=$1`, pid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("retry must not book a second payment, got %d", n)
	}
	body["amount"] = 60
	if rec := postJSON(h, h.PayInvoices, path, id, body, map[string]string{"Idempotency-Key": key}); rec.Code != http.StatusConflict {
		t.Fatalf("same key, different request: want 409, got %d %s", rec.Code, rec.Body.String())
	}
	// A key already used on POST /payments is a conflict here, not a replay.
	pkey := uniqueKey("payment")
	if rec := postPaymentWithKey(t, h, pid, pkey, map[string]any{"amount": 5}); rec.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(h, h.PayInvoices, path, id, map[string]any{"amount": 5}, map[string]string{"Idempotency-Key": pkey}); rec.Code != http.StatusConflict {
		t.Fatalf("cross-endpoint key: want 409, got %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(h, h.PayInvoices, path, id, body, map[string]string{"Idempotency-Key": "short"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid key: want 400, got %d", rec.Code)
	}
	// Without the header every request books its own payment (unchanged).
	postJSON(h, h.PayInvoices, path, id, map[string]any{"amount": 1}, nil)
	postJSON(h, h.PayInvoices, path, id, map[string]any{"amount": 1}, nil)
	if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM payments WHERE person_id=$1`, pid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("keyless requests must each book a payment, got %d payments", n)
	}
}

// WEB-02: POST /charges and POST /persons/{id}/recurring honor an optional
// Idempotency-Key.
func TestCreateChargeAndRecurringIdempotency(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	id := strconv.FormatInt(pid, 10)
	type created struct {
		ID int64 `json:"id"`
	}
	cases := []struct {
		name  string
		hf    http.HandlerFunc
		path  string
		pathV string
		body  map[string]any
		table string
	}{
		{"charge", h.CreateCharge, "/api/charges", "",
			map[string]any{"person_id": pid, "description": "Wäsche", "amount": 12.5, "charged_on": "2026-05-01"}, "charges"},
		{"recurring", h.CreateRecurringCharge, "/api/persons/" + id + "/recurring", id,
			map[string]any{"description": "Strom", "amount": 9, "period": "monthly", "start_date": "2026-05-01"}, "recurring_charges"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := uniqueKey(tc.name)
			hdr := map[string]string{"Idempotency-Key": key}
			first := postJSON(h, tc.hf, tc.path, tc.pathV, tc.body, hdr)
			second := postJSON(h, tc.hf, tc.path, tc.pathV, tc.body, hdr)
			if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
				t.Fatalf("create: %d %s / %d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
			}
			var a, b created
			_ = json.Unmarshal(first.Body.Bytes(), &a)
			_ = json.Unmarshal(second.Body.Bytes(), &b)
			if a.ID == 0 || a.ID != b.ID || second.Header().Get("Idempotent-Replayed") != "true" {
				t.Fatalf("retry must replay the first row: %d vs %d", a.ID, b.ID)
			}
			var n int
			if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM `+pgx.Identifier{tc.table}.Sanitize()+` WHERE person_id=$1`, pid).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("retry must not create a duplicate, got %d rows", n)
			}
			changed := map[string]any{}
			for k, v := range tc.body {
				changed[k] = v
			}
			changed["amount"] = 99
			if rec := postJSON(h, tc.hf, tc.path, tc.pathV, changed, hdr); rec.Code != http.StatusConflict {
				t.Fatalf("same key, different request: want 409, got %d %s", rec.Code, rec.Body.String())
			}
			// Without the header: a new row each time (unchanged behavior).
			postJSON(h, tc.hf, tc.path, tc.pathV, tc.body, nil)
			if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM `+pgx.Identifier{tc.table}.Sanitize()+` WHERE person_id=$1`, pid).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 2 {
				t.Fatalf("keyless create must add a row, got %d rows", n)
			}
		})
	}
}

// Migration 077 on existing data: a paid vehicle keeps its complete periods
// settled (paid_through), a paid recurring charge gets exactly its complete,
// un-invoiced periods as keys and loses the sticky flag. The file is re-runnable
// (IF NOT EXISTS / guarded UPDATEs), so the test replays it on seeded legacy rows.
func TestMigration077ConvertsStickyPaidFlags(t *testing.T) {
	h := testHandler(t)
	ctx := t.Context()
	sqlText, err := os.ReadFile(filepath.Join("..", "database", "migrations", "077_billing_fixes.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var today time.Time
	if err := h.Pool.QueryRow(ctx, `SELECT CURRENT_DATE`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	start := monthStart.AddDate(0, -3, 0)
	pid := createIntegrationPerson(t, h)

	open := mkStoredVehicle(t, h, pid, 30, start.Format(dateLayout))
	ended := mkStoredVehicle(t, h, pid, 30, start.Format(dateLayout))
	endedOn := today.AddDate(0, 0, -3)
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET paid=true, paid_through=NULL WHERE id=$1`, open); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET paid=true, paid_through=NULL, end_date=$2 WHERE id=$1`, ended, endedOn); err != nil {
		t.Fatal(err)
	}
	rcFree := mkRecurring(t, h, pid, 20, start.Format(dateLayout))
	rcInv := mkRecurring(t, h, pid, 20, start.Format(dateLayout))
	if _, err := h.Pool.Exec(ctx, `UPDATE recurring_charges SET paid=true WHERE id = ANY($1)`, []int64{rcFree, rcInv}); err != nil {
		t.Fatal(err)
	}
	invKey := start.Format("2006-01")
	var invID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO invoices (number, person_id, subtotal, total) VALUES ($1,$2,20,20) RETURNING id`,
		"M077-"+strconv.FormatInt(time.Now().UnixNano(), 10), pid).Scan(&invID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO invoice_source (invoice_id, kind, ref_id, period_key) VALUES ($1,'recurring',$2,$3)`, invID, rcInv, invKey); err != nil {
		t.Fatal(err)
	}

	if _, err := h.Pool.Exec(ctx, string(sqlText)); err != nil {
		t.Fatalf("replay 077: %v", err)
	}

	through := func(id int64) string {
		var d *time.Time
		if err := h.Pool.QueryRow(ctx, `SELECT paid_through FROM vehicles WHERE id=$1`, id).Scan(&d); err != nil {
			t.Fatal(err)
		}
		if d == nil {
			return ""
		}
		return d.Format(dateLayout)
	}
	if got, want := through(open), monthStart.AddDate(0, 0, -1).Format(dateLayout); got != want {
		t.Errorf("open paid vehicle: paid_through %s, want the last complete month's end %s", got, want)
	}
	wantEnded := endedOn // the whole stay is complete
	if b := monthStart.AddDate(0, 0, -1); b.After(wantEnded) {
		wantEnded = b
	}
	if got, want := through(ended), wantEnded.Format(dateLayout); got != want {
		t.Errorf("ended paid vehicle: paid_through %s, want %s (covers its end date)", got, want)
	}

	keys := func(id int64) (bool, []string) {
		var paid bool
		var ks []string
		if err := h.Pool.QueryRow(ctx, `SELECT paid, paid_periods FROM recurring_charges WHERE id=$1`, id).Scan(&paid, &ks); err != nil {
			t.Fatal(err)
		}
		return paid, ks
	}
	var complete []string
	for m := 0; m < 3; m++ {
		complete = append(complete, start.AddDate(0, m, 0).Format("2006-01"))
	}
	if paid, ks := keys(rcFree); paid || fmt.Sprint(ks) != fmt.Sprint(complete) {
		t.Errorf("paid recurring: paid=%v keys=%v, want false %v", paid, ks, complete)
	}
	if paid, ks := keys(rcInv); paid || fmt.Sprint(ks) != fmt.Sprint(complete[1:]) {
		t.Errorf("invoiced period must stay with its invoice: paid=%v keys=%v, want false %v", paid, ks, complete[1:])
	}
}
