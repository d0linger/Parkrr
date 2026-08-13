package handlers

import (
	"bytes"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"
)

// snapStr reads a string value from a JSONB snapshot map (seller/buyer).
func snapStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// pdfMoney formats an amount Austrian-style: "1.234,56 €" (thousands dot,
// decimal comma). Negative amounts (Storno documents) keep a leading minus.
func pdfMoney(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := strconv.FormatFloat(v, 'f', 2, 64) // e.g. "1234.56"
	dot := strings.IndexByte(s, '.')
	intPart, frac := s[:dot], s[dot+1:]
	var b strings.Builder
	n := len(intPart)
	for i := 0; i < n; i++ {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteByte(intPart[i])
	}
	out := b.String() + "," + frac + " €"
	if neg {
		out = "-" + out
	}
	return out
}

// pdfQty shows whole quantities without decimals, fractional ones with a comma.
func pdfQty(q float64) string {
	if q == math.Trunc(q) {
		return strconv.FormatFloat(q, 'f', 0, 64)
	}
	return strings.Replace(strconv.FormatFloat(q, 'f', 2, 64), ".", ",", 1)
}

// addrLines splits a multi-line address into non-empty trimmed lines.
func addrLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// fitText truncates s (already cp1252-encoded) with an ellipsis so it fits w mm.
func fitText(pdf *fpdf.Fpdf, s string, w float64) string {
	if pdf.GetStringWidth(s) <= w-2 {
		return s
	}
	for len(s) > 1 {
		s = s[:len(s)-1]
		if pdf.GetStringWidth(s+"...") <= w-2 {
			return s + "..."
		}
	}
	return s
}

// InvoicePDF renders a single invoice as an A4 PDF laid out per §11 UStG
// (seller/buyer, numbered items, net/USt/gross totals, payment details). The
// document is built in memory so any render error is a clean 500 before headers.
func (h *Handler) InvoicePDF(w http.ResponseWriter, r *http.Request) {
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
	writeInvoicePDF(w, iv)
}

// writeInvoicePDF renders the invoice to an A4 PDF and writes it to w. Shared by
// the authenticated InvoicePDF endpoint and the public portal PDF endpoint.
func writeInvoicePDF(w http.ResponseWriter, iv invoice) {
	const (
		left    = 20.0
		right   = 20.0
		usableW = 210.0 - left - right // 170
		dateFmt = "02.01.2006"
	)
	pdf := fpdf.New("P", "mm", "A4", "")
	tr := pdf.UnicodeTranslatorFromDescriptor("") // cp1252: umlauts, §, €, – …
	pdf.SetMargins(left, 18, right)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddPage()

	seller, buyer := iv.Seller, iv.Buyer

	// Sender one-liner (small, grey) above the recipient block.
	sender := snapStr(seller, "name")
	if a := strings.Join(addrLines(snapStr(seller, "address")), ", "); a != "" {
		sender = strings.TrimPrefix(sender+" · "+a, " · ")
	}
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(120, 120, 120)
	pdf.CellFormat(0, 4, tr(sender), "", 1, "L", false, 0, "")
	pdf.Ln(7)

	// Recipient.
	pdf.SetTextColor(25, 25, 25)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 5, tr(snapStr(buyer, "name")), "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	for _, line := range addrLines(snapStr(buyer, "address")) {
		pdf.CellFormat(0, 5, tr(line), "", 1, "L", false, 0, "")
	}
	pdf.Ln(9)

	// Title.
	title := "Rechnung"
	if iv.CancelsID != nil {
		title = "Storno-Rechnung"
	}
	if iv.Canceled {
		title += " (storniert)"
	}
	pdf.SetFont("Helvetica", "B", 17)
	pdf.SetTextColor(20, 20, 20)
	pdf.CellFormat(0, 10, tr(title), "", 1, "L", false, 0, "")
	pdf.Ln(1)

	// Meta rows.
	meta := [][2]string{
		{"Rechnungsnummer", iv.Number},
		{"Rechnungsdatum", iv.IssuedOn.Format(dateFmt)},
	}
	if iv.DueOn != nil {
		meta = append(meta, [2]string{"Fällig am", iv.DueOn.Format(dateFmt)})
	}
	if iv.LeistungFrom != nil && iv.LeistungTo != nil {
		meta = append(meta, [2]string{"Leistungszeitraum",
			iv.LeistungFrom.Format(dateFmt) + " – " + iv.LeistungTo.Format(dateFmt)})
	}
	for _, m := range meta {
		pdf.SetFont("Helvetica", "", 10)
		pdf.SetTextColor(90, 90, 90)
		pdf.CellFormat(45, 5, tr(m[0]), "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 10)
		pdf.SetTextColor(20, 20, 20)
		pdf.CellFormat(0, 5, tr(m[1]), "", 1, "L", false, 0, "")
	}
	pdf.Ln(7)

	// Items table. Columns sum to usableW (170).
	const cPos, cDesc, cQty, cUnit, cLine = 12.0, 88.0, 18.0, 26.0, 26.0
	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetFillColor(235, 238, 242)
	pdf.SetTextColor(40, 40, 40)
	pdf.CellFormat(cPos, 7, "Pos", "", 0, "L", true, 0, "")
	pdf.CellFormat(cDesc, 7, tr("Beschreibung"), "", 0, "L", true, 0, "")
	pdf.CellFormat(cQty, 7, tr("Menge"), "", 0, "R", true, 0, "")
	pdf.CellFormat(cUnit, 7, tr("Einzel"), "", 0, "R", true, 0, "")
	pdf.CellFormat(cLine, 7, tr("Betrag"), "", 1, "R", true, 0, "")

	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(25, 25, 25)
	pdf.SetDrawColor(220, 224, 228)
	fill := false
	pdf.SetFillColor(248, 249, 251)
	for _, it := range iv.Items {
		pdf.CellFormat(cPos, 6, strconv.Itoa(it.Pos), "B", 0, "L", fill, 0, "")
		pdf.CellFormat(cDesc, 6, fitText(pdf, tr(it.Description), cDesc), "B", 0, "L", fill, 0, "")
		pdf.CellFormat(cQty, 6, pdfQty(it.Quantity), "B", 0, "R", fill, 0, "")
		pdf.CellFormat(cUnit, 6, tr(pdfMoney(it.UnitAmount)), "B", 0, "R", fill, 0, "")
		pdf.CellFormat(cLine, 6, tr(pdfMoney(it.LineTotal)), "B", 1, "R", fill, 0, "")
		fill = !fill
	}
	pdf.Ln(4)

	// Totals, right-aligned.
	const labelW, valW = 44.0, 26.0
	totalsX := left + usableW - (labelW + valW)
	totalRow := func(label, val string, bold bool) {
		style := ""
		if bold {
			style = "B"
		}
		pdf.SetX(totalsX)
		pdf.SetFont("Helvetica", style, 10)
		pdf.CellFormat(labelW, 6, tr(label), "", 0, "R", false, 0, "")
		pdf.CellFormat(valW, 6, tr(val), "", 1, "R", false, 0, "")
	}
	if !iv.Kleinunternehmer {
		totalRow("Zwischensumme (netto)", pdfMoney(iv.Subtotal), false)
		rate := strings.Replace(strconv.FormatFloat(iv.UStRate, 'f', -1, 64), ".", ",", 1)
		totalRow("USt "+rate+"%", pdfMoney(iv.TaxAmount), false)
	}
	pdf.SetDrawColor(180, 185, 190)
	pdf.Line(totalsX, pdf.GetY(), left+usableW, pdf.GetY())
	totalRow("Gesamtbetrag", pdfMoney(iv.Total), true)
	pdf.Ln(6)

	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(70, 70, 70)
	if iv.Kleinunternehmer {
		pdf.MultiCell(usableW, 4.6,
			tr("Kleinunternehmer gemäß § 6 Abs 1 Z 27 UStG — kein Ausweis von Umsatzsteuer."),
			"", "L", false)
		pdf.Ln(2)
	}

	// Payment details.
	iban, bic := snapStr(seller, "iban"), snapStr(seller, "bic")
	if iban != "" {
		pay := "Zahlbar auf IBAN " + iban
		if bic != "" {
			pay += "  ·  BIC " + bic
		}
		if iv.DueOn != nil {
			pay += "  ·  bis " + iv.DueOn.Format(dateFmt)
		}
		pay += "  ·  Zahlungsreferenz " + iv.Number
		pdf.SetTextColor(25, 25, 25)
		pdf.MultiCell(usableW, 4.6, tr(pay), "", "L", false)
		pdf.Ln(1)

		// SEPA "Girocode" QR: scan to pre-fill the transfer. Use the open amount if
		// there is one, else the total (a paid invoice still documents how it was paid).
		qrAmount := iv.OpenAmount
		if qrAmount <= 0.005 {
			qrAmount = iv.Total
		}
		if payload, problem := epcPayload(snapStr(seller, "name"), iban, bic, qrAmount, iv.Number); problem == "" {
			if qrBytes, err := qrPNG(payload, 240); err == nil {
				pdf.RegisterImageOptionsReader("payqr", fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(qrBytes))
				y := pdf.GetY() + 2
				pdf.ImageOptions("payqr", left, y, 26, 26, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
				pdf.SetXY(left+30, y+3)
				pdf.SetFont("Helvetica", "", 8)
				pdf.SetTextColor(90, 90, 90)
				pdf.MultiCell(usableW-30, 4, tr("Scan zum Bezahlen (SEPA-Überweisung / Girocode)"), "", "L", false)
				pdf.SetY(y + 28)
			}
		}
	}
	if uid := snapStr(seller, "uid"); uid != "" {
		pdf.SetTextColor(90, 90, 90)
		pdf.CellFormat(0, 4.6, tr("UID: "+uid), "", 1, "L", false, 0, "")
	}
	if iv.Note != "" {
		pdf.Ln(1)
		pdf.SetTextColor(70, 70, 70)
		pdf.MultiCell(usableW, 4.6, tr(iv.Note), "", "L", false)
	}

	// Footer note from billing settings (bottom of page, small grey).
	if footer := strings.TrimSpace(snapStr(seller, "footer")); footer != "" {
		pdf.SetY(-24)
		pdf.SetFont("Helvetica", "", 7.5)
		pdf.SetTextColor(140, 140, 140)
		pdf.MultiCell(usableW, 3.6, tr(footer), "", "C", false)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		writeError(w, http.StatusInternalServerError, "PDF konnte nicht erstellt werden")
		return
	}
	// Sanitize the filename (invoice numbers are alnum/dash, but be defensive).
	fname := strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r == '\n' || r == '\r' {
			return '_'
		}
		return r
	}, "Rechnung-"+iv.Number+".pdf")
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.Header().Set("Content-Disposition", `inline; filename="`+fname+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
