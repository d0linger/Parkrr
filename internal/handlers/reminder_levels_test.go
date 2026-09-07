package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/preining/parkrr/internal/mail"
)

// multiCaptureSender fängt ALLE gesendeten Mails ab (der bestehende captureSender
// merkt sich nur die letzte — für die Stufenprüfung brauchen wir die Reihe).
type multiCaptureSender struct {
	subjects []string
	bodies   []string
}

func (c *multiCaptureSender) Enabled() bool { return true }
func (c *multiCaptureSender) Send(_ context.Context, _ []string, subject, body string) error {
	c.subjects = append(c.subjects, subject)
	c.bodies = append(c.bodies, body)
	return nil
}

var _ mail.Sender = (*multiCaptureSender)(nil)

// Das Mahnwesen hat Stufen und Gedächtnis (Hundert 15): dreimal senden ergibt
// Zahlungserinnerung → 1. Mahnung → 2. Mahnung, jede Stufe mit eigenem Betreff
// und eigenem Ton, und die Stufe entsteht aus den GESPEICHERTEN Versendungen —
// nicht aus dem Zufall, wer gerade klickt.
func TestReminderLevelsEscalate(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	sender := &multiCaptureSender{}
	h.Mail = sender

	var pid, invID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name, email) VALUES ('Mahn','Integration','mahn@example.com') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("person: %v", err)
	}
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO invoices (number, person_id, subtotal, total) VALUES ('MAHN-2026-0001', $1, 80, 80) RETURNING id`,
		pid).Scan(&invID); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = h.Pool.Exec(c, `DELETE FROM invoice_reminders WHERE invoice_id=$1`, invID)
		_ = purgeExec(c, h.Pool, `DELETE FROM invoices WHERE id=$1`, invID)
		_ = purgeExec(c, h.Pool, `DELETE FROM persons WHERE id=$1`, pid)
	})

	remind := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(invID, 10)+"/remind", nil)
		req.SetPathValue("id", strconv.FormatInt(invID, 10))
		rec := httptest.NewRecorder()
		h.RemindInvoice(rec, req)
		return rec
	}
	for i := 0; i < 4; i++ {
		if rec := remind(); rec.Code != http.StatusOK {
			t.Fatalf("remind #%d: %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if len(sender.subjects) != 4 {
		t.Fatalf("%d Mails, erwartet 4", len(sender.subjects))
	}
	wantSubj := []string{"Zahlungserinnerung", "1. Mahnung", "2. Mahnung", "2. Mahnung"}
	for i, want := range wantSubj {
		if !strings.Contains(sender.subjects[i], want) {
			t.Errorf("Betreff #%d: %q, erwartet %q darin", i+1, sender.subjects[i], want)
		}
	}
	// Der Ton eskaliert wirklich: die 1. Mahnung nennt die Erinnerung, die letzte
	// nennt die Frist.
	if !strings.Contains(sender.bodies[1], "trotz unserer Zahlungserinnerung") {
		t.Error("die 1. Mahnung liest sich wie die Erinnerung")
	}
	if !strings.Contains(sender.bodies[2], "letzte Mahnung") {
		t.Error("die 2. Mahnung nennt sich nicht als letzte")
	}
	// Und die Liste trägt das Gedächtnis (reminder_count in ListInvoices).
	lreq := httptest.NewRequest(http.MethodGet, "/api/persons/"+strconv.FormatInt(pid, 10)+"/invoices", nil)
	lreq.SetPathValue("id", strconv.FormatInt(pid, 10))
	lrec := httptest.NewRecorder()
	h.ListInvoices(lrec, lreq)
	if lrec.Code != http.StatusOK {
		t.Fatalf("list: %d", lrec.Code)
	}
	if !strings.Contains(lrec.Body.String(), `"reminder_count":4`) {
		t.Errorf("die Rechnungsliste trägt das Mahn-Gedächtnis nicht: %s", lrec.Body.String())
	}
}

// Eine GESCHEITERTE Mail zählt die Stufe nicht hoch — sonst bekäme der Kunde als
// erste Post die 1. Mahnung.
func TestFailedReminderDoesNotEscalate(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	h.Mail = &captureSender{enabled: true, err: context.DeadlineExceeded}

	var pid, invID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name, email) VALUES ('MahnFail','Integration','mf@example.com') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("person: %v", err)
	}
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO invoices (number, person_id, subtotal, total) VALUES ('MAHN-2026-0002', $1, 80, 80) RETURNING id`,
		pid).Scan(&invID); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = h.Pool.Exec(c, `DELETE FROM invoice_reminders WHERE invoice_id=$1`, invID)
		_ = purgeExec(c, h.Pool, `DELETE FROM invoices WHERE id=$1`, invID)
		_ = purgeExec(c, h.Pool, `DELETE FROM persons WHERE id=$1`, pid)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(invID, 10)+"/remind", nil)
	req.SetPathValue("id", strconv.FormatInt(invID, 10))
	rec := httptest.NewRecorder()
	h.RemindInvoice(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("gescheiterter Versand: %d, erwartet 502", rec.Code)
	}
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM invoice_reminders WHERE invoice_id=$1`, invID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("eine gescheiterte Mail wurde als Mahnung gezählt (%d)", n)
	}
}

