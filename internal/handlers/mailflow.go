package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

// ---------- Rechnung per Mail (Ausbau 19) ----------

var (
	errMailNoAddress = errors.New("person has no e-mail address")
	errMailCanceled  = errors.New("invoice is canceled")
	errMailDisabled  = errors.New("smtp is not configured")
)

// invoiceMailBody ist der Begleittext zur Rechnung, aus dem Schnappschuss der
// Rechnung selbst (Aussteller, IBAN, Fußzeile), wie reminderBody.
func invoiceMailBody(iv invoice, name string) string {
	s := iv.Seller
	const df = "02.01.2006"
	var b strings.Builder
	if name != "" {
		fmt.Fprintf(&b, "Guten Tag %s,\n\n", name)
	} else {
		b.WriteString("Guten Tag,\n\n")
	}
	fmt.Fprintf(&b, "anbei erhalten Sie die Rechnung %s vom %s über %s.\n", iv.Number, iv.IssuedOn.Format(df), pdfMoney(iv.Total))
	if iv.DueOn != nil {
		fmt.Fprintf(&b, "Fällig am %s.\n", iv.DueOn.Format(df))
	}
	b.WriteString("\n")
	if iban := snapStr(s, "iban"); iban != "" {
		b.WriteString("Bitte überweisen Sie den Betrag auf:\n")
		fmt.Fprintf(&b, "  IBAN: %s\n", iban)
		if bic := snapStr(s, "bic"); bic != "" {
			fmt.Fprintf(&b, "  BIC:  %s\n", bic)
		}
		fmt.Fprintf(&b, "  Verwendungszweck: %s\n\n", iv.Number)
	}
	b.WriteString("Mit freundlichen Grüßen\n")
	if seller := snapStr(s, "name"); seller != "" {
		b.WriteString(seller + "\n")
	}
	if footer := trim(snapStr(s, "footer")); footer != "" {
		b.WriteString("\n" + footer + "\n")
	}
	return b.String()
}

// queueInvoiceMail legt die Rechnung samt PDF in die Warteschlange und gibt die
// Empfängeradresse zurück.
func (h *Handler) queueInvoiceMail(ctx context.Context, invoiceID int64) (string, int64, error) {
	if h.Mail == nil || !h.Mail.Enabled() {
		return "", 0, errMailDisabled
	}
	iv, found, err := h.fetchInvoice(ctx, invoiceID)
	if err != nil {
		return "", 0, err
	}
	if !found {
		return "", 0, pgx.ErrNoRows
	}
	if iv.Canceled || iv.CancelsID != nil {
		return "", 0, errMailCanceled
	}
	var email, first, last string
	if err := h.Pool.QueryRow(ctx, `SELECT email, first_name, last_name FROM persons WHERE id=$1`, iv.PersonID).
		Scan(&email, &first, &last); err != nil {
		return "", 0, err
	}
	email = trim(email)
	if email == "" {
		return "", 0, errMailNoAddress
	}
	pdf, err := invoicePDFBytes(iv)
	if err != nil {
		return "", 0, err
	}
	pid := iv.PersonID
	ref := iv.ID
	id, err := enqueueMail(ctx, h.Pool, outboxRow{Kind: "invoice", RefID: &ref, PersonID: &pid, Recipient: email,
		Subject: "Rechnung " + iv.Number, Body: invoiceMailBody(iv, trim(first+" "+last)),
		AttName: "Rechnung " + iv.Number + ".pdf", Att: pdf})
	if err != nil {
		return "", 0, err
	}
	h.KickOutbox()
	return email, id, nil
}

// SendInvoiceMail: POST /api/invoices/{id}/send — Rechnung als PDF per Mail
// (über die Warteschlange; 202, sobald sie eingereiht ist).
func (h *Handler) SendInvoiceMail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	to, qid, err := h.queueInvoiceMail(r.Context(), id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "invoice not found")
		return
	case errors.Is(err, errMailDisabled):
		writeError(w, http.StatusServiceUnavailable, "E-Mail ist nicht konfiguriert (SMTP)")
		return
	case errors.Is(err, errMailCanceled):
		writeError(w, http.StatusConflict, "Rechnung ist storniert")
		return
	case errors.Is(err, errMailNoAddress):
		writeError(w, http.StatusBadRequest, "Kein E-Mail-Kontakt für diese Person hinterlegt")
		return
	case err != nil:
		serverError(w, r, "could not queue invoice mail", err)
		return
	}
	h.audit(r, "send", "invoice", id, "Rechnungsversand per E-Mail eingereiht (Warteschlange #"+strconv.FormatInt(qid, 10)+")")
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "to": to, "outbox_id": qid})
}

type outboxEntry struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	RefID     *int64     `json:"ref_id"`
	Level     int        `json:"level,omitempty"`
	Recipient string     `json:"recipient"`
	Subject   string     `json:"subject"`
	Status    string     `json:"status"`
	Attempts  int        `json:"attempts"`
	LastError string     `json:"last_error,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	NextAt    time.Time  `json:"next_attempt_at"`
}

func scanOutbox(rows pgx.Rows) ([]outboxEntry, error) {
	defer rows.Close()
	out := []outboxEntry{}
	for rows.Next() {
		var e outboxEntry
		if err := rows.Scan(&e.ID, &e.Kind, &e.RefID, &e.Level, &e.Recipient, &e.Subject, &e.Status,
			&e.Attempts, &e.LastError, &e.CreatedAt, &e.SentAt, &e.NextAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const outboxCols = `id, kind, ref_id, level, recipient, subject, status, attempts, last_error, created_at, sent_at, next_attempt_at`

// InvoiceMailings: GET /api/invoices/{id}/mailings — Versand und Mahnungen dieser
// Rechnung aus der Warteschlange.
func (h *Handler) InvoiceMailings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT `+outboxCols+` FROM mail_outbox
	  WHERE ref_id=$1 AND kind IN ('invoice', 'reminder') ORDER BY id DESC LIMIT 50`, id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	out, err := scanOutbox(rows)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ListOutbox: GET /api/mail-outbox — wartende, laufende und fehlgeschlagene Mails
// sowie die gesendeten der letzten 30 Tage.
func (h *Handler) ListOutbox(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT `+outboxCols+` FROM mail_outbox
		  WHERE status <> 'sent' OR sent_at > now() - interval '30 days'
		  ORDER BY (status = 'failed') DESC, id DESC LIMIT 200`)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	out, err := scanOutbox(rows)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// RetryOutbox: POST /api/mail-outbox/{id}/retry — eine fehlgeschlagene Mail erneut
// einreihen. Eine Rechnung bekommt ihr PDF frisch aus dem unveränderlichen Dokument.
func (h *Handler) RetryOutbox(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var kind, status, recipient string
	var ref *int64
	if err := h.Pool.QueryRow(r.Context(), `SELECT kind, status, ref_id, recipient FROM mail_outbox WHERE id=$1`, id).
		Scan(&kind, &status, &ref, &recipient); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		serverError(w, r, "query failed", err)
		return
	}
	if status != "failed" {
		writeError(w, http.StatusConflict, "Nur fehlgeschlagene Mails lassen sich erneut senden")
		return
	}
	if recipient == "anonymisiert" {
		writeError(w, http.StatusConflict, "Die Person wurde anonymisiert")
		return
	}
	var att []byte
	if kind == "invoice" {
		if ref == nil {
			writeError(w, http.StatusConflict, "Rechnung nicht mehr lesbar")
			return
		}
		iv, found, err := h.fetchInvoice(r.Context(), *ref)
		if err != nil || !found {
			writeError(w, http.StatusConflict, "Rechnung nicht mehr lesbar")
			return
		}
		if iv.Canceled || iv.CancelsID != nil {
			writeError(w, http.StatusConflict, "Rechnung ist storniert")
			return
		}
		if att, err = invoicePDFBytes(iv); err != nil {
			serverError(w, r, "could not render pdf", err)
			return
		}
	}
	var attArg any
	if len(att) > 0 {
		attArg = att
	}
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE mail_outbox SET status='queued', attempts=0, next_attempt_at=now(), last_error='',
		        attachment=COALESCE($2, attachment) WHERE id=$1 AND status='failed'`, id, attArg)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "Für diese Rechnung ist bereits eine Mahnung unterwegs")
			return
		}
		serverError(w, r, "could not requeue", err)
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "Die Mail wurde inzwischen verändert")
		return
	}
	h.audit(r, "retry", "mail_outbox", id, "Fehlgeschlagene Mail erneut eingereiht")
	h.KickOutbox()
	writeJSON(w, http.StatusOK, map[string]any{"queued": true})
}

// ---------- Mail-Automatik ----------

type mailAutomation struct {
	AutoSendInvoices bool `json:"auto_send_invoices"`
	AutoDunning      bool `json:"auto_dunning"`
	DunningDays      int  `json:"dunning_days"`
	// EndReminderDays: so viele Tage vor dem Vertragsende geht die Erinnerung
	// (Ausbau 22); 0 = aus.
	EndReminderDays int `json:"end_reminder_days"`
}

func (h *Handler) loadMailAutomation(ctx context.Context) (mailAutomation, error) {
	m := mailAutomation{DunningDays: 14}
	err := h.Pool.QueryRow(ctx, `SELECT auto_send_invoices, auto_dunning, dunning_days, end_reminder_days FROM mail_automation WHERE id=1`).
		Scan(&m.AutoSendInvoices, &m.AutoDunning, &m.DunningDays, &m.EndReminderDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, nil
	}
	return m, err
}

// GetMailAutomation: GET /api/mail-automation (admin).
func (h *Handler) GetMailAutomation(w http.ResponseWriter, r *http.Request) {
	m, err := h.loadMailAutomation(r.Context())
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// SaveMailAutomation: PUT /api/mail-automation (admin). Beides bleibt aus, bis jemand
// es bewusst einschaltet.
func (h *Handler) SaveMailAutomation(w http.ResponseWriter, r *http.Request) {
	var in mailAutomation
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.DunningDays < 3 || in.DunningDays > 90 {
		writeError(w, http.StatusBadRequest, "dunning_days muss zwischen 3 und 90 liegen")
		return
	}
	if in.EndReminderDays < 0 || in.EndReminderDays > 60 {
		writeError(w, http.StatusBadRequest, "end_reminder_days muss zwischen 0 und 60 liegen")
		return
	}
	old, err := h.loadMailAutomation(r.Context())
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if _, err := h.Pool.Exec(r.Context(),
		`INSERT INTO mail_automation (id, auto_send_invoices, auto_dunning, dunning_days, end_reminder_days, updated_at)
		 VALUES (1, $1, $2, $3, $4, now())
		 ON CONFLICT (id) DO UPDATE SET auto_send_invoices=EXCLUDED.auto_send_invoices,
		        auto_dunning=EXCLUDED.auto_dunning, dunning_days=EXCLUDED.dunning_days,
		        end_reminder_days=EXCLUDED.end_reminder_days, updated_at=now()`,
		in.AutoSendInvoices, in.AutoDunning, in.DunningDays, in.EndReminderDays); err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	h.auditChange(r, "update", "mail_automation", 1, "Mail-Automatik geändert", diffFields(
		map[string]any{"auto_send_invoices": old.AutoSendInvoices, "auto_dunning": old.AutoDunning, "dunning_days": old.DunningDays, "end_reminder_days": old.EndReminderDays},
		map[string]any{"auto_send_invoices": in.AutoSendInvoices, "auto_dunning": in.AutoDunning, "dunning_days": in.DunningDays, "end_reminder_days": in.EndReminderDays}))
	writeJSON(w, http.StatusOK, in)
}

// SetPersonDunning: PUT /api/persons/{id}/dunning {"no_dunning": bool} — diese Person
// nie automatisch mahnen. Manuelle Erinnerungen bleiben möglich.
func (h *Handler) SetPersonDunning(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		NoDunning bool `json:"no_dunning"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var old bool
	err := h.Pool.QueryRow(r.Context(),
		`WITH prev AS (SELECT no_dunning FROM persons WHERE id=$1)
		 UPDATE persons SET no_dunning=$2, updated_at=now() WHERE id=$1 RETURNING (SELECT no_dunning FROM prev)`, id, in.NoDunning).Scan(&old)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}
	if err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	h.auditChange(r, "update", "person", id, "Automatische Mahnungen "+map[bool]string{true: "ausgeschaltet", false: "eingeschaltet"}[in.NoDunning],
		diffFields(map[string]any{"no_dunning": old}, map[string]any{"no_dunning": in.NoDunning}))
	writeJSON(w, http.StatusOK, map[string]any{"no_dunning": in.NoDunning})
}

// ---------- Automatische Mahnstufen (Ausbau 20) ----------

// QueueDueReminders reiht fällige Mahnungen ein, wenn die Automatik an ist: je offener
// Rechnung die nächste Stufe (höchstens Stufe 3), sobald seit Fälligkeit bzw. letzter
// Mahnung die eingestellte Zahl Tage vergangen ist. Ausgenommen: Personen mit „nicht
// mahnen“ oder ohne Adresse, und Rechnungen, für die schon eine Mahnung unterwegs ist.
// Die Stufe wird erst bei der Zustellung gebucht (deliverReminder).
func (h *Handler) QueueDueReminders(ctx context.Context) (int, error) {
	m, err := h.loadMailAutomation(ctx)
	if err != nil || !m.AutoDunning || h.Mail == nil || !h.Mail.Enabled() {
		return 0, err
	}
	y, mo, d := h.now().Date()
	today := time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
	rows, err := h.Pool.Query(ctx,
		`SELECT i.id, i.person_id, p.email, trim(p.first_name || ' ' || p.last_name),
		        (SELECT count(*) FROM invoice_reminders r WHERE r.invoice_id = i.id),
		        COALESCE((SELECT max(r.sent_at)::date FROM invoice_reminders r WHERE r.invoice_id = i.id), i.due_on)
		   FROM invoices i JOIN persons p ON p.id = i.person_id
		  WHERE NOT i.canceled AND i.cancels_id IS NULL AND i.due_on IS NOT NULL
		    AND i.total - i.paid_amount > 0.005
		    AND NOT p.anonymized AND NOT p.no_dunning AND btrim(p.email) <> ''
		    AND NOT EXISTS (SELECT 1 FROM mail_outbox o WHERE o.kind = 'reminder' AND o.ref_id = i.id
		                     AND o.status IN ('queued', 'sending'))
		  ORDER BY i.id`)
	if err != nil {
		return 0, err
	}
	type due struct {
		id, pid   int64
		email     string
		name      string
		prior     int
		reference time.Time
	}
	var list []due
	for rows.Next() {
		var x due
		if err := rows.Scan(&x.id, &x.pid, &x.email, &x.name, &x.prior, &x.reference); err != nil {
			rows.Close()
			return 0, err
		}
		if x.prior >= 3 || today.Before(x.reference.AddDate(0, 0, m.DunningDays)) {
			continue
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	queued := 0
	for _, x := range list {
		iv, found, err := h.fetchInvoice(ctx, x.id)
		if err != nil || !found {
			continue
		}
		level := x.prior + 1
		ref, pid := x.id, x.pid
		if _, err := enqueueMail(ctx, h.Pool, outboxRow{Kind: "reminder", RefID: &ref, PersonID: &pid, Level: level,
			Recipient: trim(x.email), Subject: reminderSubject(level, iv.Number), Body: h.reminderBody(iv, x.name, level)}); err != nil {
			if isUniqueViolation(err) {
				continue // inzwischen von einem anderen Lauf eingereiht
			}
			return queued, err
		}
		queued++
	}
	if queued > 0 {
		h.AuditSystem(auth.ContextWithSystemActor(ctx), "remind", "system", 0,
			"Automatische Mahnstufen: "+strconv.Itoa(queued)+" Mahnung(en) eingereiht", nil)
		h.KickOutbox()
	}
	return queued, nil
}

// QueueInvoiceMailAuto ist der Versand nach dem automatischen Rechnungslauf. Er gilt
// nur, wenn „Rechnungen automatisch senden“ an ist; Personen ohne Adresse werden
// übersprungen (die Rechnung steht trotzdem in Parkrr).
func (h *Handler) QueueInvoiceMailAuto(ctx context.Context, invoiceID int64) {
	m, err := h.loadMailAutomation(ctx)
	if err != nil || !m.AutoSendInvoices {
		return
	}
	if _, _, err := h.queueInvoiceMail(ctx, invoiceID); err != nil && !errors.Is(err, errMailNoAddress) && !errors.Is(err, errMailDisabled) {
		slog.Warn("auto-invoice: Rechnung nicht eingereiht", "invoice_id", invoiceID, "err", err)
	}
}

// ---------- Sammel-Erinnerung (Ausbau 1) ----------

// maxBulkReminders: je Aufruf, damit die Antwort sicher im eigenen Schreibfenster
// bleibt (10 × 20 s SMTP-Frist < 5 min). Die Oberfläche teilt größere Mengen auf.
const maxBulkReminders = 10

type remindResult struct {
	InvoiceID int64  `json:"invoice_id"`
	Sent      bool   `json:"sent"`
	Level     int    `json:"level,omitempty"`
	Error     string `json:"error,omitempty"`
}

// RemindInvoices: POST /api/invoices/remind {"ids": [...]} — erinnert mehrere
// Rechnungen nacheinander. Jede läuft über DENSELBEN Handler wie der Knopf
// (RemindInvoice, per synthetischem Request wie der automatische Rechnungslauf), mit
// seiner Sperre, Stufenlogik und Protokollierung. Ein Fehler hält die übrigen nicht auf.
func (h *Handler) RemindInvoices(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(r, &in); err != nil || len(in.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids fehlen")
		return
	}
	if len(in.IDs) > maxBulkReminders {
		writeError(w, http.StatusBadRequest, "höchstens "+strconv.Itoa(maxBulkReminders)+" Rechnungen auf einmal")
		return
	}
	// Jede Erinnerung darf bis zu 20 s SMTP-Zeit brauchen; der allgemeine WriteTimeout
	// (30 s) würde die Antwort sonst abschneiden, während die Mails längst draußen sind.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
	seen := map[int64]bool{}
	out := make([]remindResult, 0, len(in.IDs))
	sent := 0
	for _, id := range in.IDs {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		if r.Context().Err() != nil {
			out = append(out, remindResult{InvoiceID: id, Error: "abgebrochen"})
			continue
		}
		req := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(id, 10)+"/remind", nil).WithContext(r.Context())
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		rec := httptest.NewRecorder()
		h.RemindInvoice(rec, req)
		res := remindResult{InvoiceID: id}
		var body struct {
			Level int    `json:"level"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code == http.StatusOK {
			res.Sent, res.Level = true, body.Level
			sent++
		} else {
			res.Error = body.Error
			if res.Error == "" {
				res.Error = http.StatusText(rec.Code)
			}
		}
		out = append(out, res)
	}
	h.audit(r, "remind", "invoice", 0, "Sammel-Erinnerung: "+strconv.Itoa(sent)+" von "+strconv.Itoa(len(out))+" versendet")
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "results": out})
}

// ---------- Vorschau für den Rechnungslauf (Ausbau 26) ----------

type previewLine struct {
	Label  string  `json:"label"`
	Period string  `json:"period,omitempty"`
	Amount float64 `json:"amount"`
}

type previewPerson struct {
	PersonID int64         `json:"person_id"`
	Name     string        `json:"name"`
	Lines    []previewLine `json:"lines"`
	Subtotal float64       `json:"subtotal"`
	Total    float64       `json:"total"`
	Warnings []string      `json:"warnings"`
}

// InvoiceRunPreview: GET /api/invoices/preview — was der nächste Rechnungslauf je
// Person fakturieren würde. Rein lesend: dieselbe Positionsermittlung
// (invoiceLinesFrom) und dieselbe Pflichtangaben-Prüfung wie CreateInvoice, aber ohne
// Transaktion, ohne Nummer und ohne Sperre.
func (h *Handler) InvoiceRunPreview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, err := h.loadBillingSettings(ctx)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	rows, err := h.Pool.Query(ctx,
		`SELECT p.id, trim(p.first_name || ' ' || p.last_name), COALESCE(p.address, ''), btrim(p.email),
		        COALESCE((SELECT i.total FROM invoices i WHERE i.person_id = p.id AND NOT i.canceled AND i.cancels_id IS NULL
		                   ORDER BY i.issued_on DESC, i.id DESC LIMIT 1), 0)
		   FROM persons p WHERE NOT p.anonymized ORDER BY p.last_name, p.first_name, p.id`)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	type person struct {
		id                   int64
		name, address, email string
		lastTotal            float64
	}
	var persons []person
	for rows.Next() {
		var p person
		if err := rows.Scan(&p.id, &p.name, &p.address, &p.email, &p.lastTotal); err != nil {
			rows.Close()
			serverError(w, r, "scan failed", err)
			return
		}
		persons = append(persons, p)
	}
	rows.Close()
	out := []previewPerson{}
	var sum float64
	for _, p := range persons {
		items, err := h.invoiceLinesFrom(r, h.Pool, p.id)
		if err != nil {
			serverError(w, r, "query failed", err)
			return
		}
		if len(items) == 0 {
			continue
		}
		pp := previewPerson{PersonID: p.id, Name: p.name, Warnings: []string{}}
		for _, it := range items {
			pp.Lines = append(pp.Lines, previewLine{Label: it.Label, Period: it.Period, Amount: round2(it.LineTotal)})
			pp.Subtotal += it.LineTotal
		}
		pp.Subtotal = round2(pp.Subtotal)
		pp.Total = pp.Subtotal
		if !s.Kleinunternehmer {
			pp.Total = round2(pp.Subtotal + round2(pp.Subtotal*s.UStRate/100))
		}
		if msg, _ := invoiceComplianceDetail(s, p.name, p.address, pp.Total); msg != "" {
			pp.Warnings = append(pp.Warnings, msg)
		}
		if p.email == "" {
			pp.Warnings = append(pp.Warnings, "Keine E-Mail-Adresse: Versand nur auf Papier oder über das Portal.")
		}
		if p.lastTotal > 0 && pp.Total > p.lastTotal*1.5 && pp.Total-p.lastTotal > 20 {
			pp.Warnings = append(pp.Warnings, "Deutlich höher als die letzte Rechnung ("+pdfMoney(p.lastTotal)+").")
		}
		sum += pp.Total
		out = append(out, pp)
	}
	writeJSON(w, http.StatusOK, map[string]any{"persons": out, "count": len(out), "total": round2(sum), "as_of": h.now()})
}
