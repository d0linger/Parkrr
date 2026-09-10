package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestAnonymizeBlankEmailClearsReminder(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 100)
	inv := createInvoice(t, h, pid)
	ctx := t.Context()
	if _, err := h.Pool.Exec(ctx, `UPDATE persons SET email='' WHERE id=$1`, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `INSERT INTO invoice_reminders(invoice_id,level,sent_to) VALUES($1,1,'erased-person@example.invalid')`, inv.ID); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/anonymize", nil)
	r.SetPathValue("id", strconv.FormatInt(pid, 10))
	rec := httptest.NewRecorder()
	h.AnonymizePerson(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymize: %d %s", rec.Code, rec.Body.String())
	}
	var anon bool
	var email, recipient string
	if err := h.Pool.QueryRow(ctx, `SELECT p.anonymized,p.email,ir.sent_to FROM persons p JOIN invoices i ON i.person_id=p.id JOIN invoice_reminders ir ON ir.invoice_id=i.id WHERE p.id=$1`, pid).Scan(&anon, &email, &recipient); err != nil {
		t.Fatal(err)
	}
	if !anon || email != "" || recipient != "anonymisiert" {
		t.Fatalf("unexpected state anon=%v email=%q recipient=%q", anon, email, recipient)
	}
}
