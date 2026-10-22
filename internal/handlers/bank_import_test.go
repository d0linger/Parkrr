package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseCamt053RejectsUnsupportedAmounts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		amount   string
		currency string
	}{
		{name: "not a number", amount: "NaN", currency: "EUR"},
		{name: "infinity", amount: "+Inf", currency: "EUR"},
		{name: "too large", amount: "1000000000000", currency: "EUR"},
		{name: "foreign currency", amount: "50.00", currency: "USD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := `<Document><BkToCstmrStmt><Stmt><Ntry><Amt Ccy="` + tc.currency + `">` + tc.amount + `</Amt>` +
				`<CdtDbtInd>CRDT</CdtDbtInd><Sts>BOOK</Sts><BookgDt><Dt>2026-10-10</Dt></BookgDt>` +
				`</Ntry></Stmt></BkToCstmrStmt></Document>`
			if got, _, err := parseCamt053([]byte(doc)); err == nil {
				t.Fatalf("unsupported bank amount accepted: %+v", got)
			}
		})
	}
}

func TestBankConcurrentAssignmentBooksOnePayment(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	firstPerson, _, _ := seasonFixture(t, h, "")
	otherPerson, _, _ := seasonFixture(t, h, "")
	var bankID int64
	if err := h.Pool.QueryRow(ctx, `INSERT INTO bank_transactions (ref, booked_on, amount)
		VALUES ($1, '2026-10-10', 50) RETURNING id`, "concurrent-"+strconv.FormatInt(time.Now().UnixNano(), 10)).Scan(&bankID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM bank_transactions WHERE id = $1`, bankID)
	})
	blocker, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `SELECT id FROM persons WHERE id = $1 FOR UPDATE`, firstPerson); err != nil {
		t.Fatal(err)
	}
	assign := func(personID int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/bank-transactions/x/assign",
			strings.NewReader(`{"person_id":`+strconv.FormatInt(personID, 10)+`}`)).WithContext(ctx)
		r.SetPathValue("id", strconv.FormatInt(bankID, 10))
		w := httptest.NewRecorder()
		h.AssignBankTransaction(w, r)
		return w
	}
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- assign(firstPerson) }()
	for {
		var waiting bool
		if err := h.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("first assignment did not reach the payment lock")
		case <-time.After(10 * time.Millisecond):
		}
	}
	second := assign(otherPerson)
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if second.Code != http.StatusConflict {
		t.Errorf("concurrent assignment = %d: %s, want 409", second.Code, second.Body.String())
	}
	select {
	case w := <-first:
		if w.Code != http.StatusOK {
			t.Fatalf("first assignment = %d: %s", w.Code, w.Body.String())
		}
	case <-ctx.Done():
		t.Fatal("first assignment did not finish")
	}
	var payments int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE idempotency_key = $1`, "bank-tx-"+strconv.FormatInt(bankID, 10)).Scan(&payments); err != nil || payments != 1 {
		t.Fatalf("payments for bank transaction = %d, want 1: %v", payments, err)
	}
}

func TestSuggestBankMatchRejectsMultipleInvoiceNumbers(t *testing.T) {
	open := []openInvoiceRef{
		{ID: 1, Number: "2026-0001", OpenAmount: 50},
		{ID: 2, Number: "2026-0002", OpenAmount: 50},
	}
	for _, tc := range []struct {
		name string
		refs []openInvoiceRef
	}{
		{name: "original order", refs: open},
		{name: "reversed order", refs: []openInvoiceRef{open[1], open[0]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := bankTxOut{Amount: 100, Remittance: strings.Join([]string{open[0].Number, open[1].Number}, " und ")}
			if got := suggestBankMatch(tx, tc.refs); got != nil {
				t.Fatalf("ambiguous invoice references produced a suggestion: %+v", got)
			}
		})
	}
	if got := suggestBankMatch(bankTxOut{Amount: 50, Remittance: "Rechnung 2026-0001"}, open); got == nil || got.ID != 1 || !got.Strong {
		t.Fatalf("unique invoice reference = %+v, want invoice 1", got)
	}
}
