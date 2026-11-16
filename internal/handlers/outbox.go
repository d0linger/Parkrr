package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/mail"
)

// Mail-Warteschlange (Ausbau 36). Automatische Mails (Rechnungsversand, automatische
// Mahnstufen) gehen hier hinein statt direkt im Request hinaus: hängt der SMTP-Server,
// wartet kein Klick, und fällt er aus, wird später erneut versucht.
//
// Manuelle Erinnerungen (RemindInvoice) bleiben bewusst synchron: dort sieht der
// Betreiber sofort, ob die Mail draußen ist. Dieselbe Regel gilt aber hier: eine
// Mahnung aus der Warteschlange bucht ihre Stufe erst, wenn sie zugestellt ist, und
// sie nimmt dieselbe Rechnungssperre wie der Knopf, damit beide nie gleichzeitig
// dieselbe Stufe verschicken.

// outboxBackoff: Wartezeit vor dem n-ten Wiederholungsversuch; danach "failed".
var outboxBackoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

// outboxStuckAfter: eine Sendung, die so lange auf "sending" steht, hat den Prozess
// nicht überlebt. Ob sie zugestellt wurde, ist unbekannt; sie wird NICHT wiederholt
// (eine doppelte Mahnung ist schlimmer als eine fehlende), sondern als fehlgeschlagen
// mit klarem Hinweis markiert.
const outboxStuckAfter = 15 * time.Minute

type outboxRow struct {
	ID        int64
	Kind      string
	RefID     *int64
	PersonID  *int64
	Level     int
	Recipient string
	Subject   string
	Body      string
	AttName   string
	Att       []byte
}

// enqueueMail legt eine Mail in die Warteschlange (im Rahmen von q, gern in einer
// Transaktion). Der Versandlauf holt sie spätestens beim nächsten Takt ab.
func enqueueMail(ctx context.Context, q dbQuerier, row outboxRow) (int64, error) {
	var id int64
	var att any
	if len(row.Att) > 0 {
		att = row.Att
	}
	err := q.QueryRow(ctx,
		`INSERT INTO mail_outbox (kind, ref_id, person_id, level, recipient, subject, body, attachment_name, attachment)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		row.Kind, row.RefID, row.PersonID, row.Level, row.Recipient, row.Subject, row.Body, row.AttName, att).Scan(&id)
	return id, err
}

// KickOutbox weckt den Versandlauf, ohne zu warten.
func (h *Handler) KickOutbox() {
	if h.OutboxKick == nil {
		return
	}
	select {
	case h.OutboxKick <- struct{}{}:
	default:
	}
}

// ProcessOutbox verschickt fällige Mails und meldet, wie viele es angefasst hat.
// Mehrere Prozesse dürfen gleichzeitig laufen: FOR UPDATE SKIP LOCKED verteilt die Zeilen.
func (h *Handler) ProcessOutbox(ctx context.Context) int {
	ctx = auth.ContextWithSystemActor(ctx)
	if _, err := h.Pool.Exec(ctx,
		`UPDATE mail_outbox SET status='failed', attachment=NULL,
		        last_error='Versand wurde unterbrochen, Zustellung unklar. Bitte prüfen und bei Bedarf erneut senden.'
		  WHERE status='sending' AND claimed_at < now() - make_interval(secs => $1)`,
		outboxStuckAfter.Seconds()); err != nil {
		slog.Warn("outbox: unterbrochene Sendungen nicht markierbar", "err", err)
	}
	if h.Mail == nil || !h.Mail.Enabled() {
		return 0
	}
	rows, err := h.Pool.Query(ctx,
		`UPDATE mail_outbox SET status='sending', attempts=attempts+1, claimed_at=now()
		  WHERE id IN (SELECT id FROM mail_outbox WHERE status='queued' AND next_attempt_at <= now()
		                ORDER BY id LIMIT 10 FOR UPDATE SKIP LOCKED)
		 RETURNING id, kind, ref_id, person_id, level, recipient, subject, body, attachment_name,
		           COALESCE(attachment, ''::bytea), attempts`)
	if err != nil {
		slog.Warn("outbox: Abholen fehlgeschlagen", "err", err)
		return 0
	}
	type claimed struct {
		outboxRow
		attempts int
	}
	var batch []claimed
	for rows.Next() {
		var c claimed
		if err := rows.Scan(&c.ID, &c.Kind, &c.RefID, &c.PersonID, &c.Level, &c.Recipient, &c.Subject,
			&c.Body, &c.AttName, &c.Att, &c.attempts); err != nil {
			rows.Close()
			slog.Warn("outbox: Zeile nicht lesbar", "err", err)
			return 0
		}
		batch = append(batch, c)
	}
	rows.Close()
	for _, c := range batch {
		h.deliverOutbox(ctx, c.outboxRow, c.attempts)
	}
	return len(batch)
}

func (h *Handler) deliverOutbox(ctx context.Context, row outboxRow, attempts int) {
	if row.Kind == "reminder" && row.RefID != nil {
		h.deliverReminder(ctx, row, attempts)
		return
	}
	if row.Kind == "invoice" {
		h.deliverInvoice(ctx, row, attempts)
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	tx, err := h.Pool.Begin(lctx)
	if err != nil {
		h.requeueSoon(ctx, row.ID, "Datenbank ausgelastet")
		return
	}
	defer releaseOutboxLocks(ctx, tx)
	if !h.holdRecipient(ctx, lctx, tx, &row) {
		return
	}
	if err := h.sendOutboxRow(lctx, row); err != nil {
		h.retryOrFail(ctx, row, attempts, err)
		return
	}
	h.markSent(ctx, row, func(context.Context, pgx.Tx) error { return nil })
}

// releaseOutboxLocks ends a delivery's lock transaction. It only read and locked
// rows, so the rollback merely releases the locks.
func releaseOutboxLocks(ctx context.Context, tx pgx.Tx) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(rctx)
}

// holdRecipient locks the person row FOR SHARE in tx and keeps it until tx ends,
// i.e. across the whole send. Anonymization, deletion and every address change
// write exactly this row, so they wait until the mail is out: no send that passed
// this point is still in flight when an anonymization reports success. The mail
// goes to the address that is current NOW — the one captured at queue time would
// send a corrected invoice to the old mailbox. false means do not send; the row
// has then already been finished or handed back. Bookkeeping uses ctx, the lock
// queries lctx.
func (h *Handler) holdRecipient(ctx, lctx context.Context, tx pgx.Tx, row *outboxRow) bool {
	if row.PersonID == nil {
		// person_id only disappears when the person is deleted (ON DELETE SET NULL).
		h.finishOutbox(ctx, row.ID, "failed", "Nicht gesendet: Person gelöscht")
		return false
	}
	var email string
	var anonymized bool
	err := tx.QueryRow(lctx, `SELECT email, anonymized FROM persons WHERE id=$1 FOR SHARE`, *row.PersonID).
		Scan(&email, &anonymized)
	gone := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !gone {
		h.requeueSoon(ctx, row.ID, "Person nicht lesbar")
		return false
	}
	// Only under the lock: an anonymization or deletion that committed first has
	// already closed the row, and its recipient no longer counts.
	var status string
	switch err := tx.QueryRow(lctx, `SELECT status FROM mail_outbox WHERE id=$1`, row.ID).Scan(&status); {
	case errors.Is(err, pgx.ErrNoRows):
		return false
	case err != nil:
		h.requeueSoon(ctx, row.ID, "Warteschlange nicht lesbar")
		return false
	case status != "sending":
		return false
	}
	to := trim(email)
	switch {
	case gone || anonymized:
		h.finishOutbox(ctx, row.ID, "failed", "Person anonymisiert, nicht gesendet")
		return false
	case to == "":
		h.finishOutbox(ctx, row.ID, "failed", "Nicht gesendet: keine E-Mail-Adresse mehr hinterlegt")
		return false
	}
	if !strings.EqualFold(to, row.Recipient) {
		row.Recipient = to
	}
	return true
}

// The invoice row lock orders delivery against CancelInvoice's UPDATE: either the
// cancellation commits first and no mail goes out, or delivery finishes before
// cancellation can commit. NO KEY UPDATE still permits the Storno's FK check.
func (h *Handler) deliverInvoice(ctx context.Context, row outboxRow, attempts int) {
	if row.RefID == nil {
		h.finishOutbox(ctx, row.ID, "failed", "Nicht gesendet: Rechnung nicht mehr vorhanden")
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	tx, err := h.Pool.Begin(lctx)
	if err != nil {
		h.requeueSoon(ctx, row.ID, "Rechnung nicht lesbar")
		return
	}
	// Invoice and person locks live in this transaction.
	defer releaseOutboxLocks(ctx, tx)
	var canceled bool
	var cancelsID *int64
	err = tx.QueryRow(lctx, `SELECT canceled, cancels_id FROM invoices WHERE id=$1 FOR NO KEY UPDATE`, *row.RefID).
		Scan(&canceled, &cancelsID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		h.finishOutbox(ctx, row.ID, "failed", "Nicht gesendet: Rechnung nicht mehr vorhanden")
		return
	case err != nil:
		h.requeueSoon(ctx, row.ID, "Rechnung nicht lesbar")
		return
	case canceled || cancelsID != nil:
		h.finishOutbox(ctx, row.ID, "failed", "Nicht gesendet: Rechnung storniert")
		return
	}
	if !h.holdRecipient(ctx, lctx, tx, &row) {
		return
	}
	if err := h.sendOutboxRow(lctx, row); err != nil {
		h.retryOrFail(ctx, row, attempts, err)
		return
	}
	h.markSent(ctx, row, func(context.Context, pgx.Tx) error { return nil })
}

// deliverReminder schickt eine automatische Mahnung unter derselben Sitzungssperre wie
// RemindInvoice und prüft unmittelbar davor, ob die vorgemerkte Stufe noch stimmt.
func (h *Handler) deliverReminder(ctx context.Context, row outboxRow, attempts int) {
	conn, err := h.Pool.Acquire(ctx)
	if err != nil {
		h.requeueSoon(ctx, row.ID, "Datenbank ausgelastet")
		return
	}
	defer conn.Release()
	key := advisoryKey("parkrr.remind.invoice." + strconv.FormatInt(*row.RefID, 10))
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		discardLockedConn(conn)
		h.requeueSoon(ctx, row.ID, "Sperre nicht prüfbar")
		return
	}
	if !got {
		h.requeueSoon(ctx, row.ID, "Für diese Rechnung läuft gerade eine Mahnung")
		return
	}
	defer func() {
		ulCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var ok bool
		if err := conn.QueryRow(ulCtx, `SELECT pg_advisory_unlock($1)`, key).Scan(&ok); err != nil || !ok {
			discardLockedConn(conn)
		}
	}()
	// Die Personensperre auf derselben Verbindung; das Rollback läuft vor dem
	// Freigeben der Sitzungssperre (defer-Reihenfolge).
	tx, err := conn.Begin(ctx)
	if err != nil {
		h.requeueSoon(ctx, row.ID, "Datenbank ausgelastet")
		return
	}
	defer releaseOutboxLocks(ctx, tx)
	if !h.holdRecipient(ctx, ctx, tx, &row) {
		return
	}
	if reason := h.reminderStillDue(ctx, *row.RefID, row.Level); reason != "" {
		h.finishOutbox(ctx, row.ID, "failed", "Nicht gesendet: "+reason)
		return
	}
	if err := h.sendOutboxRow(ctx, row); err != nil {
		h.retryOrFail(ctx, row, attempts, err)
		return
	}
	h.markSent(ctx, row, func(wctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(wctx,
			`INSERT INTO invoice_reminders (invoice_id, level, sent_to) VALUES ($1,$2,$3)`,
			*row.RefID, row.Level, row.Recipient); err != nil {
			return err
		}
		return auditExec(wctx, tx, 0, "system", "remind", "invoice", *row.RefID,
			"Automatische "+reminderLevelName(row.Level)+" versendet (Mail-Warteschlange)", nil)
	})
}

func (h *Handler) sendOutboxRow(ctx context.Context, row outboxRow) error {
	var atts []mail.Attachment
	if len(row.Att) > 0 {
		atts = []mail.Attachment{{Name: row.AttName, ContentType: "application/pdf", Data: row.Att}}
	}
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return mail.SendAll(sctx, h.Mail, []string{row.Recipient}, row.Subject, row.Body, atts)
}

func (h *Handler) retryOrFail(ctx context.Context, row outboxRow, attempts int, sendErr error) {
	msg := mail.RedactAddrs(sendErr.Error(), []string{row.Recipient})
	if attempts > len(outboxBackoff) {
		// Like the retry and success paths: record the address actually tried.
		if _, err := h.Pool.Exec(ctx,
			`UPDATE mail_outbox SET status='failed', attachment=NULL, last_error=$2, recipient=$3 WHERE id=$1`,
			row.ID, "Endgültig fehlgeschlagen nach "+strconv.Itoa(attempts)+" Versuchen: "+msg, row.Recipient); err != nil {
			slog.Warn("outbox: Abschluss nicht schreibbar", "id", row.ID, "err", err)
		}
		return
	}
	wait := outboxBackoff[attempts-1]
	// recipient: the address actually tried (holdRecipient may have updated it).
	if _, err := h.Pool.Exec(ctx,
		`UPDATE mail_outbox SET status='queued', next_attempt_at=now() + make_interval(secs => $2), last_error=$3, recipient=$4 WHERE id=$1`,
		row.ID, wait.Seconds(), msg, row.Recipient); err != nil {
		slog.Warn("outbox: Wiederholung nicht planbar", "id", row.ID, "err", err)
	}
}

// requeueSoon gibt eine Zeile ohne Versandversuch zurück (der Versuch zählt nicht).
func (h *Handler) requeueSoon(ctx context.Context, id int64, why string) {
	if _, err := h.Pool.Exec(ctx,
		`UPDATE mail_outbox SET status='queued', attempts=GREATEST(attempts-1, 0), next_attempt_at=now() + interval '1 minute', last_error=$2 WHERE id=$1`,
		id, why); err != nil {
		slog.Warn("outbox: Rückgabe nicht möglich", "id", id, "err", err)
	}
}

// markSent hält die Zustellung fest und bucht die Folge in derselben Transaktion.
// Eigene Frist, unabhängig vom Aufrufer: die Mail ist draußen, die Buchung darf nicht
// verloren gehen. Läuft, solange holdRecipient die Person noch sperrt — danach
// schriebe es die Adresse in eine Zeile zurück, die eine Anonymisierung geleert hat.
func (h *Handler) markSent(ctx context.Context, row outboxRow, after func(context.Context, pgx.Tx) error) {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	err := pgx.BeginFunc(wctx, h.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(wctx,
			`UPDATE mail_outbox SET status='sent', sent_at=now(), attachment=NULL, last_error='', recipient=$2 WHERE id=$1`,
			row.ID, row.Recipient); err != nil {
			return err
		}
		if row.Kind == "invoice" && row.RefID != nil {
			if err := auditExec(wctx, tx, 0, "system", "send", "invoice", *row.RefID, "Rechnung per E-Mail versendet (Mail-Warteschlange)", nil); err != nil {
				return err
			}
		}
		if row.Kind == "price_change" && row.RefID != nil {
			if err := auditExec(wctx, tx, 0, "system", "notice", "vehicle", *row.RefID, "Mitteilung zur Preisanpassung versendet (Mail-Warteschlange)", nil); err != nil {
				return err
			}
		}
		if row.Kind == "contract_end" && row.RefID != nil {
			if err := auditExec(wctx, tx, 0, "system", "notice", "vehicle", *row.RefID, "Erinnerung an das Vertragsende versendet (Mail-Warteschlange)", nil); err != nil {
				return err
			}
		}
		return after(wctx, tx)
	})
	if err != nil {
		slog.Error("outbox: zugestellt, aber Buchung fehlgeschlagen", "id", row.ID, "kind", row.Kind, "err", err)
		h.finishOutbox(wctx, row.ID, "sent", "Zugestellt, aber nicht vollständig verbucht")
	}
}

func (h *Handler) finishOutbox(ctx context.Context, id int64, status, msg string) {
	if _, err := h.Pool.Exec(ctx,
		`UPDATE mail_outbox SET status=$2, last_error=$3,
		        attachment=CASE WHEN $2 IN ('failed', 'sent') THEN NULL ELSE attachment END,
		        sent_at=CASE WHEN $2='sent' THEN COALESCE(sent_at, now()) ELSE sent_at END
		  WHERE id=$1`, id, status, msg); err != nil {
		slog.Warn("outbox: Abschluss nicht schreibbar", "id", id, "err", err)
	}
}

// reminderStillDue prüft, ob die vorgemerkte Stufe noch stimmt. Leer = senden.
func (h *Handler) reminderStillDue(ctx context.Context, invoiceID int64, level int) string {
	iv, found, err := h.fetchInvoice(ctx, invoiceID)
	if err != nil {
		return "Rechnung nicht lesbar"
	}
	if !found || iv.Canceled || iv.CancelsID != nil {
		return "Rechnung storniert"
	}
	if iv.OpenAmount <= 0.005 {
		return "Rechnung ist inzwischen bezahlt"
	}
	var prior int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM invoice_reminders WHERE invoice_id=$1`, invoiceID).Scan(&prior); err != nil {
		return "Mahnstand nicht lesbar"
	}
	if want := min(prior+1, 3); want != level {
		return "inzwischen wurde von Hand gemahnt"
	}
	return ""
}

// invoicePDFBytes rendert das unveränderliche Rechnungsdokument, genau wie der Download.
func invoicePDFBytes(iv invoice) ([]byte, error) {
	rec := httptest.NewRecorder()
	writeInvoicePDF(rec, iv)
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		return nil, errors.New("PDF konnte nicht erzeugt werden")
	}
	return rec.Body.Bytes(), nil
}
