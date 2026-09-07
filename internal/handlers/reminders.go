package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

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

	// Mahn-Gedächtnis (Hundert 15): die Stufe entsteht aus der Zahl der bisherigen
	// Versendungen. 1 = Zahlungserinnerung, 2 = 1. Mahnung, 3 = 2./letzte Mahnung;
	// Stufe 3 wiederholt sich — es gibt keine "5. Mahnung", die eine Eskalation
	// vortäuscht, die nicht stattfindet.
	var prior int
	if err := h.Pool.QueryRow(r.Context(),
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
	if _, err := h.Pool.Exec(r.Context(),
		`INSERT INTO invoice_reminders (invoice_id, level, sent_to) VALUES ($1,$2,$3)`,
		iv.ID, level, email); err != nil {
		slog.Error("remind invoice: could not record reminder", "invoice_id", iv.ID, "err", err)
	}
	h.audit(r, "remind", "invoice", iv.ID,
		fmt.Sprintf("%s an %s für Rechnung %s", reminderLevelName(level), email, iv.Number))
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
