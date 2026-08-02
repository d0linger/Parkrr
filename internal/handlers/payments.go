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
	Amount    float64 `json:"amount"`
	PaidOn    string  `json:"paid_on"`
	Method    string  `json:"method"`
	Note      string  `json:"note"`
	VehicleID *int64  `json:"vehicle_id"`
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
	Kind       string // "vehicle" | "charge"
	ID         int64
	Label      string
	Quantity   float64
	UnitAmount float64
	LineTotal  float64
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
	sort.Slice(items, func(i, j int) bool { return items[i].Date.Before(items[j].Date) })
	return items, nil
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
	if req.VehicleID != nil {
		var owner int64
		err := h.Pool.QueryRow(ctx, `SELECT person_id FROM vehicles WHERE id=$1`, *req.VehicleID).Scan(&owner)
		if err == pgx.ErrNoRows || (err == nil && owner != personID) {
			return time.Time{}, "vehicle does not belong to that person", nil
		}
		if err != nil {
			return time.Time{}, "", err
		}
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
		`INSERT INTO payments (person_id, amount, paid_on, method, note, vehicle_id, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+paymentColumns,
		id, req.Amount, paidOn, req.Method, req.Note, req.VehicleID, createdBy))
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "person or vehicle does not exist")
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
	// Revert the items this payment stamped, before the allocations cascade away.
	rows, err := h.Pool.Query(ctx, `SELECT kind, ref_id FROM payment_allocations WHERE payment_id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete payment")
		return
	}
	type ref struct {
		kind string
		id   int64
	}
	var refs []ref
	for rows.Next() {
		var rf ref
		if err := rows.Scan(&rf.kind, &rf.id); err != nil {
			rows.Close()
			writeError(w, http.StatusInternalServerError, "could not delete payment")
			return
		}
		refs = append(refs, rf)
	}
	rows.Close()
	if rows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "could not delete payment")
		return
	}

	ct, err := h.Pool.Exec(ctx, `DELETE FROM payments WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete payment")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "payment not found")
		return
	}
	for _, rf := range refs {
		var others int
		if err := h.Pool.QueryRow(ctx,
			`SELECT count(*) FROM payment_allocations WHERE kind=$1 AND ref_id=$2`, rf.kind, rf.id).Scan(&others); err != nil {
			continue
		}
		if others > 0 {
			continue // another payment still covers it
		}
		switch rf.kind {
		case "vehicle":
			_, _ = h.Pool.Exec(ctx, `UPDATE vehicles SET paid=false, updated_at=now() WHERE id=$1`, rf.id)
		case "charge":
			_, _ = h.Pool.Exec(ctx, `UPDATE charges SET paid=false WHERE id=$1 AND vehicle_id IS NULL`, rf.id)
		}
	}
	h.audit(r, "delete", "payment", id, "deleted payment (reverted its stamped positions)")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

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

// personCredit returns a person's Guthaben: total payments minus what has been
// allocated to items.
func (h *Handler) personCredit(ctx context.Context, personID int64) float64 {
	var credit float64
	_ = h.Pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(p.amount),0)
		      - COALESCE((SELECT SUM(a.amount) FROM payment_allocations a
		                    JOIN payments p2 ON p2.id = a.payment_id
		                   WHERE p2.person_id = $1),0)
		   FROM payments p WHERE p.person_id = $1`, personID).Scan(&credit)
	return round2(credit)
}

// ApplyCredit draws a person's Guthaben (unallocated payment remainders) against
// their currently-open items, oldest first — stamping each and linking it to the
// payment(s) whose remainder covers it. Returns how many items were settled.
func (h *Handler) ApplyCredit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ctx := r.Context()
	// Payments with unallocated remainder, oldest first.
	prows, err := h.Pool.Query(ctx,
		`SELECT p.id, p.amount - COALESCE(SUM(a.amount),0) AS rem
		   FROM payments p LEFT JOIN payment_allocations a ON a.payment_id = p.id
		  WHERE p.person_id = $1
		  GROUP BY p.id, p.amount, p.paid_on
		 HAVING p.amount - COALESCE(SUM(a.amount),0) > 0.005
		  ORDER BY p.paid_on, p.id`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	type rem struct {
		id  int64
		rem float64
	}
	var rems []rem
	for prows.Next() {
		var rm rem
		if err := prows.Scan(&rm.id, &rm.rem); err != nil {
			prows.Close()
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		rems = append(rems, rm)
	}
	prows.Close()

	items, err := h.openOwedItems(r, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	settled := 0
	for _, it := range items {
		var avail float64
		for _, rm := range rems {
			avail += rm.rem
		}
		if avail+0.005 < it.LineTotal {
			break // not enough credit left to cover this (oldest) item
		}
		if err := h.settleOne(r, it); err != nil {
			writeError(w, http.StatusInternalServerError, "could not apply credit")
			return
		}
		need := it.LineTotal
		for i := range rems {
			if need <= 0.005 {
				break
			}
			if rems[i].rem <= 0.005 {
				continue
			}
			take := rems[i].rem
			if take > need {
				take = need
			}
			if _, err := h.Pool.Exec(ctx,
				`INSERT INTO payment_allocations (payment_id, kind, ref_id, amount) VALUES ($1,$2,$3,$4)`,
				rems[i].id, it.Kind, it.ID, round2(take)); err != nil {
				writeError(w, http.StatusInternalServerError, "could not apply credit")
				return
			}
			rems[i].rem -= take
			need -= take
		}
		settled++
	}
	if settled > 0 {
		h.audit(r, "update", "payment", 0, fmt.Sprintf("applied Guthaben to %d open position(s)", settled))
	}
	writeJSON(w, http.StatusOK, map[string]any{"settled": settled})
}
