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

	subject := "Zahlungserinnerung – Rechnung " + iv.Number
	body := h.reminderBody(iv, trim(first+" "+last))

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := h.Mail.Send(ctx, []string{email}, subject, body); err != nil {
		slog.Error("remind invoice email failed", "invoice_id", iv.ID, "err", err)
		writeError(w, http.StatusBadGateway, "E-Mail konnte nicht gesendet werden")
		return
	}
	h.audit(r, "remind", "invoice", iv.ID, "Zahlungserinnerung an "+email+" für Rechnung "+iv.Number)
	writeJSON(w, http.StatusOK, map[string]any{"sent": true, "to": email})
}

// reminderBody composes the plain-text reminder from the invoice's own snapshot
// (seller/IBAN/footer), so it matches the immutable document exactly.
func (h *Handler) reminderBody(iv invoice, name string) string {
	s := iv.Seller
	const df = "02.01.2006"
	var b strings.Builder
	if name != "" {
		fmt.Fprintf(&b, "Guten Tag %s,\n\n", name)
	} else {
		b.WriteString("Guten Tag,\n\n")
	}
	fmt.Fprintf(&b, "wir möchten Sie freundlich an die offene Rechnung %s erinnern.\n\n", iv.Number)
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
