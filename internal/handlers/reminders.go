package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// remindSlots deckelt, wie viele Mahnungen GLEICHZEITIG eine Datenbankverbindung
// halten dürfen. Die Sitzungssperre unten zwingt dazu, die Verbindung über den
// gesamten SMTP-Versand (bis 20 s) belegt zu lassen — ohne Deckel könnten schon
// zehn gleichzeitige Mahnungen den ganzen Pool (database.MaxConns = 10) für die
// Dauer eines trägen Mailservers aufzehren, und JEDE andere Anfrage (Liste,
// Anmeldung, /readyz) wartete dahinter. Der Deckel liegt bewusst deutlich unter
// der Poolgröße: es bleiben immer Verbindungen übrig — auch für die, die der
// Versand selbst noch braucht (mail.WithLog schreibt mail_log über den Pool,
// synchron INNERHALB von Send).
//
// Wer keinen Platz bekommt, erfährt das sofort mit 503 statt zu warten.
var remindSlots = make(chan struct{}, 2)

// discardLockedConn wirft eine Verbindung weg, deren Sitzungssperre womöglich noch
// steht, statt sie in den Pool zurückzugeben.
//
// Eine pg_advisory_lock hängt an der SITZUNG, und pgxpool setzt eine zurückgegebene
// Sitzung NICHT zurück (kein DISCARD ALL); Release verwirft nur geschlossene, belegte
// oder in einer Transaktion steckende Verbindungen. Käme eine noch sperrende zurück,
// liefe jede weitere Mahnung derselben Rechnung über eine ANDERE Verbindung in
// pg_try_advisory_lock = false und bekäme dauerhaft 409 — bis MaxConnLifetime (1 h)
// die eine Verbindung recycelt. Nichts davon wäre für den Betreiber erklärbar.
//
// Hijack nimmt die Verbindung aus dem Pool (ein nachfolgendes Release wird damit zum
// No-op), Close beendet die Sitzung, und mit der Sitzung fällt die Sperre ohnehin.
func discardLockedConn(conn *pgxpool.Conn) {
	raw := conn.Hijack()
	if raw == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = raw.Close(ctx)
}

// RemindInvoice e-mails the invoice's payer a payment reminder (editor+). It
// requires SMTP to be configured, the invoice to still be open, and the person
// to have an e-mail address on file.
func (h *Handler) RemindInvoice(w http.ResponseWriter, r *http.Request) {
	if h.Mail == nil || !h.Mail.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "E-Mail ist nicht konfiguriert (SMTP)")
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	iv, found, err := h.fetchInvoice(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if iv.Canceled || iv.CancelsID != nil {
		writeError(w, http.StatusConflict, "Rechnung ist storniert")
		return
	}
	if iv.OpenAmount <= 0.005 {
		writeError(w, http.StatusConflict, "Rechnung ist bereits bezahlt")
		return
	}

	var email, first, last string
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT email, first_name, last_name FROM persons WHERE id=$1`, iv.PersonID,
	).Scan(&email, &first, &last); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	email = trim(email)
	if email == "" {
		writeError(w, http.StatusBadRequest, "Kein E-Mail-Kontakt für diese Person hinterlegt")
		return
	}

	// Zählen, Senden und Festhalten gehören zusammen. Standen sie unverriegelt
	// nebeneinander, lasen zwei gleichzeitige Aufrufe — ein Doppelklick während der
	// bis zu 20 Sekunden dauernden SMTP-Zustellung genügt — dieselbe Zahl, schickten
	// DIESELBE Stufe zweimal und ließen die nächste ganz aus: der Kunde bekam die
	// Zahlungserinnerung doppelt und danach die LETZTE Mahnung, ohne je eine
	// 1. Mahnung erhalten zu haben. Eine UNIQUE-Bedingung auf (invoice_id, level)
	// wäre der falsche Riegel — Stufe 3 soll sich wiederholen dürfen.
	//
	// Eigene Verbindung, weil pg_advisory_lock an der SITZUNG hängt (dasselbe Muster
	// wie runOnce in maintenance.go). Nicht blockierend: der zweite Klick bekommt eine
	// klare Antwort statt einer zweiten Mahnung.
	//
	// Der Schlüssel EINMAL berechnen und über advisoryKey (volle 64 Bit): stünde der
	// Ausdruck wie zuvor zweimal da — einmal fürs Sperren, einmal fürs Lösen —, machte
	// jede spätere Abweichung zwischen beiden die Sperre unlösbar, und diese Rechnung
	// wäre bis zum Ende der Verbindung dauerhaft mit 409 blockiert.
	lockKey := advisoryKey("parkrr.remind.invoice." + strconv.FormatInt(iv.ID, 10))

	// Die Verbindung bleibt über den SMTP-Versand hinweg belegt — anders ist eine
	// Sitzungssperre nicht zu haben. Damit sich davor keine Warteschlange bildet, die
	// den GANZEN Pool aufzehrt, wird nur kurz auf einen freien Platz gewartet: Wer
	// keinen bekommt, erfährt das sofort, statt bis zum Timeout seiner Anfrage zu
	// hängen und dabei einen Bearbeiter-Thread zu binden.
	//
	// Der Platz im Pool ist dabei nur die zweite Schranke. Die erste ist remindSlots:
	// die 3-Sekunden-Frist schützt Mahnungen VOREINANDER, aber nichts schützte den
	// Rest der Anwendung vor den Mahnungen — deren Verbindungen liegen bis zu 20 s
	// still, und jeder andere Handler wartet ohne eigene Frist auf Acquire.
	select {
	case remindSlots <- struct{}{}:
		defer func() { <-remindSlots }()
	default:
		writeError(w, http.StatusServiceUnavailable, "Es laufen bereits Mahnungen — bitte gleich nochmal versuchen")
		return
	}
	acqCtx, acqCancel := context.WithTimeout(r.Context(), 3*time.Second)
	conn, err := h.Pool.Acquire(acqCtx)
	acqCancel()
	if err != nil {
		if r.Context().Err() == nil {
			writeError(w, http.StatusServiceUnavailable, "Datenbank ist ausgelastet — bitte gleich nochmal versuchen")
			return
		}
		serverError(w, r, "query failed", err)
		return
	}
	defer conn.Release()
	var gotLock bool
	if err := conn.QueryRow(r.Context(),
		`SELECT pg_try_advisory_lock($1)`, lockKey).Scan(&gotLock); err != nil {
		// Ob die Sperre gesetzt wurde, ist hier UNBEKANNT. Bricht der Aufrufer die
		// Anfrage mitten im Hin und Her ab, kann Postgres sie längst gesetzt haben,
		// während hier nur der Context-Fehler ankommt — und das Lösen ist noch nicht
		// eingerichtet, weil es erst nach dem bestätigten Erwerb angemeldet wird.
		// conn.Release() gäbe dann eine gesunde, aber sperrende Verbindung zurück.
		discardLockedConn(conn)
		serverError(w, r, "query failed", err)
		return
	}
	if !gotLock {
		writeError(w, http.StatusConflict, "Für diese Rechnung läuft bereits eine Mahnung")
		return
	}
	defer func() {
		// Eigene Frist: context.WithoutCancel allein hat GAR keine, und eine hängende
		// Verbindung hielte sonst Bearbeiter und Poolplatz fest, bis TCP aufgibt.
		ulCtx, ulCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer ulCancel()
		var unlocked bool
		err := conn.QueryRow(ulCtx, `SELECT pg_advisory_unlock($1)`, lockKey).Scan(&unlocked)
		if err == nil && unlocked {
			return
		}
		slog.Warn("remind invoice: advisory unlock failed — Verbindung wird verworfen",
			"invoice_id", iv.ID, "unlocked", unlocked, "err", err)
		discardLockedConn(conn)
	}()

	// Mahn-Gedächtnis (Hundert 15): die Stufe entsteht aus der Zahl der bisherigen
	// Versendungen. 1 = Zahlungserinnerung, 2 = 1. Mahnung, 3 = 2./letzte Mahnung;
	// Stufe 3 wiederholt sich — es gibt keine "5. Mahnung", die eine Eskalation
	// vortäuscht, die nicht stattfindet.
	var prior int
	if err := conn.QueryRow(r.Context(),
		`SELECT count(*) FROM invoice_reminders WHERE invoice_id=$1`, iv.ID).Scan(&prior); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	level := prior + 1
	if level > 3 {
		level = 3
	}

	subject := reminderSubject(level, iv.Number)
	body := h.reminderBody(iv, trim(first+" "+last), level)

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := h.Mail.Send(ctx, []string{email}, subject, body); err != nil {
		slog.Error("remind invoice email failed", "invoice_id", iv.ID, "err", err)
		writeError(w, http.StatusBadGateway, "E-Mail konnte nicht gesendet werden")
		return
	}
	// ERST nach erfolgreichem Versand festhalten: eine gescheiterte Mail darf die
	// Stufe nicht hochzählen — sonst bekäme der Kunde als ERSTE Post die 1. Mahnung.
	//
	// Ab hier NICHT mehr am Context des Aufrufers: die Mail ist draußen, und ein
	// Browser, der während der bis zu 20 Sekunden dauernden Zustellung geschlossen
	// wird, hätte den Eintrag sonst verschluckt. Die nächste Mahnung zählte dann wieder
	// von derselben Stufe — der Kunde bekäme die Zahlungserinnerung zweimal, genau der
	// Fall, gegen den die Sperre oben steht. Auch der Protokolleintrag hängt nicht mehr
	// am Aufrufer: eine versandte Mahnung ohne Spur wäre das Schlechteste von beidem.
	recCtx, recCancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer recCancel()
	// Beide Schreibvorgänge in EINER Transaktion — und ihr Scheitern ist ein Fehler,
	// kein Protokolleintrag.
	//
	// Die Sperre oben hütet nur das Fenster zwischen Zählen und Schreiben. Ging der
	// Eintrag danach verloren und wurde das bloß geloggt, während die Antwort
	// {"sent":true,"level":1} lautete, dann las der nächste Aufruf wieder prior=0 und
	// schickte DIESELBE Stufe ein zweites Mal: der Kunde bekam die Zahlungserinnerung
	// doppelt und sprang danach auf die letzte Mahnung, ohne je eine 1. Mahnung
	// gesehen zu haben. Genau der Ausgang, gegen den die Sperre angetreten ist — nur
	// über den fehlgeschlagenen Schreibvorgang statt über das Wettrennen erreicht.
	//
	// Über conn, NICHT über h.Pool: h.audit holte sich eine ZWEITE Verbindung, während
	// diese hier noch belegt ist. Bei ausgelastetem Pool (MaxConns) warten dann alle
	// laufenden Mahnungen auf einen Platz, den nur sie selbst freigeben könnten — die
	// Anwendung stünde, bis die Anfragen ablaufen.
	recErr := pgx.BeginFunc(recCtx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(recCtx,
			`INSERT INTO invoice_reminders (invoice_id, level, sent_to) VALUES ($1,$2,$3)`,
			iv.ID, level, email); err != nil {
			return err
		}
		return h.auditChangeTx(recCtx, tx, r, "remind", "invoice", iv.ID,
			fmt.Sprintf("%s an %s für Rechnung %s", reminderLevelName(level), email, iv.Number), nil)
	})
	if recErr != nil {
		// Die Mail IST draußen — das lässt sich nicht zurücknehmen. Deshalb sagt die
		// Absage genau das und warnt vor dem zweiten Klick, statt "Senden
		// fehlgeschlagen" zu behaupten und den Betreiber zur Wiederholung einzuladen.
		slog.Error("remind invoice: could not record reminder", "invoice_id", iv.ID, "level", level, "err", recErr)
		writeError(w, http.StatusInternalServerError,
			reminderLevelName(level)+" wurde versendet, konnte aber nicht festgehalten werden. "+
				"Bitte NICHT erneut senden — der Kunde bekäme dieselbe Stufe ein zweites Mal.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "to": email, "level": level})
}

// reminderLevelName benennt die Stufe so, wie sie auch im Betreff steht.
func reminderLevelName(level int) string {
	switch level {
	case 1:
		return "Zahlungserinnerung"
	case 2:
		return "1. Mahnung"
	default:
		return "2. Mahnung (letzte Mahnung)"
	}
}

func reminderSubject(level int, number string) string {
	return reminderLevelName(level) + " – Rechnung " + number
}

// reminderBody composes the plain-text reminder from the invoice's own snapshot
// (seller/IBAN/footer), so it matches the immutable document exactly.
func (h *Handler) reminderBody(iv invoice, name string, level int) string {
	s := iv.Seller
	const df = "02.01.2006"
	var b strings.Builder
	if name != "" {
		fmt.Fprintf(&b, "Guten Tag %s,\n\n", name)
	} else {
		b.WriteString("Guten Tag,\n\n")
	}
	fmt.Fprintf(&b, "%s\n\n", reminderOpening(level, iv.Number))
	fmt.Fprintf(&b, "Rechnungsdatum:   %s\n", iv.IssuedOn.Format(df))
	if iv.DueOn != nil {
		fmt.Fprintf(&b, "Fällig am:        %s\n", iv.DueOn.Format(df))
	}
	fmt.Fprintf(&b, "Rechnungsbetrag:  %s\n", pdfMoney(iv.Total))
	fmt.Fprintf(&b, "Offener Betrag:   %s\n\n", pdfMoney(iv.OpenAmount))

	if iban := snapStr(s, "iban"); iban != "" {
		b.WriteString("Bitte überweisen Sie den offenen Betrag auf:\n")
		fmt.Fprintf(&b, "  IBAN: %s\n", iban)
		if bic := snapStr(s, "bic"); bic != "" {
			fmt.Fprintf(&b, "  BIC:  %s\n", bic)
		}
		fmt.Fprintf(&b, "  Verwendungszweck: %s\n\n", iv.Number)
	}
	if base := strings.TrimRight(h.PublicBaseURL, "/"); base != "" {
		fmt.Fprintf(&b, "Rechnungsdetails: %s/#/invoices/%d\n\n", base, iv.ID)
	}
	b.WriteString("Sollte sich Ihre Zahlung mit dieser Erinnerung überschnitten haben, betrachten Sie dieses Schreiben bitte als gegenstandslos.\n\n")
	b.WriteString("Mit freundlichen Grüßen\n")
	if seller := snapStr(s, "name"); seller != "" {
		b.WriteString(seller + "\n")
	}
	if footer := trim(snapStr(s, "footer")); footer != "" {
		b.WriteString("\n" + footer + "\n")
	}
	return b.String()
}

// reminderOpening ist der erste Satz je Stufe: freundlich, bestimmt, unmissverständlich.
// Der Ton eskaliert mit der Stufe — dieselbe Formulierung dreimal zu schicken wäre
// keine Mahnung, sondern ein Newsletter.
func reminderOpening(level int, number string) string {
	switch level {
	case 1:
		return "wir möchten Sie freundlich an die offene Rechnung " + number + " erinnern."
	case 2:
		return "trotz unserer Zahlungserinnerung ist die Rechnung " + number + " weiterhin offen. Wir bitten Sie, den offenen Betrag umgehend zu begleichen (1. Mahnung)."
	default:
		return "die Rechnung " + number + " ist trotz Erinnerung und 1. Mahnung weiterhin offen. Dies ist unsere letzte Mahnung — bitte begleichen Sie den offenen Betrag binnen 7 Tagen, andernfalls behalten wir uns weitere Schritte vor (2. Mahnung)."
	}
}
