package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

// recurringSelect joins the bound vehicle (and its category) so each row carries
// a display label; vehicle_id is NULL for person-level charges.
const recurringSelect = `SELECT rc.id, rc.person_id, rc.vehicle_id, rc.description, rc.amount, rc.period,
	rc.start_date, rc.end_date, rc.paid, rc.paid_periods, rc.paid_fixed, rc.created_at, rc.updated_at,
	COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), cat.name, '') AS vehicle_label
	FROM recurring_charges rc
	LEFT JOIN vehicles v ON v.id = rc.vehicle_id
	LEFT JOIN categories cat ON cat.id = v.category_id`

// scanRecurring reads one recurring_charges row (paid_fixed as raw JSON).
func scanRecurring(row pgx.Row) (models.RecurringCharge, error) {
	var rc models.RecurringCharge
	var fixedRaw []byte
	if err := row.Scan(&rc.ID, &rc.PersonID, &rc.VehicleID, &rc.Description, &rc.Amount, &rc.Period,
		&rc.StartDate, &rc.EndDate, &rc.Paid, &rc.PaidPeriods, &fixedRaw,
		&rc.CreatedAt, &rc.UpdatedAt, &rc.VehicleLabel); err != nil {
		return rc, err
	}
	if rc.PaidPeriods == nil {
		rc.PaidPeriods = []string{}
	}
	if len(fixedRaw) > 0 {
		_ = json.Unmarshal(fixedRaw, &rc.PaidFixed)
	}
	return rc, nil
}

// recurringPaidBound returns the paid euros of a vehicle-bound recurring charge:
// each elapsed sub-period counts as paid when the covering Pauschale's period for
// that sub-period is settled (or the vehicle's own paid flag is set), mirroring
// how a one-off bound charge settles via chargeSettled. A bound charge's own
// per-period flags are ignored — payment follows the Gefährt/Pauschale.
func recurringPaidBound(rc *models.RecurringCharge, agreements []models.FlatRatePeriod, vehiclePaid bool, now time.Time) float64 {
	p := rc.AsPeriod()
	var paid float64
	for key, cost := range p.ElapsedPeriodCosts(now) {
		layout := "2006-01"
		if rc.Period == models.BillingYearly {
			layout = "2006"
		}
		d, err := time.Parse(layout, key)
		if err != nil {
			continue
		}
		// The parsed date is the calendar period start (1st of month/year). For a
		// charge that begins mid-period, use its actual start so a Pauschale that
		// also begins on rc.StartDate is found active for that first period.
		if d.Before(rc.StartDate) {
			d = rc.StartDate
		}
		if chargeSettled(agreements, rc.VehicleID, d, false, vehiclePaid) {
			paid += cost
		}
	}
	return round2(paid)
}

// deriveRecurring fills the derived accrual/settlement fields as of now. A bound
// charge is settled via its Gefährt/Pauschale; a person-level one via its own
// per-period flags.
func deriveRecurring(rc *models.RecurringCharge, now time.Time) {
	p := rc.AsPeriod()
	rc.Accrued = round2(p.AccruedAsOf(now))
	rc.PeriodCosts = p.ElapsedPeriodCosts(now)
	// Settled must match how the charge is actually billed and balanced — Option A:
	// its OWN per-period flags only, for bound and person-level alike. A covering
	// Pauschale does NOT settle a Nebenkosten (billing.go bills it, personPeriodPaid
	// credits it only from own flags), so a coverage-based badge would show a bound
	// charge as paid while it is still billed and owed.
	rc.Settled = p.SettledAsOf(now)
}

// ptrInt64Differs reports whether two nullable ints differ, matching SQL's
// IS DISTINCT FROM (nil vs non-nil counts as different).
func ptrInt64Differs(a, b *int64) bool {
	if a == nil || b == nil {
		return (a == nil) != (b == nil)
	}
	return *a != *b
}

func (h *Handler) getRecurring(ctx context.Context, id int64) (models.RecurringCharge, error) {
	return scanRecurring(h.Pool.QueryRow(ctx, recurringSelect+` WHERE rc.id=$1`, id))
}

func (h *Handler) loadRecurringCharges(ctx context.Context, personID int64, now time.Time) ([]models.RecurringCharge, error) {
	rows, err := h.Pool.Query(ctx,
		recurringSelect+` WHERE rc.person_id=$1 ORDER BY rc.start_date DESC, rc.id DESC`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.RecurringCharge{}
	for rows.Next() {
		rc, serr := scanRecurring(rows)
		if serr != nil {
			return nil, serr
		}
		deriveRecurring(&rc, now)
		out = append(out, rc)
	}
	return out, rows.Err()
}

// loadAllRecurringCharges groups every recurring charge by person (dashboard),
// settling bound charges via each person's agreements and the global vehicle
// paid map.
func (h *Handler) loadAllRecurringCharges(ctx context.Context, now time.Time) (map[int64][]models.RecurringCharge, error) {
	rows, err := h.Pool.Query(ctx, recurringSelect)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]models.RecurringCharge{}
	for rows.Next() {
		rc, serr := scanRecurring(rows)
		if serr != nil {
			return nil, serr
		}
		deriveRecurring(&rc, now)
		out[rc.PersonID] = append(out[rc.PersonID], rc)
	}
	return out, rows.Err()
}

// recurringMonthly returns the recurring charges' accrual per calendar month of
// the given year (index 0 = January), capped at now for the current year.
func recurringMonthly(list []models.RecurringCharge, year int, now time.Time) []float64 {
	out := make([]float64, 12)
	until := models.DayAfter(now)
	for i := range list {
		p := list[i].AsPeriod()
		for m := 0; m < 12; m++ {
			mStart := time.Date(year, time.Month(m+1), 1, 0, 0, 0, 0, time.UTC)
			mEnd := mStart.AddDate(0, 1, 0)
			if mEnd.After(until) {
				mEnd = until
			}
			if mEnd.After(mStart) {
				out[m] += p.CostInRange(mStart, mEnd)
			}
		}
	}
	return out
}

// recurringSums returns the accrued and paid totals (euros) of a person's
// recurring charges as of now, reusing the flat-rate period math. A bound charge
// is settled via its Gefährt/Pauschale; a person-level one via its own flags.
func recurringSums(list []models.RecurringCharge, agreements []models.FlatRatePeriod, vehPaid map[int64]bool, now time.Time) (accrued, paid float64) {
	until := models.DayAfter(now)
	for i := range list {
		rc := &list[i]
		p := rc.AsPeriod()
		accrued += p.CostInRange(p.StartDate, until)
		if rc.VehicleID != nil {
			paid += recurringPaidBound(rc, agreements, vehPaid[*rc.VehicleID], now)
		} else {
			paid += float64(p.PaidCentsInRange(p.StartDate, until)) / 100
		}
	}
	return round2(accrued), round2(paid)
}

// ListRecurringCharges returns a person's recurring charges.
func (h *Handler) ListRecurringCharges(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	// Settlement is derived from each charge's own per-period flags (Option A), so
	// no agreements/vehicles load is needed here.
	list, err := h.loadRecurringCharges(r.Context(), id, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type recurringRequest struct {
	Description string   `json:"description"`
	Amount      *float64 `json:"amount"`
	Period      string   `json:"period"`
	StartDate   string   `json:"start_date"`
	EndDate     *string  `json:"end_date"`
	VehicleID   *int64   `json:"vehicle_id"` // optional binding to a Gefährt
}

// vehicleBelongsTo reports a 400 reason if the bound vehicle does not belong to
// personID (or does not exist); empty when valid or unbound. A non-nil error is a
// 500.
func (h *Handler) vehicleBelongsTo(ctx context.Context, vehicleID *int64, personID int64) (string, error) {
	if vehicleID == nil {
		return "", nil
	}
	var owner int64
	err := h.Pool.QueryRow(ctx, `SELECT person_id FROM vehicles WHERE id=$1`, *vehicleID).Scan(&owner)
	if err == pgx.ErrNoRows || (err == nil && owner != personID) {
		return "vehicle does not belong to that person", nil
	}
	if err != nil {
		return "", err
	}
	return "", nil
}

// parse validates and normalizes a recurring-charge request.
func (req *recurringRequest) parse() (desc, period string, amount float64, start time.Time, end *time.Time, msg string) {
	desc = trim(req.Description)
	if desc == "" {
		return "", "", 0, time.Time{}, nil, "description is required"
	}
	if !validNameLength(desc) {
		return "", "", 0, time.Time{}, nil, "description is too long"
	}
	if req.Amount == nil || *req.Amount < 0 {
		return "", "", 0, time.Time{}, nil, "amount must not be negative"
	}
	if *req.Amount > maxMoneyAmount {
		return "", "", 0, time.Time{}, nil, "amount is out of range"
	}
	switch req.Period {
	case models.BillingMonthly, models.BillingYearly:
		period = req.Period
	default:
		return "", "", 0, time.Time{}, nil, "period must be monthly or yearly"
	}
	if !validDateLength(trim(req.StartDate)) {
		return "", "", 0, time.Time{}, nil, "start_date is too long"
	}
	s, err := time.Parse(dateLayout, trim(req.StartDate))
	if err != nil {
		return "", "", 0, time.Time{}, nil, "start_date must be YYYY-MM-DD"
	}
	start = s
	if req.EndDate != nil && trim(*req.EndDate) != "" {
		if !validDateLength(trim(*req.EndDate)) {
			return "", "", 0, time.Time{}, nil, "end_date is too long"
		}
		e, eerr := time.Parse(dateLayout, trim(*req.EndDate))
		if eerr != nil {
			return "", "", 0, time.Time{}, nil, "end_date must be YYYY-MM-DD"
		}
		if !e.After(start) {
			return "", "", 0, time.Time{}, nil, "end_date must be after start_date"
		}
		end = &e
	}
	return desc, period, *req.Amount, start, end, ""
}

// CreateRecurringCharge adds a recurring extra cost for a person.
func (h *Handler) CreateRecurringCharge(w http.ResponseWriter, r *http.Request) {
	personID, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req recurringRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	desc, period, amount, start, end, msg := req.parse()
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if badMsg, verr := h.vehicleBelongsTo(r.Context(), req.VehicleID, personID); verr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	} else if badMsg != "" {
		writeError(w, http.StatusBadRequest, badMsg)
		return
	}
	var id int64
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO recurring_charges (person_id, vehicle_id, description, amount, period, start_date, end_date)
		 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		personID, req.VehicleID, desc, amount, period, start, end).Scan(&id)
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "person or vehicle does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create recurring charge")
		return
	}
	h.audit(r, "create", "recurring_charge", id, "added recurring cost "+desc)
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// UpdateRecurringCharge edits a recurring charge's terms. Changing the vehicle
// binding resets the payment state (a bound charge settles via its Gefährt, so
// its own flags would be meaningless / stale after the binding changes).
func (h *Handler) UpdateRecurringCharge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req recurringRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	desc, period, amount, start, end, msg := req.parse()
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	existing, gerr := h.getRecurring(r.Context(), id)
	if gerr == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "recurring charge not found")
		return
	}
	if gerr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if badMsg, verr := h.vehicleBelongsTo(r.Context(), req.VehicleID, existing.PersonID); verr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	} else if badMsg != "" {
		writeError(w, http.StatusBadRequest, badMsg)
		return
	}
	// Once a period of this charge is invoiced, its billing-defining fields are
	// frozen: changing period/start/amount would desync the period-keyed
	// invoice_source lock (re-billing an already-invoiced span) or make the balance
	// disagree with the issued document. end_date may extend but must not RETRACT
	// below an invoiced period (that drops it from accrual while its payment stays).
	retract, rerr := h.endDateRetractsBelowInvoiced(r.Context(), h.Pool, "recurring", id, end)
	if rerr != nil {
		writeError(w, http.StatusInternalServerError, "could not check invoices")
		return
	}
	if period != existing.Period || !start.Equal(existing.StartDate) || amount != existing.Amount || retract {
		if inv, ierr := h.refInvoiced(r.Context(), h.Pool, "recurring", id); ierr != nil {
			writeError(w, http.StatusInternalServerError, "could not check invoices")
			return
		} else if inv {
			writeError(w, http.StatusConflict, "Nebenkosten sind fakturiert – Betrag/Zeitraum nicht änderbar (Storno über die Rechnung)")
			return
		}
	}
	// Re-binding (person-level ↔ vehicle, or to a different vehicle) wipes the
	// per-period settlement flags — for a person-level charge those flags are the
	// ONLY record that its periods were paid. Refuse while any settlement exists so
	// already-paid periods can't silently reopen and re-bill the customer (A2-4).
	if ptrInt64Differs(existing.VehicleID, req.VehicleID) &&
		(existing.Paid || len(existing.PaidPeriods) > 0 || len(existing.PaidFixed) > 0) {
		writeError(w, http.StatusConflict, "Nebenkosten haben bezahlte Perioden – Bindung nicht änderbar (erst offen stellen)")
		return
	}
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE recurring_charges SET description=$1, amount=$2, period=$3, start_date=$4, end_date=$5,
		        vehicle_id=$6,
		        paid = CASE WHEN vehicle_id IS DISTINCT FROM $6 THEN false ELSE paid END,
		        paid_periods = CASE WHEN vehicle_id IS DISTINCT FROM $6 THEN '{}'::text[] ELSE paid_periods END,
		        paid_fixed = CASE WHEN vehicle_id IS DISTINCT FROM $6 THEN '{}'::jsonb ELSE paid_fixed END,
		        updated_at=now() WHERE id=$7`,
		desc, amount, period, start, end, req.VehicleID, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "vehicle does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update recurring charge")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "recurring charge not found")
		return
	}
	h.audit(r, "update", "recurring_charge", id, "updated recurring cost "+desc)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteRecurringCharge removes a recurring charge.
func (h *Handler) DeleteRecurringCharge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	// An invoiced recurring charge must not be deleted: it would orphan the
	// invoice_source lock and sever the invoice→source trail (BAO reconstruction).
	if inv, err := h.refInvoiced(r.Context(), h.Pool, "recurring", id); err != nil {
		writeError(w, http.StatusInternalServerError, "could not check invoices")
		return
	} else if inv {
		writeError(w, http.StatusConflict, "Nebenkosten sind fakturiert – nicht löschbar (Storno über die Rechnung)")
		return
	}
	// Remove its auto settle-payments in the same tx as the delete, so a paid-then-
	// deleted Nebenkosten leaves no orphan Zahlungseingang (phantom money-in).
	var affected int64
	txErr := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := clearRecurringSettlementTx(r.Context(), tx, id); err != nil {
			return err
		}
		ct, err := tx.Exec(r.Context(), `DELETE FROM recurring_charges WHERE id=$1`, id)
		if err != nil {
			return err
		}
		affected = ct.RowsAffected()
		return nil
	})
	if txErr != nil {
		writeError(w, http.StatusInternalServerError, "could not delete recurring charge")
		return
	}
	if affected == 0 {
		writeError(w, http.StatusNotFound, "recurring charge not found")
		return
	}
	h.audit(r, "delete", "recurring_charge", id, "deleted recurring cost")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// SetRecurringChargePaid toggles the whole Nebenkosten paid via its master slider.
// Like the Pauschale slider, "bezahlt" now books a real Zahlungseingang per completed
// period (a running period stays on the off-book credit) and "offen" reverses it, so
// a paid recurring charge shows up in the payments list — not just as a flag.
func (h *Handler) SetRecurringChargePaid(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Paid bool `json:"paid"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()
	createdBy := createdByFrom(ctx)
	rc, err := h.getRecurring(ctx, id)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "recurring charge not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update recurring charge")
		return
	}
	verb := "recurring marked open"
	if req.Paid {
		verb = "recurring marked paid"
	}
	// Periods settled through an invoice must not also be booked here (double-count);
	// reuse the same period-lock check the per-period toggle uses.
	locked, lerr := h.lockedPositions(ctx, rc.PersonID)
	if lerr != nil {
		writeError(w, http.StatusInternalServerError, "could not update recurring charge")
		return
	}
	txErr := pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
		// "Reset all": clear the per-period flags and this charge's settle payments,
		// then re-derive from the master flag (mirrors the Pauschale slider).
		if _, err := tx.Exec(ctx,
			`UPDATE recurring_charges SET paid=$1, paid_periods='{}', paid_fixed='{}'::jsonb, updated_at=now() WHERE id=$2`,
			req.Paid, id); err != nil {
			return err
		}
		if err := clearRecurringSettlementTx(ctx, tx, id); err != nil {
			return err
		}
		if req.Paid {
			p := rc.AsPeriod()
			for _, per := range p.ElapsedPeriodsDetailed(time.Now()) {
				if !per.Complete || locked[lockKey("recurring", id, per.Key)] {
					continue // running, or settled through an invoice → skip
				}
				if err := recordPeriodPaymentTx(ctx, tx, rc.PersonID, "recurring", id, per.Key, per.Cost, createdBy); err != nil {
					return err
				}
			}
		}
		// Audit in the tx: the settlement change and its trail commit together (C7).
		return h.auditTx(ctx, tx, r, "update", "recurring_charge", id, verb)
	})
	if txErr != nil {
		writeError(w, http.StatusInternalServerError, "could not update recurring charge")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// clearRecurringSettlementTx removes a recurring charge's auto settle-payments — used
// when it is toggled open and before it is deleted, so no orphan Zahlungseingang
// (phantom money-in) outlives the settlement or the row.
func clearRecurringSettlementTx(ctx context.Context, tx pgx.Tx, id int64) error {
	_, err := tx.Exec(ctx,
		`DELETE FROM payments WHERE auto AND settles_kind='recurring' AND settles_ref=$1`, id)
	return err
}

// SetRecurringChargePeriodPaid settles a single elapsed sub-period: a whole
// period (amount omitted) or a fixed partial amount, mirroring the agreements.
func (h *Handler) SetRecurringChargePeriodPaid(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		PeriodKey string   `json:"period_key"`
		Paid      bool     `json:"paid"`
		Amount    *float64 `json:"amount"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// A fixed partial payment must be positive and in range; a whole-period
	// prepayment omits it.
	if req.Paid && req.Amount != nil && (*req.Amount <= 0 || *req.Amount > maxMoneyAmount) {
		writeError(w, http.StatusBadRequest, "amount is out of range")
		return
	}
	rc, err := h.getRecurring(r.Context(), id)
	if err == pgx.ErrNoRows {
		writeError(w, http.StatusNotFound, "recurring charge not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	// Only periods that have begun can be paid (guards against bogus keys).
	valid := false
	p := rc.AsPeriod()
	for _, k := range p.ElapsedPeriodKeys(time.Now()) {
		if k == req.PeriodKey {
			valid = true
			break
		}
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid period")
		return
	}
	// An invoiced period is settled through the invoice, not the per-period flag.
	// Block BOTH directions: marking would double-credit once the invoice is
	// Storno'd; UNmarking would drop a fixed partial the invoice already billed
	// around (open = cost − partial), leaving a phantom debt equal to the partial.
	{
		locked, lerr := h.lockedPositions(r.Context(), rc.PersonID)
		if lerr != nil {
			writeError(w, http.StatusInternalServerError, "query failed")
			return
		}
		if locked[lockKey("recurring", id, req.PeriodKey)] {
			writeError(w, http.StatusConflict, "Periode ist fakturiert – über die Rechnung begleichen")
			return
		}
	}
	// Serialize the read-modify-write of the whole paid_periods/paid_fixed columns
	// under FOR UPDATE (the agreement path is already tx-guarded); otherwise two
	// concurrent per-period settlements clobber each other and lose one.
	txErr := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var periodsRaw []string
		var fixedRaw []byte
		if err := tx.QueryRow(r.Context(),
			`SELECT paid_periods, paid_fixed FROM recurring_charges WHERE id=$1 FOR UPDATE`, id).
			Scan(&periodsRaw, &fixedRaw); err != nil {
			return err
		}
		fixed := map[string]float64{}
		if len(fixedRaw) > 0 {
			// Fail the tx on malformed paid_fixed rather than silently proceeding with
			// an empty map, which would wipe the other periods' recorded partials.
			if err := json.Unmarshal(fixedRaw, &fixed); err != nil {
				return err
			}
		}
		periods := removeString(periodsRaw, req.PeriodKey)
		delete(fixed, req.PeriodKey)
		if req.Paid {
			if req.Amount == nil {
				periods = append(periods, req.PeriodKey) // whole period prepaid
			} else {
				fixed[req.PeriodKey] = *req.Amount // fixed partial (validated > 0 above)
			}
		}
		fixedJSON, _ := json.Marshal(fixed)
		if _, err := tx.Exec(r.Context(),
			`UPDATE recurring_charges SET paid_periods=$1, paid_fixed=$2, updated_at=now() WHERE id=$3`,
			periods, string(fixedJSON), id); err != nil {
			return err
		}
		// Book/remove the real Zahlungseingang mirroring the off-book flag (Fix 1): a
		// completed whole period or an explicit partial becomes a payments row; a
		// toggle-off deletes it. The off-book credit skips periods with such a payment,
		// so the balance never double-counts.
		if req.Paid {
			if amt, ok := periodPaymentAmount(rc.AsPeriod(), req.PeriodKey, req.Amount, time.Now()); ok {
				if err := recordPeriodPaymentTx(r.Context(), tx, rc.PersonID, "recurring", id, req.PeriodKey, amt, createdByFrom(r.Context())); err != nil {
					return err
				}
			} else if err := deletePeriodPaymentTx(r.Context(), tx, "recurring", id, req.PeriodKey); err != nil {
				return err
			}
		} else if err := deletePeriodPaymentTx(r.Context(), tx, "recurring", id, req.PeriodKey); err != nil {
			return err
		}
		return h.auditTx(r.Context(), tx, r, "update", "recurring_charge", id,
			"Nebenkosten-Periode "+req.PeriodKey+": "+periodPaidAuditState(req.Paid, req.Amount))
	})
	if txErr != nil {
		writeError(w, http.StatusInternalServerError, "could not update recurring charge")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// removeString returns s without any elements equal to v (order preserved).
func removeString(s []string, v string) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
