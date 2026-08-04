package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

// billingSettings is the GUI-editable invoicing configuration (one row, id=1).
// Austria: kleinunternehmer => § 6 Abs 1 Z 27 UStG (no USt); otherwise ust_rate
// (20 / 13 / 10) is shown. next_invoice_no drives the gapless invoice number.
type billingSettings struct {
	SellerName       string  `json:"seller_name"`
	SellerAddress    string  `json:"seller_address"`
	SellerUID        string  `json:"seller_uid"`
	Kleinunternehmer bool    `json:"kleinunternehmer"`
	UStRate          float64 `json:"ust_rate"`
	InvoicePrefix    string  `json:"invoice_prefix"`
	NextInvoiceNo    int     `json:"next_invoice_no"`
	NumberPad        int     `json:"number_pad"`
	IBAN             string  `json:"iban"`
	BIC              string  `json:"bic"`
	PaymentTermsDays int     `json:"payment_terms_days"`
	FooterNote       string  `json:"footer_note"`
}

// austrianRates are the valid USt rates when not a Kleinunternehmer.
var austrianRates = map[float64]bool{20: true, 13: true, 10: true}

func (h *Handler) loadBillingSettings(ctx context.Context) (billingSettings, error) {
	var s billingSettings
	err := h.Pool.QueryRow(ctx,
		`SELECT seller_name, seller_address, seller_uid, kleinunternehmer, ust_rate,
		        invoice_prefix, next_invoice_no, number_pad, iban, bic, payment_terms_days, footer_note
		   FROM billing_settings WHERE id=1`,
	).Scan(&s.SellerName, &s.SellerAddress, &s.SellerUID, &s.Kleinunternehmer, &s.UStRate,
		&s.InvoicePrefix, &s.NextInvoiceNo, &s.NumberPad, &s.IBAN, &s.BIC, &s.PaymentTermsDays, &s.FooterNote)
	return s, err
}

// GetBillingSettings returns the invoicing configuration (admin).
func (h *Handler) GetBillingSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.loadBillingSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load billing settings")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// SaveBillingSettings updates the invoicing configuration (admin). next_invoice_no
// is only allowed to move forward, so a gap can be opened deliberately but the
// sequence can't be rewound onto an already-used number.
func (h *Handler) SaveBillingSettings(w http.ResponseWriter, r *http.Request) {
	var in billingSettings
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.SellerName = trim(in.SellerName)
	in.SellerAddress = trim(in.SellerAddress)
	in.SellerUID = trim(in.SellerUID)
	in.InvoicePrefix = trim(in.InvoicePrefix)
	in.IBAN = trim(in.IBAN)
	in.BIC = trim(in.BIC)
	in.FooterNote = trim(in.FooterNote)
	if !in.Kleinunternehmer && !austrianRates[in.UStRate] {
		writeError(w, http.StatusBadRequest, "USt-Satz muss 20, 13 oder 10 sein")
		return
	}
	if !in.Kleinunternehmer && in.SellerUID == "" {
		writeError(w, http.StatusBadRequest, "bei USt-Ausweis ist die UID-Nummer Pflicht")
		return
	}
	if in.NumberPad < 1 || in.NumberPad > 10 {
		in.NumberPad = 4
	}
	if in.NextInvoiceNo < 1 {
		in.NextInvoiceNo = 1
	}
	if in.PaymentTermsDays < 0 {
		in.PaymentTermsDays = 0
	}
	// Never rewind the sequence below what's already been issued.
	var cur int
	_ = h.Pool.QueryRow(r.Context(), `SELECT next_invoice_no FROM billing_settings WHERE id=1`).Scan(&cur)
	if in.NextInvoiceNo < cur {
		in.NextInvoiceNo = cur
	}
	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE billing_settings SET seller_name=$1, seller_address=$2, seller_uid=$3,
		        kleinunternehmer=$4, ust_rate=$5, invoice_prefix=$6, next_invoice_no=$7,
		        number_pad=$8, iban=$9, bic=$10, payment_terms_days=$11, footer_note=$12
		  WHERE id=1`,
		in.SellerName, in.SellerAddress, in.SellerUID, in.Kleinunternehmer, in.UStRate,
		in.InvoicePrefix, in.NextInvoiceNo, in.NumberPad, in.IBAN, in.BIC, in.PaymentTermsDays, in.FooterNote,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save billing settings")
		return
	}
	h.audit(r, "update", "billing", 0, "updated invoicing settings")
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// --- Invoices ---

type invoiceItem struct {
	Pos         int     `json:"pos"`
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitAmount  float64 `json:"unit_amount"`
	LineTotal   float64 `json:"line_total"`
}

type invoice struct {
	ID               int64          `json:"id"`
	Number           string         `json:"number"`
	PersonID         int64          `json:"person_id"`
	IssuedOn         time.Time      `json:"issued_on"`
	DueOn            *time.Time     `json:"due_on"`
	Subtotal         float64        `json:"subtotal"`
	UStRate          float64        `json:"ust_rate"`
	TaxAmount        float64        `json:"tax_amount"`
	Total            float64        `json:"total"`
	Kleinunternehmer bool           `json:"kleinunternehmer"`
	Seller           map[string]any `json:"seller"`
	Buyer            map[string]any `json:"buyer"`
	Note             string         `json:"note"`
	Canceled         bool           `json:"canceled"`             // storniert (immutable original, superseded)
	CancelsID        *int64         `json:"cancels_id,omitempty"` // set on a Storno document -> the original
	PaidAmount       float64        `json:"paid_amount"`          // sum of payments allocated to this invoice
	OpenAmount       float64        `json:"open_amount"`          // total - paid_amount (the open item)
	Status           string         `json:"status"`               // offen | teilbezahlt | bezahlt | storniert | storno
	Items            []invoiceItem  `json:"items,omitempty"`
}

// invoiceStatus derives the OP status from the amounts and lifecycle flags.
func invoiceStatus(total, paid float64, canceled bool, cancelsID *int64) string {
	switch {
	case cancelsID != nil:
		return "storno" // the counter-document itself
	case canceled:
		return "storniert"
	case total > 0.005 && paid+0.005 >= total:
		return "bezahlt"
	case paid > 0.005:
		return "teilbezahlt"
	default:
		return "offen"
	}
}

type createInvoiceRequest struct {
	Note string `json:"note"`
}

// invoiceLines composes the full list of a person's open positions for an
// invoice — everything they owe, not just the boolean-settled items: standalone
// vehicle rent, standalone AND vehicle-bound unsettled one-off charges, open
// Pauschalen, and the open recurring total. The line totals sum to the person's
// open balance.
func (h *Handler) invoiceLines(r *http.Request, personID int64) ([]owedItem, error) {
	ctx := r.Context()
	now := time.Now()
	until := now.AddDate(0, 0, 1)

	vehicles, _, err := h.loadVehiclesWithCategories(r, personID)
	if err != nil {
		return nil, err
	}
	ags, err := h.loadAgreements(ctx, personID, now)
	if err != nil {
		return nil, err
	}
	setFlatRateCoverage(vehicles, map[int64][]models.FlatRatePeriod{personID: ags}, now)
	vehPaid := vehiclePaidMap(vehicles)
	vehLabel := func(vid int64) string {
		for i := range vehicles {
			if vehicles[i].ID == vid {
				v := &vehicles[i]
				if s := strings.TrimSpace(v.Label); s != "" {
					return s
				}
				if s := strings.TrimSpace(v.LicensePlate); s != "" {
					return s
				}
				return v.CategoryName
			}
		}
		return "Gefährt"
	}

	// Standalone vehicles (open rent) + standalone one-off charges.
	lines, err := h.openOwedItems(r, personID)
	if err != nil {
		return nil, err
	}

	// Vehicle-bound one-off charges not settled via their vehicle/Pauschale.
	crows, err := h.Pool.Query(ctx,
		`SELECT id, description, quantity, amount, charged_on, vehicle_id
		   FROM charges WHERE person_id=$1 AND vehicle_id IS NOT NULL AND NOT paid`, personID)
	if err != nil {
		return nil, err
	}
	type bound struct {
		id          int64
		desc        string
		qty, amount float64
		on          time.Time
		vid         int64
	}
	var bcs []bound
	for crows.Next() {
		var b bound
		if err := crows.Scan(&b.id, &b.desc, &b.qty, &b.amount, &b.on, &b.vid); err != nil {
			crows.Close()
			return nil, err
		}
		bcs = append(bcs, b)
	}
	crows.Close()
	if crows.Err() != nil {
		return nil, crows.Err()
	}
	for _, b := range bcs {
		vid := b.vid
		if chargeSettled(ags, &vid, b.on, false, vehPaid[vid]) {
			continue
		}
		if b.qty <= 0 {
			b.qty = 1
		}
		lt := round2(b.amount * b.qty)
		if lt <= 0.005 {
			continue
		}
		lines = append(lines, owedItem{Kind: "charge", ID: b.id, Label: vehLabel(vid) + ": " + b.desc, Quantity: b.qty, UnitAmount: b.amount, LineTotal: lt})
	}

	// Open Pauschalen (flat-rate): unpaid accrued per agreement.
	for i := range ags {
		a := &ags[i]
		owed := round2(a.CostInRange(a.StartDate, until) - float64(a.PaidCentsInRange(a.StartDate, until))/100)
		if owed <= 0.005 {
			continue
		}
		label := "Pauschale"
		if s := strings.TrimSpace(a.Note); s != "" {
			label += ": " + s
		}
		lines = append(lines, owedItem{Kind: "agreement", ID: a.ID, Label: label, Quantity: 1, UnitAmount: owed, LineTotal: owed})
	}

	// Recurring extra costs: the open amount as one aggregate line.
	recurs, err := h.loadRecurringCharges(ctx, personID, ags, vehPaid, now)
	if err != nil {
		return nil, err
	}
	recAccrued, recPaid := recurringSums(recurs, ags, vehPaid, now)
	if recOpen := round2(recAccrued - recPaid); recOpen > 0.005 {
		lines = append(lines, owedItem{Kind: "recurring", ID: 0, Label: "Wiederkehrende Nebenkosten (offen)", Quantity: 1, UnitAmount: recOpen, LineTotal: recOpen})
	}

	// Fakturier-Sperre: drop discrete positions (vehicles, one-off charges) that an
	// active (non-canceled) invoice already billed, so nothing is invoiced twice.
	// Pauschale/recurring are periodic and not yet locked (see migration 026).
	locked, err := h.lockedPositions(ctx, personID)
	if err != nil {
		return nil, err
	}
	if len(locked) > 0 {
		kept := lines[:0]
		for _, it := range lines {
			if (it.Kind == "vehicle" || it.Kind == "charge") && locked[it.Kind+":"+strconv.FormatInt(it.ID, 10)] {
				continue
			}
			kept = append(kept, it)
		}
		lines = kept
	}
	return lines, nil
}

// lockedPositions returns the set "kind:ref_id" of positions already billed by a
// non-canceled invoice of the person.
func (h *Handler) lockedPositions(ctx context.Context, personID int64) (map[string]bool, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT s.kind, s.ref_id FROM invoice_source s
		    JOIN invoices i ON i.id = s.invoice_id
		   WHERE i.person_id = $1 AND NOT i.canceled`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var kind string
		var ref int64
		if err := rows.Scan(&kind, &ref); err != nil {
			return nil, err
		}
		out[kind+":"+strconv.FormatInt(ref, 10)] = true
	}
	return out, rows.Err()
}

// invoiceComplianceError checks the §11 UStG (AT) mandatory invoice fields and
// returns a non-empty message naming what's missing, so an incomplete/non-compliant
// invoice is never issued (GoBD/BAO: Richtigkeit & Vollständigkeit).
func invoiceComplianceError(s billingSettings, buyerName string) string {
	var missing []string
	if strings.TrimSpace(s.SellerName) == "" {
		missing = append(missing, "Name des Ausstellers")
	}
	if strings.TrimSpace(s.SellerAddress) == "" {
		missing = append(missing, "Anschrift des Ausstellers")
	}
	if strings.TrimSpace(buyerName) == "" {
		missing = append(missing, "Name des Leistungsempfängers")
	}
	if !s.Kleinunternehmer {
		if strings.TrimSpace(s.SellerUID) == "" {
			missing = append(missing, "UID-Nummer (bei USt-Ausweis Pflicht)")
		}
		if !austrianRates[s.UStRate] {
			missing = append(missing, "gültiger USt-Satz (20/13/10)")
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "Rechnung nicht ausstellbar – Pflichtangaben fehlen (§ 11 UStG): " + strings.Join(missing, ", ") +
		". Bitte unter Rechnungen → Einstellungen ergänzen."
}

// CreateInvoice issues an invoice for a person's open positions. It allocates the
// next gapless number, snapshots seller/buyer + tax, and stores the line items —
// all in one transaction so a failure never burns a number. Austrian tax: no USt
// for a Kleinunternehmer (§ 6 Abs 1 Z 27 UStG); otherwise USt on top of the net
// line totals.
func (h *Handler) CreateInvoice(w http.ResponseWriter, r *http.Request) {
	pid, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req createInvoiceRequest
	if err := decodeJSON(r, &req); err != nil && err != errNotJSON {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	items, err := h.invoiceLines(r, pid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read open positions")
		return
	}
	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "keine offenen Positionen zum Abrechnen")
		return
	}

	var person struct{ First, Last, Address string }
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT first_name, last_name, COALESCE(address,'') FROM persons WHERE id=$1`, pid,
	).Scan(&person.First, &person.Last, &person.Address); err != nil {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}

	// §11 UStG mandatory-field check BEFORE burning a number.
	settings, err := h.loadBillingSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load billing settings")
		return
	}
	if msg := invoiceComplianceError(settings, trim(person.First+" "+person.Last)); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}

	var createdBy *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		createdBy = &u.ID
	}

	var out invoice
	txErr := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		s, err := loadBillingSettingsTx(r.Context(), tx)
		if err != nil {
			return err
		}
		number := s.InvoicePrefix + fmt.Sprintf("%0*d", s.NumberPad, s.NextInvoiceNo)
		if _, err := tx.Exec(r.Context(),
			`UPDATE billing_settings SET next_invoice_no = next_invoice_no + 1 WHERE id=1`); err != nil {
			return err
		}

		var subtotal float64
		for _, it := range items {
			subtotal += it.LineTotal
		}
		subtotal = round2(subtotal)
		rate := 0.0
		tax := 0.0
		if !s.Kleinunternehmer {
			rate = s.UStRate
			tax = round2(subtotal * rate / 100)
		}
		total := round2(subtotal + tax)

		seller := map[string]any{"name": s.SellerName, "address": s.SellerAddress, "uid": s.SellerUID,
			"iban": s.IBAN, "bic": s.BIC, "footer": s.FooterNote}
		buyer := map[string]any{"name": trim(person.First + " " + person.Last), "address": person.Address}
		sellerJSON, _ := json.Marshal(seller)
		buyerJSON, _ := json.Marshal(buyer)

		issued := time.Now()
		var due *time.Time
		if s.PaymentTermsDays > 0 {
			d := issued.AddDate(0, 0, s.PaymentTermsDays)
			due = &d
		}

		var invID int64
		if err := tx.QueryRow(r.Context(),
			`INSERT INTO invoices (number, person_id, issued_on, due_on, subtotal, ust_rate,
			        tax_amount, total, kleinunternehmer, seller_snapshot, buyer_snapshot, note, created_by)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
			number, pid, issued, due, subtotal, rate, tax, total, s.Kleinunternehmer,
			string(sellerJSON), string(buyerJSON), trim(req.Note), createdBy,
		).Scan(&invID); err != nil {
			return err
		}
		for i, it := range items {
			if _, err := tx.Exec(r.Context(),
				`INSERT INTO invoice_items (invoice_id, pos, description, quantity, unit_amount, line_total)
				 VALUES ($1,$2,$3,$4,$5,$6)`,
				invID, i+1, it.Label, it.Quantity, it.UnitAmount, it.LineTotal); err != nil {
				return err
			}
			// Lock discrete positions so they can't be invoiced again (released on Storno).
			if it.Kind == "vehicle" || it.Kind == "charge" {
				if _, err := tx.Exec(r.Context(),
					`INSERT INTO invoice_source (invoice_id, kind, ref_id) VALUES ($1,$2,$3)`,
					invID, it.Kind, it.ID); err != nil {
					return err
				}
			}
		}
		out = invoice{ID: invID, Number: number, PersonID: pid, IssuedOn: issued, DueOn: due,
			Subtotal: subtotal, UStRate: rate, TaxAmount: tax, Total: total,
			Kleinunternehmer: s.Kleinunternehmer, Seller: seller, Buyer: buyer, Note: trim(req.Note)}
		return nil
	})
	if txErr != nil {
		writeError(w, http.StatusInternalServerError, "could not create invoice")
		return
	}
	h.audit(r, "create", "invoice", out.ID, "issued invoice "+out.Number+" ("+fmt.Sprintf("%.2f €", out.Total)+")")
	writeJSON(w, http.StatusCreated, out)
}

// CancelInvoice issues a Storno (cancelling document) for an invoice: no delete
// (Unveränderbarkeit) — a new numbered document mirrors the original with negated
// amounts, points at it via cancels_id, marks the original canceled, and releases
// its locked positions so they can be re-invoiced.
func (h *Handler) CancelInvoice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var o struct {
		number                        string
		personID                      int64
		subtotal, ustRate, tax, total float64
		klein, canceled               bool
		sellerJSON, buyerJSON         []byte
		cancelsID                     *int64
	}
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT number, person_id, subtotal, ust_rate, tax_amount, total, kleinunternehmer,
		        seller_snapshot, buyer_snapshot, canceled, cancels_id FROM invoices WHERE id=$1`, id,
	).Scan(&o.number, &o.personID, &o.subtotal, &o.ustRate, &o.tax, &o.total, &o.klein,
		&o.sellerJSON, &o.buyerJSON, &o.canceled, &o.cancelsID); err != nil {
		writeError(w, http.StatusNotFound, "invoice not found")
		return
	}
	if o.canceled {
		writeError(w, http.StatusConflict, "Rechnung ist bereits storniert")
		return
	}
	if o.cancelsID != nil {
		writeError(w, http.StatusBadRequest, "ein Storno-Beleg kann nicht storniert werden")
		return
	}
	var createdBy *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		createdBy = &u.ID
	}

	// Read the original items up front (single-conn tx can't interleave query+exec).
	type row struct {
		pos                  int
		desc                 string
		qty, unit, lineTotal float64
	}
	var origItems []row
	irows, err := h.Pool.Query(r.Context(),
		`SELECT pos, description, quantity, unit_amount, line_total FROM invoice_items WHERE invoice_id=$1 ORDER BY pos`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	for irows.Next() {
		var it row
		if err := irows.Scan(&it.pos, &it.desc, &it.qty, &it.unit, &it.lineTotal); err != nil {
			irows.Close()
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		origItems = append(origItems, it)
	}
	irows.Close()
	if irows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	note := "Storno zu Rechnung " + o.number
	var out invoice
	txErr := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		s, err := loadBillingSettingsTx(r.Context(), tx)
		if err != nil {
			return err
		}
		number := s.InvoicePrefix + fmt.Sprintf("%0*d", s.NumberPad, s.NextInvoiceNo)
		if _, err := tx.Exec(r.Context(), `UPDATE billing_settings SET next_invoice_no = next_invoice_no + 1 WHERE id=1`); err != nil {
			return err
		}
		issued := time.Now()
		var stornoID int64
		if err := tx.QueryRow(r.Context(),
			`INSERT INTO invoices (number, person_id, issued_on, subtotal, ust_rate, tax_amount, total,
			        kleinunternehmer, seller_snapshot, buyer_snapshot, note, cancels_id, created_by)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
			number, o.personID, issued, -o.subtotal, o.ustRate, -o.tax, -o.total, o.klein,
			string(o.sellerJSON), string(o.buyerJSON), note, id, createdBy,
		).Scan(&stornoID); err != nil {
			return err
		}
		for _, it := range origItems {
			if _, err := tx.Exec(r.Context(),
				`INSERT INTO invoice_items (invoice_id, pos, description, quantity, unit_amount, line_total)
				 VALUES ($1,$2,$3,$4,$5,$6)`,
				stornoID, it.pos, it.desc, it.qty, -it.unit, -it.lineTotal); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(r.Context(), `UPDATE invoices SET canceled=true, paid_amount=0 WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM invoice_source WHERE invoice_id=$1`, id); err != nil {
			return err
		}
		// Release any payments tied to the original -> back to on-account (Guthaben).
		if _, err := tx.Exec(r.Context(), `DELETE FROM invoice_payments WHERE invoice_id=$1`, id); err != nil {
			return err
		}
		out = invoice{ID: stornoID, Number: number, PersonID: o.personID, IssuedOn: issued,
			Subtotal: -o.subtotal, UStRate: o.ustRate, TaxAmount: -o.tax, Total: -o.total,
			Kleinunternehmer: o.klein, Note: note, CancelsID: &id}
		return nil
	})
	if txErr != nil {
		writeError(w, http.StatusInternalServerError, "could not cancel invoice")
		return
	}
	h.audit(r, "update", "invoice", id, "storniert – Gegenbeleg "+out.Number)
	writeJSON(w, http.StatusOK, out)
}

type invoiceAlloc struct {
	InvoiceID int64   `json:"invoice_id"`
	Amount    float64 `json:"amount"`
}

type payInvoicesRequest struct {
	Amount      float64        `json:"amount"`
	PaidOn      string         `json:"paid_on"`
	Method      string         `json:"method"`
	Note        string         `json:"note"`
	Auto        bool           `json:"auto"`        // allocate oldest-first across open invoices
	Allocations []invoiceAlloc `json:"allocations"` // explicit per-invoice
}

// PayInvoices records a payment and allocates it to the person's open invoices —
// the whole thing in ONE transaction. Paying an invoice settles every position on
// it (rent, Pauschale, recurring, charges) at once. Any amount beyond the open
// invoices stays as the person's Guthaben (on-account). Guthaben itself is still
// payments − accrued (unchanged), so this never double-counts.
func (h *Handler) PayInvoices(w http.ResponseWriter, r *http.Request) {
	pid, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req payInvoicesRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	pr := paymentRequest{Amount: req.Amount, Method: req.Method, Note: req.Note, PaidOn: req.PaidOn}
	paidOn, badMsg, serr := h.validatePayment(r.Context(), pid, &pr)
	if serr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if badMsg != "" {
		writeError(w, http.StatusBadRequest, badMsg)
		return
	}
	explicit := map[int64]float64{}
	for _, a := range req.Allocations {
		explicit[a.InvoiceID] = a.Amount
	}

	var createdBy *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		createdBy = &u.ID
	}

	type result struct {
		PaymentID int64   `json:"payment_id"`
		Allocated float64 `json:"allocated"`
		Guthaben  float64 `json:"guthaben"`
		Invoices  int     `json:"invoices_settled"`
	}
	var out result
	txErr := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(),
			`INSERT INTO payments (person_id, amount, paid_on, method, note, created_by)
			 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			pid, pr.Amount, paidOn, pr.Method, pr.Note, createdBy).Scan(&out.PaymentID); err != nil {
			return err
		}
		// Lock the person's open invoices, oldest first.
		rows, err := tx.Query(r.Context(),
			`SELECT id, total, paid_amount FROM invoices
			  WHERE person_id=$1 AND NOT canceled AND cancels_id IS NULL AND (total - paid_amount) > 0.005
			  ORDER BY issued_on, id FOR UPDATE`, pid)
		if err != nil {
			return err
		}
		type inv struct {
			id          int64
			total, paid float64
		}
		var open []inv
		for rows.Next() {
			var iv inv
			if err := rows.Scan(&iv.id, &iv.total, &iv.paid); err != nil {
				rows.Close()
				return err
			}
			open = append(open, iv)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		openSet := map[int64]bool{}
		for _, iv := range open {
			openSet[iv.id] = true
		}
		for id := range explicit {
			if !openSet[id] {
				return errInvoiceNotOpen
			}
		}

		remaining := pr.Amount
		for _, iv := range open {
			if remaining < 0.005 {
				break
			}
			if len(explicit) > 0 {
				if _, ok := explicit[iv.id]; !ok {
					continue // explicit selection: skip unchosen invoices
				}
			}
			pay := round2(iv.total - iv.paid)
			if pay > remaining {
				pay = round2(remaining)
			}
			if amt, ok := explicit[iv.id]; ok && amt > 0 && amt < pay {
				pay = round2(amt)
			}
			if pay < 0.005 {
				continue
			}
			if _, err := tx.Exec(r.Context(),
				`INSERT INTO invoice_payments (invoice_id, payment_id, amount) VALUES ($1,$2,$3)`,
				iv.id, out.PaymentID, pay); err != nil {
				return err
			}
			if _, err := tx.Exec(r.Context(),
				`UPDATE invoices SET paid_amount = paid_amount + $1 WHERE id=$2`, pay, iv.id); err != nil {
				return err
			}
			remaining -= pay
			out.Invoices++
		}
		out.Allocated = round2(pr.Amount - remaining)
		out.Guthaben = round2(remaining)
		return nil
	})
	if txErr != nil {
		if txErr == errInvoiceNotOpen {
			writeError(w, http.StatusBadRequest, "eine gewählte Rechnung ist nicht offen (storniert/bezahlt/fremd)")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not record payment")
		return
	}
	h.audit(r, "create", "payment", out.PaymentID,
		fmt.Sprintf("Zahlung %.2f € auf %d Rechnung(en) (Guthaben %.2f €)", pr.Amount, out.Invoices, out.Guthaben))
	writeJSON(w, http.StatusCreated, out)
}

var errInvoiceNotOpen = fmt.Errorf("invoice not open")

// overdueInvoice is one row of the dunning ("Mahnwesen") view.
type overdueInvoice struct {
	ID          int64     `json:"id"`
	Number      string    `json:"number"`
	PersonID    int64     `json:"person_id"`
	PersonName  string    `json:"person_name"`
	DueOn       time.Time `json:"due_on"`
	Total       float64   `json:"total"`
	OpenAmount  float64   `json:"open_amount"`
	Status      string    `json:"status"`
	DaysOverdue int       `json:"days_overdue"`
}

// OverdueInvoices lists non-canceled, unpaid/partly-paid invoices whose due date
// has passed — the "who do I need to chase" list (Mahnwesen light).
func (h *Handler) OverdueInvoices(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT i.id, i.number, i.person_id, trim(p.first_name || ' ' || p.last_name),
		        i.due_on, i.total, i.paid_amount, (CURRENT_DATE - i.due_on) AS days
		   FROM invoices i JOIN persons p ON p.id = i.person_id
		  WHERE NOT i.canceled AND i.cancels_id IS NULL AND i.due_on IS NOT NULL
		    AND i.due_on < CURRENT_DATE AND (i.total - i.paid_amount) > 0.005
		  ORDER BY i.due_on`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []overdueInvoice{}
	for rows.Next() {
		var o overdueInvoice
		var paid float64
		if err := rows.Scan(&o.ID, &o.Number, &o.PersonID, &o.PersonName, &o.DueOn, &o.Total, &paid, &o.DaysOverdue); err != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		o.OpenAmount = round2(o.Total - paid)
		o.Status = invoiceStatus(o.Total, paid, false, nil)
		out = append(out, o)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func loadBillingSettingsTx(ctx context.Context, tx pgx.Tx) (billingSettings, error) {
	var s billingSettings
	err := tx.QueryRow(ctx,
		`SELECT seller_name, seller_address, seller_uid, kleinunternehmer, ust_rate,
		        invoice_prefix, next_invoice_no, number_pad, iban, bic, payment_terms_days, footer_note
		   FROM billing_settings WHERE id=1 FOR UPDATE`,
	).Scan(&s.SellerName, &s.SellerAddress, &s.SellerUID, &s.Kleinunternehmer, &s.UStRate,
		&s.InvoicePrefix, &s.NextInvoiceNo, &s.NumberPad, &s.IBAN, &s.BIC, &s.PaymentTermsDays, &s.FooterNote)
	return s, err
}

// ListInvoices returns a person's invoices, newest first (no line items).
func (h *Handler) ListInvoices(w http.ResponseWriter, r *http.Request) {
	pid, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, number, issued_on, due_on, subtotal, ust_rate, tax_amount, total, kleinunternehmer, canceled, cancels_id, paid_amount
		   FROM invoices WHERE person_id=$1 ORDER BY issued_on DESC, id DESC`, pid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []invoice{}
	for rows.Next() {
		var iv invoice
		iv.PersonID = pid
		if err := rows.Scan(&iv.ID, &iv.Number, &iv.IssuedOn, &iv.DueOn, &iv.Subtotal,
			&iv.UStRate, &iv.TaxAmount, &iv.Total, &iv.Kleinunternehmer, &iv.Canceled, &iv.CancelsID, &iv.PaidAmount); err != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		iv.OpenAmount = round2(iv.Total - iv.PaidAmount)
		iv.Status = invoiceStatus(iv.Total, iv.PaidAmount, iv.Canceled, iv.CancelsID)
		out = append(out, iv)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// GetInvoice returns one invoice with its line items and seller/buyer snapshots
// (for the printable view).
func (h *Handler) GetInvoice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var iv invoice
	var sellerJSON, buyerJSON []byte
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT id, number, person_id, issued_on, due_on, subtotal, ust_rate, tax_amount, total,
		        kleinunternehmer, seller_snapshot, buyer_snapshot, note, canceled, cancels_id, paid_amount
		   FROM invoices WHERE id=$1`, id,
	).Scan(&iv.ID, &iv.Number, &iv.PersonID, &iv.IssuedOn, &iv.DueOn, &iv.Subtotal, &iv.UStRate,
		&iv.TaxAmount, &iv.Total, &iv.Kleinunternehmer, &sellerJSON, &buyerJSON, &iv.Note,
		&iv.Canceled, &iv.CancelsID, &iv.PaidAmount); err != nil {
		writeError(w, http.StatusNotFound, "invoice not found")
		return
	}
	iv.OpenAmount = round2(iv.Total - iv.PaidAmount)
	iv.Status = invoiceStatus(iv.Total, iv.PaidAmount, iv.Canceled, iv.CancelsID)
	_ = json.Unmarshal(sellerJSON, &iv.Seller)
	_ = json.Unmarshal(buyerJSON, &iv.Buyer)

	rows, err := h.Pool.Query(r.Context(),
		`SELECT pos, description, quantity, unit_amount, line_total FROM invoice_items
		   WHERE invoice_id=$1 ORDER BY pos`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	iv.Items = []invoiceItem{}
	for rows.Next() {
		var it invoiceItem
		if err := rows.Scan(&it.Pos, &it.Description, &it.Quantity, &it.UnitAmount, &it.LineTotal); err != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		iv.Items = append(iv.Items, it)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, iv)
}
