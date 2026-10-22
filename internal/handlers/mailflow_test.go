package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/mail"
)

// attachSender records mails including attachments, like a relay would receive them.
type attachSender struct {
	err   error
	sent  []attachMail
	calls int
}

type attachMail struct {
	to      []string
	subject string
	body    string
	atts    []mail.Attachment
}

func (a *attachSender) Enabled() bool { return true }
func (a *attachSender) Send(ctx context.Context, to []string, subject, body string) error {
	return a.SendWithAttachments(ctx, to, subject, body, nil)
}
func (a *attachSender) SendWithAttachments(_ context.Context, to []string, subject, body string, atts []mail.Attachment) error {
	a.calls++
	if a.err != nil {
		return a.err
	}
	a.sent = append(a.sent, attachMail{to: to, subject: subject, body: body, atts: atts})
	return nil
}

func mailInvoiceFixture(t *testing.T, h *Handler, email string) (int64, invoice) {
	t.Helper()
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	if _, err := h.Pool.Exec(context.Background(), `UPDATE persons SET email=$2, address='Hauptstraße 1, 1010 Wien' WHERE id=$1`, pid, email); err != nil {
		t.Fatal(err)
	}
	chargeFor(t, h, pid, 42.0)
	return pid, createInvoice(t, h, pid)
}

func outboxStatus(t *testing.T, h *Handler, id int64) (status string, attempts int, hasAtt bool, lastErr string) {
	t.Helper()
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT status, attempts, attachment IS NOT NULL, last_error FROM mail_outbox WHERE id=$1`, id).
		Scan(&status, &attempts, &hasAtt, &lastErr); err != nil {
		t.Fatal(err)
	}
	return
}

// Rechnung per Mail: eingereiht mit PDF, vom Lauf zugestellt, danach ohne Anhang und
// mit Audit; der Versandstatus steht an der Rechnung.
func TestSendInvoiceMailThroughOutbox(t *testing.T) {
	h := testHandler(t)
	_, iv := mailInvoiceFixture(t, h, "rechnung.empfang@example.com")
	send := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/send", nil)
		req.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
		w := httptest.NewRecorder()
		h.SendInvoiceMail(w, req)
		return w
	}
	if w := send(); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("without SMTP: %d, want 503", w.Code)
	}
	fake := &attachSender{}
	h.Mail = fake
	w := send()
	if w.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	var res struct {
		OutboxID int64 `json:"outbox_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if st, _, att, _ := outboxStatus(t, h, res.OutboxID); st != "queued" || !att {
		t.Fatalf("queued row: status=%s attachment=%v", st, att)
	}
	if n := h.ProcessOutbox(context.Background()); n < 1 {
		t.Fatalf("ProcessOutbox handled %d rows", n)
	}
	if len(fake.sent) != 1 {
		t.Fatalf("sent %d mails, want 1", len(fake.sent))
	}
	m := fake.sent[0]
	if m.to[0] != "rechnung.empfang@example.com" || !strings.Contains(m.subject, iv.Number) {
		t.Errorf("mail = %v %q", m.to, m.subject)
	}
	if len(m.atts) != 1 || !bytes.HasPrefix(m.atts[0].Data, []byte("%PDF")) || !strings.HasSuffix(m.atts[0].Name, ".pdf") {
		t.Fatalf("attachment missing or not a PDF: %+v", m.atts)
	}
	if st, _, att, _ := outboxStatus(t, h, res.OutboxID); st != "sent" || att {
		t.Errorf("after delivery: status=%s attachment kept=%v", st, att)
	}
	lw := httptest.NewRecorder()
	lreq := httptest.NewRequest(http.MethodGet, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/mailings", nil)
	lreq.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
	h.InvoiceMailings(lw, lreq)
	if !strings.Contains(lw.Body.String(), `"status":"sent"`) {
		t.Errorf("mailings = %s", lw.Body.String())
	}
}

// Ein Fehlschlag wird mit wachsendem Abstand wiederholt, nach dem letzten Versuch
// endgültig "failed"; eine hängengebliebene Sendung wird nicht wiederholt.
func TestOutboxRetryAndStuckRows(t *testing.T) {
	h := testHandler(t)
	_, iv := mailInvoiceFixture(t, h, "retry@example.com")
	fake := &attachSender{err: errors.New("451 temporary failure for retry@example.com")}
	h.Mail = fake
	_, id, err := h.queueInvoiceMail(context.Background(), iv.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.ProcessOutbox(context.Background())
	st, attempts, _, lastErr := outboxStatus(t, h, id)
	if st != "queued" || attempts != 1 || strings.Contains(lastErr, "retry@example.com") {
		t.Fatalf("after first failure: status=%s attempts=%d err=%q (address must be redacted)", st, attempts, lastErr)
	}
	var later bool
	_ = h.Pool.QueryRow(context.Background(), `SELECT next_attempt_at > now() FROM mail_outbox WHERE id=$1`, id).Scan(&later)
	if !later {
		t.Error("next attempt must wait")
	}
	// Bis zum letzten Versuch durchspielen.
	for i := 0; i < len(outboxBackoff); i++ {
		_, _ = h.Pool.Exec(context.Background(), `UPDATE mail_outbox SET next_attempt_at=now() WHERE id=$1`, id)
		h.ProcessOutbox(context.Background())
	}
	if st, attempts, att, _ := outboxStatus(t, h, id); st != "failed" || attempts != len(outboxBackoff)+1 || att {
		t.Fatalf("after all attempts: status=%s attempts=%d attachment=%v", st, attempts, att)
	}
	// Erneut senden holt das PDF frisch aus der Rechnung.
	fake.err = nil
	rw := httptest.NewRecorder()
	rreq := httptest.NewRequest(http.MethodPost, "/api/mail-outbox/"+strconv.FormatInt(id, 10)+"/retry", nil)
	rreq.SetPathValue("id", strconv.FormatInt(id, 10))
	h.RetryOutbox(rw, rreq)
	if rw.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", rw.Code, rw.Body.String())
	}
	h.ProcessOutbox(context.Background())
	if st, _, _, _ := outboxStatus(t, h, id); st != "sent" || len(fake.sent) != 1 || len(fake.sent[0].atts) != 1 {
		t.Fatalf("retry did not deliver with attachment: status=%s sent=%d", st, len(fake.sent))
	}

	// Hängengeblieben: "sending" seit 20 Minuten -> failed, nicht erneut gesendet.
	_, stuck, err := h.queueInvoiceMail(context.Background(), iv.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = h.Pool.Exec(context.Background(), `UPDATE mail_outbox SET status='sending', claimed_at=now()-interval '20 minutes' WHERE id=$1`, stuck)
	before := fake.calls
	h.ProcessOutbox(context.Background())
	if st, _, _, msg := outboxStatus(t, h, stuck); st != "failed" || !strings.Contains(msg, "Zustellung unklar") || fake.calls != before {
		t.Fatalf("stuck row: status=%s msg=%q extra sends=%d", st, msg, fake.calls-before)
	}
}

// Mahnautomatik: aus, bis sie eingeschaltet wird; dann Stufe 1 nach der Frist, gebucht
// erst bei Zustellung; „nicht mahnen“ und eine laufende Mahnung werden respektiert.
func TestAutomaticDunning(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, iv := mailInvoiceFixture(t, h, "mahnung.auto@example.com")
	fake := &attachSender{}
	h.Mail = fake
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `UPDATE mail_automation SET auto_dunning=false, dunning_days=14 WHERE id=1`)
	})
	// Rechnungen sind unveränderlich (BAO § 131), also wandert die Uhr statt des
	// Fälligkeitsdatums: 40 Tage später ist die Frist von 14 Tagen sicher vorbei.
	h.Now = func() time.Time { return time.Now().AddDate(0, 0, 40) }
	countFor := func() (n int) {
		_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM mail_outbox WHERE kind='reminder' AND ref_id=$1`, iv.ID).Scan(&n)
		return
	}
	if n, _ := h.QueueDueReminders(ctx); n != 0 || countFor() != 0 {
		t.Fatal("automation is off by default and must not queue anything")
	}
	if _, err := h.Pool.Exec(ctx, `UPDATE mail_automation SET auto_dunning=true, dunning_days=14 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	// Person schaltet ab.
	_, _ = h.Pool.Exec(ctx, `UPDATE persons SET no_dunning=true WHERE id=$1`, pid)
	_, _ = h.QueueDueReminders(ctx)
	if countFor() != 0 {
		t.Fatal("no_dunning person must not be reminded automatically")
	}
	_, _ = h.Pool.Exec(ctx, `UPDATE persons SET no_dunning=false WHERE id=$1`, pid)
	if _, err := h.QueueDueReminders(ctx); err != nil {
		t.Fatal(err)
	}
	if countFor() != 1 {
		t.Fatalf("expected one queued reminder, got %d", countFor())
	}
	// Ein zweiter Lauf reiht nichts doppelt ein, und der Knopf verweigert, solange sie wartet.
	_, _ = h.QueueDueReminders(ctx)
	if countFor() != 1 {
		t.Fatal("second run queued a duplicate")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/remind", nil)
	req.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
	rw := httptest.NewRecorder()
	h.RemindInvoice(rw, req)
	if rw.Code != http.StatusConflict {
		t.Fatalf("manual remind while one is queued: %d, want 409", rw.Code)
	}
	var levels int
	_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM invoice_reminders WHERE invoice_id=$1`, iv.ID).Scan(&levels)
	if levels != 0 {
		t.Fatal("the level must not be booked before delivery")
	}
	h.ProcessOutbox(ctx)
	var level int
	if err := h.Pool.QueryRow(ctx, `SELECT level FROM invoice_reminders WHERE invoice_id=$1`, iv.ID).Scan(&level); err != nil || level != 1 {
		t.Fatalf("booked level = %d (%v), want 1 after delivery", level, err)
	}
	if len(fake.sent) != 1 || !strings.Contains(fake.sent[0].subject, "Zahlungserinnerung") {
		t.Fatalf("sent = %+v", fake.sent)
	}
	// Gerade gemahnt (heute): die nächste Stufe ist erst nach weiteren 14 Tagen fällig.
	h.Now = time.Now
	_, _ = h.QueueDueReminders(ctx)
	if countFor() != 1 {
		t.Fatal("next level queued before the waiting period")
	}
}

// Wurde zwischen Einreihen und Versand von Hand gemahnt, ist die vorgemerkte Stufe
// überholt und wird nicht verschickt.
func TestQueuedReminderSupersededByManual(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, iv := mailInvoiceFixture(t, h, "ueberholt@example.com")
	fake := &attachSender{}
	h.Mail = fake
	ref := iv.ID
	id, err := enqueueMail(ctx, h.Pool, outboxRow{Kind: "reminder", RefID: &ref, PersonID: &pid, Level: 1,
		Recipient: "ueberholt@example.com", Subject: "x", Body: "y"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(ctx, `INSERT INTO invoice_reminders (invoice_id, level, sent_to) VALUES ($1, 1, 'ueberholt@example.com')`, iv.ID); err != nil {
		t.Fatal(err)
	}
	h.ProcessOutbox(ctx)
	if st, _, _, msg := outboxStatus(t, h, id); st != "failed" || !strings.Contains(msg, "von Hand") || len(fake.sent) != 0 {
		t.Fatalf("superseded reminder: status=%s msg=%q sent=%d", st, msg, len(fake.sent))
	}
}

func TestRemindInvoicesBulk(t *testing.T) {
	h := testHandler(t)
	_, a := mailInvoiceFixture(t, h, "bulk.a@example.com")
	_, b := mailInvoiceFixture(t, h, "")
	h.Mail = &attachSender{}
	body := `{"ids":[` + strconv.FormatInt(a.ID, 10) + `,` + strconv.FormatInt(b.ID, 10) + `,` + strconv.FormatInt(a.ID, 10) + `]}`
	w := httptest.NewRecorder()
	h.RemindInvoices(w, httptest.NewRequest(http.MethodPost, "/api/invoices/remind", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("bulk: %d %s", w.Code, w.Body.String())
	}
	var res struct {
		Sent    int            `json:"sent"`
		Results []remindResult `json:"results"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Sent != 1 || len(res.Results) != 2 {
		t.Fatalf("sent=%d results=%+v (duplicate id must count once)", res.Sent, res.Results)
	}
	if res.Results[1].Sent || !strings.Contains(res.Results[1].Error, "E-Mail") {
		t.Errorf("invoice without address: %+v", res.Results[1])
	}
	big := `{"ids":[` + strings.Repeat("1,", maxBulkReminders) + `1]}`
	w = httptest.NewRecorder()
	h.RemindInvoices(w, httptest.NewRequest(http.MethodPost, "/api/invoices/remind", strings.NewReader(big)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("more than %d ids: %d, want 400", maxBulkReminders, w.Code)
	}
}

// Die Vorschau zeigt, was fakturiert würde, ohne eine Nummer zu verbrauchen.
func TestInvoiceRunPreview(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	chargeFor(t, h, pid, 55.5)
	var next int
	_ = h.Pool.QueryRow(ctx, `SELECT next_invoice_no FROM billing_settings WHERE id=1`).Scan(&next)
	w := httptest.NewRecorder()
	h.InvoiceRunPreview(w, httptest.NewRequest(http.MethodGet, "/api/invoices/preview", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	var res struct {
		Persons []previewPerson `json:"persons"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	var found *previewPerson
	for i := range res.Persons {
		if res.Persons[i].PersonID == pid {
			found = &res.Persons[i]
		}
	}
	if found == nil || found.Subtotal != 55.5 || len(found.Lines) != 1 {
		t.Fatalf("person not previewed correctly: %+v", found)
	}
	if !strings.Contains(strings.Join(found.Warnings, " "), "Keine E-Mail-Adresse") {
		t.Errorf("warnings = %v", found.Warnings)
	}
	var after int
	_ = h.Pool.QueryRow(ctx, `SELECT next_invoice_no FROM billing_settings WHERE id=1`).Scan(&after)
	if after != next {
		t.Fatalf("preview consumed an invoice number: %d -> %d", next, after)
	}
}

// Anonymisieren leert wartende Mails der Person und verschickt sie nicht mehr.
func TestAnonymizeClearsOutbox(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, iv := mailInvoiceFixture(t, h, "weg@example.com")
	h.Mail = &attachSender{}
	_, id, err := h.queueInvoiceMail(ctx, iv.ID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/anonymize", nil)
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	w := httptest.NewRecorder()
	h.AnonymizePerson(w, req)
	if w.Code >= 300 {
		t.Fatalf("anonymize: %d %s", w.Code, w.Body.String())
	}
	var recipient, body, status string
	var att bool
	if err := h.Pool.QueryRow(ctx, `SELECT recipient, body, status, attachment IS NOT NULL FROM mail_outbox WHERE id=$1`, id).
		Scan(&recipient, &body, &status, &att); err != nil {
		t.Fatal(err)
	}
	if recipient != "anonymisiert" || body != "" || att || status != "failed" {
		t.Fatalf("outbox after anonymize: recipient=%q body=%q status=%s attachment=%v", recipient, body, status, att)
	}
}

func TestMailAutomationAndPersonDunning(t *testing.T) {
	h := testHandler(t)
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `UPDATE mail_automation SET auto_send_invoices=false, auto_dunning=false, dunning_days=14 WHERE id=1`)
	})
	put := func(body string) int {
		w := httptest.NewRecorder()
		h.SaveMailAutomation(w, httptest.NewRequest(http.MethodPut, "/api/mail-automation", strings.NewReader(body)))
		return w.Code
	}
	if c := put(`{"auto_dunning":true,"dunning_days":2}`); c != http.StatusBadRequest {
		t.Errorf("dunning_days 2: %d, want 400", c)
	}
	if c := put(`{"auto_send_invoices":true,"auto_dunning":true,"dunning_days":21}`); c != http.StatusOK {
		t.Fatalf("save: %d", c)
	}
	m, err := h.loadMailAutomation(context.Background())
	if err != nil || !m.AutoSendInvoices || !m.AutoDunning || m.DunningDays != 21 {
		t.Fatalf("loaded %+v %v", m, err)
	}
	pid := createIntegrationPerson(t, h)
	req := httptest.NewRequest(http.MethodPut, "/api/persons/"+strconv.FormatInt(pid, 10)+"/dunning", strings.NewReader(`{"no_dunning":true}`))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	w := httptest.NewRecorder()
	h.SetPersonDunning(w, req)
	var flag bool
	_ = h.Pool.QueryRow(context.Background(), `SELECT no_dunning FROM persons WHERE id=$1`, pid).Scan(&flag)
	if w.Code != http.StatusOK || !flag {
		t.Fatalf("set dunning: %d flag=%v", w.Code, flag)
	}
}
