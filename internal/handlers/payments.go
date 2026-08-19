package handlers

import (
	"context"
	"errors"
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
	Reversed  bool      `json:"reversed"` // storniert: kept for audit, excluded from all money sums
	// Items are the resolved positions this payment settles (Gefährt/Pauschale/
	// Zusatzkosten + Zeitraum + Betrag), filled by ListPayments so the overview can
	// show what a payment covers — even across several Gefährte or Pauschalen.
	Items []attrItem `json:"items,omitempty"`
}

// paymentMethods is the closed set the UI offers; anything else is rejected so a
// typo can't create an unfilterable method.
var paymentMethods = map[string]bool{"bar": true, "ueberweisung": true, "paypal": true, "sonstiges": true}

const paymentColumns = `id, person_id, amount, paid_on, method, note, vehicle_id, created_at, reversed`

func scanPayment(row pgx.Row) (payment, error) {
	var p payment
	err := row.Scan(&p.ID, &p.PersonID, &p.Amount, &p.PaidOn, &p.Method, &p.Note, &p.VehicleID, &p.CreatedAt, &p.Reversed)
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
	// Attach the resolved positions (Gefährt/Pauschale/Zeitraum) each payment settles.
	items, ierr := h.resolvePaymentItems(r.Context(), id)
	if ierr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	for i := range out {
		out[i].Items = items[out[i].ID]
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

// periodPaidAuditState renders a per-period settlement change for the audit log —
// the state plus the fixed-partial amount (money received) when one was recorded,
// so a recorded Teilbetrag is reconstructable from the trail.
func periodPaidAuditState(paid bool, amount *float64) string {
	if !paid {
		return "offen"
	}
	if amount != nil {
		return fmt.Sprintf("Teilbetrag %.2f €", *amount)
	}
	return "bezahlt"
}

// periodKeySet turns a list of paid sub-period keys into a lookup set.
func periodKeySet(keys []string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// createdByFrom returns the acting user's id (for payments.created_by) or nil when
// the request has no authenticated actor (system paths). Kept here so the agreement
// and recurring handlers can stamp their period auto-payments without importing auth.
func createdByFrom(ctx context.Context) *int64 {
	if u, ok := auth.UserFrom(ctx); ok {
		return &u.ID
	}
	return nil
}

// periodPaymentNote labels a per-period settlement auto-payment for the payments
// list / audit trail, e.g. "Pauschale 2026-05 (Slider)".
func periodPaymentNote(kind, period string) string {
	label := "Periode"
	switch kind {
	case "agreement":
		label = "Pauschale"
	case "recurring":
		label = "Nebenkosten"
	}
	return label + " " + period + " (Slider)"
}

// recordPeriodPaymentTx books (or refreshes) the real Zahlungseingang that settles
// ONE Pauschale/Nebenkosten sub-period. Per-period settlements historically only
// flipped an off-book flag; this makes the received money a first-class payment so
// it shows in the payments list and audit like every other. The row is auto=true
// (system-managed toggle state, deletable on un-toggle) and linked to
// (settles_kind, settles_ref, settles_period). The payments immutability trigger
// blocks UPDATEing an auto row's amount, so a re-mark (partial↔whole) is
// delete-then-insert, never an upsert. A zero amount records no row — the settled
// flag alone marks a 0-cost period paid.
func recordPeriodPaymentTx(ctx context.Context, tx pgx.Tx, personID int64, kind string, refID int64, period string, amount float64, createdBy *int64) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM payments WHERE auto AND settles_kind=$1 AND settles_ref=$2 AND settles_period=$3`,
		kind, refID, period); err != nil {
		return err
	}
	if amount < 0.005 {
		return nil
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO payments (person_id, amount, method, note, auto, settles_kind, settles_ref, settles_period, created_by)
		 VALUES ($1,$2,'bar',$3,true,$4,$5,$6,$7)`,
		personID, amount, periodPaymentNote(kind, period), kind, refID, period, createdBy)
	return err
}

// deletePeriodPaymentTx removes the real Zahlungseingang for a sub-period toggled
// back open (auto rows are removable wholesale, migration 035).
func deletePeriodPaymentTx(ctx context.Context, tx pgx.Tx, kind string, refID int64, period string) error {
	_, err := tx.Exec(ctx,
		`DELETE FROM payments WHERE auto AND settles_kind=$1 AND settles_ref=$2 AND settles_period=$3`,
		kind, refID, period)
	return err
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

// boundCharge is one open Zusatzkosten charge bound to a vehicle: a vehicle's
// slider also settles the charges bound to it (they follow the Gefährt via
// chargeSettled), so their open amount folds into the same auto-payment — the
// money matches the "bezahlt · Gefährt" display and the balance nets to 0
// (otherwise the bound charge stays owed while showing paid).
type boundCharge struct {
	id    int64
	total float64
}

// toggleOwed computes what a paid-toggle's auto-payment must cover: the item's
// own open amount plus (vehicle only) its bound open charges. Only standalone,
// still-open items are in openOwedItems; that's their owed amount. Reads via the
// pool — call BEFORE opening the write tx: openOwedItems uses the pool, and
// reading it while holding the tx's connection can deadlock the pool under
// concurrency (see settlePaymentTx).
func (h *Handler) toggleOwed(r *http.Request, kind string, refID, personID int64) (float64, []boundCharge, error) {
	ctx := r.Context()
	items, err := h.openOwedItems(r, personID)
	if err != nil {
		return 0, nil, err
	}
	var amt float64
	for _, it := range items {
		if it.Kind == kind && it.ID == refID {
			amt = it.LineTotal
		}
	}
	var bound []boundCharge
	if kind == "vehicle" {
		rows, qerr := h.Pool.Query(ctx,
			`SELECT id, amount, quantity FROM charges WHERE vehicle_id=$1 AND NOT paid`, refID)
		if qerr != nil {
			return 0, nil, qerr
		}
		for rows.Next() {
			var id int64
			var a, q float64
			if err := rows.Scan(&id, &a, &q); err != nil {
				rows.Close()
				return 0, nil, err
			}
			if q <= 0 {
				q = 1
			}
			if t := round2(a * q); t > 0.005 {
				bound = append(bound, boundCharge{id, t})
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, nil, err
		}
	}
	return amt, bound, nil
}

// syncTogglePaymentTx keeps a per-item "auto" payment in step with its paid
// toggle, so flipping the one-tap slider actually moves money (P2.3): setting an
// item paid records a payment for its open amount linked to it; setting it open
// removes that auto-payment. Manual payments (auto=false) are never touched.
// Idempotent.
//
// Runs inside the caller's transaction together with the item's paid-flag
// UPDATE, so a failure can never leave a phantom payment/credit: both commit or
// neither does. amt/bound come from toggleOwed, read before the tx while the
// item is still open.
func (h *Handler) syncTogglePaymentTx(ctx context.Context, tx pgx.Tx, kind string, refID, personID int64, paid bool, amt float64, bound []boundCharge) error {
	if !paid {
		// A vehicle's auto-payment also settled the charges bound to it — reopen
		// them (their paid flag was set by this payment) before removing it.
		if kind == "vehicle" {
			if _, err := tx.Exec(ctx,
				`UPDATE charges SET paid=false WHERE vehicle_id=$1 AND id IN (
				   SELECT ca.ref_id FROM payment_allocations ca
				    WHERE ca.kind='charge' AND ca.payment_id IN (
				      SELECT payment_id FROM payment_allocations WHERE kind='vehicle' AND ref_id=$1))`, refID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx,
			`DELETE FROM payments WHERE auto AND id IN (
			   SELECT payment_id FROM payment_allocations WHERE kind=$1 AND ref_id=$2)`, kind, refID)
		return err
	}
	var boundTotal float64
	for _, b := range bound {
		boundTotal += b.total
	}
	if amt+boundTotal < 0.005 {
		return nil // nothing open to pay (already covered / zero) — just the flag flips
	}
	var createdBy *int64
	if u, ok := auth.UserFrom(ctx); ok {
		createdBy = &u.ID
	}
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
		 VALUES ($1,$2,'bar','Slider „bezahlt"',true,$3) RETURNING id`, personID, amt+boundTotal, createdBy).Scan(&pid); err != nil {
		return err
	}
	// ON CONFLICT guards the toggle-vs-manual-payment race the advisory lock
	// doesn't cover (CreatePayment takes no such lock). The primary allocation is
	// the "settled" marker even when amt is 0 (a 0-rent vehicle with bound charges).
	if _, err := tx.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, kind, ref_id, amount) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (kind, ref_id) DO NOTHING`, pid, kind, refID, amt); err != nil {
		return err
	}
	for _, b := range bound {
		if _, err := tx.Exec(ctx,
			`INSERT INTO payment_allocations (payment_id, kind, ref_id, amount) VALUES ($1,'charge',$2,$3)
			 ON CONFLICT (kind, ref_id) DO NOTHING`, pid, b.id, b.total); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE charges SET paid=true WHERE id=$1`, b.id); err != nil {
			return err
		}
	}
	return nil
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
// validatePayment normalizes and validates a payment request, returning the
// effective paid_on date and a non-empty message when the input is invalid.
func (h *Handler) validatePayment(req *paymentRequest) (time.Time, string) {
	req.Method = trim(req.Method)
	req.Note = trim(req.Note)
	if req.Method == "" {
		req.Method = "bar"
	}
	if !paymentMethods[req.Method] {
		return time.Time{}, "unknown payment method"
	}
	if req.Amount <= 0 {
		return time.Time{}, "amount must be greater than 0"
	}
	// Reject out-of-range amounts before they hit NUMERIC(12,2) and 500 with an
	// overflow error. maxMoneyAmount fits the column with headroom.
	if req.Amount > maxMoneyAmount {
		return time.Time{}, "amount exceeds the allowed maximum"
	}
	if !validNameLength(req.Note) {
		return time.Time{}, "note is too long"
	}
	paidOn := time.Now()
	if trim(req.PaidOn) != "" {
		if !validDateLength(trim(req.PaidOn)) {
			return time.Time{}, "paid_on is too long"
		}
		t, perr := time.Parse(dateLayout, trim(req.PaidOn))
		if perr != nil {
			return time.Time{}, "paid_on must be YYYY-MM-DD"
		}
		paidOn = t
	}
	return paidOn, ""
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
	paidOn, badMsg := h.validatePayment(&req)
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
		// Trail commits with the payment (atomic).
		if err := h.auditChangeTx(ctx, tx, r, "create", "payment", p.ID,
			fmt.Sprintf("recorded payment %.2f € (%s)", p.Amount, p.Method),
			map[string]any{
				"amount":    map[string]any{"old": nil, "new": p.Amount},
				"method":    map[string]any{"old": nil, "new": p.Method},
				"person_id": map[string]any{"old": nil, "new": p.PersonID},
			}); err != nil {
			return err
		}
		if settled > 0 {
			if err := h.auditChangeTx(ctx, tx, r, "update", "payment", p.ID,
				fmt.Sprintf("stamped %d position(s) as paid", settled),
				diffFields(map[string]any{"settled_positions": 0},
					map[string]any{"settled_positions": settled})); err != nil {
				return err
			}
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
	var reversedBy *int64
	if u, ok := auth.UserFrom(ctx); ok {
		reversedBy = &u.ID
	}
	deleted := false
	var delAmt float64
	var delMethod string
	var delOn time.Time
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

		// BAO §131: don't hard-delete a booked money-in — reverse it (Storno). The
		// row is KEPT and flagged reversed (excluded from every money sum, retained
		// for audit); its allocations and invoice links are removed so the settlement
		// is undone. Re-reversing (already reversed) affects no row → not found.
		if err := tx.QueryRow(ctx,
			`UPDATE payments SET reversed=true, reversed_at=now(), reversed_by=$2
			   WHERE id=$1 AND NOT reversed RETURNING amount, method, paid_on`, id, reversedBy).
			Scan(&delAmt, &delMethod, &delOn); err != nil {
			if err == pgx.ErrNoRows {
				return errPaymentNotFound
			}
			return err
		}
		deleted = true
		if _, err := tx.Exec(ctx, `DELETE FROM payment_allocations WHERE payment_id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM invoice_payments WHERE payment_id=$1`, id); err != nil {
			return err
		}
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
		// Reversal trail commits with the reversal (atomic) — the retained-record
		// Storno and its audit row are inseparable (BAO §131).
		return h.auditChangeTx(ctx, tx, r, "update", "payment", id, fmt.Sprintf(
			"Zahlung storniert: %.2f € (%s, %s) – Datensatz bleibt erhalten, Zuordnungen zurückgesetzt",
			delAmt, delMethod, delOn.Format("2006-01-02")),
			diffFields(map[string]any{"reversed": false, "amount": delAmt, "method": delMethod},
				map[string]any{"reversed": true, "amount": delAmt, "method": delMethod}))
	})
	if txErr == errPaymentNotFound || (txErr == nil && !deleted) {
		writeError(w, http.StatusNotFound, "payment not found")
		return
	}
	if txErr != nil {
		writeError(w, http.StatusInternalServerError, "could not delete payment")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reversed"})
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
	// Don't swallow: paymentsTotal drives the Guthaben drawdown budget below.
	if err := h.Pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0) FROM payments WHERE person_id=$1 AND NOT reversed`, id).Scan(&paymentsTotal); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	var latestPayment int64
	// A missing latest payment (no rows) is not an error — it means no Guthaben to
	// apply; only a real query error should surface.
	if err := h.Pool.QueryRow(ctx, `SELECT id FROM payments WHERE person_id=$1 AND NOT reversed ORDER BY paid_on DESC, id DESC LIMIT 1`, id).Scan(&latestPayment); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

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
		if settled > 0 {
			return h.auditChangeTx(ctx, tx, r, "update", "payment", 0,
				fmt.Sprintf("applied Guthaben to %d open position(s)", settled),
				diffFields(map[string]any{"settled_positions": 0},
					map[string]any{"settled_positions": settled}))
		}
		return nil
	})
	if txErr != nil {
		writeError(w, http.StatusInternalServerError, "could not apply credit")
		return
	}
	for _, vid := range archive {
		h.autoArchiveIfClosed(r, vid)
	}
	writeJSON(w, http.StatusOK, map[string]any{"settled": settled})
}
