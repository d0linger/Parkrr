package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/mail"
)

func invoiceMailingsRequest(h *Handler, id int64) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/invoices/"+strconv.FormatInt(id, 10)+"/mailings", nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	w := httptest.NewRecorder()
	h.InvoiceMailings(w, req)
	return w
}

func retryOutboxRequest(h *Handler, id int64) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/mail-outbox/"+strconv.FormatInt(id, 10)+"/retry", nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	w := httptest.NewRecorder()
	h.RetryOutbox(w, req)
	return w
}

func cancelMailInvoice(t *testing.T, h *Handler, id int64) invoice {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(id, 10)+"/cancel", nil)
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	w := httptest.NewRecorder()
	h.CancelInvoice(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	var storno invoice
	if err := json.Unmarshal(w.Body.Bytes(), &storno); err != nil {
		t.Fatal(err)
	}
	return storno
}

func cleanupMailRows(t *testing.T, h *Handler, ids ...int64) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := h.Pool.Exec(context.Background(), `DELETE FROM mail_outbox WHERE id=ANY($1)`, ids); err != nil {
			t.Errorf("cleanup outbox rows: %v", err)
		}
	})
}

func TestInvoiceMailingsExcludesVehicleNotifications(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, iv := mailInvoiceFixture(t, h, "invoice.owner@example.com")
	other := createIntegrationPerson(t, h)
	want := map[int64]bool{}
	for _, kind := range []string{"invoice", "reminder", "contract_end", "price_change"} {
		person, recipient, subject := pid, "invoice.owner@example.com", "Invoice history"
		if kind == "contract_end" || kind == "price_change" {
			person, recipient, subject = other, "other.person@example.com", "Private vehicle notice"
		}
		// Vehicle and invoice IDs occupy independent namespaces; the shared ref_id
		// alone cannot establish which document a row belongs to.
		id, err := enqueueMail(ctx, h.Pool, outboxRow{Kind: kind, RefID: &iv.ID, PersonID: &person,
			Recipient: recipient, Subject: subject, Body: "Test"})
		if err != nil {
			t.Fatal(err)
		}
		cleanupMailRows(t, h, id)
		if kind == "invoice" || kind == "reminder" {
			want[id] = true
		}
	}
	w := invoiceMailingsRequest(h, iv.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("mailings: %d %s", w.Code, w.Body.String())
	}
	var entries []outboxEntry
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		t.Fatalf("mailings returned %d rows, want %d invoice-related rows: %s", len(entries), len(want), w.Body.String())
	}
	for _, entry := range entries {
		if !want[entry.ID] {
			t.Errorf("unrelated mailing exposed: %+v", entry)
		}
		delete(want, entry.ID)
	}
	if len(want) != 0 || strings.Contains(w.Body.String(), "other.person@example.com") || strings.Contains(w.Body.String(), "Private vehicle notice") {
		t.Fatalf("invoice history omitted expected mail or exposed another person's notification: %s", w.Body.String())
	}
}

func TestOutboxRejectsInvoiceCanceledBeforeDelivery(t *testing.T) {
	for _, retryFirst := range []bool{false, true} {
		name := "queued"
		if retryFirst {
			name = "retried"
		}
		t.Run(name, func(t *testing.T) {
			h := testHandler(t)
			ctx := context.Background()
			_, iv := mailInvoiceFixture(t, h, "canceled.delivery@example.com")
			fake := &attachSender{}
			h.Mail = fake
			_, id, err := h.queueInvoiceMail(ctx, iv.ID)
			if err != nil {
				t.Fatal(err)
			}
			cleanupMailRows(t, h, id)
			if retryFirst {
				h.finishOutbox(ctx, id, "failed", "Test relay unavailable")
				if w := retryOutboxRequest(h, id); w.Code != http.StatusOK {
					t.Fatalf("retry before cancellation: %d %s", w.Code, w.Body.String())
				}
			}
			cancelMailInvoice(t, h, iv.ID)
			if n := h.ProcessOutbox(ctx); n != 1 {
				t.Fatalf("processed %d rows, want 1", n)
			}
			if st, attempts, att, msg := outboxStatus(t, h, id); st != "failed" || attempts != 1 || att || !strings.Contains(msg, "storniert") {
				t.Fatalf("canceled invoice delivery: status=%s attempts=%d attachment=%v error=%q", st, attempts, att, msg)
			}
			if fake.calls != 0 {
				t.Fatalf("canceled invoice reached sender %d times", fake.calls)
			}
			if n := h.ProcessOutbox(ctx); n != 0 {
				t.Fatalf("stopped canceled mail was claimed again: %d", n)
			}
		})
	}
}

func TestRetryOutboxRejectsCanceledInvoiceAndStorno(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, iv := mailInvoiceFixture(t, h, "canceled.retry@example.com")
	fake := &attachSender{}
	h.Mail = fake
	_, originalMail, err := h.queueInvoiceMail(ctx, iv.ID)
	if err != nil {
		t.Fatal(err)
	}
	cleanupMailRows(t, h, originalMail)
	h.finishOutbox(ctx, originalMail, "failed", "Test relay unavailable")
	storno := cancelMailInvoice(t, h, iv.ID)
	stornoMail, err := enqueueMail(ctx, h.Pool, outboxRow{Kind: "invoice", RefID: &storno.ID, PersonID: &pid,
		Recipient: "canceled.retry@example.com", Subject: "Storno", Body: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	cleanupMailRows(t, h, stornoMail)
	h.finishOutbox(ctx, stornoMail, "failed", "Test relay unavailable")
	for _, id := range []int64{originalMail, stornoMail} {
		if w := retryOutboxRequest(h, id); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "storniert") {
			t.Errorf("retry canceled document: %d %s", w.Code, w.Body.String())
		}
		if st, attempts, att, msg := outboxStatus(t, h, id); st != "failed" || attempts != 0 || att || msg != "Test relay unavailable" {
			t.Errorf("rejected retry changed stopped row: status=%s attempts=%d attachment=%v error=%q", st, attempts, att, msg)
		}
	}
	if n := h.ProcessOutbox(ctx); n != 0 || fake.calls != 0 {
		t.Fatalf("rejected retries were delivered: processed=%d sender calls=%d", n, fake.calls)
	}

	// Cancellation still releases the position for a corrected invoice. The new,
	// valid document remains deliverable through the same queue and retry path.
	corrected := createInvoice(t, h, pid)
	_, correctedMail, err := h.queueInvoiceMail(ctx, corrected.ID)
	if err != nil {
		t.Fatal(err)
	}
	cleanupMailRows(t, h, correctedMail)
	h.finishOutbox(ctx, correctedMail, "failed", "Test relay unavailable")
	if w := retryOutboxRequest(h, correctedMail); w.Code != http.StatusOK {
		t.Fatalf("retry corrected invoice: %d %s", w.Code, w.Body.String())
	}
	if n := h.ProcessOutbox(ctx); n != 1 || len(fake.sent) != 1 || !strings.Contains(fake.sent[0].subject, corrected.Number) {
		t.Fatalf("corrected invoice not delivered: processed=%d sent=%+v", n, fake.sent)
	}
	if st, _, att, _ := outboxStatus(t, h, correctedMail); st != "sent" || att {
		t.Fatalf("corrected invoice status=%s attachment retained=%v", st, att)
	}
}

func TestOutboxRejectsQueuedStorno(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, iv := mailInvoiceFixture(t, h, "storno.delivery@example.com")
	storno := cancelMailInvoice(t, h, iv.ID)
	fake := &attachSender{}
	h.Mail = fake
	// queueInvoiceMail already rejects Storno documents. Guard delivery as well
	// against an older or malformed queued row without blocking the PDF itself.
	if _, _, err := h.queueInvoiceMail(ctx, storno.ID); err != errMailCanceled {
		t.Fatalf("queue Storno: %v, want errMailCanceled", err)
	}
	stored, found, err := h.fetchInvoice(ctx, storno.ID)
	if err != nil || !found {
		t.Fatalf("fetch Storno: found=%v error=%v", found, err)
	}
	pdf, err := invoicePDFBytes(stored)
	if err != nil || len(pdf) == 0 {
		t.Fatalf("Storno PDF no longer readable: %v", err)
	}
	id, err := enqueueMail(ctx, h.Pool, outboxRow{Kind: "invoice", RefID: &storno.ID, PersonID: &pid,
		Recipient: "storno.delivery@example.com", Subject: "Storno", Body: "Test", Att: pdf})
	if err != nil {
		t.Fatal(err)
	}
	cleanupMailRows(t, h, id)
	if n := h.ProcessOutbox(ctx); n != 1 || fake.calls != 0 {
		t.Fatalf("Storno reached sender: processed=%d calls=%d", n, fake.calls)
	}
	if st, _, att, msg := outboxStatus(t, h, id); st != "failed" || att || !strings.Contains(msg, "storniert") {
		t.Fatalf("Storno delivery: status=%s attachment=%v error=%q", st, att, msg)
	}
}

type blockingInvoiceSender struct {
	attachSender
	started chan struct{}
	release chan struct{}
}

func (s *blockingInvoiceSender) SendWithAttachments(ctx context.Context, to []string, subject, body string, atts []mail.Attachment) error {
	close(s.started)
	select {
	case <-s.release:
		return s.attachSender.SendWithAttachments(ctx, to, subject, body, atts)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestInvoiceDeliverySerializesCancellation(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, iv := mailInvoiceFixture(t, h, "concurrent.delivery@example.com")
	fake := &blockingInvoiceSender{started: make(chan struct{}), release: make(chan struct{})}
	h.Mail = fake
	_, id, err := h.queueInvoiceMail(ctx, iv.ID)
	if err != nil {
		t.Fatal(err)
	}
	cleanupMailRows(t, h, id)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(fake.release) }) }
	var workers sync.WaitGroup
	defer func() {
		release()
		cancel()
		workers.Wait()
	}()
	delivered := make(chan int, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		delivered <- h.ProcessOutbox(ctx)
	}()
	select {
	case <-fake.started:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not reach sender")
	}
	creq := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/cancel", nil).WithContext(ctx)
	creq.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
	canceled := make(chan *httptest.ResponseRecorder, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		w := httptest.NewRecorder()
		h.CancelInvoice(w, creq)
		canceled <- w
	}()

	// Wait for PostgreSQL to confirm the competing cancellation is blocked on
	// the row, rather than assuming that a goroutine has run after a sleep.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		if err := h.Pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
			AND wait_event_type='Lock' AND query LIKE 'UPDATE invoices SET canceled=true, paid_amount=0%')`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case w := <-canceled:
			t.Fatalf("cancellation committed before delivery finished: %d %s", w.Code, w.Body.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("cancellation never waited for the invoice delivery lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	release()
	select {
	case n := <-delivered:
		if n != 1 || len(fake.sent) != 1 {
			t.Fatalf("delivery before cancellation: processed=%d sent=%d", n, len(fake.sent))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delivery did not finish")
	}
	select {
	case w := <-canceled:
		if w.Code != http.StatusOK {
			t.Fatalf("cancellation after delivery: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not finish after delivery")
	}
	if st, _, att, _ := outboxStatus(t, h, id); st != "sent" || att {
		t.Fatalf("delivered invoice state=%s attachment retained=%v", st, att)
	}
	var isCanceled bool
	if err := h.Pool.QueryRow(ctx, `SELECT canceled FROM invoices WHERE id=$1`, iv.ID).Scan(&isCanceled); err != nil || !isCanceled {
		t.Fatalf("invoice cancellation did not persist: canceled=%v error=%v", isCanceled, err)
	}
}
