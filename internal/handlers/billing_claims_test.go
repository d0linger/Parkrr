package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCreateInvoiceRejectsConcurrentlyPaidCharge(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	blocker, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := blocker.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback blocker: %v", err)
		}
	}()
	if _, err := blocker.Exec(ctx, `SELECT id FROM billing_settings WHERE id=1 FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/invoices", strings.NewReader(`{}`)).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("id", strconv.FormatInt(pid, 10))
		rec := httptest.NewRecorder()
		h.CreateInvoice(rec, r)
		done <- rec
	}()
	for {
		var blocked bool
		err := h.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FROM billing_settings WHERE id=1 FOR UPDATE%')`).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	paid := postPayment(t, h, pid, map[string]any{"amount": 100, "allocate": true})
	if paid.Code != http.StatusCreated {
		t.Fatalf("payment: %d %s", paid.Code, paid.Body.String())
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var inv *httptest.ResponseRecorder
	select {
	case inv = <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if inv.Code != http.StatusConflict {
		t.Fatalf("guarded invoice: %d %s", inv.Code, inv.Body.String())
	}
	var payments, invoices int
	if err := h.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payments WHERE person_id=$1),(SELECT count(*) FROM invoices WHERE person_id=$1)`, pid).Scan(&payments, &invoices); err != nil {
		t.Fatal(err)
	}
	if payments != 1 || invoices != 0 {
		t.Fatalf("guarded counts payments=%d invoices=%d", payments, invoices)
	}
	t.Log("GUARD VERIFIED: payment 201, raced invoice 409, payment retained, no invoice committed")

}

func TestChargeClaimsExcludeConcurrentOwners(t *testing.T) {
	for _, invoiceFirst := range []bool{true, false} {
		name := "payment_first"
		if invoiceFirst {
			name = "invoice_first"
		}
		t.Run(name, func(t *testing.T) {
			h := testHandler(t)
			compliantSeller(t, h)
			pid := createIntegrationPerson(t, h)
			chargeFor(t, h, pid, 10)
			iv := createInvoice(t, h, pid)
			var cid, payID int64
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if err := h.Pool.QueryRow(ctx, `INSERT INTO charges(person_id,description,amount,quantity) VALUES($1,'claim guard',100,1) RETURNING id`, pid).Scan(&cid); err != nil {
				t.Fatal(err)
			}
			if err := h.Pool.QueryRow(ctx, `INSERT INTO payments(person_id,amount,method) VALUES($1,100,'bar') RETURNING id`, pid).Scan(&payID); err != nil {
				t.Fatal(err)
			}
			invSQL := `INSERT INTO invoice_source(invoice_id,kind,ref_id,period_key) VALUES($1,'charge',$2,'')`
			paySQL := `INSERT INTO payment_allocations(payment_id,kind,ref_id,amount) VALUES($1,'charge',$2,100) ON CONFLICT(kind,ref_id) DO NOTHING`
			firstSQL, secondSQL := paySQL, invSQL
			secondTable := "invoice_source"
			firstID, secondID := payID, iv.ID
			if invoiceFirst {
				firstSQL, secondSQL = invSQL, paySQL
				secondTable = "payment_allocations"
				firstID, secondID = iv.ID, payID
			}
			tx, err := h.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
					t.Errorf("rollback first claim: %v", err)
				}
			}()
			if _, err := tx.Exec(ctx, firstSQL, firstID, cid); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := h.Pool.Exec(ctx, secondSQL, secondID, cid); done <- err }()
			for {
				var blocked bool
				if err := h.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
					WHERE datname=current_database() AND wait_event='advisory' AND query LIKE $1)`,
					"INSERT INTO "+secondTable+"%").Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("second claim did not wait: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			var secondErr error
			select {
			case secondErr = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var pgErr *pgconn.PgError
			if !errors.As(secondErr, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "charge_claim_exclusive" {
				t.Fatalf("expected claim conflict, got %v", secondErr)
			}
			var ic, pc int
			if err := h.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM invoice_source WHERE kind='charge' AND ref_id=$1),(SELECT count(*) FROM payment_allocations WHERE kind='charge' AND ref_id=$1)`, cid).Scan(&ic, &pc); err != nil {
				t.Fatal(err)
			}
			if ic+pc != 1 {
				t.Fatalf("claim counts invoice=%d payment=%d", ic, pc)
			}
			t.Logf("GUARD VERIFIED %s: second statement started before first commit, waited, then rejected 23505; invoice=%d payment=%d", name, ic, pc)
		})
	}
}
