package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

// maxMoneyAmount is the upper bound for a single money value. NUMERIC(12,2) holds
// up to 9,999,999,999.99; this leaves headroom and rejects overflow/garbage input
// with a clean 400 instead of a NUMERIC-overflow 500.
const maxMoneyAmount = 1e9

// maxQuantity bounds a charge's quantity, which is stored as NUMERIC(10,2)
// (max ~99,999,999.99). A generous ceiling well under the column keeps a huge
// quantity from overflowing to a 500.
const maxQuantity = 1e6

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

	var items []owedItem
	vehicles, _, err := h.loadVehiclesWithCategories(r, personID)
	if err != nil {
		return nil, err
	}
	for i := range vehicles {
		v := &vehicles[i]
		// coveringAgreements honors person-wide agreements (empty VehicleIDs = all)
		// and the start-date guard — a raw VehicleIDs scan would miss both and list
		// covered rent as individually owed.
		if v.Paid || v.Archived || v.AccruedCost <= 0.005 || len(coveringAgreements(ags, v.ID, v.StartDate)) > 0 {
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
	// Drop any position an active invoice already billed so it can't be settled a
	// second time via the slider / an allocation. A position is locked at the
	// (kind, ref) level regardless of period: a wholesale charge or ANY invoiced
	// vehicle period takes the item off the manual money path — settle via the
	// invoice instead.
	if len(locked) > 0 {
		lockedRef := map[string]bool{}
		for k := range locked {
			if idx := strings.LastIndex(k, ":"); idx >= 0 {
				lockedRef[k[:idx]] = true
			}
		}
		kept := items[:0]
		for _, it := range items {
			if lockedRef[it.Kind+":"+strconv.FormatInt(it.ID, 10)] {
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
		// Serialize concurrent toggles of the SAME item: a transaction-scoped
		// advisory lock makes the exists-check below reliable, so racing toggles
		// (double-tap, retry, two tabs) mint exactly one auto-payment, not N.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`,
			kind+":"+strconv.FormatInt(refID, 10)); err != nil {
			return err
		}
		// Any allocation (auto OR manual) already settles this item — don't mint a
		// second one. Checking only p.auto would let a manually-settled item that was
		// toggled open and paid again hit the (kind,ref_id) unique index → 500.
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM payment_allocations WHERE kind=$1 AND ref_id=$2)`,
			kind, refID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil // already covered (won the race elsewhere)
		}
		var pid int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO payments (person_id, amount, method, note, auto, created_by)
			 VALUES ($1,$2,'bar','Slider „bezahlt"',true,$3) RETURNING id`, personID, amt, createdBy).Scan(&pid); err != nil {
			return err
		}
		// ON CONFLICT guards the toggle-vs-manual-payment race the advisory lock
		// doesn't cover (CreatePayment takes no such lock).
		_, err := tx.Exec(ctx,
			`INSERT INTO payment_allocations (payment_id, kind, ref_id, amount) VALUES ($1,$2,$3,$4)
			 ON CONFLICT (kind, ref_id) DO NOTHING`,
			pid, kind, refID, amt)
		return err
	})
}

// settleItemTx settles ONE open position inside a transaction, atomically and
// idempotently: it CLAIMS the position via a payment_allocations row (unique on
// (kind, ref_id)) first, and only flips the paid toggle if the claim won. A lost
// race — the position was already allocated by another payment — is a clean no-op,
// never an orphaned paid flag with no allocation. Returns whether it settled and,
// for a settled vehicle, its id so the caller can auto-archive after commit.
func settleItemTx(ctx context.Context, tx pgx.Tx, paymentID int64, it owedItem) (settled bool, archiveVehicle int64, err error) {
	ct, err := tx.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, kind, ref_id, amount)
		 VALUES ($1,$2,$3,$4) ON CONFLICT (kind, ref_id) DO NOTHING`,
		paymentID, it.Kind, it.ID, it.LineTotal)
	if err != nil {
		return false, 0, err
	}
	if ct.RowsAffected() == 0 {
		return false, 0, nil // already settled by another payment — skip, no orphan
	}
	switch it.Kind {
	case "vehicle":
		if _, err := tx.Exec(ctx, `UPDATE vehicles SET paid=true, updated_at=now() WHERE id=$1`, it.ID); err != nil {
			return false, 0, err
		}
		return true, it.ID, nil
	case "charge":
		if _, err := tx.Exec(ctx, `UPDATE charges SET paid=true WHERE id=$1 AND vehicle_id IS NULL`, it.ID); err != nil {
			return false, 0, err
		}
	}
	return true, 0, nil
}

// settlePaymentTx applies a freshly-recorded payment to the person's open items —
// explicit selection (req.Allocations) or auto oldest-first (req.Allocate), whole
// items only — inside the caller's transaction. `items` must be read BEFORE the tx
// (openOwedItems uses the pool; reading it here would grab a second connection
// while holding the tx's, deadlocking the pool under concurrency). The unallocated
// remainder stays as the payment's Guthaben. Returns the count settled and the
// vehicle ids to auto-archive after the tx commits.
func (h *Handler) settlePaymentTx(ctx context.Context, tx pgx.Tx, paymentID int64, req *paymentRequest, items []owedItem) (int, []int64, error) {
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
	var archive []int64
	for _, it := range targets {
		if remaining+0.005 < it.LineTotal {
			if strict {
				break
			}
			continue // explicit: skip an unaffordable pick, still settle the rest
		}
		ok, veh, serr := settleItemTx(ctx, tx, paymentID, it)
		if serr != nil {
			return settled, archive, serr
		}
		if !ok {
			continue // lost the race for this item; leave the amount for the rest
		}
		if veh > 0 {
			archive = append(archive, veh)
		}
		remaining -= it.LineTotal
		settled++
	}
	return settled, archive, nil
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
	// Reject out-of-range amounts before they hit NUMERIC(12,2) and 500 with an
	// overflow error. maxMoneyAmount fits the column with headroom.
	if req.Amount > maxMoneyAmount {
		return time.Time{}, "amount exceeds the allowed maximum", nil
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
	// Read the open items BEFORE the tx (openOwedItems uses the pool; reading it
	// inside the tx would hold two connections and deadlock the pool under load).
	ctx := r.Context()
	var openItems []owedItem
	if req.Allocate || len(req.Allocations) > 0 {
		its, oerr := h.openOwedItems(r, id)
		if oerr != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		openItems = its
	}
	// Record the payment and settle its items in ONE transaction so a settlement
	// failure can never leave a half-allocated payment or a paid-but-unallocated
	// orphan (the whole thing commits or rolls back together).
	var p payment
	var settled int
	var archive []int64
	txErr := pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
		pp, err := scanPayment(tx.QueryRow(ctx,
			`INSERT INTO payments (person_id, amount, paid_on, method, note, created_by)
			 VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+paymentColumns,
			id, req.Amount, paidOn, req.Method, req.Note, createdBy))
		if err != nil {
			return err
		}
		p = pp
		if len(openItems) > 0 {
			n, arch, serr := h.settlePaymentTx(ctx, tx, p.ID, &req, openItems)
			if serr != nil {
				return serr
			}
			settled, archive = n, arch
		}
		return nil
	})
	if txErr != nil {
		if isForeignKeyViolation(txErr) {
			writeError(w, http.StatusBadRequest, "person does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not record payment")
		return
	}
	h.audit(r, "create", "payment", p.ID, fmt.Sprintf("recorded payment %.2f € (%s)", p.Amount, p.Method))
	if settled > 0 {
		h.audit(r, "update", "payment", p.ID, fmt.Sprintf("stamped %d position(s) as paid", settled))
	}
	// Archive now-closed vehicles after the money is durably committed.
	for _, vid := range archive {
		h.autoArchiveIfClosed(r, vid)
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
	// No payment to link the drawdown to → nothing to apply (and no orphan risk).
	if latestPayment == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"settled": 0})
		return
	}
	budget := round2(paymentsTotal - round2(accrued-openTotal))
	settled := 0
	var archive []int64
	txErr := pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
		for _, it := range items {
			if budget+0.005 < it.LineTotal {
				break // not enough paid to cover this (oldest) open item
			}
			ok, veh, serr := settleItemTx(ctx, tx, latestPayment, it)
			if serr != nil {
				return serr
			}
			if !ok {
				continue // already settled by another payment (race)
			}
			if veh > 0 {
				archive = append(archive, veh)
			}
			budget -= it.LineTotal
			settled++
		}
		return nil
	})
	if txErr != nil {
		writeError(w, http.StatusInternalServerError, "could not apply credit")
		return
	}
	if settled > 0 {
		h.audit(r, "update", "payment", 0, fmt.Sprintf("applied Guthaben to %d open position(s)", settled))
	}
	for _, vid := range archive {
		h.autoArchiveIfClosed(r, vid)
	}
	writeJSON(w, http.StatusOK, map[string]any{"settled": settled})
}
