package handlers

import (
	"bytes"
	"fmt"
	"image/png"
	"net/http"
	"strings"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
)

// qrPNG renders content as a QR-code PNG (raw bytes).
func qrPNG(content string, size int) ([]byte, error) {
	code, err := qr.Encode(content, qr.M, qr.Auto)
	if err != nil {
		return nil, err
	}
	scaled, err := barcode.Scale(code, size, size)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, scaled); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// epcPayload builds an EPC069-12 ("Girocode") SEPA credit-transfer payload.
// Scanning it with a banking app pre-fills payee, IBAN, amount and reference.
// Returns a non-empty problem string (for a 409) when it can't be built.
func epcPayload(name, iban, bic string, amount float64, reference string) (payload, problem string) {
	iban = strings.ReplaceAll(strings.TrimSpace(iban), " ", "")
	if iban == "" {
		return "", "keine IBAN in den Rechnungs-Einstellungen hinterlegt"
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Zahlungsempfaenger"
	}
	name = truncRunes(name, 70)
	ref := truncRunes(strings.TrimSpace(reference), 140)
	amt := ""
	if amount >= 0.01 && amount <= 999999999.99 {
		amt = fmt.Sprintf("EUR%.2f", amount)
	}
	// EPC069-12 body: 12 LF-separated lines. Version 002 permits an empty BIC.
	lines := []string{
		"BCD", "002", "1", "SCT",
		strings.TrimSpace(bic),
		name, iban, amt,
		"",  // purpose
		"",  // structured remittance
		ref, // unstructured remittance
		"",  // beneficiary-to-originator info
	}
	payload = strings.Join(lines, "\n")
	if len(payload) > 331 { // EPC hard limit
		return "", "Zahldaten überschreiten die SEPA-QR-Grenze"
	}
	return payload, ""
}

// serveInvoicePayQR renders the SEPA pay QR for an open invoice as a PNG.
func (h *Handler) serveInvoicePayQR(w http.ResponseWriter, iv invoice) {
	if iv.Canceled || iv.CancelsID != nil {
		writeError(w, http.StatusConflict, "Rechnung ist storniert")
		return
	}
	if iv.OpenAmount <= 0.005 {
		writeError(w, http.StatusConflict, "Rechnung ist bereits bezahlt")
		return
	}
	payload, problem := epcPayload(snapStr(iv.Seller, "name"), snapStr(iv.Seller, "iban"),
		snapStr(iv.Seller, "bic"), iv.OpenAmount, iv.Number)
	if problem != "" {
		writeError(w, http.StatusConflict, problem)
		return
	}
	pngBytes, err := qrPNG(payload, 320)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "QR konnte nicht erstellt werden")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("Content-Disposition", "inline")
	_, _ = w.Write(pngBytes)
}

// PayQR serves the SEPA "pay this invoice" QR for an invoice (authenticated).
func (h *Handler) PayQR(w http.ResponseWriter, r *http.Request) {
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
	h.serveInvoicePayQR(w, iv)
}

// PortalPayQR serves the pay QR for a person's own invoice behind a valid token.
func (h *Handler) PortalPayQR(w http.ResponseWriter, r *http.Request) {
	pid, ok := h.resolvePortalPerson(r.Context(), r.PathValue("token"))
	if !ok {
		writeError(w, http.StatusNotFound, "Link ungültig oder abgelaufen")
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
	if !found || iv.PersonID != pid {
		writeError(w, http.StatusNotFound, "invoice not found")
		return
	}
	h.serveInvoicePayQR(w, iv)
}
