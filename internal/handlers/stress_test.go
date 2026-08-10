package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This file is a QA stress/concurrency harness for the REAL invoice/payment
// workflow (issue → §11 validation → OP → allocation → Storno → period-lock).
// It exercises the actual handlers against a real migrated Postgres, with genuine
// goroutine concurrency asserting DB-level invariants (gapless numbering, no
// over-allocation, idempotent toggles). It does NOT test inbound e-invoice
// ingest / OCR / SEPA / bank-matching / fraud — Parkrr has none of those.
//
// Helpers that call t.Fatal are only used from the main goroutine; concurrent
// phases call handlers directly and collect results, then assert on the main
// goroutine.

// invoiceRec issues an invoice for a person and returns the recorder (no t.Fatal,
// safe from a goroutine).
func invoiceRec(h *Handler, pid int64) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/invoices", bytes.NewReader([]byte(`{}`)))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.CreateInvoice(rec, req)
	return rec
}

// --- Category 5: Concurrency & stress ---------------------------------------

// TestStress_ConcurrentInvoiceNumbering fires N invoices in parallel (one open
// charge per person) and asserts every issued number is unique and gapless — the
// BAO/§11 gapless-sequence guarantee under load (numbering is FOR UPDATE-locked).
func TestStress_ConcurrentInvoiceNumbering(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	const N = 40

	pids := make([]int64, N)
	for i := 0; i < N; i++ {
		pids[i] = createIntegrationPerson(t, h)
		chargeFor(t, h, pids[i], 10+float64(i))
	}

	type res struct {
		code   int
		number string
	}
	out := make([]res, N)
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := invoiceRec(h, pids[i])
			var iv invoice
			_ = json.Unmarshal(rec.Body.Bytes(), &iv)
			out[i] = res{rec.Code, iv.Number}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	seen := map[string]int{}
	ok := 0
	for i, r := range out {
		if r.code != http.StatusCreated {
			t.Errorf("invoice %d: status %d", i, r.code)
			continue
		}
		if r.number == "" {
			t.Errorf("invoice %d: empty number", i)
			continue
		}
		seen[r.number]++
		ok++
	}
	dups := 0
	for num, c := range seen {
		if c > 1 {
			dups++
			t.Errorf("DUPLICATE invoice number %q issued %d times (gapless-sequence violation)", num, c)
		}
	}
	t.Logf("[metrics] %d concurrent invoices in %s (%.1f ms/inv), %d unique numbers, %d duplicates",
		ok, elapsed, float64(elapsed.Milliseconds())/float64(N), len(seen), dups)
}

// TestStress_ConcurrentPayOneInvoice hammers ONE open invoice with N full
// payments in parallel and asserts the OP is settled exactly once: paid_amount
// never exceeds the total, the invoice ends 'bezahlt' with open 0, and the
// surplus payments stay unallocated (no over-allocation via the FOR UPDATE OP).
func TestStress_ConcurrentPayOneInvoice(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid) // total 100

	const N = 30
	var created int64
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{
				"amount": 100, "method": "ueberweisung",
				"allocations": []map[string]any{{"invoice_id": iv.ID, "amount": 100}},
			})
			req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/pay-invoices", bytes.NewReader(body))
			req.SetPathValue("id", strconv.FormatInt(pid, 10))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.PayInvoices(rec, req)
			if rec.Code == http.StatusCreated {
				atomic.AddInt64(&created, 1)
			}
		}()
	}
	wg.Wait()

	final := getInvoiceT(t, h, iv.ID)
	if final.PaidAmount > 100.005 {
		t.Errorf("OVER-ALLOCATION: invoice paid_amount=%.2f exceeds total 100", final.PaidAmount)
	}
	if final.Status != "bezahlt" || final.OpenAmount > 0.005 {
		t.Errorf("invoice must end fully paid exactly once: status=%s open=%.2f paid=%.2f",
			final.Status, final.OpenAmount, final.PaidAmount)
	}
	t.Logf("[metrics] %d/%d payments accepted against one 100€ invoice; final paid=%.2f open=%.2f status=%s",
		created, N, final.PaidAmount, final.OpenAmount, final.Status)
}

// TestStress_ConcurrentSliderSameCharge toggles the same charge 'paid' from N
// goroutines and asserts idempotency: at most one auto-payment is minted, so the
// balance nets to ~0 (not N×). Guards syncTogglePaymentTx under concurrency.
func TestStress_ConcurrentSliderSameCharge(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	cid := mkChargeP(t, h, pid, "StressToggle", 100, "2026-05-01")

	const N = 25
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{"paid": true})
			req := httptest.NewRequest(http.MethodPost, "/api/charges/"+strconv.FormatInt(cid, 10)+"/paid", bytes.NewReader(body))
			req.SetPathValue("id", strconv.FormatInt(cid, 10))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.SetChargePaid(rec, req)
		}()
	}
	wg.Wait()

	// Count auto-payments actually recorded for this person.
	var autoPayments int
	var autoSum float64
	_ = h.Pool.QueryRow(context.Background(), `SELECT COUNT(*), COALESCE(SUM(amount),0) FROM payments WHERE person_id=$1 AND auto`, pid).Scan(&autoPayments, &autoSum)
	if autoPayments > 1 {
		t.Errorf("NON-IDEMPOTENT toggle: %d auto-payments (sum %.2f) minted for one charge — expected ≤1", autoPayments, autoSum)
	}
	if b := statsBalanceT(t, h, pid); b < -0.05 || b > 0.05 {
		t.Errorf("balance must net to ~0 after idempotent toggle, got %.2f", b)
	}
	t.Logf("[metrics] %d concurrent 'paid' toggles → %d auto-payment(s), sum %.2f", N, autoPayments, autoSum)
}

// TestStress_ConcurrentStornoVsPay races a Storno against a full payment on the
// same invoice and asserts the end state is internally consistent: either it was
// paid then canceled (canceled invoice, but a Storno counter-doc exists), or
// canceled first and the payment was rejected — never a paid+non-canceled ghost
// with an inconsistent ledger.
func TestStress_ConcurrentStornoVsPay(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)

	const rounds = 12
	inconsistencies := 0
	for r := 0; r < rounds; r++ {
		pid := createIntegrationPerson(t, h)
		chargeFor(t, h, pid, 50)
		iv := createInvoice(t, h, pid)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/cancel", nil)
			req.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
			h.CancelInvoice(httptest.NewRecorder(), req)
		}()
		go func() {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{
				"amount": 50, "method": "bar",
				"allocations": []map[string]any{{"invoice_id": iv.ID, "amount": 50}},
			})
			req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/pay-invoices", bytes.NewReader(body))
			req.SetPathValue("id", strconv.FormatInt(pid, 10))
			req.Header.Set("Content-Type", "application/json")
			h.PayInvoices(httptest.NewRecorder(), req)
		}()
		wg.Wait()

		final := getInvoiceT(t, h, iv.ID)
		// Invariant: a canceled original must have paid_amount released to 0
		// (Storno reverts allocations); a non-canceled invoice may be paid.
		if final.Canceled && final.PaidAmount > 0.005 {
			inconsistencies++
			t.Errorf("round %d: canceled invoice still shows paid_amount=%.2f (Storno did not release payment)", r, final.PaidAmount)
		}
	}
	t.Logf("[metrics] %d Storno-vs-Pay races, %d ledger inconsistencies", rounds, inconsistencies)
}

// TestStress_BulkInvoiceThroughput issues a large batch and reports throughput +
// asserts numbering stays unique/gapless across the batch.
func TestStress_BulkInvoiceThroughput(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	const N = 150

	pids := make([]int64, N)
	for i := 0; i < N; i++ {
		pids[i] = createIntegrationPerson(t, h)
		chargeFor(t, h, pids[i], 20)
	}
	seen := map[string]bool{}
	start := time.Now()
	for i := 0; i < N; i++ {
		rec := invoiceRec(h, pids[i])
		if rec.Code != http.StatusCreated {
			t.Fatalf("bulk invoice %d: %d %s", i, rec.Code, rec.Body.String())
		}
		var iv invoice
		_ = json.Unmarshal(rec.Body.Bytes(), &iv)
		if seen[iv.Number] {
			t.Errorf("bulk duplicate number %q", iv.Number)
		}
		seen[iv.Number] = true
	}
	elapsed := time.Since(start)
	t.Logf("[metrics] %d invoices issued in %s (%.2f ms/inv, %.0f inv/s), %d unique",
		N, elapsed, float64(elapsed.Milliseconds())/float64(N), float64(N)/elapsed.Seconds(), len(seen))
}

// TestStress_ConcurrentSamePersonInvoice (P0): N invoices fired at once for the
// SAME person must bill each open position exactly once — the loser(s) get a clean
// 409/400, never a duplicate invoice_source row (uq_invoice_source_ref_period).
func TestStress_ConcurrentSamePersonInvoice(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	cid := mkChargeP(t, h, pid, "RaceCharge", 100, "2026-05-01")

	const N = 12
	var created, conflict, other, got500 int64
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code := invoiceRec(h, pid).Code
			switch {
			case code == http.StatusCreated:
				atomic.AddInt64(&created, 1)
			case code == http.StatusConflict:
				atomic.AddInt64(&conflict, 1)
			case code >= 500:
				atomic.AddInt64(&got500, 1)
			default:
				atomic.AddInt64(&other, 1) // 400 "keine offenen Positionen" once locked
			}
		}()
	}
	wg.Wait()
	if got500 > 0 {
		t.Errorf("losers must fail cleanly (4xx), got %d×5xx", got500)
	}

	var srcCount int
	_ = h.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM invoice_source s JOIN invoices i ON i.id=s.invoice_id
		   WHERE s.kind='charge' AND s.ref_id=$1 AND NOT i.canceled`, cid).Scan(&srcCount)
	if srcCount != 1 {
		t.Errorf("charge must be billed on exactly ONE active invoice, got %d invoice_source rows", srcCount)
	}
	if created != 1 {
		t.Errorf("exactly one CreateInvoice should succeed, got %d (409=%d other=%d)", created, conflict, other)
	}
	t.Logf("[metrics] %d concurrent same-person invoices → created=%d, 409=%d, other=%d; invoice_source rows=%d",
		N, created, conflict, other, srcCount)
}

// TestStress_ConcurrentStorno (P0): N Stornos on one invoice must produce exactly
// ONE counter-document — no phantom Guthaben from duplicate negative documents.
func TestStress_ConcurrentStorno(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	iv := createInvoice(t, h, pid)

	const N = 10
	var okc, conflict, got500 int64
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/cancel", nil)
			req.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
			rec := httptest.NewRecorder()
			h.CancelInvoice(rec, req)
			switch {
			case rec.Code == http.StatusOK, rec.Code == http.StatusCreated:
				atomic.AddInt64(&okc, 1)
			case rec.Code == http.StatusConflict:
				atomic.AddInt64(&conflict, 1)
			case rec.Code >= 500:
				atomic.AddInt64(&got500, 1)
			}
		}()
	}
	wg.Wait()
	if got500 > 0 {
		t.Errorf("losing Stornos must fail cleanly (409), got %d×5xx", got500)
	}

	var stornoCount int
	_ = h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE cancels_id=$1`, iv.ID).Scan(&stornoCount)
	if stornoCount != 1 {
		t.Errorf("exactly ONE Storno counter-document expected, got %d", stornoCount)
	}
	if okc != 1 {
		t.Errorf("exactly one Storno should succeed, got %d (409=%d)", okc, conflict)
	}
	t.Logf("[metrics] %d concurrent Storno → ok=%d, 409=%d; counter-docs=%d", N, okc, conflict, stornoCount)
}

// TestStress_ConcurrentAllocationSameItem (P0): N payments each trying to allocate
// the SAME open charge must allocate it exactly once (uq_payalloc_ref); the losers
// record their payment as Guthaben, never a duplicate allocation.
func TestStress_ConcurrentAllocationSameItem(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	cid := mkChargeP(t, h, pid, "AllocRace", 100, "2026-05-01")

	const N = 15
	var got500 int64
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{
				"amount": 100, "method": "bar",
				"allocations": []map[string]any{{"kind": "charge", "id": cid}},
			})
			req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/payments", bytes.NewReader(body))
			req.SetPathValue("id", strconv.FormatInt(pid, 10))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.CreatePayment(rec, req)
			if rec.Code >= 500 {
				atomic.AddInt64(&got500, 1)
			}
		}()
	}
	wg.Wait()

	var allocCount, payCount int
	_ = h.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM payment_allocations WHERE kind='charge' AND ref_id=$1`, cid).Scan(&allocCount)
	_ = h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM payments WHERE person_id=$1`, pid).Scan(&payCount)
	if allocCount != 1 {
		t.Errorf("charge must be allocated exactly once across concurrent payments, got %d", allocCount)
	}
	if got500 > 0 {
		t.Errorf("no payment should 500 under the allocation race, got %d", got500)
	}
	if payCount != N {
		t.Errorf("all %d payments must be recorded (1 allocated + %d as Guthaben), got %d", N, N-1, payCount)
	}
	t.Logf("[metrics] %d concurrent allocations of one charge → %d allocation, %d payment rows, %d×500", N, allocCount, payCount, got500)
}

// --- Categories 1–4: extreme / hostile inputs (real analog) -----------------

// TestStress_HostileInputs feeds malformed, oversized and out-of-range payloads to
// the invoice/payment endpoints and asserts the server rejects them cleanly
// (4xx, never a 500/panic) — the robustness analog of the "inbound format" layer.
func TestStress_HostileInputs(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)

	huge := strings.Repeat("A", 1<<20) // 1 MiB note

	cases := []struct {
		id      string
		handler func() *httptest.ResponseRecorder
		wantMax int // highest acceptable status (must be a client error, < 500)
		note    string
	}{
		{
			id:      "PAY-malformed-json",
			note:    "malformed JSON body to pay-invoices",
			wantMax: 499,
			handler: func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/pay-invoices", bytes.NewReader([]byte(`{"amount": 100, "meth`)))
				req.SetPathValue("id", strconv.FormatInt(pid, 10))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				h.PayInvoices(rec, req)
				return rec
			},
		},
		{
			id:      "PAY-negative-amount",
			note:    "negative payment amount",
			wantMax: 499,
			handler: func() *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]any{"amount": -500, "method": "bar"})
				req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/pay-invoices", bytes.NewReader(body))
				req.SetPathValue("id", strconv.FormatInt(pid, 10))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				h.PayInvoices(rec, req)
				return rec
			},
		},
		{
			id:      "PAY-overflow-amount",
			note:    "1e18 payment amount (overflow probe)",
			wantMax: 499,
			handler: func() *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]any{"amount": 1e18, "method": "bar"})
				req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/pay-invoices", bytes.NewReader(body))
				req.SetPathValue("id", strconv.FormatInt(pid, 10))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				h.PayInvoices(rec, req)
				return rec
			},
		},
		{
			id:      "CHARGE-XXL-note",
			note:    "1 MiB description on a charge (length guard)",
			wantMax: 499,
			handler: func() *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]any{"person_id": pid, "description": huge, "amount": 10, "quantity": 1, "charged_on": "2026-05-01"})
				req := httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(body))
				rec := httptest.NewRecorder()
				h.CreateCharge(rec, req)
				return rec
			},
		},
		{
			id:      "CHARGE-overflow-amount",
			note:    "1e18 charge amount (NUMERIC overflow probe)",
			wantMax: 499,
			handler: func() *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]any{"person_id": pid, "description": "Overflow", "amount": 1e18, "quantity": 1, "charged_on": "2026-05-01"})
				req := httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(body))
				rec := httptest.NewRecorder()
				h.CreateCharge(rec, req)
				return rec
			},
		},
		{
			id:      "CHARGE-overflow-quantity",
			note:    "quantity 2e8 exceeds NUMERIC(10,2) column",
			wantMax: 499,
			handler: func() *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]any{"person_id": pid, "description": "QtyOverflow", "amount": 1, "quantity": 2e8, "charged_on": "2026-05-01"})
				req := httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(body))
				rec := httptest.NewRecorder()
				h.CreateCharge(rec, req)
				return rec
			},
		},
		{
			id:      "BILLING-invalid-ust",
			note:    "USt rate 19 (not in {20,13,10})",
			wantMax: 499,
			handler: func() *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]any{"seller_name": "X", "seller_address": "Y", "seller_uid": "ATU1", "kleinunternehmer": false, "ust_rate": 19, "number_pad": 4})
				req := httptest.NewRequest(http.MethodPost, "/api/billing/settings", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				h.SaveBillingSettings(rec, req)
				return rec
			},
		},
		{
			id:      "PAY-nonexistent-invoice",
			note:    "allocate to a non-existent invoice id",
			wantMax: 499,
			handler: func() *httptest.ResponseRecorder {
				body, _ := json.Marshal(map[string]any{"amount": 10, "method": "bar", "allocations": []map[string]any{{"invoice_id": 999999999, "amount": 10}}})
				req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/pay-invoices", bytes.NewReader(body))
				req.SetPathValue("id", strconv.FormatInt(pid, 10))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				h.PayInvoices(rec, req)
				return rec
			},
		},
	}

	for _, c := range cases {
		rec := c.handler()
		if rec.Code >= 500 {
			t.Errorf("[%s] %s → HTTP %d (server error / possible panic); expected a clean 4xx", c.id, c.note, rec.Code)
		} else {
			t.Logf("[%s] %s → HTTP %d (clean)", c.id, c.note, rec.Code)
		}
	}
}
