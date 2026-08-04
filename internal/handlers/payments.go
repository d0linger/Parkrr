package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

// payment is one recorded money-in entry (see migration 023_payments.sql).
type payment struct {
	ID        int64     `json:"id"`
	PersonID  int64     `json:"person_id"`
	Amount    float64   `json:"amount"`
	PaidOn    time.Time `json:"paid_on"`
	Method    string    `json:"method"`
	Note      string    `json:"note"`
	VehicleID *int64    `json:"vehicle_id"`
	CreatedAt time.Time `json:"created_at"`
}

// paymentMethods is the closed set the UI offers; anything else is rejected so a
// typo can't create an unfilterable method.
var paymentMethods = map[string]bool{"bar": true, "ueberweisung": true, "paypal": true, "sonstiges": true}

const paymentColumns = `id, person_id, amount, paid_on, method, note, vehicle_id, created_at`

func scanPayment(row pgx.Row) (payment, error) {
	var p payment
	err := row.Scan(&p.ID, &p.PersonID, &p.Amount, &p.PaidOn, &p.Method, &p.Note, &p.VehicleID, &p.CreatedAt)
	return p, err
}

// ListPayments returns a person's recorded payments, newest first.
func (h *Handler) ListPayments(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT `+paymentColumns+` FROM payments WHERE person_id=$1 ORDER BY paid_on DESC, id DESC`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []payment{}
	for rows.Next() {
		p, serr := scanPayment(rows)
		if serr != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		out = append(out, p)
	}
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type allocRef struct {
	Kind string `json:"kind"` // "vehicle" | "charge"
	ID   int64  `json:"id"`
}

type paymentRequest struct {
	Amount float64 `json:"amount"`
	PaidOn string  `json:"paid_on"`
	Method string  `json:"method"`
	Note   string  `json:"note"`
	// Allocate (auto) marks open items paid oldest first up to Amount. Allocations
	// (explicit) settles exactly the chosen open items. Either way, each settled
	// item is linked to this payment (payment_allocations); the unallocated
	// remainder becomes the person's Guthaben.
	Allocate    bool       `json:"allocate"`
	Allocations []allocRef `json:"allocations"`
}

// owedItem is one open, individually-owed position (a standalone vehicle's rent
// or a standalone one-off charge). It carries both the settlement fields and the
// line fields an invoice needs.
type owedItem struct {
	Date       time.Time
	Kind       string // "vehicle" | "charge" | "agreement" | "recurring"
	ID         int64
	Period     string // sub-period key for periodic kinds; "" for discrete ones
	Label      string
	Quantity   float64
	UnitAmount float64
	LineTotal  float64
}

// lockKey is the identity of a fakturier-lockable position: kind + ref id +
// sub-period ("" for discrete vehicle/charge, "YYYY-MM"/"YYYY" for periodic).
func lockKey(kind string, refID int64, period string) string {
	return kind + ":" + strconv.FormatInt(refID, 10) + ":" + period
}

// periodKeySet turns a list of paid sub-period keys into a lookup set.
func periodKeySet(keys []string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// openOwedItems enumerates a person's open individually-owed positions, oldest
// first: uncovered active vehicles (rent) and standalone unpaid one-off charges.
// Pauschale-covered vehicles and per-period Pauschale/recurring costs are left to
// their own settlement.
func (h *Handler) openOwedItems(r *http.Request, personID int64) ([]owedItem, error) {
	ctx := r.Context()
	now := time.Now()

	ags, err := h.loadAgreements(ctx, personID, now)
	if err != nil {
		return nil, err
	}
	covered := map[int64]bool{}
	for i := range ags {
		for _, vid := range ags[i].VehicleIDs {
			covered[vid] = true
		}
	}

	var items []owedItem
	vehicles, _, err := h.loadVehiclesWithCategories(r, personID)
	if err != nil {
		return nil, err
	}
	for i := range vehicles {
		v := &vehicles[i]
		if v.Paid || v.Archived || covered[v.ID] || v.AccruedCost <= 0.005 {
			continue
		}
		label := strings.TrimSpace(v.Label)
		if label == "" {
			label = strings.TrimSpace(v.LicensePlate)
		}
		if label == "" {
			label = v.CategoryName
		}
		items = append(items, owedItem{
			Date: v.StartDate, Kind: "vehicle", ID: v.ID,
			Label:    "Einstellplatz: " + label + " (ab " + v.StartDate.Format("02.01.2006") + ")",
			Quantity: 1, UnitAmount: v.AccruedCost, LineTotal: v.AccruedCost,
		})
	}

	rows, err := h.Pool.Query(ctx,
		`SELECT id, description, quantity, amount, charged_on FROM charges
		   WHERE person_id=$1 AND vehicle_id IS NULL AND NOT paid`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var it owedItem
		var desc string
		var qty, unit float64
		if err := rows.Scan(&it.ID, &desc, &qty, &unit, &it.Date); err != nil {
			return nil, err
		}
		if qty <= 0 {
			qty = 1
		}
		it.Kind, it.Label, it.Quantity, it.UnitAmount, it.LineTotal = "charge", desc, qty, unit, round2(unit*qty)
		if it.LineTotal > 0.005 {
			items = append(items, it)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// A position already billed by an active invoice is settled via that invoice —
	// exclude it here so it can't be paid a second time (slider / allocation).
	locked, err := h.lockedPositions(ctx, personID)
	if err != nil {
		return nil, err
	}
	if len(locked) > 0 {
		kept := items[:0]
		for _, it := range items {
			if locked[lockKey(it.Kind, it.ID, "")] {
				continue
			}
			kept = append(kept, it)
		}
		items = kept
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Date.Before(items[j].Date) })
	return items, nil
}

// syncTogglePayment keeps a per-item "auto" payment in step with its paid toggle,
// so flipping the one-tap slider actually moves money (P2.3): setting an item paid
// records a payment for its open amount linked to it; setting it open removes that
// auto-payment. Manual payments (auto=false) are never touched. Idempotent.
func (h *Handler) syncTogglePayment(r *http.Request, kind string, refID, personID int64, paid bool) error {
	ctx := r.Context()
	if !paid {
		_, err := h.Pool.Exec(ctx,
			`DELETE FROM payments WHERE auto AND id IN (
			   SELECT payment_id FROM payment_allocations WHERE kind=$1 AND ref_id=$2)`, kind, refID)
		return err
	}
	var exists bool
	if err := h.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM payment_allocations a JOIN payments p ON p.id=a.payment_id
		   WHERE p.auto AND a.kind=$1 AND a.ref_id=$2)`, kind, refID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil // already covered
	}
	// Only standalone, still-open items are in openOwedItems; that's their owed amount.
	items, err := h.openOwedItems(r, personID)
	if err != nil {
		return err
	}
	var amt float64
	for _, it := range items {
		if it.Kind == kind && it.ID == refID {
			amt = it.LineTotal
		}
	}
	if amt < 0.005 {
		return nil // nothing open to pay (already covered / zero) — just the flag flips
	}
	var createdBy *int64
	if u, ok := auth.UserFrom(ctx); ok {
		createdBy = &u.ID
	}
	return pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
		var pid int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO payments (person_id, amount, method, note, auto, created_by)
			 VALUES ($1,$2,'bar','Slider „bezahlt"',true,$3) RETURNING id`, personID, amt, createdBy).Scan(&pid); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO payment_allocations (payment_id, kind, ref_id, amount) VALUES ($1,$2,$3,$4)`,
			pid, kind, refID, amt)
		return err
	})
}

// settleOne flips the existing paid toggle for one open item (reusing the same
// updates as the manual sliders, so there is no parallel paid state).
func (h *Handler) settleOne(r *http.Request, it owedItem) error {
	switch it.Kind {
	case "vehicle":
		if _, err := h.Pool.Exec(r.Context(), `UPDATE vehicles SET paid=true, updated_at=now() WHERE id=$1`, it.ID); err != nil {
			return err
		}
		h.autoArchiveIfClosed(r, it.ID)
	case "charge":
		if _, err := h.Pool.Exec(r.Context(), `UPDATE charges SET paid=true WHERE id=$1 AND vehicle_id IS NULL`, it.ID); err != nil {
			return err
		}
	}
	return nil
}

// settlePayment applies a freshly-recorded payment to the person's open items:
// explicit selection (req.Allocations) or auto oldest-first (req.Allocate), whole
// items only. Each settled item is linked to the payment (payment_allocations)
// and its existing paid toggle is flipped; the leftover amount stays as the
// payment's unallocated remainder (Guthaben). Returns the number settled.
func (h *Handler) settlePayment(r *http.Request, paymentID, personID int64, req *paymentRequest) (int, error) {
	items, err := h.openOwedItems(r, personID)
	if err != nil {
		return 0, err
	}
	// Choose targets in the person's oldest-first order.
	var targets []owedItem
	strict := false
	if len(req.Allocations) > 0 {
		want := map[string]bool{}
		for _, a := range req.Allocations {
			want[a.Kind+":"+strconv.FormatInt(a.ID, 10)] = true
		}
		for _, it := range items {
			if want[it.Kind+":"+strconv.FormatInt(it.ID, 10)] {
				targets = append(targets, it)
			}
		}
	} else if req.Allocate {
		targets = items
		strict = true // auto: stop at the first item the amount can't cover
	}

	remaining := req.Amount
	settled := 0
	for _, it := range targets {
		if remaining+0.005 < it.LineTotal {
			if strict {
				break
			}
			continue // explicit: skip an unaffordable pick, still settle the rest
		}
		if err := h.settleOne(r, it); err != nil {
			return settled, err
		}
		if _, err := h.Pool.Exec(r.Context(),
			`INSERT INTO payment_allocations (payment_id, kind, ref_id, amount) VALUES ($1,$2,$3,$4)`,
			paymentID, it.Kind, it.ID, it.LineTotal); err != nil {
			return settled, err
		}
		remaining -= it.LineTotal
		settled++
	}
	return settled, nil
}

// validatePayment normalizes and checks a payment request, returning the parsed
// date. A non-empty badMsg is a 400 reason; a non-nil error is a 500. A bound
// vehicle (optional context) must belong to the same person.
func (h *Handler) validatePayment(ctx context.Context, personID int64, req *paymentRequest) (time.Time, string, error) {
	req.Method = trim(req.Method)
	req.Note = trim(req.Note)
	if req.Method == "" {
		req.Method = "bar"
	}
	if !paymentMethods[req.Method] {
		return time.Time{}, "unknown payment method", nil
	}
	if req.Amount <= 0 {
		return time.Time{}, "amount must be greater than 0", nil
	}
	if !validNameLength(req.Note) {
		return time.Time{}, "note is too long", nil
	}
	paidOn := time.Now()
	if trim(req.PaidOn) != "" {
		t, perr := time.Parse(dateLayout, trim(req.PaidOn))
		if perr != nil {
			return time.Time{}, "paid_on must be YYYY-MM-DD", nil
		}
		paidOn = t
	}
	return paidOn, "", nil
}

// CreatePayment records a payment for a person (editor role).
func (h *Handler) CreatePayment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req paymentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	paidOn, badMsg, serr := h.validatePayment(r.Context(), id, &req)
	if serr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if badMsg != "" {
		writeError(w, http.StatusBadRequest, badMsg)
		return
	}
	var createdBy *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		createdBy = &u.ID
	}
	p, err := scanPayment(h.Pool.QueryRow(r.Context(),
		`INSERT INTO payments (person_id, amount, paid_on, method, note, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+paymentColumns,
		id, req.Amount, paidOn, req.Method, req.Note, createdBy))
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "person does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not record payment")
		return
	}
	h.audit(r, "create", "payment", p.ID, fmt.Sprintf("recorded payment %.2f € (%s)", p.Amount, p.Method))

	settled := 0
	if req.Allocate || len(req.Allocations) > 0 {
		if n, aerr := h.settlePayment(r, p.ID, id, &req); aerr != nil {
			slog.Warn("payment allocation failed", "payment_id", p.ID, "err", aerr)
		} else if n > 0 {
			settled = n
			h.audit(r, "update", "payment", p.ID, fmt.Sprintf("stamped %d position(s) as paid", n))
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": p.ID, "person_id": p.PersonID, "amount": p.Amount, "paid_on": p.PaidOn,
		"method": p.Method, "note": p.Note, "vehicle_id": p.VehicleID, "created_at": p.CreatedAt,
		"settled": settled,
	})
}

// DeletePayment removes a recorded payment (editor role) and reverts the items
// it stamped: for each of its allocations, if no OTHER payment still covers that
// item, its paid toggle is cleared. Manually-toggled items (no allocation) are
// never touched. The allocations themselves cascade with the payment.
func (h *Handler) DeletePayment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ctx := r.Context()
	type ref struct {
		kind string
		id   int64
	}
	type invPay struct {
		invoiceID int64
		amount    float64
	}
	deleted := false
	txErr := pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
		// Position toggles this payment stamped, and invoice allocations it funded.
		var refs []ref
		prows, err := tx.Query(ctx, `SELECT kind, ref_id FROM payment_allocations WHERE payment_id=$1`, id)
		if err != nil {
			return err
		}
		for prows.Next() {
			var rf ref
			if err := prows.Scan(&rf.kind, &rf.id); err != nil {
				prows.Close()
				return err
			}
			refs = append(refs, rf)
		}
		prows.Close()
		if err := prows.Err(); err != nil {
			return err
		}
		var invPays []invPay
		irows, err := tx.Query(ctx, `SELECT invoice_id, amount FROM invoice_payments WHERE payment_id=$1`, id)
		if err != nil {
			return err
		}
		for irows.Next() {
			var ip invPay
			if err := irows.Scan(&ip.invoiceID, &ip.amount); err != nil {
				irows.Close()
				return err
			}
			invPays = append(invPays, ip)
		}
		irows.Close()
		if err := irows.Err(); err != nil {
			return err
		}

		ct, err := tx.Exec(ctx, `DELETE FROM payments WHERE id=$1`, id) // cascades allocations + invoice_payments
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return errPaymentNotFound
		}
		deleted = true
		// Un-stamp positions no longer covered by any payment.
		for _, rf := range refs {
			var others int
			if err := tx.QueryRow(ctx,
				`SELECT count(*) FROM payment_allocations WHERE kind=$1 AND ref_id=$2`, rf.kind, rf.id).Scan(&others); err != nil {
				return err
			}
			if others > 0 {
				continue
			}
			switch rf.kind {
			case "vehicle":
				if _, err := tx.Exec(ctx, `UPDATE vehicles SET paid=false, updated_at=now() WHERE id=$1`, rf.id); err != nil {
					return err
				}
			case "charge":
				if _, err := tx.Exec(ctx, `UPDATE charges SET paid=false WHERE id=$1 AND vehicle_id IS NULL`, rf.id); err != nil {
					return err
				}
			}
		}
		// Give back the amount to each invoice this payment covered.
		for _, ip := range invPays {
			if _, err := tx.Exec(ctx,
				`UPDATE invoices SET paid_amount = GREATEST(0, paid_amount - $1) WHERE id=$2`, ip.amount, ip.invoiceID); err != nil {
				return err
			}
		}
		return nil
	})
	if txErr == errPaymentNotFound || (txErr == nil && !deleted) {
		writeError(w, http.StatusNotFound, "payment not found")
		return
	}
	if txErr != nil {
		writeError(w, http.StatusInternalServerError, "could not delete payment")
		return
	}
	h.audit(r, "delete", "payment", id, "deleted payment (reverted stamps + invoice allocations)")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

var errPaymentNotFound = fmt.Errorf("payment not found")

// OpenItems returns a person's open individually-owed positions (for the payment
// dialog's selection list).
func (h *Handler) OpenItems(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	items, err := h.openOwedItems(r, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	type openItem struct {
		Kind  string  `json:"kind"`
		ID    int64   `json:"id"`
		Label string  `json:"label"`
		Owed  float64 `json:"owed"`
	}
	out := make([]openItem, 0, len(items))
	for _, it := range items {
		out = append(out, openItem{Kind: it.Kind, ID: it.ID, Label: it.Label, Owed: it.LineTotal})
	}
	writeJSON(w, http.StatusOK, out)
}

// personCredit returns a person's Guthaben: the true overpayment, money received
// beyond everything owed (rent + all charges + recurring). Not the unallocated
// remainder — costs that settle without an allocation (vehicle-bound charges,
// Pauschale/recurring periods) must not look like credit.
func (h *Handler) personCredit(r *http.Request, personID int64) float64 {
	var payments float64
	_ = h.Pool.QueryRow(r.Context(),
		`SELECT COALESCE(SUM(amount),0) FROM payments WHERE person_id=$1`, personID).Scan(&payments)
	accrued, err := h.personAccruedTotal(r, personID)
	if err != nil {
		return 0
	}
	if c := round2(payments - accrued); c > 0 {
		return c
	}
	return 0
}

// ApplyCredit stamps a person's currently-open items (oldest first) that their
// payments already cover but which weren't stamped yet — i.e. draws down the
// Guthaben. The budget is the paid amount minus the cost already covered
// (Aufgelaufen − offene Posten), so items are stamped only up to what was really
// paid. Each stamped item is linked to the person's latest payment for revert.
func (h *Handler) ApplyCredit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ctx := r.Context()
	items, err := h.openOwedItems(r, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if len(items) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"settled": 0})
		return
	}
	accrued, err := h.personAccruedTotal(r, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	var openTotal, paymentsTotal float64
	for _, it := range items {
		openTotal += it.LineTotal
	}
	_ = h.Pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM payments WHERE person_id=$1`, id).Scan(&paymentsTotal)
	var latestPayment int64
	_ = h.Pool.QueryRow(ctx, `SELECT id FROM payments WHERE person_id=$1 ORDER BY paid_on DESC, id DESC LIMIT 1`, id).Scan(&latestPayment)

	// budget = money paid that isn't already tied up in non-open (covered) costs.
	budget := round2(paymentsTotal - round2(accrued-openTotal))
	settled := 0
	for _, it := range items {
		if budget+0.005 < it.LineTotal {
			break // not enough paid to cover this (oldest) open item
		}
		if err := h.settleOne(r, it); err != nil {
			writeError(w, http.StatusInternalServerError, "could not apply credit")
			return
		}
		if latestPayment > 0 {
			if _, err := h.Pool.Exec(ctx,
				`INSERT INTO payment_allocations (payment_id, kind, ref_id, amount) VALUES ($1,$2,$3,$4)`,
				latestPayment, it.Kind, it.ID, it.LineTotal); err != nil {
				writeError(w, http.StatusInternalServerError, "could not apply credit")
				return
			}
		}
		budget -= it.LineTotal
		settled++
	}
	if settled > 0 {
		h.audit(r, "update", "payment", 0, fmt.Sprintf("applied Guthaben to %d open position(s)", settled))
	}
	writeJSON(w, http.StatusOK, map[string]any{"settled": settled})
}
