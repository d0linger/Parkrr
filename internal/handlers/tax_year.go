package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/preining/parkrr/internal/auth"
)

// Steuerjahr (Einnahmenaufstellung): die Zahlungseingänge eines Kalenderjahres
// für die Einnahmen-Ausgaben-Rechnung — hier für Einkünfte aus Vermietung und
// Verpachtung (Beilage E1b). Maßgeblich ist der Zufluss (§ 19 EStG), also das
// Zahlungsdatum, nicht die Rechnung. Stornierte Zahlungen zählen nicht.
//
// Die 15-Tage-Regel für regelmäßig wiederkehrende Einnahmen wird nur
// VORGESCHLAGEN (Standard aus): sie verlangt, dass auch die Fälligkeit im
// 15-Tage-Fenster liegt, und die kennt Parkrr nicht. Erkannt werden nur
// Periodenzahlungen mit Periodenschlüssel direkt am Jahreswechsel:
//   - Dezember-Periode, eingegangen 1.–15. Jänner des Folgejahres → Vorjahr;
//   - Jänner-Periode bzw. Jahresperiode des Folgejahres, eingegangen
//     17.–31. Dezember → Folgejahr.

// taxYearPayment is one money-in row of the report.
type taxYearPayment struct {
	ID           int64     `json:"id"`
	PaidOn       time.Time `json:"paid_on"`
	BookedOn     time.Time `json:"booked_on"`
	DateAdjusted bool      `json:"date_adjusted"`
	PersonID     int64     `json:"person_id"`
	Person       string    `json:"person"`
	Amount       float64   `json:"amount"`
	Method       string    `json:"method"`
	Slider       bool      `json:"slider"`
	Kind         string    `json:"kind"`
	Period       string    `json:"period"`
	RuleYear     int       `json:"rule_year"`
	PropertyID   int64     `json:"property_id"`
	Property     string    `json:"property"`
	kindSplit    map[string]float64
}

// taxYearGroup is one labeled sum (by kind or by payment method).
type taxYearGroup struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`
	Amount float64 `json:"amount"`
}

// taxYearReport is the JSON shape of GET /api/reports/tax-year.
type taxYearReport struct {
	Year                  int                   `json:"year"`
	TotalByDate           float64               `json:"total_by_date"`
	TotalWithRule         float64               `json:"total_with_rule"`
	Count                 int                   `json:"count"`
	ReversedCount         int                   `json:"reversed_count"`
	ReversedAmount        float64               `json:"reversed_amount"`
	SliderCount           int                   `json:"slider_count"`
	SliderAmount          float64               `json:"slider_amount"`
	ByMonth               []float64             `json:"by_month"`
	ByKind                []taxYearGroup        `json:"by_kind"`
	ByMethod              []taxYearGroup        `json:"by_method"`
	ShiftedIn             []taxYearPayment      `json:"shifted_in"`
	ShiftedOut            []taxYearPayment      `json:"shifted_out"`
	ReviewPayments        []taxYearPayment      `json:"review_payments"`
	Properties            []taxProperty         `json:"properties"`
	PropertySummaries     []taxPropertySummary  `json:"property_summaries"`
	Categories            []taxCategory         `json:"categories"`
	Expenses              []taxExpense          `json:"expenses"`
	RecurringExpenses     []taxRecurringExpense `json:"recurring_expenses"`
	Assets                []taxAsset            `json:"assets"`
	E1B                   []taxE1BLine          `json:"e1b"`
	Settings              taxSettings           `json:"settings"`
	ExpenseTotal          float64               `json:"expense_total"`
	DepreciationTotal     float64               `json:"depreciation_total"`
	Surplus               float64               `json:"surplus"`
	SurplusWithRule       float64               `json:"surplus_with_rule"`
	SmallBusinessPercent  float64               `json:"small_business_percent"`
	SmallBusinessExceeded bool                  `json:"small_business_exceeded"`
	Locked                bool                  `json:"locked"`
	payments              []taxYearPayment      // all rows relevant to the year, for CSV/PDF
}

// #nosec G101 -- display labels ("credit" is Guthaben), not credentials
var taxKindLabels = map[string]string{
	"vehicle":   "Einstellplatz-Miete",
	"agreement": "Pauschalen",
	"recurring": "Nebenkosten",
	"charge":    "Zusatzkosten",
	"invoice":   "Über Rechnungen",
	"credit":    "Guthaben / Vorauszahlung",
}

var taxKindOrder = []string{"vehicle", "agreement", "recurring", "charge", "invoice", "credit"}

var taxMethodLabels = map[string]string{
	"bar": "Bar", "ueberweisung": "Überweisung", "paypal": "PayPal", "sonstiges": "Sonstiges", "": "Ohne Angabe",
}

// periodicKinds are the regularly recurring settlement kinds the 15-day rule
// can apply to.
var periodicKinds = map[string]bool{"vehicle": true, "agreement": true, "recurring": true}

// taxRuleYear returns the year a payment belongs to under the 15-day rule for
// regularly recurring income, or the payment date's year when it does not apply.
func taxRuleYear(paidOn time.Time, kind, period string) int {
	y := paidOn.Year()
	if !periodicKinds[kind] || period == "" {
		return y
	}
	m, d := paidOn.Month(), paidOn.Day()
	if pm, err := time.Parse("2006-01", period); err == nil {
		switch {
		case m == time.January && d <= 15 && pm.Year() == y-1 && pm.Month() == time.December:
			return y - 1
		case m == time.December && d >= 17 && pm.Year() == y+1 && pm.Month() == time.January:
			return y + 1
		}
		return y
	}
	if py, err := strconv.Atoi(period); err == nil && len(period) == 4 {
		if m == time.December && d >= 17 && py == y+1 {
			return y + 1
		}
	}
	return y
}

// buildTaxYearReport loads every non-reversed payment from 17 Dec of the prior
// year to 15 Jan of the next and aggregates the year by payment date, with the
// 15-day-rule alternative alongside.
func (h *Handler) buildTaxYearReport(ctx context.Context, year int) (taxYearReport, error) {
	rep := taxYearReport{Year: year, ByMonth: make([]float64, 12), ShiftedIn: []taxYearPayment{}, ShiftedOut: []taxYearPayment{}, ReviewPayments: []taxYearPayment{}, E1B: []taxE1BLine{}}
	from := time.Date(year-1, time.December, 17, 0, 0, 0, 0, time.UTC)
	to := time.Date(year+1, time.January, 15, 0, 0, 0, 0, time.UTC)

	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(sum(p.amount), 0) FROM payments p
		  LEFT JOIN payment_tax_metadata m ON m.payment_id=p.id
		  WHERE p.reversed AND COALESCE(m.received_on,p.paid_on) >= make_date($1, 1, 1)
		    AND COALESCE(m.received_on,p.paid_on) < make_date($1 + 1, 1, 1)`, year).
		Scan(&rep.ReversedCount, &rep.ReversedAmount); err != nil {
		return rep, err
	}

	rows, err := h.Pool.Query(ctx,
		`SELECT p.id, COALESCE(m.received_on,p.paid_on), p.paid_on, p.person_id,
		        trim(per.first_name || ' ' || per.last_name), p.amount, p.method,
		        p.auto, COALESCE(p.settles_kind, ''), COALESCE(p.settles_period, ''),
		        tp.id, tp.name
		   FROM payments p
		   JOIN persons per ON per.id = p.person_id
		   LEFT JOIN payment_tax_metadata m ON m.payment_id=p.id
		   JOIN tax_properties tp ON tp.id=COALESCE(m.tax_property_id,
		        (SELECT id FROM tax_properties ORDER BY is_default DESC,id LIMIT 1))
		  WHERE NOT p.reversed AND COALESCE(m.received_on,p.paid_on) BETWEEN $1 AND $2
		  ORDER BY COALESCE(m.received_on,p.paid_on), p.id`, from, to)
	if err != nil {
		return rep, err
	}
	var all []taxYearPayment
	for rows.Next() {
		var p taxYearPayment
		if err := rows.Scan(&p.ID, &p.PaidOn, &p.BookedOn, &p.PersonID, &p.Person, &p.Amount, &p.Method,
			&p.Slider, &p.Kind, &p.Period, &p.PropertyID, &p.Property); err != nil {
			rows.Close()
			return rep, err
		}
		p.DateAdjusted = !sameTaxDay(p.PaidOn, p.BookedOn)
		p.RuleYear = taxRuleYear(p.PaidOn, p.Kind, p.Period)
		if p.PaidOn.Year() == year || p.RuleYear == year {
			all = append(all, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return rep, err
	}
	if err := h.splitTaxKinds(ctx, all); err != nil {
		return rep, err
	}

	byKind, byMethod := map[string]float64{}, map[string]float64{}
	for _, p := range all {
		inYear := p.PaidOn.Year() == year
		if p.RuleYear == year {
			rep.TotalWithRule += p.Amount
		}
		switch {
		case inYear && p.RuleYear != year:
			rep.ShiftedOut = append(rep.ShiftedOut, p)
		case !inYear && p.RuleYear == year:
			rep.ShiftedIn = append(rep.ShiftedIn, p)
		}
		if !inYear {
			continue
		}
		rep.Count++
		rep.TotalByDate += p.Amount
		rep.ByMonth[p.PaidOn.Month()-1] += p.Amount
		byMethod[p.Method] += p.Amount
		if p.Slider {
			rep.SliderCount++
			rep.SliderAmount += p.Amount
			rep.ReviewPayments = append(rep.ReviewPayments, p)
		}
		for k, v := range p.kindSplit {
			byKind[k] += v
		}
	}
	rep.payments = all
	rep.TotalByDate, rep.TotalWithRule = round2(rep.TotalByDate), round2(rep.TotalWithRule)
	rep.SliderAmount, rep.ReversedAmount = round2(rep.SliderAmount), round2(rep.ReversedAmount)
	for i := range rep.ByMonth {
		rep.ByMonth[i] = round2(rep.ByMonth[i])
	}
	rep.ByKind = []taxYearGroup{}
	for _, k := range taxKindOrder {
		if v := round2(byKind[k]); v != 0 {
			rep.ByKind = append(rep.ByKind, taxYearGroup{Key: k, Label: taxKindLabels[k], Amount: v})
		}
	}
	rep.ByMethod = []taxYearGroup{}
	for m, v := range byMethod {
		label, ok := taxMethodLabels[m]
		if !ok {
			label = m
		}
		rep.ByMethod = append(rep.ByMethod, taxYearGroup{Key: m, Label: label, Amount: round2(v)})
	}
	sort.Slice(rep.ByMethod, func(i, j int) bool { return rep.ByMethod[i].Amount > rep.ByMethod[j].Amount })
	if err := h.addTaxBook(ctx, &rep); err != nil {
		return rep, err
	}
	// With multiple tax objects every payment needs an explicit assignment in
	// the review list. With one object, only slider payments need date review.
	if len(rep.Properties) > 1 {
		rep.ReviewPayments = rep.ReviewPayments[:0]
		for _, p := range rep.payments {
			if p.PaidOn.Year() == year {
				rep.ReviewPayments = append(rep.ReviewPayments, p)
			}
		}
	}
	return rep, nil
}

// splitTaxKinds attributes each payment to what it settled: a period payment to
// its kind; otherwise its allocations, its invoice payments, and the unassigned
// rest as credit. Invoice lines carry no kind, so invoice money stays one group.
func (h *Handler) splitTaxKinds(ctx context.Context, ps []taxYearPayment) error {
	ids := make([]int64, 0, len(ps))
	for i := range ps {
		ps[i].kindSplit = map[string]float64{}
		if ps[i].Kind == "" {
			ids = append(ids, ps[i].ID)
		} else {
			ps[i].kindSplit[ps[i].Kind] = ps[i].Amount
		}
	}
	if len(ids) == 0 {
		return nil
	}
	assigned := map[int64]map[string]float64{}
	rows, err := h.Pool.Query(ctx,
		`SELECT payment_id, kind, sum(amount) FROM payment_allocations WHERE payment_id = ANY($1) GROUP BY 1, 2
		 UNION ALL
		 SELECT payment_id, 'invoice', sum(amount) FROM invoice_payments WHERE payment_id = ANY($1) GROUP BY 1`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var kind string
		var amt float64
		if err := rows.Scan(&id, &kind, &amt); err != nil {
			return err
		}
		if assigned[id] == nil {
			assigned[id] = map[string]float64{}
		}
		assigned[id][kind] += amt
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range ps {
		if ps[i].Kind != "" {
			continue
		}
		rest := ps[i].Amount
		for k, v := range assigned[ps[i].ID] {
			ps[i].kindSplit[k] += v
			rest -= v
		}
		if rest > 0.005 {
			ps[i].kindSplit["credit"] += rest
		}
	}
	return nil
}

// TaxYear returns the income report of ?year= (default: the previous year, the
// one a tax return is usually prepared for).
func (h *Handler) TaxYear(w http.ResponseWriter, r *http.Request) {
	rep, err := h.buildTaxYearReport(r.Context(), parseYearParam(r, h.now().Year()-1))
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// taxRuleParam reports whether ?rule=1 asks for the 15-day rule.
func taxRuleParam(r *http.Request) bool { return r.URL.Query().Get("rule") == "1" }

// TaxYearCSV exports every payment the report counts, with both year columns.
func (h *Handler) TaxYearCSV(w http.ResponseWriter, r *http.Request) {
	year := parseYearParam(r, h.now().Year()-1)
	rep, err := h.buildTaxYearReport(r.Context(), year)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	header := []string{"zuflussdatum", "buchungstag", "steuerobjekt", "person", "betrag_eur", "art", "periode", "zahlungsart", "ueber_schalter",
		"jahr_nach_zahlungsdatum", "jahr_nach_15_tage_regel"}
	rows := make([][]string, 0, len(rep.payments))
	for _, p := range rep.payments {
		rows = append(rows, []string{csvDate(p.PaidOn), csvDate(p.BookedOn), p.Property, p.Person, csvMoney(p.Amount), taxKindText(p),
			p.Period, taxMethodLabels[p.Method], boolJaNein(p.Slider),
			strconv.Itoa(p.PaidOn.Year()), strconv.Itoa(p.RuleYear)})
	}
	writeCSVDownload(w, r, "einnahmen-"+strconv.Itoa(year), header, rows, h.now())
}

// taxKindText names what a payment settled, e.g. "Einstellplatz-Miete" or
// "Über Rechnungen + Guthaben / Vorauszahlung" for a split payment.
func taxKindText(p taxYearPayment) string {
	out := ""
	for _, k := range taxKindOrder {
		if p.kindSplit[k] > 0.005 {
			if out != "" {
				out += " + "
			}
			out += taxKindLabels[k]
		}
	}
	return out
}

// TaxYearPDF renders the Einnahmenaufstellung for the tax adviser.
func (h *Handler) TaxYearPDF(w http.ResponseWriter, r *http.Request) {
	year := parseYearParam(r, h.now().Year()-1)
	rep, err := h.buildTaxYearReport(r.Context(), year)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	rule := taxRuleParam(r)
	ys := strconv.Itoa(year)

	pdf, tr := newPDF()
	const left, right = 20.0, 20.0
	pdf.SetMargins(left, 18, right)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddPage()
	pdf.SetFont(pdfFontName, "B", 16)
	pdf.SetTextColor(20, 20, 20)
	pdf.CellFormat(0, 9, tr("Einnahmenaufstellung "+ys), "", 1, "L", false, 0, "")
	pdf.SetFont(pdfFontName, "", 9)
	pdf.SetTextColor(110, 110, 110)
	pdf.CellFormat(0, 5, tr("Vermietung und Verpachtung · Zuflussprinzip (§ 19 EStG) · erstellt am "+h.now().Format("02.01.2006")), "", 1, "L", false, 0, "")
	pdf.Ln(4)

	const cL, cR = 120.0, 50.0
	line := func(label string, v float64, bold bool, border string) {
		style := ""
		if bold {
			style = "B"
		}
		pdf.SetFont(pdfFontName, style, 9.5)
		pdf.SetTextColor(20, 20, 20)
		pdf.CellFormat(cL, 6.5, tr(fitText(pdf, label, cL)), border, 0, "L", false, 0, "")
		pdf.CellFormat(cR, 6.5, tr(pdfMoney(v)), border, 1, "R", false, 0, "")
	}
	section := func(title string) {
		pdf.Ln(3)
		pdf.SetFont(pdfFontName, "B", 9)
		pdf.SetFillColor(235, 238, 242)
		pdf.SetTextColor(40, 40, 40)
		pdf.CellFormat(cL+cR, 7, tr(title), "", 1, "L", true, 0, "")
	}

	section("Summe")
	line("Einnahmen nach Zahlungsdatum ("+itoa(int64(rep.Count))+" Zahlungen)", rep.TotalByDate, !rule, "B")
	if rule {
		line("Einnahmen mit 15-Tage-Regel (wiederkehrende Einnahmen am Jahreswechsel)", rep.TotalWithRule, true, "B")
	}
	section("Nach Monat (Zahlungsdatum)")
	months := []string{"Jänner", "Februar", "März", "April", "Mai", "Juni", "Juli", "August", "September", "Oktober", "November", "Dezember"}
	for i, v := range rep.ByMonth {
		line(months[i], v, false, "B")
	}
	section("Nach Art (Zahlungsdatum)")
	for _, g := range rep.ByKind {
		line(g.Label, g.Amount, false, "B")
	}
	section("Nach Zahlungsart (Zahlungsdatum)")
	for _, g := range rep.ByMethod {
		line(g.Label, g.Amount, false, "B")
	}
	section("Werbungskosten und Ergebnis")
	line("Werbungskosten (ohne AfA)", rep.ExpenseTotal, false, "B")
	line("Absetzung für Abnutzung (AfA)", rep.DepreciationTotal, false, "B")
	line("Überschuss nach Zahlungsdatum", rep.Surplus, !rule, "B")
	if rule {
		line("Überschuss mit 15-Tage-Regel", rep.SurplusWithRule, true, "B")
	}
	if len(rep.PropertySummaries) > 1 {
		section("Ergebnis je Steuerobjekt")
		for _, p := range rep.PropertySummaries {
			value := p.Surplus
			if rule {
				value = p.SurplusWithRule
			}
			line(p.Name, value, false, "B")
		}
	}
	section("E1b-Vorschau")
	for _, p := range rep.PropertySummaries {
		if len(rep.PropertySummaries) > 1 {
			line(p.Name, 0, true, "")
		}
		for _, e := range rep.E1B {
			if e.PropertyID == p.ID {
				line("Kennzahl "+e.Code+" · "+e.Label, taxE1BAmount(rep, e, rule), false, "B")
			}
		}
	}
	if rule && (len(rep.ShiftedIn) > 0 || len(rep.ShiftedOut) > 0) {
		section("Verschiebungen nach der 15-Tage-Regel")
		for _, p := range rep.ShiftedIn {
			line("+ "+p.PaidOn.Format("02.01.2006")+" "+p.Person+" (Periode "+p.Period+")", p.Amount, false, "B")
		}
		for _, p := range rep.ShiftedOut {
			line("− "+p.PaidOn.Format("02.01.2006")+" "+p.Person+" (Periode "+p.Period+")", -p.Amount, false, "B")
		}
	}
	pdf.Ln(4)
	pdf.SetFont(pdfFontName, "", 8)
	pdf.SetTextColor(110, 110, 110)
	note := "Nicht enthalten: " + itoa(int64(rep.ReversedCount)) + " stornierte Zahlungen (" + pdfMoney(rep.ReversedAmount) + "). "
	if rep.SliderCount > 0 {
		note += itoa(int64(rep.SliderCount)) + " Zahlungen (" + pdfMoney(rep.SliderAmount) +
			") wurden über den \"bezahlt\"-Schalter gebucht; ihr Datum ist der Buchungstag, nicht zwingend der Geldeingang. "
	}
	note += "E1b-Zuordnungen sind eine Vorschau und vor Abgabe fachlich zu prüfen. Keine Steuerberatung."
	pdf.MultiCell(cL+cR, 4.2, tr(note), "", "L", false)

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="parkrr-einnahmen-`+ys+`.pdf"`)
	if err := pdf.Output(w); err != nil {
		// Header und Rumpf sind schon raus; nur protokollieren (wie OutstandingReportPDF).
		auth.SetRequestError(r.Context(), err)
		slog.Error("einnahmen-pdf: Ausgabe fehlgeschlagen", "err", err)
	}
}

// TaxYearPackage exports one self-contained adviser package: summary PDF, CSVs
// and every immutable receipt attached to an expense of the selected year.
func (h *Handler) TaxYearPackage(w http.ResponseWriter, r *http.Request) {
	year := parseYearParam(r, h.now().Year()-1)
	rep, err := h.buildTaxYearReport(r.Context(), year)
	if err != nil {
		serverError(w, r, "Exportpaket konnte nicht erstellt werden", err)
		return
	}
	pdfBytes, err := buildTaxPackagePDF(rep, taxRuleParam(r), h.now())
	if err != nil {
		serverError(w, r, "Exportpaket konnte nicht erstellt werden", err)
		return
	}
	receipts, err := h.Pool.Query(r.Context(), `
		SELECT a.id,a.expense_id,a.filename,a.data
		  FROM tax_expense_receipts a JOIN tax_expenses e ON e.id=a.expense_id
		 WHERE e.paid_on >= make_date($1,1,1) AND e.paid_on < make_date($1+1,1,1)
		 ORDER BY e.paid_on,e.id,a.id`, year)
	if err != nil {
		serverError(w, r, "Belege konnten nicht geladen werden", err)
		return
	}
	defer receipts.Close()

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="parkrr-steuerjahr-`+strconv.Itoa(year)+`.zip"`)
	zw := zip.NewWriter(w)
	writeBytes := func(name string, data []byte) error {
		entry, e := zw.Create(name)
		if e != nil {
			return e
		}
		_, e = entry.Write(data)
		return e
	}
	if err := writeBytes("steuerjahr-"+strconv.Itoa(year)+".pdf", pdfBytes); err != nil {
		auth.SetRequestError(r.Context(), err)
		_ = zw.Close()
		return
	}
	if err := writeTaxPackageCSVs(zw, rep, taxRuleParam(r)); err != nil {
		auth.SetRequestError(r.Context(), err)
		_ = zw.Close()
		return
	}
	receiptCount := 0
	for receipts.Next() {
		var receiptID, expenseID int64
		var filename string
		var data []byte
		if err := receipts.Scan(&receiptID, &expenseID, &filename, &data); err != nil {
			auth.SetRequestError(r.Context(), err)
			_ = zw.Close()
			return
		}
		base := safeDownloadFilename(filepath.Base(filename))
		if base == "" || base == "." {
			base = "beleg"
		}
		name := fmt.Sprintf("belege/ausgabe-%d-%d-%s", expenseID, receiptID, strings.ReplaceAll(base, "/", "_"))
		if err := writeBytes(name, data); err != nil {
			auth.SetRequestError(r.Context(), err)
			_ = zw.Close()
			return
		}
		receiptCount++
	}
	if err := receipts.Err(); err != nil {
		auth.SetRequestError(r.Context(), err)
		_ = zw.Close()
		return
	}
	manifest := fmt.Sprintf("Parkrr Steuerjahr %d\nZahlungen: %d\nAusgabenbuchungen: %d\nBelege: %d\nErstellt: %s\n\nE1b-Zuordnungen sind eine Vorschau. Keine Steuerberatung.\n",
		year, rep.Count, len(rep.Expenses), receiptCount, h.now().Format("02.01.2006 15:04"))
	if err := writeBytes("LESE_MICH.txt", []byte(manifest)); err != nil {
		auth.SetRequestError(r.Context(), err)
	}
	if err := zw.Close(); err != nil {
		auth.SetRequestError(r.Context(), err)
	}
}

func buildTaxPackagePDF(rep taxYearReport, rule bool, now time.Time) ([]byte, error) {
	pdf, tr := newPDF()
	pdf.SetMargins(20, 18, 20)
	pdf.SetAutoPageBreak(true, 18)
	pdf.AddPage()
	pdf.SetFont(pdfFontName, "B", 16)
	pdf.CellFormat(0, 9, tr("Steuerjahr "+strconv.Itoa(rep.Year)), "", 1, "L", false, 0, "")
	pdf.SetFont(pdfFontName, "", 9)
	pdf.SetTextColor(100, 100, 100)
	pdf.CellFormat(0, 6, tr("Vermietung und Verpachtung · erstellt am "+now.Format("02.01.2006")), "", 1, "L", false, 0, "")
	pdf.Ln(4)
	income, surplus := rep.TotalByDate, rep.Surplus
	if rule {
		income, surplus = rep.TotalWithRule, rep.SurplusWithRule
	}
	lines := [][2]string{
		{"Einnahmen", pdfMoney(income)}, {"Werbungskosten", pdfMoney(rep.ExpenseTotal)},
		{"AfA", pdfMoney(rep.DepreciationTotal)}, {"Überschuss", pdfMoney(surplus)},
	}
	for _, row := range lines {
		pdf.SetFont(pdfFontName, "B", 10)
		pdf.SetTextColor(20, 20, 20)
		pdf.CellFormat(125, 8, tr(row[0]), "B", 0, "L", false, 0, "")
		pdf.CellFormat(45, 8, tr(row[1]), "B", 1, "R", false, 0, "")
	}
	for _, p := range rep.PropertySummaries {
		pdf.Ln(4)
		pdf.SetFont(pdfFontName, "B", 11)
		pdf.CellFormat(0, 7, tr(p.Name), "", 1, "L", false, 0, "")
		pdf.SetFont(pdfFontName, "", 9)
		for _, e := range rep.E1B {
			if e.PropertyID != p.ID {
				continue
			}
			pdf.CellFormat(125, 6, tr("Kennzahl "+e.Code+" · "+e.Label), "B", 0, "L", false, 0, "")
			pdf.CellFormat(45, 6, tr(pdfMoney(taxE1BAmount(rep, e, rule))), "B", 1, "R", false, 0, "")
		}
	}
	pdf.Ln(4)
	pdf.SetFont(pdfFontName, "", 8)
	pdf.SetTextColor(100, 100, 100)
	pdf.MultiCell(170, 4.2, tr("E1b-Zuordnungen sind eine Vorschau und vor Abgabe fachlich zu prüfen. Keine Steuerberatung."), "", "L", false)
	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeTaxPackageCSVs(zw *zip.Writer, rep taxYearReport, rule bool) error {
	writeCSV := func(name string, header []string, rows [][]string) error {
		entry, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := entry.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
			return err
		}
		cw := csv.NewWriter(entry)
		cw.Comma = ';'
		for i := range header {
			header[i] = csvSafe(header[i])
		}
		if err := cw.Write(header); err != nil {
			return err
		}
		for _, row := range rows {
			for i := range row {
				row[i] = csvSafe(row[i])
			}
			if err := cw.Write(row); err != nil {
				return err
			}
		}
		cw.Flush()
		return cw.Error()
	}
	incomeRows := make([][]string, 0, len(rep.payments))
	for _, p := range rep.payments {
		incomeRows = append(incomeRows, []string{csvDate(p.PaidOn), csvDate(p.BookedOn), p.Property, p.Person,
			csvMoney(p.Amount), taxKindText(p), p.Period, taxMethodLabels[p.Method], boolJaNein(p.Slider), strconv.Itoa(p.RuleYear)})
	}
	if err := writeCSV("einnahmen.csv", []string{"zuflussdatum", "buchungstag", "steuerobjekt", "person", "betrag_eur", "art", "periode", "zahlungsart", "ueber_schalter", "regeljahr"}, incomeRows); err != nil {
		return err
	}
	expenseRows := make([][]string, 0, len(rep.Expenses))
	for _, e := range rep.Expenses {
		reverses := ""
		if e.ReversesID != nil {
			reverses = strconv.FormatInt(*e.ReversesID, 10)
		}
		expenseRows = append(expenseRows, []string{e.PaidOn, e.Property, e.Category, e.E1BCode, csvMoney(e.Amount),
			csvMoney(e.VATAmount), e.Payee, e.Description, e.PaymentMethod, reverses, strconv.Itoa(e.ReceiptCount)})
	}
	if err := writeCSV("werbungskosten.csv", []string{"zahlungsdatum", "steuerobjekt", "kategorie", "e1b_kennzahl", "betrag_eur", "ust_eur", "empfaenger", "beschreibung", "zahlungsart", "storniert_id", "belege"}, expenseRows); err != nil {
		return err
	}
	assetRows := make([][]string, 0, len(rep.Assets))
	for _, a := range rep.Assets {
		assetRows = append(assetRows, []string{a.Property, a.Name, a.InServiceOn, csvMoney(a.DepreciableBasis),
			strconv.FormatFloat(a.UsefulLifeYears, 'f', 2, 64), boolJaNein(a.HalfYearRule), a.DisposedOn, csvMoney(a.YearDepreciation), a.Notes})
	}
	if err := writeCSV("anlageverzeichnis.csv", []string{"steuerobjekt", "wirtschaftsgut", "in_betrieb", "bemessungsgrundlage_eur", "nutzungsdauer_jahre", "halbjahresregel", "ausgeschieden_am", "afa_im_jahr_eur", "notiz"}, assetRows); err != nil {
		return err
	}
	e1bRows := make([][]string, 0, len(rep.E1B))
	propertyNames := map[int64]string{}
	for _, p := range rep.Properties {
		propertyNames[p.ID] = p.Name
	}
	for _, e := range rep.E1B {
		e1bRows = append(e1bRows, []string{propertyNames[e.PropertyID], e.Code, e.Label, csvMoney(taxE1BAmount(rep, e, rule))})
	}
	return writeCSV("e1b-vorschau.csv", []string{"steuerobjekt", "kennzahl", "bezeichnung", "betrag_eur"}, e1bRows)
}

func taxE1BAmount(rep taxYearReport, line taxE1BLine, rule bool) float64 {
	if !rule || line.Code != "9460" {
		return line.Amount
	}
	for _, property := range rep.PropertySummaries {
		if property.ID == line.PropertyID {
			return property.IncomeWithRule
		}
	}
	return line.Amount
}
