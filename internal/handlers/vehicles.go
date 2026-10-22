package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

const dateLayout = "2006-01-02"

// errArchived is returned when a mutation is attempted on an archived (closed)
// vehicle. Archived vehicles are read-only; the client must reactivate first.
const errArchived = "vehicle is archived – reactivate it first to make changes"

// ensureVehicleWritable turns the "load-then-check" preamble shared by the
// mutating vehicle handlers into one call: it writes 404 when the row was
// missing (scanErr set) or 409 when the vehicle is archived (read-only), and
// returns false in either case. Callers still fetch whatever other columns they
// need alongside `archived` in the same query.
func ensureVehicleWritable(w http.ResponseWriter, archived bool, scanErr error) bool {
	if scanErr != nil {
		// Only a genuinely absent row is a 404; any other scan failure (e.g. the
		// database being unreachable) is a server error, not a "not found".
		if errors.Is(scanErr, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "vehicle not found")
		} else {
			writeError(w, http.StatusInternalServerError, "query failed")
		}
		return false
	}
	if archived {
		writeError(w, http.StatusConflict, errArchived)
		return false
	}
	return true
}

// autoArchiveIfClosed archives a vehicle that has just become fully closed
// (cancelled, or collected AND paid), so settled vehicles drop out of the
// active lists automatically. It is a no-op for still-open or already-archived
// vehicles. Archiving never touches billing – it only affects list/edit
// visibility – so it is safe to run after any status/payment change.
func (h *Handler) autoArchiveIfClosed(r *http.Request, id int64) {
	err := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := lockFinancialSourceTx(r.Context(), tx, "vehicle", id, 0); err != nil {
			return err
		}
		ct, err := tx.Exec(r.Context(), `UPDATE vehicles v SET archived=true, updated_at=now()
		 WHERE v.id=$1 AND NOT v.archived AND (`+vehicleArchivePredicate+`)`, id, businessDay(h.now()))
		if err != nil || ct.RowsAffected() == 0 {
			return err
		}
		return h.auditChangeTx(r.Context(), tx, r, "update", "vehicle", id,
			"Gefährt archiviert (abgeschlossen)",
			diffFields(map[string]any{"archived": false}, map[string]any{"archived": true}))
	})
	if err != nil {
		slog.Error("vehicle auto-archive failed", "vehicle_id", id, "err", err)
	}
}

// vehicleSelect is the shared column list + joins for reading vehicles,
// including a live photo count. Append WHERE/ORDER as needed.
const vehicleSelect = `SELECT v.id, v.person_id, v.category_id, v.label, v.license_plate,
	        v.notes, v.billing_period, v.rate, v.cost_override, v.start_date, v.end_date,
	        v.status, v.reserved_from, v.reserved_until, v.paid, v.archived, v.created_at, v.updated_at, v.needs_power, v.planner_symbol,
	        c.name, c.default_monthly_cost, c.default_yearly_cost,
	        p.first_name, p.last_name,
	        (SELECT count(*) FROM vehicle_photos vp WHERE vp.vehicle_id = v.id),
	        v.spot_id, sp.hall_id, hl.name, v.paid_through,
	        (SELECT jsonb_agg(jsonb_build_object('from', to_char(rc.effective_from, 'YYYY-MM-DD"T"00:00:00"Z"'), 'amount', rc.rate)
	                ORDER BY rc.effective_from) FROM vehicle_rate_changes rc WHERE rc.vehicle_id = v.id),
	        (SELECT jsonb_agg(jsonb_build_object('id', vp.id,
	                'from', to_char(vp.start_date, 'YYYY-MM-DD"T"00:00:00"Z"'),
	                'until', CASE WHEN vp.ended THEN to_char(vp.until, 'YYYY-MM-DD"T"00:00:00"Z"') ELSE '9999-12-31T00:00:00Z' END,
	                'planned', to_char(vp.until, 'YYYY-MM-DD"T"00:00:00"Z"'), 'ended', vp.ended, 'hold_spot', vp.hold_spot)
	                ORDER BY vp.start_date) FROM vehicle_pauses vp WHERE vp.vehicle_id = v.id)
	 FROM vehicles v
	 JOIN categories c ON c.id = v.category_id
	 JOIN persons p ON p.id = v.person_id
	 LEFT JOIN spots sp ON sp.id = v.spot_id
	 LEFT JOIN halls hl ON hl.id = sp.hall_id`

// ListVehicles returns vehicles, optionally filtered by ?person_id=.
func (h *Handler) ListVehicles(w http.ResponseWriter, r *http.Request) {
	var (
		rows pgx.Rows
		err  error
	)
	limit, offset := pageParams(r, 1000, 1000)
	var personID int64 // 0 = unfiltered
	if pid := r.URL.Query().Get("person_id"); pid != "" {
		if !validNumericParamLength(pid) {
			writeError(w, http.StatusBadRequest, "person_id is too long")
			return
		}
		n, perr := strconv.ParseInt(pid, 10, 64)
		if perr != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid person_id")
			return
		}
		personID = n
		// X-Total-Count wie bei /persons: die Liste ist bei 1000 gedeckelt, und ohne die
		// Gesamtzahl kann das Frontend eine abgeschnittene nicht von einer vollständigen
		// unterscheiden (Hundert UX-53).
		h.totalCount(w, r.Context(), `SELECT count(*) FROM vehicles WHERE person_id=$1`, personID)
		rows, err = h.Pool.Query(r.Context(),
			vehicleSelect+` WHERE v.person_id = $1 ORDER BY v.start_date DESC, v.id DESC LIMIT $2 OFFSET $3`,
			personID, limit, offset)
	} else {
		h.totalCount(w, r.Context(), `SELECT count(*) FROM vehicles`)
		rows, err = h.Pool.Query(r.Context(),
			vehicleSelect+` ORDER BY v.start_date DESC, v.id DESC LIMIT $1 OFFSET $2`, limit, offset)
	}
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()

	now := h.now()
	vehicles := []models.Vehicle{}
	for rows.Next() {
		v, cat, err := scanVehicleRow(rows)
		if err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		enrich(&v, cat, now)
		vehicles = append(vehicles, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "scan failed", err)
		return
	}
	rows.Close()

	// Derive per-vehicle flat-rate coverage so the list can show "in Pauschale"
	// and defer payment to the agreement where a vehicle is fully covered.
	// Scoped to the person when the request is filtered.
	agByPerson, err := h.loadAllAgreements(r.Context(), personID)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	setFlatRateCoverage(vehicles, agByPerson, now)
	if err := h.setVehicleInvoiceStatus(r.Context(), vehicles); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if hallStaff(r) {
		for i := range vehicles {
			redactVehicleMoney(&vehicles[i])
		}
	}
	writeJSON(w, http.StatusOK, vehicles)
}

type vehicleRequest struct {
	PersonID      int64    `json:"person_id"`
	CategoryID    int64    `json:"category_id"`
	Label         string   `json:"label"`
	LicensePlate  string   `json:"license_plate"`
	Notes         string   `json:"notes"`
	BillingPeriod string   `json:"billing_period"`
	Rate          *float64 `json:"rate"`
	CostOverride  *float64 `json:"cost_override"`
	StartDate     string   `json:"start_date"`
	EndDate       *string  `json:"end_date"`
	Status        string   `json:"status"`
	ReservedFrom  *string  `json:"reserved_from"`
	ReservedUntil *string  `json:"reserved_until"`
	NeedsPower    bool     `json:"needs_power"`
	PlannerSymbol *string  `json:"planner_symbol"`
}

// plannerSymbols are the valid per-vehicle Garagenplaner symbol overrides; empty/nil
// means "automatic" (derive from the category). Kept in sync with the frontend library.
var plannerSymbols = map[string]bool{
	"PKW": true, "Motorrad": true, "Transporter": true, "Wohnmobil": true, "Wohnwagen": true,
	"Anhänger": true, "Boot / Trailer": true, "Traktor": true, "Ladewagen": true, "Rückewagen": true, "Kipper": true,
}

// normPlannerSymbol validates/normalizes a planner_symbol: nil/"" -> nil (auto); one of
// the built-in keys -> itself; "custom:<id>" -> itself iff that uploaded icon exists.
// Anything else is rejected so bad values return 400 rather than reaching the DB.
func (h *Handler) normPlannerSymbol(ctx context.Context, in *string) (*string, error) {
	if in == nil {
		return nil, nil
	}
	s := trim(*in)
	if s == "" {
		return nil, nil
	}
	if plannerSymbols[s] {
		return &s, nil
	}
	if len(s) > 7 && s[:7] == "custom:" {
		id, err := strconv.ParseInt(s[7:], 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("invalid planner_symbol")
		}
		var exists bool
		if err := h.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM planner_icons WHERE id=$1)`, id).Scan(&exists); err != nil {
			return nil, errors.New("could not validate planner_symbol")
		}
		if !exists {
			return nil, errors.New("invalid planner_symbol")
		}
		return &s, nil
	}
	return nil, errors.New("invalid planner_symbol")
}

type parsedVehicle struct {
	req           vehicleRequest
	startDate     time.Time
	endDate       *time.Time
	reservedFrom  *time.Time
	reservedUntil *time.Time
}

// errDateTooLong lets callers distinguish an over-long date (→ "<field> is too
// long") from a malformed one (→ "<field> must be YYYY-MM-DD").
var errDateTooLong = errors.New("date is too long")

func parseOptDate(s *string) (*time.Time, error) {
	if s == nil || trim(*s) == "" {
		return nil, nil
	}
	if !validDateLength(trim(*s)) {
		return nil, errDateTooLong
	}
	t, err := time.Parse(dateLayout, trim(*s))
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (h *Handler) parseVehicleRequest(r *http.Request) (*parsedVehicle, error) {
	var req vehicleRequest
	if err := decodeJSON(r, &req); err != nil {
		return nil, errors.New("invalid request body")
	}
	return h.validateVehicleRequest(r.Context(), req)
}

// validateVehicleRequest normalizes and checks a vehicle request. Shared by the JSON
// endpoints and the CSV import, so an imported Gefährt passes the same rules.
func (h *Handler) validateVehicleRequest(ctx context.Context, req vehicleRequest) (*parsedVehicle, error) {
	req.Label = trim(req.Label)
	req.LicensePlate = trim(req.LicensePlate)
	req.Notes = trim(req.Notes)
	if req.BillingPeriod == "" {
		req.BillingPeriod = models.BillingMonthly
	}
	if req.BillingPeriod != models.BillingMonthly && req.BillingPeriod != models.BillingYearly {
		return nil, errors.New("billing_period must be 'monthly' or 'yearly'")
	}
	if req.Status == "" {
		req.Status = models.StatusStored
	}
	if !models.ValidStatuses[req.Status] {
		return nil, errors.New("invalid status")
	}
	if req.PersonID <= 0 || req.CategoryID <= 0 {
		return nil, errors.New("person_id and category_id are required")
	}
	if req.CostOverride != nil && (*req.CostOverride < 0 || *req.CostOverride > maxMoneyAmount) {
		return nil, errors.New("cost_override is out of range")
	}
	if req.Rate != nil && (*req.Rate < 0 || *req.Rate > maxMoneyAmount) {
		return nil, errors.New("rate is out of range")
	}

	if !validNameLength(req.Label) {
		return nil, errors.New("label is too long")
	}
	if !validNameLength(req.LicensePlate) {
		return nil, errors.New("license_plate is too long")
	}
	if !validNoteLength(req.Notes) {
		return nil, errors.New("notes is too long")
	}

	if !validDateLength(trim(req.StartDate)) {
		return nil, errors.New("start_date is too long")
	}
	start, err := time.Parse(dateLayout, trim(req.StartDate))
	if err != nil {
		return nil, errors.New("start_date must be YYYY-MM-DD")
	}
	endPtr, err := parseOptDate(req.EndDate)
	if errors.Is(err, errDateTooLong) {
		return nil, errors.New("end_date is too long")
	} else if err != nil {
		return nil, errors.New("end_date must be YYYY-MM-DD")
	}
	if endPtr != nil && endPtr.Before(start) {
		return nil, errors.New("end_date must not be before start_date")
	}
	resFrom, err := parseOptDate(req.ReservedFrom)
	if errors.Is(err, errDateTooLong) {
		return nil, errors.New("reserved_from is too long")
	} else if err != nil {
		return nil, errors.New("reserved_from must be YYYY-MM-DD")
	}
	resUntil, err := parseOptDate(req.ReservedUntil)
	if errors.Is(err, errDateTooLong) {
		return nil, errors.New("reserved_until is too long")
	} else if err != nil {
		return nil, errors.New("reserved_until must be YYYY-MM-DD")
	}
	sym, err := h.normPlannerSymbol(ctx, req.PlannerSymbol)
	if err != nil {
		return nil, err
	}
	req.PlannerSymbol = sym
	return &parsedVehicle{
		req: req, startDate: start, endDate: endPtr,
		reservedFrom: resFrom, reservedUntil: resUntil,
	}, nil
}

// effectiveRate picks the rate to persist: the explicit rate, else the legacy
// cost override, else the fallback. The fallback is the Tarif default on create
// and the vehicle's *existing* rate on update, so an omitted rate never silently
// re-prices an existing vehicle.
func effectiveRate(req vehicleRequest, fallback float64) float64 {
	if req.Rate != nil {
		return *req.Rate
	}
	if req.CostOverride != nil {
		return *req.CostOverride
	}
	return fallback
}

// persistedCostOverride preserves the legacy fallback contract while making an
// explicitly submitted zero rate distinguishable from an old, uninitialized
// rate=0 row. No data migration or reinterpretation of existing rows is needed.
func persistedCostOverride(req vehicleRequest, existing *float64) *float64 {
	if req.Rate != nil && *req.Rate == 0 {
		zero := 0.0
		return &zero
	}
	if req.CostOverride != nil {
		return req.CostOverride
	}
	if req.Rate != nil {
		return nil
	}
	return existing
}

func markZeroRate(rate float64, override *float64) *float64 {
	if rate == 0 && override == nil {
		zero := 0.0
		return &zero
	}
	return override
}

// storedEffectiveRate resolves the amount that billing actually sees for both
// current and legacy rows. Comparing this value lets an invoiced legacy row be
// normalized from rate=0 to its existing Tarif amount, while still blocking a
// real price change such as adding the explicit free-booking marker.
func storedEffectiveRate(ctx context.Context, q rowQuerier, categoryID int64, period string, rate float64, override *float64) (float64, error) {
	if rate != 0 {
		return rate, nil
	}
	if override != nil {
		return *override, nil
	}
	return categoryDefaultRate(ctx, q, categoryID, period)
}

// rowQuerier is satisfied by both *pgxpool.Pool and pgx.Tx, so lookups can run
// standalone or inside a caller's transaction.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// dbQuerier is the read surface shared by a pool and a transaction. Billing
// snapshots use it to ensure every source row is read on the same connection and
// transaction that later claims the rows in invoice_source.
type dbQuerier interface {
	rowQuerier
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// refFinanciallyLocked reports whether a source currently participates in an
// issued invoice or a non-reversed payment. Corrections must first reverse that
// document/payment instead of rewriting the referenced principal in place.
func (h *Handler) refFinanciallyLocked(ctx context.Context, q rowQuerier, kind string, refID int64) (bool, error) {
	var locked bool
	err := q.QueryRow(ctx,
		`SELECT
		   EXISTS(SELECT 1 FROM invoice_source s JOIN invoices i ON i.id=s.invoice_id
		           WHERE s.kind=$1 AND s.ref_id=$2 AND NOT i.canceled)
		   OR EXISTS(SELECT 1 FROM payment_allocations a JOIN payments p ON p.id=a.payment_id
		             WHERE a.kind=$1 AND a.ref_id=$2 AND NOT p.reversed)
		   OR EXISTS(SELECT 1 FROM payments p
		             WHERE p.settles_kind=$1 AND p.settles_ref=$2 AND NOT p.reversed)`,
		kind, refID).Scan(&locked)
	return locked, err
}

// vehicleOwnerReferenced prevents a direct owner rewrite from splitting related
// charges or agreement membership across two customers. Handover protocols and
// vehicle attachments count too (PRT-01): they carry the owner snapshotted at
// creation, and a reassigned vehicle would otherwise hold records of two people.
func (h *Handler) vehicleOwnerReferenced(ctx context.Context, q rowQuerier, vehicleID int64) (bool, error) {
	var referenced bool
	err := q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM charges WHERE vehicle_id=$1)
		     OR EXISTS(SELECT 1 FROM recurring_charges WHERE vehicle_id=$1)
		     OR EXISTS(SELECT 1 FROM flat_rate_period_vehicles WHERE vehicle_id=$1)
		     OR EXISTS(SELECT 1 FROM invoice_source WHERE kind='vehicle' AND ref_id=$1)
		     OR EXISTS(SELECT 1 FROM payment_allocations WHERE kind='vehicle' AND ref_id=$1)
		     OR EXISTS(SELECT 1 FROM handover_protocols WHERE vehicle_id=$1)
		     OR EXISTS(SELECT 1 FROM attachments WHERE vehicle_id=$1)`,
		vehicleID).Scan(&referenced)
	return referenced, err
}

func sameOptionalDate(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// categoryDefaultRate returns the current Tarif default for the billing period.
// The shared lock serializes price capture with UpdateCategory until the caller's
// transaction commits. Category updates never lock or rewrite vehicle rows.
func categoryDefaultRate(ctx context.Context, q rowQuerier, categoryID int64, period string) (float64, error) {
	var monthly, yearly float64
	if err := q.QueryRow(ctx,
		`SELECT default_monthly_cost, default_yearly_cost FROM categories WHERE id=$1 FOR SHARE`, categoryID).
		Scan(&monthly, &yearly); err != nil {
		return 0, err
	}
	if period == models.BillingYearly {
		return yearly, nil
	}
	return monthly, nil
}

// CreateVehicle adds a vehicle for a person.
func (h *Handler) CreateVehicle(w http.ResponseWriter, r *http.Request) {
	pv, err := h.parseVehicleRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create vehicle")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	id, err := h.insertVehicleTx(r, tx, pv)
	if err != nil {
		switch {
		case errors.Is(err, errVehicleCategoryMissing):
			writeError(w, http.StatusBadRequest, "category does not exist")
		case isForeignKeyViolation(err):
			writeError(w, http.StatusBadRequest, "person or category does not exist")
		default:
			writeError(w, http.StatusInternalServerError, "could not create vehicle")
		}
		return
	}
	if err := h.auditCreatedTx(r.Context(), tx, r, "vehicle", id, "created vehicle "+vehicleLabel(pv.req.Label, pv.req.LicensePlate),
		vehicleCreatedSnapshot(pv)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create vehicle")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create vehicle")
		return
	}
	h.writeVehicle(w, r.Context(), id, http.StatusCreated)
}

var errVehicleCategoryMissing = errors.New("category does not exist")

// vehicleCreatedSnapshot is the audit snapshot of a newly created vehicle.
func vehicleCreatedSnapshot(pv *parsedVehicle) map[string]any {
	return map[string]any{"label": pv.req.Label, "license_plate": pv.req.LicensePlate,
		"person_id": pv.req.PersonID, "category_id": pv.req.CategoryID,
		"status": pv.req.Status, "billing_period": pv.req.BillingPeriod,
		"start_date": pv.req.StartDate}
}

// insertVehicleTx creates a validated vehicle inside tx: locks the price (Tarif
// default unless a rate was given) and writes the first status entry. Shared by
// CreateVehicle and the CSV import; each caller writes its own audit entry.
func (h *Handler) insertVehicleTx(r *http.Request, tx pgx.Tx, pv *parsedVehicle) (int64, error) {
	var fallback float64
	if pv.req.Rate == nil && pv.req.CostOverride == nil {
		def, derr := categoryDefaultRate(r.Context(), tx, pv.req.CategoryID, pv.req.BillingPeriod)
		if derr != nil {
			if errors.Is(derr, pgx.ErrNoRows) {
				return 0, errVehicleCategoryMissing
			}
			return 0, derr
		}
		fallback = def
	}
	rate := effectiveRate(pv.req, fallback)
	// A zero Tarif is also a deliberately free booking. Mark it now so a later
	// edit of the Tarif cannot re-price this already locked vehicle.
	costOverride := markZeroRate(rate, persistedCostOverride(pv.req, nil))
	var id int64
	if err := tx.QueryRow(r.Context(),
		`INSERT INTO vehicles (person_id, category_id, label, license_plate, notes,
		        billing_period, rate, cost_override, start_date, end_date, status,
		        reserved_from, reserved_until, needs_power, planner_symbol)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING id`,
		pv.req.PersonID, pv.req.CategoryID, pv.req.Label, pv.req.LicensePlate,
		pv.req.Notes, pv.req.BillingPeriod, rate, costOverride,
		pv.startDate, pv.endDate, pv.req.Status, pv.reservedFrom, pv.reservedUntil, pv.req.NeedsPower, pv.req.PlannerSymbol,
	).Scan(&id); err != nil {
		return 0, err
	}
	if err := h.recordStatusTx(r, tx, id, "", pv.req.Status, "angelegt"); err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateVehicle edits a vehicle.
func (h *Handler) UpdateVehicle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	pv, err := h.parseVehicleRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update vehicle")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var oldStatus, oldPeriod string
	if !lockFinancialSource(w, r, tx, "vehicle", id, pv.req.PersonID) {
		return
	}
	var oldRate float64
	var oldStart time.Time
	var oldPersonID int64
	var archived bool
	// Read the FULL editable row, not just the billing fields: a rename or a plate
	// change must be provable too, and the audit diff below compares every column
	// the UPDATE writes.
	var oldLabel, oldPlate, oldNotes string
	var oldCategoryID int64
	var oldCostOverride *float64
	var oldEnd, oldResFrom, oldResUntil *time.Time
	var oldNeedsPower bool
	var oldSymbol *string
	scanErr := tx.QueryRow(r.Context(),
		`SELECT status, rate, billing_period, start_date, person_id, archived,
		        label, license_plate, notes, category_id, cost_override,
		        end_date, reserved_from, reserved_until, needs_power, planner_symbol
		   FROM vehicles WHERE id=$1 FOR UPDATE`, id).
		Scan(&oldStatus, &oldRate, &oldPeriod, &oldStart, &oldPersonID, &archived,
			&oldLabel, &oldPlate, &oldNotes, &oldCategoryID, &oldCostOverride,
			&oldEnd, &oldResFrom, &oldResUntil, &oldNeedsPower, &oldSymbol)
	if !ensureVehicleWritable(w, archived, scanErr) {
		return
	}
	// Fallback is the existing rate, so an omitted rate keeps the locked price.
	if err := h.guardInvoicedPartialExtension(r.Context(), tx, "vehicle", id, oldEnd, pv.endDate, oldPeriod); err != nil {
		if !writeSettlementConflict(w, err) {
			serverError(w, r, "could not check billed rental end", err)
		}
		return
	}
	// Freeze the billing-defining fields once a period is invoiced: changing
	// rate/period/start/person would desync the period-keyed invoice_source lock
	// (re-billing an invoiced span) or make the balance disagree with the issued
	// document. Mirrors the agreement/recurring freeze. end_date stays editable
	// (collect/re-store), but must not RETRACT below an invoiced period.
	oldEffectiveRate, effectiveErr := storedEffectiveRate(r.Context(), tx, oldCategoryID, oldPeriod, oldRate, oldCostOverride)
	if effectiveErr != nil {
		writeError(w, http.StatusInternalServerError, "could not resolve existing vehicle rate")
		return
	}
	// An omitted price also preserves legacy bookings when their category or
	// billing period is edited. Resolve the old agreement before changing either
	// source of the fallback; only this explicitly edited row is normalized.
	rate := effectiveRate(pv.req, oldEffectiveRate)
	costOverride := markZeroRate(rate, persistedCostOverride(pv.req, oldCostOverride))
	newEffectiveRate, effectiveErr := storedEffectiveRate(r.Context(), tx, pv.req.CategoryID, pv.req.BillingPeriod, rate, costOverride)
	if effectiveErr != nil {
		if errors.Is(effectiveErr, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "category does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not resolve vehicle rate")
		return
	}
	billingChanged := newEffectiveRate != oldEffectiveRate || pv.req.BillingPeriod != oldPeriod ||
		!pv.startDate.Equal(oldStart) || pv.req.PersonID != oldPersonID
	if billingChanged {
		if locked, ierr := h.refFinanciallyLocked(r.Context(), tx, "vehicle", id); ierr != nil {
			writeError(w, http.StatusInternalServerError, "could not check invoices")
			return
		} else if locked {
			writeError(w, http.StatusConflict, "Gefährt ist fakturiert – Preis/Zeitraum nicht änderbar (Storno über die Rechnung)")
			return
		}
	}
	if pv.req.PersonID != oldPersonID {
		if referenced, rerr := h.vehicleOwnerReferenced(r.Context(), tx, id); rerr != nil {
			writeError(w, http.StatusInternalServerError, "could not check vehicle references")
			return
		} else if referenced {
			writeError(w, http.StatusConflict, "Fahrzeug mit Buchungen, Pauschale, Übergabeprotokoll oder Anhängen kann nicht direkt einer anderen Person zugeordnet werden")
			return
		}
	}
	if retract, ierr := h.endDateRetractsBelowInvoiced(r.Context(), tx, "vehicle", id, pv.endDate); ierr != nil {
		writeError(w, http.StatusInternalServerError, "could not check invoices")
		return
	} else if retract {
		writeError(w, http.StatusConflict, "Enddatum liegt vor einer fakturierten Periode – Storno über die Rechnung")
		return
	}
	ct, err := tx.Exec(r.Context(),
		`UPDATE vehicles SET person_id=$1, category_id=$2, label=$3, license_plate=$4,
		        notes=$5, billing_period=$6, rate=$7, cost_override=$8, start_date=$9,
		        end_date=$10, status=$11, reserved_from=$12, reserved_until=$13, needs_power=$14,
		        planner_symbol=$15, spot_id=CASE WHEN $11 IN ('collected','cancelled') THEN NULL ELSE spot_id END, updated_at=now()
		 WHERE id=$16`,
		pv.req.PersonID, pv.req.CategoryID, pv.req.Label, pv.req.LicensePlate,
		pv.req.Notes, pv.req.BillingPeriod, rate, costOverride,
		pv.startDate, pv.endDate, pv.req.Status, pv.reservedFrom, pv.reservedUntil, pv.req.NeedsPower, pv.req.PlannerSymbol, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "person or category does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update vehicle")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "vehicle not found")
		return
	}
	if oldStatus != pv.req.Status {
		if err := h.recordStatusTx(r, tx, id, oldStatus, pv.req.Status, "über Bearbeitung geändert"); err != nil {
			writeError(w, http.StatusInternalServerError, "could not update vehicle")
			return
		}
	}
	// status/rate/period/start/person were read above for the writability checks,
	// so the before/after of the billing-relevant fields costs no extra query.
	if err := h.auditChangeTx(r.Context(), tx, r, "update", "vehicle", id,
		"updated vehicle "+vehicleLabel(pv.req.Label, pv.req.LicensePlate), diffFields(
			map[string]any{"status": oldStatus, "rate": oldRate, "billing_period": oldPeriod,
				"start_date": oldStart.Format("2006-01-02"), "person_id": oldPersonID,
				"label": oldLabel, "license_plate": oldPlate, "notes": oldNotes,
				"category_id": oldCategoryID, "cost_override": oldCostOverride,
				"end_date": auditDate(oldEnd), "reserved_from": auditDate(oldResFrom),
				"reserved_until": auditDate(oldResUntil), "needs_power": oldNeedsPower,
				"planner_symbol": oldSymbol},
			map[string]any{"status": pv.req.Status, "rate": rate, "billing_period": pv.req.BillingPeriod,
				"start_date": pv.req.StartDate, "person_id": pv.req.PersonID,
				"label": pv.req.Label, "license_plate": pv.req.LicensePlate, "notes": pv.req.Notes,
				"category_id": pv.req.CategoryID, "cost_override": costOverride,
				"end_date": strPtr(pv.req.EndDate), "reserved_from": strPtr(pv.req.ReservedFrom),
				"reserved_until": strPtr(pv.req.ReservedUntil), "needs_power": pv.req.NeedsPower,
				"planner_symbol": pv.req.PlannerSymbol})); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update vehicle")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update vehicle")
		return
	}
	// An edit can change status/paid-relevant state too — keep archival behavior
	// consistent with the status-slider endpoint.
	h.autoArchiveIfClosed(r, id)
	h.writeVehicle(w, r.Context(), id, http.StatusOK)
}

type vehiclePlannerRequest struct {
	NeedsPower    bool    `json:"needs_power"`
	PlannerSymbol *string `json:"planner_symbol"`
}

// UpdateVehiclePlanner edits ONLY the Garagenplaner display attributes (Ladebedarf +
// symbol override) so they can be changed straight from the Stellplatzverwaltung without
// the full vehicle payload. Purely organizational — no rate/period, so no freeze checks.
// It writes the same columns as UpdateVehicle, so both stay in sync.
func (h *Handler) UpdateVehiclePlanner(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req vehiclePlannerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sym, err := h.normPlannerSymbol(r.Context(), req.PlannerSymbol)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var prevPower bool
	var prevSymbol *string
	err = h.Pool.QueryRow(r.Context(),
		`WITH prev AS (SELECT needs_power, planner_symbol FROM vehicles WHERE id=$3)
		 UPDATE vehicles SET needs_power=$1, planner_symbol=$2, updated_at=now() WHERE id=$3 AND archived=false
		 RETURNING (SELECT needs_power FROM prev), (SELECT planner_symbol FROM prev)`,
		req.NeedsPower, sym, id).Scan(&prevPower, &prevSymbol)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "could not update vehicle")
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// No row updated: distinguish a missing vehicle (404) from an archived,
		// read-only one (409) — same contract as UpdateVehicle.
		var archived bool
		scanErr := h.Pool.QueryRow(r.Context(), `SELECT archived FROM vehicles WHERE id=$1`, id).Scan(&archived)
		if !ensureVehicleWritable(w, archived, scanErr) {
			return
		}
		writeError(w, http.StatusNotFound, "vehicle not found")
		return
	}
	h.auditChange(r, "update", "vehicle", id, "Planer-Attribute geändert", diffFields(
		map[string]any{"needs_power": prevPower, "planner_symbol": prevSymbol},
		map[string]any{"needs_power": req.NeedsPower, "planner_symbol": sym}))
	w.WriteHeader(http.StatusNoContent)
}

// DeleteVehicle removes a vehicle.
func (h *Handler) DeleteVehicle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete vehicle")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var lockedID int64
	if !lockFinancialSource(w, r, tx, "vehicle", id, 0) {
		return
	}
	if err := tx.QueryRow(r.Context(), `SELECT id FROM vehicles WHERE id=$1 FOR UPDATE`, id).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "vehicle not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not delete vehicle")
		return
	}
	if inv, ierr := h.refFinanciallyLocked(r.Context(), tx, "vehicle", id); ierr != nil {
		writeError(w, http.StatusInternalServerError, "could not delete vehicle")
		return
	} else if inv {
		writeError(w, http.StatusConflict, "Fahrzeug ist Teil einer ausgestellten Rechnung und kann nicht gelöscht werden (Storno statt Löschen).")
		return
	}
	// A Gefährt is master data: keep enough to identify it after the row is gone.
	var delLabel, delPlate, delStatus string
	var delPerson int64
	err = tx.QueryRow(r.Context(),
		`DELETE FROM vehicles WHERE id = $1 RETURNING label, license_plate, status, person_id`, id).
		Scan(&delLabel, &delPlate, &delStatus, &delPerson)
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusConflict,
				"Fahrzeug hat geschützte Belege oder Übergabeprotokolle und kann nicht gelöscht werden.")
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "vehicle not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not delete vehicle")
		return
	}
	name := delLabel
	if name == "" {
		name = delPlate
	}
	if err := h.auditDeletedTx(r.Context(), tx, r, "vehicle", id, "deleted vehicle "+name, map[string]any{
		"label": delLabel, "license_plate": delPlate, "status": delStatus, "person_id": delPerson,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete vehicle")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete vehicle")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

type statusRequest struct {
	Status string `json:"status"`
	Note   string `json:"note"`
	Date   string `json:"date"` // optional pickup/end date (YYYY-MM-DD) when collecting
}

// ChangeVehicleStatus transitions a vehicle to a new status and logs history.
func (h *Handler) ChangeVehicleStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req statusRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !models.ValidStatuses[req.Status] {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	// Validate the status transition note length up front to protect against
	// resource exhaustion (DoS) and database bloat.
	if !validNoteLength(req.Note) {
		writeError(w, http.StatusBadRequest, "notes is too long")
		return
	}
	var oldStatus string
	var oldEnd *time.Time // a status change can set or clear the end date — audit it
	var start time.Time
	var personID int64
	var archived bool
	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		serverError(w, r, "could not change status", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `SELECT person_id FROM vehicles WHERE id=$1`, id).Scan(&personID); err != nil {
		ensureVehicleWritable(w, false, err)
		return
	}
	if err := lockInvoicePersonTx(ctx, tx, personID); err != nil {
		serverError(w, r, "could not change status", err)
		return
	}
	var lockedPersonID int64
	scanErr := tx.QueryRow(ctx,
		`SELECT status, archived, end_date, start_date, person_id FROM vehicles WHERE id=$1 FOR UPDATE`, id).
		Scan(&oldStatus, &archived, &oldEnd, &start, &lockedPersonID)
	if !ensureVehicleWritable(w, archived, scanErr) {
		return
	}
	if lockedPersonID != personID {
		writeError(w, http.StatusConflict, "Kundenzuordnung wurde geändert – bitte neu laden")
		return
	}
	var billingPeriod string
	if err := tx.QueryRow(ctx, `SELECT billing_period FROM vehicles WHERE id=$1`, id).Scan(&billingPeriod); err != nil {
		serverError(w, r, "could not check rental period", err)
		return
	}
	date := trim(req.Date)
	var proposedEnd *time.Time
	if req.Status == models.StatusCollected || req.Status == models.StatusCancelled {
		end := businessDay(h.now())
		if oldEnd != nil {
			end = *oldEnd
		}
		if date != "" {
			if !validDateLength(date) {
				writeError(w, http.StatusBadRequest, "date is too long")
				return
			}
			parsed, err := time.Parse(dateLayout, date)
			if err != nil {
				writeError(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
				return
			}
			end = parsed
		}
		proposedEnd = &end
	}
	if err := h.guardInvoicedPartialExtension(ctx, tx, "vehicle", id, oldEnd, proposedEnd, billingPeriod); err != nil {
		if !writeSettlementConflict(w, err) {
			serverError(w, r, "could not check billed rental end", err)
		}
		return
	}
	// When collecting, set the pickup/end date: use the caller-supplied date if
	// given (allows back-dating), otherwise keep any existing end date or today.
	var ct pgconn.CommandTag
	var uerr error
	switch {
	// A closing status (collected OR cancelled) ends storage, so it MUST set an
	// end_date — otherwise rent keeps accruing forever on a cancelled vehicle that
	// is then archived and can never be invoiced/paid/cleared (perpetual phantom
	// receivable).
	case (req.Status == models.StatusCollected || req.Status == models.StatusCancelled) && date != "":
		end := *proposedEnd
		if end.Before(start) {
			writeError(w, http.StatusBadRequest, "Enddatum darf nicht vor Mietbeginn liegen")
			return
		}
		// A back-dated close must not fall below an already-invoiced period — that
		// would drop it from accrual while its payment stays → phantom Guthaben
		// (mirrors the UpdateVehicle guard).
		if retract, ierr := h.endDateRetractsBelowInvoiced(ctx, tx, "vehicle", id, &end); ierr != nil {
			writeError(w, http.StatusInternalServerError, "could not check invoices")
			return
		} else if retract {
			writeError(w, http.StatusConflict, "Enddatum liegt vor einer fakturierten Periode – Storno über die Rechnung")
			return
		}
		ct, uerr = tx.Exec(ctx,
			`UPDATE vehicles SET status=$1, end_date=$2, spot_id=NULL, updated_at=now() WHERE id=$3`,
			req.Status, end, id)
	case req.Status == models.StatusCollected || req.Status == models.StatusCancelled:
		end := businessDay(h.now())
		if oldEnd != nil {
			end = *oldEnd
		}
		// Canceling a future reservation closes it without inventing an invalid
		// rental interval; collection, in contrast, must have a valid actual date.
		if end.Before(start) && req.Status == models.StatusCancelled {
			end = start
		}
		if end.Before(start) {
			writeError(w, http.StatusBadRequest, "Enddatum darf nicht vor Mietbeginn liegen")
			return
		}
		if retract, err := h.endDateRetractsBelowInvoiced(ctx, tx, "vehicle", id, &end); err != nil {
			serverError(w, r, "could not check invoices", err)
			return
		} else if retract {
			writeError(w, http.StatusConflict, "Enddatum liegt vor einer fakturierten Periode – Storno über die Rechnung")
			return
		}
		ct, uerr = tx.Exec(ctx,
			`UPDATE vehicles SET status=$1, end_date=$2, spot_id=NULL, updated_at=now() WHERE id=$3`,
			req.Status, end, id)
	default:
		// stored / reserved re-open storage: clear any end_date left by a prior
		// collected/cancelled, otherwise CostInRange stays capped at the stale date
		// and the (now active-looking) vehicle silently accrues nothing.
		ct, uerr = tx.Exec(ctx,
			`UPDATE vehicles SET status=$1, end_date=NULL, updated_at=now() WHERE id=$2`, req.Status, id)
	}
	// Don't record history/audit for a write that never landed: a swallowed error
	// (or 0 rows) would claim a transition that didn't happen — and for a closing
	// status that means rent keeps accruing while the log says "collected".
	if uerr != nil {
		writeError(w, http.StatusInternalServerError, "could not change status")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "vehicle not found")
		return
	}
	if err := h.recordStatusTx(r, tx, id, oldStatus, req.Status, trim(req.Note)); err != nil {
		serverError(w, r, "could not record status history", err)
		return
	}
	// Re-read the end date: the branches above set, keep or clear it.
	var newEnd *time.Time
	if err := tx.QueryRow(ctx, `SELECT end_date FROM vehicles WHERE id=$1`, id).Scan(&newEnd); err != nil {
		serverError(w, r, "could not read status", err)
		return
	}
	if err := h.auditChangeTx(ctx, tx, r, "update", "vehicle", id,
		"Status Gefährt #"+strconv.FormatInt(id, 10)+": "+oldStatus+" → "+req.Status,
		diffFields(map[string]any{"status": oldStatus, "end_date": auditDate(oldEnd)},
			map[string]any{"status": req.Status, "end_date": auditDate(newEnd)})); err != nil {
		serverError(w, r, "could not record status audit", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, r, "could not change status", err)
		return
	}
	h.autoArchiveIfClosed(r, id)
	h.writeVehicle(w, r.Context(), id, http.StatusOK)
}

// VehicleHistory returns the status history of a vehicle, newest first.
func (h *Handler) VehicleHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, vehicle_id, old_status, new_status, note, changed_by, created_at
		 FROM vehicle_status_history WHERE vehicle_id=$1 ORDER BY created_at DESC`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []models.StatusChange{}
	for rows.Next() {
		var s models.StatusChange
		if err := rows.Scan(&s.ID, &s.VehicleID, &s.OldStatus, &s.NewStatus,
			&s.Note, &s.ChangedBy, &s.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) recordStatusTx(r *http.Request, q execer, vehicleID int64, oldStatus, newStatus, note string) error {
	by := ""
	if u, ok := auth.UserFrom(r.Context()); ok {
		by = u.Username
	}
	_, err := q.Exec(r.Context(),
		`INSERT INTO vehicle_status_history (vehicle_id, old_status, new_status, note, changed_by)
		 VALUES ($1,$2,$3,$4,$5)`, vehicleID, oldStatus, newStatus, note, by)
	return err
}

type paidRequest struct {
	Paid bool `json:"paid"`
}

// MarkPaid sets the simple open/paid marker on a vehicle.
func (h *Handler) MarkPaid(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req paidRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var archived, curPaid bool
	var personID int64
	var curThrough *time.Time
	scanErr := h.Pool.QueryRow(r.Context(),
		`SELECT archived, paid, person_id, paid_through FROM vehicles WHERE id=$1`, id).
		Scan(&archived, &curPaid, &personID, &curThrough)
	if !ensureVehicleWritable(w, archived, scanErr) {
		return
	}
	// P2.3: read the owed amount and record the auto-payment while still open,
	// then flip the flag in the same transaction. Read through tx after taking
	// the person/row locks so the amount is current without another pool connection.
	today := businessDay(h.now())
	newThrough := curThrough // unchanged unless the flag flips
	failMsg := "could not update payment status"
	if err := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		// CreateInvoice's per-person lock first, then the invoice check: an invoice
		// can no longer claim the vehicle's periods between the check and the flag
		// (BIL-05).
		if err := lockInvoicePersonTx(r.Context(), tx, personID); err != nil {
			return err
		}
		var lockedPerson int64
		if err := tx.QueryRow(r.Context(), `SELECT person_id,archived,paid,paid_through FROM vehicles WHERE id=$1 FOR UPDATE`, id).
			Scan(&lockedPerson, &archived, &curPaid, &curThrough); err != nil {
			return err
		}
		if lockedPerson != personID || archived {
			return &settlementConflictError{msg: "Gefährt wurde zwischenzeitlich geändert – bitte neu laden"}
		}
		newThrough = curThrough
		var amt float64
		var bound []boundCharge
		if req.Paid && !curPaid {
			var err error
			amt, bound, err = h.toggleOwedFrom(r, tx, "vehicle", id, personID)
			if err != nil {
				return err
			}
		}
		if req.Paid && !curPaid {
			// A vehicle already billed by an active invoice is settled through that
			// invoice. Marking it "bezahlt" would record no payment (it's excluded from
			// openOwedItems) yet settle rent the invoice bills: block the slider.
			// Fail CLOSED: a query error must not skip the guard.
			if inv, err := periodInvoicedTx(r.Context(), tx, "vehicle", id, ""); err != nil {
				failMsg = "could not verify invoice status"
				return err
			} else if inv {
				return &settlementConflictError{"Fahrzeug ist bereits fakturiert – über die Rechnung begleichen"}
			}
			if err := h.syncTogglePaymentTx(r.Context(), tx, "vehicle", id, personID, true, amt, bound); err != nil {
				failMsg = "could not record payment"
				return err
			}
			// The settlement pays the rent accrued through today — paid_through records
			// exactly that, so later rent is owed and invoiced again (BIL-02). If a
			// payment already claimed the vehicle, its booking day is the boundary.
			through := today
			var claimedAt *time.Time
			if err := tx.QueryRow(r.Context(),
				`SELECT max(created_at) FROM payment_allocations WHERE kind='vehicle' AND ref_id=$1`, id).
				Scan(&claimedAt); err != nil {
				return err
			}
			if claimedAt != nil {
				if d := businessDay(claimedAt.In(h.now().Location())); d.Before(through) {
					through = d
				}
			}
			if _, err := tx.Exec(r.Context(),
				`UPDATE vehicles SET paid=true, paid_through=$2, updated_at=now() WHERE id=$1`, id, through); err != nil {
				return err
			}
			newThrough = &through
			return nil
		}
		if !req.Paid && curPaid {
			// Money a regular payment allocated to the vehicle can't be toggled away:
			// the allocation would survive with the flag open, and the vehicle would be
			// both paid and billed. Reverse (storno) that payment instead.
			var manual bool
			if err := tx.QueryRow(r.Context(),
				`SELECT EXISTS(SELECT 1 FROM payment_allocations a JOIN payments p ON p.id=a.payment_id
				   WHERE a.kind='vehicle' AND a.ref_id=$1 AND NOT p.auto)`, id).Scan(&manual); err != nil {
				return err
			}
			if manual {
				return &settlementConflictError{"Fahrzeug wurde über eine Zahlung beglichen – bitte die Zahlung stornieren"}
			}
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE vehicles SET paid=$1, paid_through=CASE WHEN $1 THEN paid_through END, updated_at=now() WHERE id=$2`,
			req.Paid, id); err != nil {
			return err
		}
		if !req.Paid {
			newThrough = nil
		}
		if !req.Paid && curPaid {
			// Don't swallow this: if removing the auto-payment fails the flag flips
			// open with it (same tx) — a discarded error would leave a phantom
			// money-in inflating the balance.
			if err := h.syncTogglePaymentTx(r.Context(), tx, "vehicle", id, personID, false, 0, nil); err != nil {
				failMsg = "could not reverse payment"
				return err
			}
		}
		return nil
	}); err != nil {
		var conflict *settlementConflictError
		if errors.As(err, &conflict) {
			writeError(w, http.StatusConflict, conflict.msg)
			return
		}
		if writeSettlementConflict(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, failMsg)
		return
	}
	label := "offen"
	if req.Paid {
		label = "bezahlt"
	}
	h.auditChange(r, "update", "vehicle", id, "Zahlung "+h.vehicleDesc(r, id)+": "+label,
		diffFields(map[string]any{"paid": curPaid, "paid_through": dateOrNil(curThrough)},
			map[string]any{"paid": req.Paid, "paid_through": dateOrNil(newThrough)}))
	h.autoArchiveIfClosed(r, id)
	h.writeVehicle(w, r.Context(), id, http.StatusOK)
}

// ReactivateVehicle lifts a vehicle out of the archive, making it active and
// editable again. Used by the one-tap "Reaktivieren" action.
func (h *Handler) ReactivateVehicle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var prevArchived bool
	err := h.Pool.QueryRow(r.Context(),
		`WITH prev AS (SELECT archived FROM vehicles WHERE id=$1)
		 UPDATE vehicles SET archived=false, updated_at=now() WHERE id=$1
		 RETURNING (SELECT archived FROM prev)`, id).Scan(&prevArchived)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "vehicle not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not reactivate vehicle")
		return
	}
	h.auditChange(r, "update", "vehicle", id, "Gefährt reaktiviert: "+h.vehicleDesc(r, id),
		diffFields(map[string]any{"archived": prevArchived}, map[string]any{"archived": false}))
	h.writeVehicle(w, r.Context(), id, http.StatusOK)
}

// vehicleDesc returns a "label (person)" description for audit messages.
func (h *Handler) vehicleDesc(r *http.Request, id int64) string {
	var label, plate, first, last string
	_ = h.Pool.QueryRow(r.Context(),
		`SELECT v.label, v.license_plate, p.first_name, p.last_name
		 FROM vehicles v JOIN persons p ON p.id = v.person_id WHERE v.id=$1`, id,
	).Scan(&label, &plate, &first, &last)
	name := trim(first + " " + last)
	desc := vehicleLabel(label, plate)
	if name != "" {
		desc += " (" + name + ")"
	}
	return desc
}

type duplicateRequest struct {
	StartDate string `json:"start_date"`
	// Saisonwechsel (Ausbau 21), beide optional und standardmäßig aus:
	// CurrentTariff nimmt den heutigen Tarifpreis statt des vereinbarten Preises,
	// Reserve legt die Kopie als Reservierung ab dem Beginn an statt eingelagert.
	CurrentTariff bool `json:"current_tariff"`
	Reserve       bool `json:"reserve"`
}

// DuplicateVehicle re-stores a vehicle for a new period, reusing all master
// data (type, plate, label, pricing, notes) and copying its photos. Used for
// recurring seasonal storage so nothing has to be re-entered.
func (h *Handler) DuplicateVehicle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req duplicateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	start := businessDay(h.now())
	if trim(req.StartDate) != "" {
		if !validDateLength(trim(req.StartDate)) {
			writeError(w, http.StatusBadRequest, "start_date is too long")
			return
		}
		s, err := time.Parse(dateLayout, trim(req.StartDate))
		if err != nil {
			writeError(w, http.StatusBadRequest, "start_date must be YYYY-MM-DD")
			return
		}
		start = s
	}

	ctx := r.Context()
	tx, err := h.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		serverError(w, r, "could not duplicate vehicle", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var categoryID int64
	var period string
	var sourceRate float64
	var sourceOverride *float64
	// Nach einer Wertsicherung (Ausbau 29) ist der zuletzt angepasste Satz der
	// vereinbarte Preis, nicht mehr der ursprüngliche.
	if err := tx.QueryRow(ctx,
		`SELECT v.category_id, v.billing_period,
		        COALESCE((SELECT rc.rate FROM vehicle_rate_changes rc WHERE rc.vehicle_id = v.id ORDER BY rc.effective_from DESC LIMIT 1), v.rate),
		        v.cost_override
		   FROM vehicles v WHERE v.id=$1`, id).
		Scan(&categoryID, &period, &sourceRate, &sourceOverride); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "vehicle not found")
			return
		}
		serverError(w, r, "could not read vehicle price", err)
		return
	}
	// Re-storage deliberately copies the agreed price, including individual and
	// free prices. A legacy fallback must become a fixed price on the NEW copy,
	// not another booking whose amount follows future tariff changes.
	rate, err := storedEffectiveRate(ctx, tx, categoryID, period, sourceRate, sourceOverride)
	if err != nil {
		serverError(w, r, "could not resolve vehicle price", err)
		return
	}
	costOverride := markZeroRate(rate, sourceOverride)
	if req.CurrentTariff {
		// Der heutige Tarif statt des Vorjahrespreises; wie beim Anlegen festgeschrieben.
		def, derr := categoryDefaultRate(ctx, tx, categoryID, period)
		if derr != nil {
			serverError(w, r, "could not resolve tariff", derr)
			return
		}
		rate = def
		costOverride = markZeroRate(rate, nil)
	}
	status := models.StatusStored
	var reservedFrom *time.Time
	if req.Reserve {
		status = models.StatusReserved
		reservedFrom = &start
	}
	var newID int64
	err = tx.QueryRow(ctx,
		`INSERT INTO vehicles (person_id, category_id, label, license_plate, notes,
		        billing_period, rate, cost_override, start_date, status, reserved_from,
		        length_m, width_m, height_m, weight_t, needs_power, planner_symbol)
		 SELECT person_id, category_id, label, license_plate, notes,
		        billing_period, $3, $4, $2, $5, $6,
		        length_m, width_m, height_m, weight_t, needs_power, planner_symbol
		 FROM vehicles WHERE id = $1
		 RETURNING id`, id, start, rate, costOverride, status, reservedFrom).Scan(&newID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "vehicle not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not duplicate vehicle")
		return
	}

	// Copy the photos so the new period reuses the existing images. Nur die Galerie:
	// Protokollfotos gehören zum Protokoll der alten Einlagerung (Ausbau 24).
	if _, err := tx.Exec(ctx,
		`INSERT INTO vehicle_photos (vehicle_id, filename, content_type, byte_size, data, sort_order)
		 SELECT $1, filename, content_type, byte_size, data, sort_order
		 FROM vehicle_photos WHERE vehicle_id = $2 AND handover_id IS NULL ORDER BY sort_order, id`, newID, id); err != nil {
		serverError(w, r, "could not copy vehicle photos", err)
		return
	}
	if err := h.recordStatusTx(r, tx, newID, "", status, "erneut eingestellt"); err != nil {
		serverError(w, r, "could not copy vehicle history", err)
		return
	}
	if err := h.auditCreatedTx(ctx, tx, r, "vehicle", newID, "re-stored vehicle from #"+strconv.FormatInt(id, 10),
		map[string]any{"restored_from_vehicle_id": id, "status": status, "rate": rate, "current_tariff": req.CurrentTariff}); err != nil {
		serverError(w, r, "could not record vehicle copy", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, r, "could not duplicate vehicle", err)
		return
	}
	h.writeVehicle(w, r.Context(), newID, http.StatusCreated)
}

// writeVehicle re-reads a single vehicle with joins and writes it as JSON,
// including its flat-rate coverage fields so single-vehicle responses agree
// with the list endpoints (paid-slider visibility depends on them).
func (h *Handler) writeVehicle(w http.ResponseWriter, ctx context.Context, id int64, status int) {
	row := h.Pool.QueryRow(ctx, vehicleSelect+` WHERE v.id = $1`, id)
	v, cat, err := scanVehicleRow(row)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load vehicle")
		return
	}
	now := h.now()
	enrich(&v, cat, now)
	// Coverage must be accurate — a silent fallback would report a bound vehicle
	// as uncovered (wrong paid-slider/amounts), so fail loudly instead.
	agByPerson, err := h.loadAllAgreements(ctx, v.PersonID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load vehicle")
		return
	}
	vs := []models.Vehicle{v}
	setFlatRateCoverage(vs, agByPerson, now)
	if err := h.setVehicleInvoiceStatus(ctx, vs); err != nil {
		writeError(w, http.StatusInternalServerError, "could not load vehicle")
		return
	}
	writeJSON(w, status, vs[0])
}

// rowScanner unifies pgx.Row and pgx.Rows for scanning helpers.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanVehicleRow reads one vehicle row together with its category.
func scanVehicleRow(row rowScanner) (models.Vehicle, models.Category, error) {
	var v models.Vehicle
	var cat models.Category
	var firstName, lastName string
	var hallName *string
	var rateChanges, pauses []byte
	err := row.Scan(&v.ID, &v.PersonID, &v.CategoryID, &v.Label, &v.LicensePlate,
		&v.Notes, &v.BillingPeriod, &v.Rate, &v.CostOverride, &v.StartDate, &v.EndDate,
		&v.Status, &v.ReservedFrom, &v.ReservedUntil, &v.Paid, &v.Archived, &v.CreatedAt, &v.UpdatedAt,
		&v.NeedsPower, &v.PlannerSymbol,
		&cat.Name, &cat.DefaultMonthlyCost, &cat.DefaultYearlyCost,
		&firstName, &lastName, &v.PhotoCount,
		&v.SpotID, &v.HallID, &hallName, &v.PaidThrough, &rateChanges, &pauses)
	if err != nil {
		return v, cat, err
	}
	// Preisänderungen der Wertsicherung (Ausbau 29) gehören zur Abrechnung des
	// Gefährts: jede Ladung über vehicleSelect bringt sie mit.
	if len(rateChanges) > 0 {
		if err := json.Unmarshal(rateChanges, &v.RateChanges); err != nil {
			return v, cat, err
		}
	}
	// Ruhezeiten (Kurz weg): die Abrechnung ruht in ihnen, die Oberfläche zeigt sie.
	if len(pauses) > 0 {
		if err := json.Unmarshal(pauses, &v.Pauses); err != nil {
			return v, cat, err
		}
	}
	cat.ID = v.CategoryID
	v.CategoryName = cat.Name
	v.PersonName = trim(firstName + " " + lastName)
	if hallName != nil {
		v.HallName = *hallName
	}
	return v, cat, nil
}

// archiveInvoiceSettledCollected archives a person's collected vehicles that are
// now settled through fully-paid invoice(s) — called after an invoice payment so
// paying the invoice closes the vehicle out, mirroring autoArchiveIfClosed's
// invoice branch for the bulk (one payment can settle several invoices/vehicles).
func (h *Handler) archiveInvoiceSettledCollected(ctx context.Context, personID int64) {
	err := pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
		if err := lockInvoicePersonTx(ctx, tx, personID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE vehicles v SET archived=true, updated_at=now()
		  WHERE v.person_id=$1 AND v.archived=false AND v.status='collected'
		    AND (`+vehicleArchivePredicate+`)`, personID, businessDay(h.now()))
		return err
	})
	if err != nil {
		slog.Error("invoice vehicle auto-archive failed", "person_id", personID, "err", err)
	}
}

// setVehicleInvoiceStatus fills Invoiced/InvoiceOpen for each vehicle from the
// invoice_source → invoices join: Invoiced when a non-canceled invoice bills it,
// InvoiceOpen when at least one covering invoice is not fully paid. One batched
// query for the whole slice.
func (h *Handler) setVehicleInvoiceStatus(ctx context.Context, vehicles []models.Vehicle) error {
	return h.setVehicleInvoiceStatusFrom(ctx, h.Pool, vehicles)
}

func (h *Handler) setVehicleInvoiceStatusFrom(ctx context.Context, q dbQuerier, vehicles []models.Vehicle) error {
	if len(vehicles) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(vehicles))
	for i := range vehicles {
		ids = append(ids, vehicles[i].ID)
	}
	rows, err := q.Query(ctx,
		`SELECT s.ref_id, count(*) FILTER (WHERE (i.total - i.paid_amount) > 0.005) AS open_n
		   FROM invoice_source s JOIN invoices i ON i.id = s.invoice_id
		  WHERE s.kind='vehicle' AND NOT i.canceled AND s.ref_id = ANY($1)
		  GROUP BY s.ref_id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	openN := map[int64]int{}
	for rows.Next() {
		var ref int64
		var n int
		if err := rows.Scan(&ref, &n); err != nil {
			return err
		}
		openN[ref] = n // presence of the key = invoiced (≥1 covering invoice)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range vehicles {
		if n, ok := openN[vehicles[i].ID]; ok {
			vehicles[i].Invoiced = true
			vehicles[i].InvoiceOpen = n > 0
		}
	}
	return nil
}

// enrich fills in derived cost/status fields on a vehicle as of the given time.
func enrich(v *models.Vehicle, cat models.Category, now time.Time) {
	// Der heute gültige Satz: nach einer Wertsicherung nicht mehr der vereinbarte.
	v.EffectiveRate = v.RateAt(cat, now)
	v.AccruedCost = round2(v.AccruedCostAsOf(cat, now))
	v.IsActive = v.Status == models.StatusStored || v.Status == models.StatusReserved
}

// dateOrNil renders a nullable DATE for an audit diff.
func dateOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format(dateLayout)
}

func vehicleLabel(label, plate string) string {
	if label != "" {
		return label
	}
	if plate != "" {
		return plate
	}
	return "(ohne Bezeichnung)"
}

func round2(f float64) float64 {
	// math.Round is half-away-from-zero, so negative (overpaid) balances round
	// correctly; the old int64(f*100+0.5) truncated toward zero for negatives.
	return math.Round(f*100) / 100
}
