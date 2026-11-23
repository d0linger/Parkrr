//go:build integration

package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

func TestReviewToggleReversalRetainsReceiptAndTaxMetadata(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	cid := mkChargeP(t, h, pid, "History", 100, "2026-05-01")
	setChargePaidT(t, h, cid, true)
	var id int64
	var paidOn time.Time
	if err := h.Pool.QueryRow(t.Context(), `SELECT id,paid_on FROM payments WHERE person_id=$1`, pid).Scan(&id, &paidOn); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(t.Context(), `INSERT INTO payment_tax_metadata(payment_id,received_on,tax_property_id) SELECT $1,$2,id FROM tax_properties ORDER BY id LIMIT 1`, id, paidOn); err != nil {
		t.Fatal(err)
	}
	setChargePaidT(t, h, cid, false)
	var reversed bool
	var date time.Time
	var propertyID int64
	var allocations, audits int
	if err := h.Pool.QueryRow(t.Context(), `SELECT p.reversed,p.paid_on,m.tax_property_id,
		(SELECT count(*) FROM payment_allocations WHERE payment_id=p.id),
		(SELECT count(*) FROM audit_log WHERE entity='payment' AND entity_id=p.id AND changes ? 'released_allocations')
		FROM payments p JOIN payment_tax_metadata m ON m.payment_id=p.id WHERE p.id=$1`, id).
		Scan(&reversed, &date, &propertyID, &allocations, &audits); err != nil {
		t.Fatal(err)
	}
	if !reversed || !date.Equal(paidOn) || propertyID == 0 || allocations != 0 || audits != 1 {
		t.Fatalf("receipt/history lost: reversed=%v date=%v property=%d allocations=%d audits=%d", reversed, date, propertyID, allocations, audits)
	}
	setChargePaidT(t, h, cid, true)
	var active, history int
	if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FILTER(WHERE NOT reversed),count(*) FROM payments WHERE person_id=$1`, pid).Scan(&active, &history); err != nil {
		t.Fatal(err)
	}
	if active != 1 || history != 2 {
		t.Fatalf("expected one active and one retained receipt, got %d/%d", active, history)
	}
}

func TestReviewRepeatedPeriodPaymentPreservesIdentity(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	start := firstOfMonthMonthsAgo(3)
	aid := mkAgreement(t, h, pid, 30, "monthly", start.Format(dateLayout))
	key := start.Format("2006-01")
	setAgreementPeriodPaidT(t, h, aid, key, true)
	var before int64
	if err := h.Pool.QueryRow(t.Context(), `SELECT id FROM payments WHERE settles_kind='agreement' AND settles_ref=$1 AND settles_period=$2`, aid, key).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := pgx.BeginFunc(t.Context(), h.Pool, func(tx pgx.Tx) error {
		return recordPeriodPaymentTx(t.Context(), tx, pid, "agreement", aid, key, 30, nil)
	}); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := h.Pool.QueryRow(t.Context(), `SELECT id FROM payments WHERE settles_kind='agreement' AND settles_ref=$1 AND settles_period=$2`, aid, key).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("identical paid click replaced receipt %d with %d", before, after)
	}
}

func TestReviewSplitCreditConcurrentInvoiceSettlement(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	h.Now = func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	pid := createIntegrationPerson(t, h)
	for range 2 {
		rec := postPayment(t, h, pid, map[string]any{"amount": 50, "paid_on": "2026-05-01", "method": "bar"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("advance: %d %s", rec.Code, rec.Body.String())
		}
	}
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid)
	// This unrelated running rental must not consume the invoice credit budget.
	mkStoredVehicle(t, h, pid, 30, "2026-10-01")
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 3)
	for range 3 {
		wg.Go(func() {
			req := httptest.NewRequest(http.MethodPost, "/apply-credit", nil)
			req.SetPathValue("id", strconv.FormatInt(pid, 10))
			rec := httptest.NewRecorder()
			h.ApplyCredit(rec, req)
			results <- rec
		})
	}
	wg.Wait()
	close(results)
	for rec := range results {
		if rec.Code != http.StatusOK {
			t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
		}
	}
	current := getInvoiceT(t, h, iv.ID)
	if current.OpenAmount != 0 || current.PaidAmount != 100 {
		t.Fatalf("split credit not consumed exactly once: %+v", current)
	}
	var receipts, allocations int
	var amount float64
	if err := h.Pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM payments WHERE person_id=$1),count(*),sum(amount) FROM invoice_payments WHERE invoice_id=$2`, pid, iv.ID).Scan(&receipts, &allocations, &amount); err != nil {
		t.Fatal(err)
	}
	if receipts != 2 || allocations != 2 || amount != 100 {
		t.Fatalf("cash changed: receipts=%d allocations=%d amount=%v", receipts, allocations, amount)
	}
}

func TestReviewPartialInvoicedEndCannotBeExtended(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	h.Now = func() time.Time { return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC) }
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, "2026-09-01")
	setStatus(t, h, vid, "collected", "2026-10-06")
	createInvoice(t, h, pid)
	rec := changeStatus(t, h, vid, map[string]any{"status": "stored"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("invoiced partial period cannot be reopened in place: %d %s", rec.Code, rec.Body.String())
	}
}

func TestReviewSliderRefreshesAmountAfterSourceLock(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	cid := mkChargeP(t, h, pid, "Concurrent price", 100, "2026-05-01")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	blocker, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if err := lockInvoicePersonTx(ctx, blocker, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, `UPDATE charges SET amount=150 WHERE id=$1`, cid); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/paid", strings.NewReader(`{"paid":true}`)).WithContext(ctx)
		req.SetPathValue("id", strconv.FormatInt(cid, 10))
		rec := httptest.NewRecorder()
		h.SetChargePaid(rec, req)
		done <- rec
	}()
	waitForLockWait(ctx, t, h, "%pg_advisory_xact_lock%")
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rec := <-done
	if rec.Code != http.StatusOK {
		t.Fatalf("paid: %d %s", rec.Code, rec.Body.String())
	}
	var amount float64
	if err := h.Pool.QueryRow(ctx, `SELECT amount FROM payments WHERE person_id=$1 AND NOT reversed`, pid).Scan(&amount); err != nil {
		t.Fatal(err)
	}
	if amount != 150 {
		t.Fatalf("slider used stale amount: %v", amount)
	}
}

func TestReviewLogoutFailureKeepsCookieForRetry(t *testing.T) {
	h := testHandler(t)
	mgr, err := auth.NewManager(h.Pool, auth.SessionConfig{MaxAge: 3600}, false, false, "synthetic-review-secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Deterministic session-DELETE failure; no schema or real session needed.
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "synthetic-session"})
	rec := httptest.NewRecorder()
	(&AuthHandler{Handler: h, Auth: mgr}).Logout(rec, req)
	if rec.Code != http.StatusInternalServerError || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("unconfirmed logout reported success or discarded retry cookie: %d %v", rec.Code, rec.Result().Cookies())
	}
}

func TestReviewAttributionPageMatchesFullHistory(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	start := firstOfMonthMonthsAgo(3)
	aid := mkAgreement(t, h, pid, 30, "monthly", start.Format(dateLayout))
	for i := range 3 {
		setAgreementPeriodPaidT(t, h, aid, start.AddDate(0, i, 0).Format("2006-01"), true)
	}
	all, err := h.resolvePaymentItems(t.Context(), pid)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("fixture: expected three receipts, got %d", len(all))
	}
	for id, want := range all {
		page, err := h.resolvePaymentItemsFor(t.Context(), pid, []int64{id})
		if err != nil || len(page) != 1 || !reflect.DeepEqual(page[id], want) {
			t.Fatalf("page differs: %v %v", page, err)
		}
	}
	empty, err := h.resolvePaymentItemsFor(t.Context(), pid, []int64{})
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty page: %v %v", empty, err)
	}
}

func TestReviewTaxExportStableIDsAndManifest(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	receipt := postPayment(t, h, pid, map[string]any{"amount": 12, "method": "bar", "paid_on": "2094-05-01"})
	if receipt.Code != http.StatusCreated {
		t.Fatalf("receipt: %d %s", receipt.Code, receipt.Body.String())
	}
	rec := httptest.NewRecorder()
	h.TaxYearPackage(rec, httptest.NewRequest(http.MethodGet, "/api/reports/tax-year.zip?year=2094", nil))
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("zip: %d %v", rec.Code, err)
	}
	entries := map[string][]byte{}
	for _, entry := range zr.File {
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries[entry.Name] = data
	}
	var manifest struct {
		Version int `json:"version"`
		Year    int `json:"year"`
	}
	if err := json.Unmarshal(entries["manifest.json"], &manifest); err != nil || manifest.Version != 3 || manifest.Year != 2094 {
		t.Fatalf("manifest: %+v %v", manifest, err)
	}
	if !bytes.Contains(entries["einnahmen.csv"], []byte("payment_id;person_id;steuerobjekt_id")) || !bytes.Contains(entries["zuordnungen.csv"], []byte("allocation_id;payment_id")) {
		t.Fatal("stable linkage columns missing")
	}
}

func TestReviewFinancialCorrectionReopensArchive(t *testing.T) {
	for _, action := range []string{"reverse_payment", "cancel_invoice"} {
		t.Run(action, func(t *testing.T) {
			h := testHandler(t)
			compliantSeller(t, h)
			h.Now = func() time.Time { return time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC) }
			pid := createIntegrationPerson(t, h)
			vid := mkStoredVehicle(t, h, pid, 30, "2026-09-01")
			setStatus(t, h, vid, "collected", "2026-09-30")
			iv := createInvoice(t, h, pid)
			payInvoices(t, h, pid, map[string]any{"amount": iv.Total, "method": "bar", "auto": true})
			if !getVehicleT(t, h, pid, vid).Archived {
				t.Fatal("fully covered rental was not archived")
			}
			req := httptest.NewRequest(http.MethodDelete, "/correction", nil)
			rec := httptest.NewRecorder()
			if action == "reverse_payment" {
				var paymentID int64
				if err := h.Pool.QueryRow(t.Context(), `SELECT payment_id FROM invoice_payments WHERE invoice_id=$1`, iv.ID).Scan(&paymentID); err != nil {
					t.Fatal(err)
				}
				req.SetPathValue("id", strconv.FormatInt(paymentID, 10))
				h.DeletePayment(rec, req)
			} else {
				req.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
				h.CancelInvoice(rec, req)
			}
			if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
				t.Fatalf("correction: %d %s", rec.Code, rec.Body.String())
			}
			if getVehicleT(t, h, pid, vid).Archived {
				t.Fatal("uncovered financial position remained hidden in archive")
			}
		})
	}
}

func TestReviewPaymentGuardRefreshesMetadataAfterWait(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	rec := postPayment(t, h, pid, map[string]any{"amount": 10, "paid_on": "2092-06-01", "method": "bar"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("receipt: %d %s", rec.Code, rec.Body.String())
	}
	var payment payment
	if err := json.Unmarshal(rec.Body.Bytes(), &payment); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	blocker, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `SELECT id FROM payments WHERE id=$1 FOR UPDATE`, payment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, `INSERT INTO payment_tax_metadata(payment_id,received_on,tax_property_id) SELECT $1,'2093-06-01',id FROM tax_properties ORDER BY id LIMIT 1`, payment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, `INSERT INTO tax_year_locks(year) VALUES(2093)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM tax_year_locks WHERE year=2093`) })
	done := make(chan error, 1)
	go func() {
		done <- pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
			return protectPaymentChangesTx(ctx, tx, []int64{payment.ID})
		})
	}()
	waitForLockWait(ctx, t, h, "%payments%FOR UPDATE%")
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var conflict *settlementConflictError
	if err := <-done; !errors.As(err, &conflict) || !strings.Contains(conflict.msg, "2093") {
		t.Fatalf("guard missed committed metadata year: %v", err)
	}
}

func TestReviewAttributionPageMeasurement(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	aid := mkAgreement(t, h, pid, 30, "monthly", "2026-01-01")
	if _, err := h.Pool.Exec(t.Context(), `INSERT INTO payments(person_id,amount,method,paid_on,settles_kind,settles_ref,settles_period)
		SELECT $1,30,'bar','2095-06-01','agreement',$2,to_char(date '1900-01-01'+n*interval '1 month','YYYY-MM')
		FROM generate_series(1,2000) n`, pid, aid); err != nil {
		t.Fatal(err)
	}
	var last int64
	if err := h.Pool.QueryRow(t.Context(), `SELECT max(id) FROM payments WHERE person_id=$1`, pid).Scan(&last); err != nil {
		t.Fatal(err)
	}
	query := `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT p.id,p.settles_period,p.amount,
		(SELECT string_agg(COALESCE(NULLIF(v.label,''),NULLIF(v.license_plate,''),cat.name,'Gefährt'),', ' ORDER BY v.id)
		FROM flat_rate_period_vehicles fv JOIN vehicles v ON v.id=fv.vehicle_id
		LEFT JOIN categories cat ON cat.id=v.category_id WHERE fv.period_id=p.settles_ref)
		FROM payments p WHERE p.person_id=$1 AND NOT p.reversed AND ($2::bigint[] IS NULL OR p.id=ANY($2))
		AND p.settles_kind='agreement' AND p.settles_period<>'extras'`
	for _, tc := range []struct {
		name string
		ids  []int64
		want int
	}{
		{"full-history", nil, 2000}, {"page", []int64{last - 1, last}, 2},
	} {
		var raw []byte
		if err := h.Pool.QueryRow(t.Context(), query, pid, tc.ids).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var plans []struct {
			Plan struct {
				Rows   float64 `json:"Actual Rows"`
				Blocks int     `json:"Shared Hit Blocks"`
			} `json:"Plan"`
			Duration float64 `json:"Execution Time"`
		}
		if err := json.Unmarshal(raw, &plans); err != nil || len(plans) != 1 || plans[0].Plan.Rows != float64(tc.want) {
			t.Fatalf("plan: %s %v", raw, err)
		}
		t.Logf("%s: rows=%g shared_hit_blocks=%d execution_ms=%.3f (synthetic warm/local; no production latency claim)", tc.name, plans[0].Plan.Rows, plans[0].Plan.Blocks, plans[0].Duration)
	}
}

func TestReviewDuplicatePreservesDetailsAndRollsBackPhotoFailure(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 30, "2026-01-01")
	if _, err := h.Pool.Exec(t.Context(), `UPDATE vehicles SET length_m=6,width_m=2.4,height_m=3,weight_t=3.5,
		needs_power=true,planner_symbol='car',rate=0,cost_override=0,paid=true WHERE id=$1`, vid); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(t.Context(), `INSERT INTO vehicle_photos(vehicle_id,filename,content_type,data,sort_order)
		VALUES($1,'first','image/png','x',9),($1,'second','image/png','y',2)`, vid); err != nil {
		t.Fatal(err)
	}
	request := func(ctx context.Context) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/duplicate", strings.NewReader(`{"start_date":"2026-11-01"}`)).WithContext(ctx)
		req.SetPathValue("id", strconv.FormatInt(vid, 10))
		rec := httptest.NewRecorder()
		h.DuplicateVehicle(rec, req)
		return rec
	}
	rec := request(t.Context())
	if rec.Code != http.StatusCreated {
		t.Fatalf("copy: %d %s", rec.Code, rec.Body.String())
	}
	var copy struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &copy); err != nil {
		t.Fatal(err)
	}
	var correct bool
	if err := h.Pool.QueryRow(t.Context(), `SELECT length_m=6 AND width_m=2.4 AND height_m=3 AND weight_t=3.5
		AND needs_power AND planner_symbol='car' AND rate=0 AND cost_override=0 AND NOT paid AND NOT archived
		AND spot_id IS NULL AND paid_through IS NULL AND end_date IS NULL
		AND (SELECT array_agg(sort_order ORDER BY sort_order) FROM vehicle_photos WHERE vehicle_id=v.id)=ARRAY[2,9]
		FROM vehicles v WHERE v.id=$1`, copy.ID).Scan(&correct); err != nil || !correct {
		t.Fatalf("copy contract violated: %v %v", correct, err)
	}
	blocker, err := h.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(t.Context(), `LOCK TABLE vehicle_photos IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- request(ctx) }()
	barrier, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	waitForLockWait(barrier, t, h, "%INSERT INTO vehicle_photos%")
	cancel() // Abort precisely after the new vehicle INSERT, during photo copying.
	rec = <-done
	if rec.Code == http.StatusCreated {
		t.Fatal("failed photo copy reported success")
	}
	if err := blocker.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM vehicles WHERE person_id=$1`, pid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("failed copy left a partial vehicle: count=%d", count)
	}
}
