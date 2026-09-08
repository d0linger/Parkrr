package handlers

import (
	"net/http"
	"sort"
	"strconv"
)

// OutstandingReportPDF liefert die Offene-Posten-Liste als druckfertiges A4-PDF
// (Hundert 17). Die Zahlen existierten bisher nur als Bildschirmliste und als CSV
// — was dem Steuerberater oder der Ablage fehlt, ist der BELEG mit Stichtag:
// "so standen die Forderungen am 30.09." als Dokument, nicht als lebende Ansicht.
//
// Dieselbe Quelle wie Dashboard und CSV (outstandingByPerson), also dieselben
// Zahlen — ein Report, der anders rechnet als die Ansicht daneben, wäre schlimmer
// als keiner.
func (h *Handler) OutstandingReportPDF(w http.ResponseWriter, r *http.Request) {
	bal, err := h.outstandingByPerson(r, 0)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	names, err := h.personNames(r)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	type row struct {
		name string
		open float64
	}
	rows := make([]row, 0, len(bal))
	var sum float64
	for pid, open := range bal {
		open = round2(open)
		// Nur echte Forderungen: Guthaben (negativ) und Nullsalden gehören nicht auf
		// eine Offene-Posten-Liste — sie ist die Mahn-/Nachfass-Grundlage.
		if open < 0.005 {
			continue
		}
		name := names[pid]
		if name == "" {
			name = "Person #" + itoa(pid)
		}
		rows = append(rows, row{name: name, open: open})
		sum += open
	}
	// Größte Forderung zuerst — die Liste ist eine Arbeitsliste, oben steht, was
	// zuerst nachgefasst gehört. Namensgleichheit bricht alphabetisch.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].open != rows[j].open {
			return rows[i].open > rows[j].open
		}
		return rows[i].name < rows[j].name
	})

	pdf, tr := newPDF()
	const left, right = 20.0, 20.0
	pdf.SetMargins(left, 18, right)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddPage()

	pdf.SetFont(pdfFontName, "B", 16)
	pdf.SetTextColor(20, 20, 20)
	pdf.CellFormat(0, 9, tr("Offene Posten"), "", 1, "L", false, 0, "")
	pdf.SetFont(pdfFontName, "", 9)
	pdf.SetTextColor(110, 110, 110)
	pdf.CellFormat(0, 5, tr("Stichtag "+h.now().Format("02.01.2006")+" · aufgelaufene Forderungen abzüglich erfasster Zahlungen"), "", 1, "L", false, 0, "")
	pdf.Ln(4)

	const cName, cOpen = 120.0, 50.0
	head := func() {
		pdf.SetFont(pdfFontName, "B", 9)
		pdf.SetFillColor(235, 238, 242)
		pdf.SetTextColor(40, 40, 40)
		pdf.CellFormat(cName, 7, tr("Person"), "", 0, "L", true, 0, "")
		pdf.CellFormat(cOpen, 7, tr("Offen"), "", 1, "R", true, 0, "")
	}
	head()
	pdf.SetFont(pdfFontName, "", 9)
	pdf.SetTextColor(20, 20, 20)
	if len(rows) == 0 {
		pdf.SetTextColor(110, 110, 110)
		pdf.CellFormat(0, 8, tr("Keine offenen Posten — alles beglichen."), "", 1, "L", false, 0, "")
	}
	for _, rw := range rows {
		// fitText kürzt überlange Namen, damit die Betragsspalte stehen bleibt.
		pdf.CellFormat(cName, 6.5, tr(fitText(pdf, rw.name, cName)), "B", 0, "L", false, 0, "")
		pdf.CellFormat(cOpen, 6.5, tr(pdfMoney(rw.open)), "B", 1, "R", false, 0, "")
	}
	pdf.Ln(1)
	pdf.SetFont(pdfFontName, "B", 10)
	pdf.CellFormat(cName, 8, tr("Summe ("+itoa(int64(len(rows)))+" Personen)"), "T", 0, "L", false, 0, "")
	pdf.CellFormat(cOpen, 8, tr(pdfMoney(sum)), "T", 1, "R", false, 0, "")

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition",
		`attachment; filename="parkrr-offene-posten-`+h.now().Format("2006-01-02")+`.pdf"`)
	if err := pdf.Output(w); err != nil {
		// Header sind schon raus; mehr als loggen geht hier nicht.
		serverError(w, r, "PDF-Erzeugung fehlgeschlagen", err)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
