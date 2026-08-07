package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// addPeriodPayment routes a loaded period-payment row onto an agreement: a NULL
// amount is a whole-period (prepaid) payment, a set amount is a fixed partial.
func addPeriodPayment(a *models.FlatRatePeriod, key string, amount *float64) {
	if amount == nil {
		a.PaidPeriods = append(a.PaidPeriods, key)
		return
	}
	if a.PaidFixed == nil {
		a.PaidFixed = map[string]float64{}
	}
	a.PaidFixed[key] = *amount
}

// loadAgreements returns a person's flat-rate agreements with their covered
// vehicle ids and derived accrued cost (as of now), newest first.
// attachVehiclesAndPayments fills VehicleIDs and per-period payments for every
// agreement in byID (keyed by the id set ids). Two batched queries, no N+1;
// shared by loadAgreements and loadAllAgreements.
func (h *Handler) attachVehiclesAndPayments(ctx context.Context,
	byID map[int64]*models.FlatRatePeriod, ids []int64) error {

	vrows, err := h.Pool.Query(ctx,
		`SELECT period_id, vehicle_id FROM flat_rate_period_vehicles WHERE period_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer vrows.Close()
	for vrows.Next() {
		var pid, vid int64
		if err := vrows.Scan(&pid, &vid); err != nil {
			return err
		}
		if a := byID[pid]; a != nil {
			a.VehicleIDs = append(a.VehicleIDs, vid)
		}
	}
	if err := vrows.Err(); err != nil {
		return err
	}
	vrows.Close()

	prows, err := h.Pool.Query(ctx,
		`SELECT period_id, period_key, amount FROM flat_rate_period_payments WHERE period_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer prows.Close()
	for prows.Next() {
		var pid int64
		var key string
		var amount *float64
		if err := prows.Scan(&pid, &key, &amount); err != nil {
			return err
		}
		if a := byID[pid]; a != nil {
			addPeriodPayment(a, key, amount)
		}
	}
	return prows.Err()
}

func (h *Handler) loadAgreements(ctx context.Context, personID int64, now time.Time) ([]models.FlatRatePeriod, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT id, person_id, amount, period, start_date, end_date, paid, note, created_at, updated_at
		 FROM flat_rate_periods WHERE person_id=$1 ORDER BY start_date DESC, id DESC`, personID)
	if err != nil {
		return nil, err
	}
	out := []models.FlatRatePeriod{}
	ids := []int64{}
	for rows.Next() {
		var a models.FlatRatePeriod
		if err := rows.Scan(&a.ID, &a.PersonID, &a.Amount, &a.Period, &a.StartDate,
			&a.EndDate, &a.Paid, &a.Note, &a.CreatedAt, &a.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		a.VehicleIDs = []int64{}
		a.PaidPeriods = []string{}
		out = append(out, a)
		ids = append(ids, a.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		byID := make(map[int64]*models.FlatRatePeriod, len(out))
		for i := range out {
			byID[out[i].ID] = &out[i]
		}
		if err := h.attachVehiclesAndPayments(ctx, byID, ids); err != nil {
			return nil, err
		}
	}
	for i := range out {
		out[i].Accrued = round2(out[i].AccruedAsOf(now))
		out[i].Settled = out[i].SettledAsOf(now)
		out[i].PeriodCosts = out[i].ElapsedPeriodCosts(now)
	}
	if err := h.setAgreementInvoiceStatus(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// setAgreementInvoiceStatus marks, per agreement, which sub-periods are settled
// through a fully-paid Rechnung (invoice_source kind='agreement' × a non-canceled,
// fully-paid invoice) — the Pauschale twin of setVehicleInvoiceStatus. A period
// billed on a paid invoice is settled even though its per-period flag is unset, so
// the progress bar must count it; otherwise an invoiced+paid Pauschale shows
// "0 % bezahlt".
func (h *Handler) setAgreementInvoiceStatus(ctx context.Context, agreements []models.FlatRatePeriod) error {
	if len(agreements) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(agreements))
	for i := range agreements {
		ids = append(ids, agreements[i].ID)
	}
	rows, err := h.Pool.Query(ctx,
		`SELECT s.ref_id, s.period_key FROM invoice_source s JOIN invoices i ON i.id = s.invoice_id
		  WHERE s.kind='agreement' AND NOT i.canceled AND (i.total - i.paid_amount) <= 0.005
		    AND s.ref_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	paid := map[int64][]string{}
	for rows.Next() {
		var ref int64
		var period string
		if err := rows.Scan(&ref, &period); err != nil {
			return err
		}
		paid[ref] = append(paid[ref], period)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range agreements {
		if keys, ok := paid[agreements[i].ID]; ok {
			agreements[i].InvoicePaidPeriods = keys
		}
	}
	return nil
}

// ListAgreements returns the flat-rate agreements of a person.
func (h *Handler) ListAgreements(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	list, err := h.loadAgreements(r.Context(), id, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type agreementRequest struct {
	Amount     *float64 `json:"amount"`
	Period     string   `json:"period"`
	StartDate  string   `json:"start_date"`
	EndDate    *string  `json:"end_date"`
	Note       string   `json:"note"`
	VehicleIDs []int64  `json:"vehicle_ids"`
	Paid       bool     `json:"paid"`
	// NewVehicles are devices created inline while building the Pauschale: real
	// vehicle records for the person, typed from a Tarif and stored from the
	// agreement's start date, then covered by the agreement.
	NewVehicles []newVehicleReq `json:"new_vehicles"`
	// EditVehicles carries edits to already-bound vehicles made in the unified
	// dialog (type/label/plate), so the Pauschale and its vehicles are managed in
	// one place. Ignored for ids that do not belong to the person.
	EditVehicles []editVehicleReq `json:"edit_vehicles"`
}

type newVehicleReq struct {
	CategoryID   int64  `json:"category_id"`
	Label        string `json:"label"`
	LicensePlate string `json:"license_plate"`
}

type editVehicleReq struct {
	ID           int64  `json:"id"`
	CategoryID   int64  `json:"category_id"`
	Label        string `json:"label"`
	LicensePlate string `json:"license_plate"`
}

// errNoCategory flags a missing/invalid Tarif for an inline vehicle.
var errNoCategory = errors.New("tariff does not exist")

// createdVehicle is an inline device created while saving a Pauschale.
type createdVehicle struct {
	id    int64
	label string
	plate string
}

// createVehiclesTx creates the inline "new devices" for an agreement as real
// vehicle records within the caller's transaction: person-owned, typed from a
// Tarif, stored from the agreement's start date with the Tarif's price locked
// on (Option A). Running in the same transaction as the agreement save means a
// later failure rolls the vehicles back instead of orphaning them.
func createVehiclesTx(ctx context.Context, tx pgx.Tx, personID int64, period string, start time.Time, news []newVehicleReq) ([]createdVehicle, error) {
	out := make([]createdVehicle, 0, len(news))
	for _, nv := range news {
		if nv.CategoryID <= 0 {
			return nil, errNoCategory
		}
		// Same lookup CreateVehicle uses, run inside this transaction. Classify a
		// missing Tarif here, at the source — the only place ErrNoRows means that.
		rate, err := categoryDefaultRate(ctx, tx, nv.CategoryID, period)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, errNoCategory
			}
			return nil, err
		}
		label, plate := trim(nv.Label), trim(nv.LicensePlate)
		var id int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO vehicles (person_id, category_id, label, license_plate, billing_period, rate, start_date, status)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,'stored') RETURNING id`,
			personID, nv.CategoryID, label, plate, period, rate, start).Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, createdVehicle{id: id, label: label, plate: plate})
	}
	return out, nil
}

// currentAgreementVehicles returns the vehicle ids currently covered by an
// agreement, read through the caller's tx.
func currentAgreementVehicles(ctx context.Context, tx pgx.Tx, id int64) ([]int64, error) {
	rows, err := tx.Query(ctx, `SELECT vehicle_id FROM flat_rate_period_vehicles WHERE period_id=$1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// sameInt64Set reports whether a and b contain the same ids (order-independent);
// both are expected deduplicated.
func sameInt64Set(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[int64]bool, len(a))
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

// agreementSaveError maps a saveAgreement failure to a client (message, status).
// errNoCategory is classified at its source (createVehiclesTx); a foreign-key
// violation means the person or a covered vehicle vanished mid-save — telling
// the user "tariff does not exist" for those would point at the wrong field.
func agreementSaveError(err error, fallback string) (string, int) {
	if errors.Is(err, errNoCategory) {
		return "tariff does not exist", http.StatusBadRequest
	}
	if errors.Is(err, errAgreementInvoiced) {
		return "Pauschale ist fakturiert – Betrag/Zeitraum/Gefährte nicht änderbar (Storno über die Rechnung)", http.StatusConflict
	}
	if isForeignKeyViolation(err) {
		return "person or vehicle does not exist", http.StatusBadRequest
	}
	return fallback, http.StatusInternalServerError
}

// errAgreementInvoiced: an edit tried to change the billing-defining fields
// (period/start/amount) of a Pauschale that already has an invoiced period —
// which would desync the period-keyed invoice_source lock or make the balance
// disagree with the issued document.
var errAgreementInvoiced = errors.New("agreement has an invoiced period")

// parse validates the request and returns a partially-filled agreement (without
// person id). VehicleIDs is normalised (deduplicated).
func (req *agreementRequest) parse() (models.FlatRatePeriod, string) {
	var a models.FlatRatePeriod
	if req.Amount == nil || *req.Amount <= 0 {
		return a, "amount must be greater than zero"
	}
	if *req.Amount > maxMoneyAmount {
		return a, "amount is out of range"
	}
	period := req.Period
	if period == "" {
		period = models.BillingMonthly
	}
	if period != models.BillingMonthly && period != models.BillingYearly {
		return a, "period must be 'monthly' or 'yearly'"
	}
	if !validDateLength(trim(req.StartDate)) {
		return a, "start_date is too long"
	}
	start, err := time.Parse(dateLayout, trim(req.StartDate))
	if err != nil {
		return a, "start_date must be YYYY-MM-DD"
	}
	end, err := parseOptDate(req.EndDate)
	if err != nil {
		return a, "end_date must be YYYY-MM-DD"
	}
	if end != nil && end.Before(start) {
		return a, "end_date must not be before start_date"
	}
	if !validNoteLength(req.Note) {
		return a, "note is too long"
	}
	// Vehicles created/edited inline via the Pauschale bypass parseVehicleRequest,
	// so their label/plate length is capped here too.
	for _, nv := range req.NewVehicles {
		if !validNameLength(trim(nv.Label)) {
			return a, "label is too long"
		}
		if !validNameLength(trim(nv.LicensePlate)) {
			return a, "license_plate is too long"
		}
	}
	for _, ev := range req.EditVehicles {
		if !validNameLength(trim(ev.Label)) {
			return a, "label is too long"
		}
		if !validNameLength(trim(ev.LicensePlate)) {
			return a, "license_plate is too long"
		}
	}
	seen := map[int64]bool{}
	vids := []int64{}
	for _, v := range req.VehicleIDs {
		if v > 0 && !seen[v] {
			seen[v] = true
			vids = append(vids, v)
		}
	}
	a.Amount, a.Period, a.StartDate, a.EndDate = *req.Amount, period, start, end
	a.Note, a.VehicleIDs, a.Paid = trim(req.Note), vids, req.Paid
	return a, ""
}

// agreementsOverlapInTime reports whether the two agreements' windows intersect.
// EndDate is INCLUSIVE (matching the billing window, which covers through the
// last day): the exclusive upper bound is EndDate+1, so two same-vehicle
// agreements handing off on date D (A ends D, B starts D) DO overlap — both bill
// D — and are correctly flagged as a conflict. nil = open-ended.
func agreementsOverlapInTime(a, b models.FlatRatePeriod) bool {
	aEnd, bEnd := farFuture, farFuture
	if a.EndDate != nil {
		aEnd = a.EndDate.AddDate(0, 0, 1)
	}
	if b.EndDate != nil {
		bEnd = b.EndDate.AddDate(0, 0, 1)
	}
	return a.StartDate.Before(bEnd) && b.StartDate.Before(aEnd)
}

// agreementsConflict reports whether a and b overlap in time AND share at least
// one covered vehicle (empty VehicleIDs = all vehicles).
func agreementsConflict(a, b models.FlatRatePeriod) bool {
	if !agreementsOverlapInTime(a, b) {
		return false // disjoint in time
	}
	if len(a.VehicleIDs) == 0 || len(b.VehicleIDs) == 0 {
		return true // one covers all vehicles
	}
	set := make(map[int64]bool, len(a.VehicleIDs))
	for _, v := range a.VehicleIDs {
		set[v] = true
	}
	for _, v := range b.VehicleIDs {
		if set[v] {
			return true
		}
	}
	return false
}

// checkOverlap returns an error message if the candidate conflicts with an
// existing agreement of the person (excludeID skips the row being updated).
func (h *Handler) checkOverlap(ctx context.Context, personID, excludeID int64, cand models.FlatRatePeriod, now time.Time) (string, error) {
	existing, err := h.loadAgreements(ctx, personID, now)
	if err != nil {
		return "", err
	}
	for i := range existing {
		if existing[i].ID == excludeID {
			continue
		}
		if agreementsConflict(cand, existing[i]) {
			return "overlaps an existing agreement for one or more of the same vehicles", nil
		}
	}
	return "", nil
}

// vehiclesBelongTo reports whether every id in vids is a vehicle of personID.
func (h *Handler) vehiclesBelongTo(ctx context.Context, personID int64, vids []int64) (bool, error) {
	if len(vids) == 0 {
		return true, nil
	}
	var n int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM vehicles WHERE id = ANY($1) AND person_id = $2`, vids, personID).Scan(&n); err != nil {
		return false, err
	}
	return n == len(vids), nil
}

// validateAgreement checks vehicle ownership and time/vehicle overlap. Returns a
// (clientMessage, httpStatus) to send, or ("", 0) when the candidate is valid.
func (h *Handler) validateAgreement(ctx context.Context, personID, excludeID int64, cand models.FlatRatePeriod, hasNew bool) (string, int) {
	ok, err := h.vehiclesBelongTo(ctx, personID, cand.VehicleIDs)
	if err != nil {
		return "query failed", http.StatusInternalServerError
	}
	if !ok {
		return "all covered vehicles must belong to this person", http.StatusBadRequest
	}
	// A new-devices-only agreement covers just those brand-new vehicles, so it
	// cannot clash with agreements listing specific existing vehicles — but a
	// person-wide agreement (empty VehicleIDs) covers the new vehicles too, so a
	// time overlap with one of those is still a conflict (double billing).
	if len(cand.VehicleIDs) == 0 && hasNew {
		existing, err := h.loadAgreements(ctx, personID, time.Now())
		if err != nil {
			return "query failed", http.StatusInternalServerError
		}
		for i := range existing {
			if existing[i].ID == excludeID || len(existing[i].VehicleIDs) > 0 {
				continue
			}
			if agreementsOverlapInTime(cand, existing[i]) {
				return "overlaps an existing agreement for one or more of the same vehicles", http.StatusConflict
			}
		}
		return "", 0
	}
	msg, err := h.checkOverlap(ctx, personID, excludeID, cand, time.Now())
	if err != nil {
		return "query failed", http.StatusInternalServerError
	}
	if msg != "" {
		return msg, http.StatusConflict
	}
	return "", 0
}

// CreateAgreement adds a flat-rate agreement to a person.
func (h *Handler) CreateAgreement(w http.ResponseWriter, r *http.Request) {
	pid, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	h.persistAgreement(w, r, 0, pid)
}

// UpdateAgreement edits an existing agreement.
func (h *Handler) UpdateAgreement(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var pid int64
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT person_id FROM flat_rate_periods WHERE id=$1`, id).Scan(&pid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agreement not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	h.persistAgreement(w, r, id, pid)
}

// persistAgreement runs the shared create/update pipeline: parse, validate,
// save in one transaction, then audit and an immediate archive sweep. id==0
// inserts (verb/audit wording and the failure message are derived from that);
// everything else is identical between the two entry points.
func (h *Handler) persistAgreement(w http.ResponseWriter, r *http.Request, id, pid int64) {
	create := id == 0
	var req agreementRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cand, msg := req.parse()
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if m, code := h.validateAgreement(r.Context(), pid, id, cand, len(req.NewVehicles) > 0); m != "" {
		writeError(w, code, m)
		return
	}
	// Inline devices are created inside saveAgreement's transaction and covered,
	// so a new-device-only Pauschale is scoped to them (not "all vehicles") and a
	// failure never leaves orphaned vehicles behind.
	failMsg := "could not update agreement"
	if create {
		failMsg = "could not create agreement"
	}
	if err := h.saveAgreement(r, id, pid, cand, req.NewVehicles, req.EditVehicles); err != nil {
		msg, code := agreementSaveError(err, failMsg)
		writeError(w, code, msg)
		return
	}
	// Audit entity_id mirrors the pre-refactor behavior: the person for a
	// create (the new agreement id isn't surfaced here), the agreement id for
	// an update.
	verb, verbWord, auditID := "update", "geändert", id
	if create {
		verb, verbWord, auditID = "create", "angelegt", pid
	}
	h.audit(r, verb, "flatrate", auditID, "Pauschale "+verbWord+" für "+h.personLabel(r, pid))
	// Archive bound vehicles right away if the agreement is now finished and
	// settled, rather than waiting for the periodic sweep.
	_, _ = h.ArchiveSettledExpiredVehicles(r.Context(), pid)
	h.writeAgreements(w, r, pid)
}

// saveAgreement inserts (id==0) or updates an agreement and replaces its covered
// vehicles, in one transaction. news creates inline devices; edits applies
// type/label/plate changes to already-bound vehicles (managed from the same
// dialog).
func (h *Handler) saveAgreement(r *http.Request, id, personID int64, a models.FlatRatePeriod, news []newVehicleReq, edits []editVehicleReq) error {
	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Create inline devices in this same transaction and cover them, so a later
	// failure rolls them back instead of leaving orphaned vehicles.
	created, err := createVehiclesTx(ctx, tx, personID, a.Period, a.StartDate, news)
	if err != nil {
		return err
	}
	for _, c := range created {
		a.VehicleIDs = append(a.VehicleIDs, c.id)
	}
	// Apply edits to already-bound vehicles (type/label/plate). Scoped to the
	// person, so a foreign id is silently ignored. A bad Tarif fails the tx.
	for _, ev := range edits {
		if ev.ID <= 0 || ev.CategoryID <= 0 {
			continue
		}
		if _, err := tx.Exec(ctx,
			`UPDATE vehicles SET category_id=$1, label=$2, license_plate=$3, updated_at=now()
			 WHERE id=$4 AND person_id=$5`,
			ev.CategoryID, trim(ev.Label), trim(ev.LicensePlate), ev.ID, personID); err != nil {
			return err
		}
	}

	if id == 0 {
		if err := tx.QueryRow(ctx,
			`INSERT INTO flat_rate_periods (person_id, amount, period, start_date, end_date, paid, note)
			 VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			personID, a.Amount, a.Period, a.StartDate, a.EndDate, a.Paid, a.Note).Scan(&id); err != nil {
			return err
		}
	} else {
		// Changing the billing period (monthly<->yearly) invalidates existing
		// per-period payment keys ("YYYY-MM" vs "YYYY"): they would linger but
		// never be counted by PaidCentsInRange. Clear them so the payment state
		// is visibly reset instead of silently uncounted.
		var oldPeriod string
		var oldStart time.Time
		var oldAmount float64
		if err := tx.QueryRow(ctx,
			`SELECT period, start_date, amount FROM flat_rate_periods WHERE id=$1 FOR UPDATE`, id).
			Scan(&oldPeriod, &oldStart, &oldAmount); err != nil {
			return err
		}
		// Freeze the billing-defining fields once a period is invoiced: changing
		// period/start/amount would desync the period-keyed invoice_source lock
		// (re-billing an invoiced span) or make the balance disagree with the
		// issued document. Note/vehicles/end_date stay editable.
		// Membership too: adding/removing a covered vehicle on an invoiced agreement
		// re-bills (an unbound vehicle then bills its period individually) or
		// phantom-credits (a newly-covered vehicle drops its individual accrual for
		// an already-invoiced period). And end_date must not retract below an
		// invoiced period (that drops it from accrual while its payment stays).
		curVeh, verr := currentAgreementVehicles(ctx, tx, id)
		if verr != nil {
			return verr
		}
		retract, rerr := h.endDateRetractsBelowInvoiced(ctx, tx, "agreement", id, a.EndDate)
		if rerr != nil {
			return rerr
		}
		if oldPeriod != a.Period || !oldStart.Equal(a.StartDate) || oldAmount != a.Amount ||
			!sameInt64Set(curVeh, a.VehicleIDs) || retract {
			if inv, ierr := h.refInvoiced(ctx, tx, "agreement", id); ierr != nil {
				return ierr
			} else if inv {
				return errAgreementInvoiced
			}
		}
		if oldPeriod != a.Period {
			if _, err := tx.Exec(ctx, `DELETE FROM flat_rate_period_payments WHERE period_id=$1`, id); err != nil {
				return err
			}
		}
		// Deliberately does NOT touch paid: the master flag is managed only via
		// the paid endpoints, and the edit form doesn't send it — writing it here
		// would silently reset a paid agreement to offen on every edit.
		if _, err := tx.Exec(ctx,
			`UPDATE flat_rate_periods SET amount=$1, period=$2, start_date=$3, end_date=$4,
			        note=$5, updated_at=now() WHERE id=$6`,
			a.Amount, a.Period, a.StartDate, a.EndDate, a.Note, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM flat_rate_period_vehicles WHERE period_id=$1`, id); err != nil {
			return err
		}
	}
	for _, vid := range a.VehicleIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO flat_rate_period_vehicles (period_id, vehicle_id) VALUES ($1,$2)
			 ON CONFLICT DO NOTHING`, id, vid); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// Post-commit, best-effort: status history + audit for the created devices.
	for _, c := range created {
		h.recordStatus(r, c.id, "", models.StatusStored, "über Pauschale angelegt")
		h.audit(r, "create", "vehicle", c.id, "created vehicle via Pauschale "+vehicleLabel(c.label, c.plate))
	}
	return nil
}

// DeleteAgreement removes an agreement.
// clearAgreementSettlementTx removes an agreement's auto settlement money before the
// row is deleted: un-settle the bound extras it paid, then delete its period + extras
// auto-payments. Without this a deleted paid Pauschale leaves orphan Zahlungseingänge
// that still count as money-in (phantom Guthaben).
func clearAgreementSettlementTx(ctx context.Context, tx pgx.Tx, id int64) error {
	if _, err := tx.Exec(ctx,
		`UPDATE charges SET paid=false WHERE id IN (
		    SELECT pa.ref_id FROM payment_allocations pa JOIN payments p ON p.id=pa.payment_id
		     WHERE pa.kind='charge' AND p.settles_kind='agreement' AND p.settles_ref=$1 AND p.settles_period='extras')`, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx,
		`DELETE FROM payments WHERE auto AND settles_kind='agreement' AND settles_ref=$1`, id)
	return err
}

func (h *Handler) DeleteAgreement(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	// An invoiced Pauschale must not be deleted: its invoice_source lock has no FK
	// to this row, so deleting it orphans the lock and severs the invoice→source
	// trail (BAO reconstruction). Mirror DeleteVehicle.
	if inv, err := h.refInvoiced(r.Context(), h.Pool, "agreement", id); err != nil {
		writeError(w, http.StatusInternalServerError, "could not check invoices")
		return
	} else if inv {
		writeError(w, http.StatusConflict, "Pauschale ist fakturiert – nicht löschbar (Storno über die Rechnung)")
		return
	}
	// Optionally delete the covered vehicles too; otherwise they are only unbound
	// (the join cascades) and fall back to individual billing.
	deleteVehicles := r.URL.Query().Get("delete_vehicles") == "true"
	var pid int64
	_ = h.Pool.QueryRow(r.Context(), `SELECT person_id FROM flat_rate_periods WHERE id=$1`, id).Scan(&pid)

	var affected int64
	if deleteVehicles {
		tx, err := h.Pool.Begin(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not delete agreement")
			return
		}
		defer func() { _ = tx.Rollback(r.Context()) }()
		if err := clearAgreementSettlementTx(r.Context(), tx, id); err != nil {
			writeError(w, http.StatusInternalServerError, "could not delete agreement")
			return
		}
		// Delete only vehicles exclusive to this agreement (their join rows
		// cascade). A vehicle shared with another agreement — possible for
		// non-overlapping windows — is left intact and merely unbound here.
		if _, err := tx.Exec(r.Context(),
			`DELETE FROM vehicles WHERE id IN (SELECT vehicle_id FROM flat_rate_period_vehicles WHERE period_id=$1)
			   AND id NOT IN (SELECT vehicle_id FROM flat_rate_period_vehicles WHERE period_id <> $1)`, id); err != nil {
			writeError(w, http.StatusInternalServerError, "could not delete vehicles")
			return
		}
		ct, err := tx.Exec(r.Context(), `DELETE FROM flat_rate_periods WHERE id=$1`, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not delete agreement")
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "could not delete agreement")
			return
		}
		affected = ct.RowsAffected()
	} else {
		txErr := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
			if err := clearAgreementSettlementTx(r.Context(), tx, id); err != nil {
				return err
			}
			ct, err := tx.Exec(r.Context(), `DELETE FROM flat_rate_periods WHERE id=$1`, id)
			if err != nil {
				return err
			}
			affected = ct.RowsAffected()
			return nil
		})
		if txErr != nil {
			writeError(w, http.StatusInternalServerError, "could not delete agreement")
			return
		}
	}
	if affected == 0 {
		writeError(w, http.StatusNotFound, "agreement not found")
		return
	}
	msg := "Pauschale gelöscht für " + h.personLabel(r, pid)
	if deleteVehicles {
		msg = "Pauschale inkl. Gefährte gelöscht für " + h.personLabel(r, pid)
	}
	h.audit(r, "delete", "flatrate", id, msg)
	// Coverage changed: kept vehicles may now be archive-eligible (or, no longer
	// covered by a finished agreement, due to wake) — reconcile immediately like
	// every other agreement mutation.
	_, _ = h.ArchiveSettledExpiredVehicles(r.Context(), pid)
	h.writeAgreements(w, r, pid)
}

type agreementPaidRequest struct {
	Paid bool `json:"paid"`
}

// SetAgreementPaid toggles a whole Pauschale paid via the master slider. "bezahlt"
// now moves real money, mirroring the per-item sliders: it books a real
// Zahlungseingang for every completed rent period AND settles the bound Zusatzkosten
// open on the covered vehicles at that moment (Fix 2 semantics, extended to the
// Pauschale). "offen" reverses both. Before this, the slider only flipped a flag and
// deleted the per-period rows — so a paid Pauschale showed no payment and left its
// extras owed.
func (h *Handler) SetAgreementPaid(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req agreementPaidRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx := r.Context()
	createdBy := createdByFrom(ctx)
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update agreement")
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// An invoiced Pauschale is settled through its Rechnung, not the master slider.
	// Block the toggle (both directions) when a non-canceled invoice bills any of its
	// periods: booking per-period payments here would double-count the invoice payment
	// (phantom Guthaben). Mirrors the per-period handler's fakturier guard.
	var invoiced bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM invoice_source s JOIN invoices i ON i.id=s.invoice_id
		   WHERE s.kind='agreement' AND s.ref_id=$1 AND NOT i.canceled)`, id).Scan(&invoiced); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update agreement")
		return
	}
	if invoiced {
		writeError(w, http.StatusConflict, "Pauschale ist fakturiert – über die Rechnung begleichen")
		return
	}

	// Set the flag and load the money fields in one shot — needed to book the real
	// per-period rent payments below, not just flip paid.
	var ag models.FlatRatePeriod
	if err := tx.QueryRow(ctx,
		`UPDATE flat_rate_periods SET paid=$1, updated_at=now() WHERE id=$2
		 RETURNING person_id, amount, period, start_date, end_date`,
		req.Paid, id).Scan(&ag.PersonID, &ag.Amount, &ag.Period, &ag.StartDate, &ag.EndDate); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agreement not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update agreement")
		return
	}
	ag.ID = id
	pid := ag.PersonID

	// "Reset all": drop the per-period rows and every real Zahlungseingang this
	// agreement's settlement had booked (rent periods + extras), then re-derive from
	// the new state. Also un-settles bound extras this agreement had settled — and
	// clears any orphan a per-period toggle followed by a master toggle left behind.
	if _, err := tx.Exec(ctx, `DELETE FROM flat_rate_period_payments WHERE period_id=$1`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update agreement")
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE charges SET paid=false WHERE id IN (
		    SELECT pa.ref_id FROM payment_allocations pa JOIN payments p ON p.id=pa.payment_id
		     WHERE pa.kind='charge' AND p.settles_kind='agreement' AND p.settles_ref=$1 AND p.settles_period='extras')`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update agreement")
		return
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM payments WHERE auto AND settles_kind='agreement' AND settles_ref=$1`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update agreement")
		return
	}

	if req.Paid {
		// (a) Rent: one real Zahlungseingang per COMPLETED elapsed period. A still-
		// running period keeps the off-book credit (its cost is not final yet).
		for _, per := range ag.ElapsedPeriodsDetailed(time.Now()) {
			if !per.Complete {
				continue
			}
			if err := recordPeriodPaymentTx(ctx, tx, pid, "agreement", id, per.Key, per.Cost, createdBy); err != nil {
				writeError(w, http.StatusInternalServerError, "could not update agreement")
				return
			}
		}
		// (b) Extras: settle the bound Zusatzkosten open on covered vehicles right now
		// (charges added later stay billable — Option A).
		if err := h.settleAgreementExtrasTx(ctx, tx, ag, createdBy); err != nil {
			writeError(w, http.StatusInternalServerError, "could not update agreement")
			return
		}
	}

	state := "offen"
	if req.Paid {
		state = "bezahlt"
	}
	// Audit inside the tx (before commit): the settlement change and its trail
	// commit together (C7 / BAO §131).
	if err := h.auditTx(ctx, tx, r, "update", "flatrate", id,
		"Pauschale "+personLabelTx(ctx, tx, pid)+" "+state); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update agreement")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update agreement")
		return
	}
	// Settling the last open period may finish the agreement -> archive vehicles.
	_, _ = h.ArchiveSettledExpiredVehicles(ctx, pid)
	h.writeAgreements(w, r, pid)
}

// settleAgreementExtrasTx books ONE auto-payment covering the bound Zusatzkosten
// currently open on the agreement's covered vehicles, allocates a line per charge,
// and marks them paid — the Pauschale-slider twin of the vehicle slider (Fix 2). A
// charge already settled elsewhere (its (kind,ref) allocation exists) is skipped, so
// the recorded amount matches exactly what this settles. Nothing open → no row. The
// payment carries settles_period='extras' so SetAgreementPaid can find and reverse
// exactly it on "offen".
func (h *Handler) settleAgreementExtrasTx(ctx context.Context, tx pgx.Tx, ag models.FlatRatePeriod, createdBy *int64) error {
	agreementID, personID := ag.ID, ag.PersonID
	// Covered vehicles: the explicit links, or — for a person-wide agreement with no
	// links — the person's vehicles that fall within the agreement's window.
	vrows, err := tx.Query(ctx, `SELECT vehicle_id FROM flat_rate_period_vehicles WHERE period_id=$1`, agreementID)
	if err != nil {
		return err
	}
	var vids []int64
	for vrows.Next() {
		var v int64
		if err := vrows.Scan(&v); err != nil {
			vrows.Close()
			return err
		}
		vids = append(vids, v)
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return err
	}

	// Open bound charges not already settled — neither by another payment allocation
	// nor by an invoice (an invoiced charge is settled through its Rechnung; settling
	// it again here would double-count it).
	const notSettled = `NOT c.paid
	     AND NOT EXISTS (SELECT 1 FROM payment_allocations pa WHERE pa.kind='charge' AND pa.ref_id=c.id)
	     AND NOT EXISTS (SELECT 1 FROM invoice_source s JOIN invoices i ON i.id=s.invoice_id
	                      WHERE s.kind='charge' AND s.ref_id=c.id AND NOT i.canceled)`
	var crows pgx.Rows
	if len(vids) == 0 {
		// Person-wide agreement: covers the person's vehicles that existed within its
		// window. A vehicle that began after the agreement's (inclusive) end date was
		// never covered (coveringAgreements' start guard), so its extras aren't settled.
		crows, err = tx.Query(ctx,
			`SELECT c.id, c.amount, c.quantity FROM charges c JOIN vehicles v ON v.id=c.vehicle_id
			  WHERE c.person_id=$1 AND `+notSettled+`
			    AND ($2::timestamptz IS NULL OR v.start_date <= $2)`, personID, ag.EndDate)
	} else {
		crows, err = tx.Query(ctx,
			`SELECT c.id, c.amount, c.quantity FROM charges c
			  WHERE c.person_id=$1 AND c.vehicle_id IS NOT NULL AND `+notSettled+`
			    AND c.vehicle_id = ANY($2)`, personID, vids)
	}
	if err != nil {
		return err
	}
	type ext struct {
		id    int64
		total float64
	}
	var exts []ext
	var total float64
	for crows.Next() {
		var cid int64
		var amt, qty float64
		if err := crows.Scan(&cid, &amt, &qty); err != nil {
			crows.Close()
			return err
		}
		if qty <= 0 {
			qty = 1
		}
		if t := round2(amt * qty); t > 0.005 {
			exts = append(exts, ext{cid, t})
			total += t
		}
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return err
	}
	if round2(total) < 0.005 {
		return nil
	}

	var payID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO payments (person_id, amount, method, note, auto, settles_kind, settles_ref, settles_period, created_by)
		 VALUES ($1,$2,'bar','Pauschale Zusatzkosten (Slider)',true,'agreement',$3,'extras',$4) RETURNING id`,
		personID, round2(total), agreementID, createdBy).Scan(&payID); err != nil {
		return err
	}
	for _, e := range exts {
		if _, err := tx.Exec(ctx,
			`INSERT INTO payment_allocations (payment_id, kind, ref_id, amount) VALUES ($1,'charge',$2,$3)
			 ON CONFLICT (kind, ref_id) DO NOTHING`, payID, e.id, e.total); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE charges SET paid=true WHERE id=$1`, e.id); err != nil {
			return err
		}
	}
	return nil
}

type agreementPeriodPaidRequest struct {
	PeriodKey string `json:"period_key"`
	Paid      bool   `json:"paid"`
	// Amount, when set on a paid=true request, records a fixed partial payment
	// ("Teilbetrag") for the period; nil means the whole period is paid (prepaid).
	Amount *float64 `json:"amount"`
}

// SetAgreementPeriodPaid marks a single sub-period (a year "YYYY" or month
// "YYYY-MM") of an agreement paid or open. Turning a period off while the master
// Paid flag is set first records every elapsed period as an explicit paid
// row and clears the master flag, so only the chosen period becomes open.
func (h *Handler) SetAgreementPeriodPaid(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req agreementPeriodPaidRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	key := trim(req.PeriodKey)
	if key == "" {
		writeError(w, http.StatusBadRequest, "period_key is required")
		return
	}
	// A set fixed partial must be positive and in range (a whole-period prepayment
	// omits Amount entirely) — symmetric with the recurring path.
	if req.Amount != nil && (*req.Amount <= 0 || *req.Amount > maxMoneyAmount) {
		writeError(w, http.StatusBadRequest, "amount is out of range")
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update payment")
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()

	// Read + lock the agreement inside the transaction so the master-flag branch
	// below acts on a consistent snapshot even under concurrent paid toggles.
	var a models.FlatRatePeriod
	if err := tx.QueryRow(r.Context(),
		`SELECT id, person_id, period, start_date, end_date, amount, paid
		 FROM flat_rate_periods WHERE id=$1 FOR UPDATE`, id).
		Scan(&a.ID, &a.PersonID, &a.Period, &a.StartDate, &a.EndDate, &a.Amount, &a.Paid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "agreement not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update payment")
		return
	}

	// The key must be one of the agreement's elapsed sub-periods — checked before
	// EITHER mutation path. On mark, a bad key would store a payment row that
	// PaidCentsInRange never counts (money recorded, balance unchanged); on
	// unmark, a bad key on a master-paid agreement would still materialize the
	// elapsed rows and clear the master flag — state changed by garbage input.
	valid := false
	for _, k := range a.ElapsedPeriodKeys(time.Now()) {
		if k == key {
			valid = true
			break
		}
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "period_key does not match an elapsed period of this agreement")
		return
	}
	// An invoiced period is settled through the invoice. Block BOTH directions:
	// marking would double-credit once the invoice is Storno'd; UNmarking would
	// drop a fixed partial that the invoice already billed around (open = cost −
	// partial), leaving a phantom debt equal to the partial. The invoice, not this
	// flag, governs a fakturierte Periode.
	{
		var locked bool
		if err := tx.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM invoice_source s JOIN invoices i ON i.id=s.invoice_id
			   WHERE s.kind='agreement' AND s.ref_id=$1 AND s.period_key=$2 AND NOT i.canceled)`, id, key).Scan(&locked); err != nil {
			writeError(w, http.StatusInternalServerError, "could not update payment")
			return
		}
		if locked {
			writeError(w, http.StatusConflict, "Periode ist fakturiert – über die Rechnung begleichen")
			return
		}
	}

	if req.Paid {
		// amount NULL = whole period (prepaid); a value = fixed partial. Upsert so
		// re-marking switches between the two.
		if _, err := tx.Exec(r.Context(),
			`INSERT INTO flat_rate_period_payments (period_id, period_key, amount) VALUES ($1,$2,$3)
			 ON CONFLICT (period_id, period_key) DO UPDATE SET amount = EXCLUDED.amount`, id, key, req.Amount); err != nil {
			writeError(w, http.StatusInternalServerError, "could not update payment")
			return
		}
		// Book the real Zahlungseingang mirroring this off-book settlement (Fix 1): a
		// completed whole period or an explicit partial. A still-running whole period
		// stays off-book (its cost isn't final); its payment is booked once complete.
		if amt, ok := periodPaymentAmount(a, key, req.Amount, time.Now()); ok {
			if err := recordPeriodPaymentTx(r.Context(), tx, a.PersonID, "agreement", id, key, amt, createdByFrom(r.Context())); err != nil {
				writeError(w, http.StatusInternalServerError, "could not update payment")
				return
			}
		} else if err := deletePeriodPaymentTx(r.Context(), tx, "agreement", id, key); err != nil {
			writeError(w, http.StatusInternalServerError, "could not update payment")
			return
		}
	} else {
		if a.Paid {
			if _, err := tx.Exec(r.Context(),
				`INSERT INTO flat_rate_period_payments (period_id, period_key)
				 SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`,
				id, a.ElapsedPeriodKeys(time.Now())); err != nil {
				writeError(w, http.StatusInternalServerError, "could not update payment")
				return
			}
			if _, err := tx.Exec(r.Context(),
				`UPDATE flat_rate_periods SET paid=false, updated_at=now() WHERE id=$1`, id); err != nil {
				writeError(w, http.StatusInternalServerError, "could not update payment")
				return
			}
		}
		if _, err := tx.Exec(r.Context(),
			`DELETE FROM flat_rate_period_payments WHERE period_id=$1 AND period_key=$2`, id, key); err != nil {
			writeError(w, http.StatusInternalServerError, "could not update payment")
			return
		}
		// Remove the real Zahlungseingang this period's settlement had booked (Fix 1).
		// Periods materialized from a cleared master flag stay off-book (no payment to
		// delete), so this only drops the one the toggle created.
		if err := deletePeriodPaymentTx(r.Context(), tx, "agreement", id, key); err != nil {
			writeError(w, http.StatusInternalServerError, "could not update payment")
			return
		}
	}
	// Audit inside the tx, before commit: the paid-state change and its trail
	// commit together (BAO §131).
	if err := h.auditTx(r.Context(), tx, r, "update", "flatrate", id,
		"Pauschale "+personLabelTx(r.Context(), tx, a.PersonID)+" "+key+": "+periodPaidAuditState(req.Paid, req.Amount)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update payment")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update payment")
		return
	}
	// Settling the last open period may finish the agreement -> archive vehicles.
	_, _ = h.ArchiveSettledExpiredVehicles(r.Context(), a.PersonID)
	h.writeAgreements(w, r, a.PersonID)
}

func (h *Handler) writeAgreements(w http.ResponseWriter, r *http.Request, personID int64) {
	list, err := h.loadAgreements(r.Context(), personID, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// loadAllAgreements returns agreements grouped by person id — every person's
// when personID is 0, or just that person's. The join tables are read scoped to
// the loaded agreement ids, so a person-scoped call never scans other people's
// join or payment rows.
func (h *Handler) loadAllAgreements(ctx context.Context, personID int64) (map[int64][]models.FlatRatePeriod, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT id, person_id, amount, period, start_date, end_date, paid FROM flat_rate_periods
		 WHERE $1 = 0 OR person_id = $1`, personID)
	if err != nil {
		return nil, err
	}
	out := map[int64][]models.FlatRatePeriod{}
	byID := map[int64]*models.FlatRatePeriod{}
	ids := []int64{}
	for rows.Next() {
		var a models.FlatRatePeriod
		if err := rows.Scan(&a.ID, &a.PersonID, &a.Amount, &a.Period, &a.StartDate, &a.EndDate, &a.Paid); err != nil {
			rows.Close()
			return nil, err
		}
		a.VehicleIDs = []int64{}
		a.PaidPeriods = []string{}
		out[a.PersonID] = append(out[a.PersonID], a)
		ids = append(ids, a.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	for pid := range out {
		for i := range out[pid] {
			byID[out[pid][i].ID] = &out[pid][i]
		}
	}
	if err := h.attachVehiclesAndPayments(ctx, byID, ids); err != nil {
		return nil, err
	}
	return out, nil
}

// setFlatRateCoverage fills each vehicle's derived flat-rate coverage fields
// (FlatRateCovered + UncoveredCost as of now) from the given per-person
// agreements and category lookup, so the UI can show an "in Pauschale" marker
// and defer payment to the agreement when a vehicle is fully covered.
func setFlatRateCoverage(vehicles []models.Vehicle, agByPerson map[int64][]models.FlatRatePeriod, now time.Time) {
	for i := range vehicles {
		v := &vehicles[i]
		covering := coveringAgreements(agByPerson[v.PersonID], v.ID, v.StartDate)
		v.FlatRateCovered = len(covering) > 0
		if v.FlatRateCovered {
			// Bound vehicles carry no individual cost — billed via the Pauschale.
			v.UncoveredCost = 0
			for j := range covering {
				if covering[j].ActiveAt(now) {
					v.FlatRateActive = true
					break
				}
			}
		}
	}
}

// coveringAgreements returns the agreements that cover the given vehicle. A
// vehicle that started after an agreement's (inclusive) end date never existed
// during that agreement, so it is not covered by it — this matters for
// person-wide agreements (empty VehicleIDs), which would otherwise implicitly
// "cover" vehicles created long after the agreement ended.
func coveringAgreements(agreements []models.FlatRatePeriod, vehicleID int64, vehicleStart time.Time) []models.FlatRatePeriod {
	var out []models.FlatRatePeriod
	for i := range agreements {
		a := &agreements[i]
		if !a.Covers(vehicleID) {
			continue
		}
		if a.EndDate != nil && vehicleStart.After(*a.EndDate) {
			continue // vehicle started after this agreement had already ended
		}
		out = append(out, agreements[i])
	}
	return out
}

// ArchiveSettledExpiredVehicles reconciles bound vehicles with their Pauschalen:
// a vehicle whose every covering agreement has ended AND is fully paid is
// archived (a finished-and-settled agreement tidies its vehicles away), and —
// symmetrically — an archived vehicle bound to an agreement that is open again
// (extended, or a payment taken back) is reactivated, unless it is physically
// closed itself (cancelled, or collected+paid). That makes the sweep reversible:
// an accidental "bezahlt" tap that archived the vehicles is undone by tapping
// back to "offen". personID > 0 scopes the sweep to that person (the common
// case after an agreement mutation); 0 sweeps everyone (background job).
// Returns the number of vehicles archived.
func (h *Handler) ArchiveSettledExpiredVehicles(ctx context.Context, personID int64) (int64, error) {
	now := time.Now()
	agByPerson, err := h.loadAllAgreements(ctx, personID)
	if err != nil {
		return 0, err
	}
	done := map[int64]bool{} // agreement id -> ended && settled
	for _, ags := range agByPerson {
		for i := range ags {
			done[ags[i].ID] = ags[i].Ended(now) && ags[i].SettledAsOf(now)
		}
	}
	// Candidate vehicles: vehicles of any person that has an agreement (covers
	// explicit joins and person-wide agreements alike).
	rows, err := h.Pool.Query(ctx,
		`SELECT v.id, v.person_id, v.start_date, v.archived, v.status, v.paid FROM vehicles v
		 WHERE EXISTS (SELECT 1 FROM flat_rate_periods p WHERE p.person_id = v.person_id)
		   AND ($1 = 0 OR v.person_id = $1)`, personID)
	if err != nil {
		return 0, err
	}
	var toArchive, toWake []int64
	for rows.Next() {
		var vid, pid int64
		var vstart time.Time
		var archived, paid bool
		var status string
		if err := rows.Scan(&vid, &pid, &vstart, &archived, &status, &paid); err != nil {
			rows.Close()
			return 0, err
		}
		covering := coveringAgreements(agByPerson[pid], vid, vstart)
		if len(covering) == 0 {
			continue // not bound to any agreement -> standalone
		}
		allDone := true
		for i := range covering {
			if !done[covering[i].ID] {
				allDone = false
				break
			}
		}
		closed := status == models.StatusCancelled || (status == models.StatusCollected && paid)
		if allDone && !archived {
			toArchive = append(toArchive, vid)
		} else if !allDone && archived && !closed {
			toWake = append(toWake, vid)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(toWake) > 0 {
		if _, err := h.Pool.Exec(ctx,
			`UPDATE vehicles SET archived=false, updated_at=now() WHERE id = ANY($1) AND archived=true`, toWake); err != nil {
			return 0, err
		}
	}
	if len(toArchive) == 0 {
		return 0, nil
	}
	ct, err := h.Pool.Exec(ctx,
		`UPDATE vehicles SET archived=true, updated_at=now() WHERE id = ANY($1) AND archived=false`, toArchive)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// personRent computes rent accrued and paid over [from, to) for a person:
// flat-rate agreements bill their amount, standalone vehicles bill per-vehicle,
// and a vehicle bound to any Pauschale bills nothing itself (ownership model —
// the agreement's flat amount is the only charge, regardless of how long each
// bound vehicle is stored).
func personRent(agreements []models.FlatRatePeriod, vehicles []models.Vehicle, cats map[int64]models.Category, from, to time.Time) (accrued float64) {
	for i := range agreements {
		accrued += agreements[i].CostInRange(from, to)
	}
	for i := range vehicles {
		v := &vehicles[i]
		// A vehicle bound to a Pauschale has no individual cost — it is billed
		// entirely through the agreement. Only standalone vehicles bill per-vehicle.
		if len(coveringAgreements(agreements, v.ID, v.StartDate)) > 0 {
			continue
		}
		accrued += v.CostInRange(cats[v.CategoryID], from, to)
	}
	return accrued
}
