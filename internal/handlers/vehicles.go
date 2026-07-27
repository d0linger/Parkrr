package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

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
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE vehicles SET archived=true, updated_at=now()
		 WHERE id=$1 AND archived=false
		   AND (status='cancelled' OR (status='collected' AND paid))`, id)
	if err == nil && ct.RowsAffected() > 0 {
		h.audit(r, "update", "vehicle", id, "Gefährt archiviert (abgeschlossen): "+h.vehicleDesc(r, id))
	}
}

// vehicleSelect is the shared column list + joins for reading vehicles,
// including a live photo count. Append WHERE/ORDER as needed.
const vehicleSelect = `SELECT v.id, v.person_id, v.category_id, v.label, v.license_plate,
	        v.notes, v.billing_period, v.rate, v.cost_override, v.start_date, v.end_date,
	        v.status, v.reserved_from, v.reserved_until, v.paid, v.archived, v.created_at, v.updated_at,
	        c.name, c.default_monthly_cost, c.default_yearly_cost,
	        p.first_name, p.last_name,
	        (SELECT count(*) FROM vehicle_photos vp WHERE vp.vehicle_id = v.id)
	 FROM vehicles v
	 JOIN categories c ON c.id = v.category_id
	 JOIN persons p ON p.id = v.person_id`

// ListVehicles returns vehicles, optionally filtered by ?person_id=.
func (h *Handler) ListVehicles(w http.ResponseWriter, r *http.Request) {
	var (
		rows pgx.Rows
		err  error
	)
	limit, offset := pageParams(r, 1000, 1000)
	var personID int64 // 0 = unfiltered
	if pid := r.URL.Query().Get("person_id"); pid != "" {
		n, perr := strconv.ParseInt(pid, 10, 64)
		if perr != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid person_id")
			return
		}
		personID = n
		rows, err = h.Pool.Query(r.Context(),
			vehicleSelect+` WHERE v.person_id = $1 ORDER BY v.start_date DESC, v.id DESC LIMIT $2 OFFSET $3`,
			personID, limit, offset)
	} else {
		rows, err = h.Pool.Query(r.Context(),
			vehicleSelect+` ORDER BY v.start_date DESC, v.id DESC LIMIT $1 OFFSET $2`, limit, offset)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	now := time.Now()
	vehicles := []models.Vehicle{}
	for rows.Next() {
		v, cat, err := scanVehicleRow(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		enrich(&v, cat, now)
		vehicles = append(vehicles, v)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "scan failed")
		return
	}
	rows.Close()

	// Derive per-vehicle flat-rate coverage so the list can show "in Pauschale"
	// and defer payment to the agreement where a vehicle is fully covered.
	// Scoped to the person when the request is filtered.
	agByPerson, err := h.loadAllAgreements(r.Context(), personID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	setFlatRateCoverage(vehicles, agByPerson, now)
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
}

type parsedVehicle struct {
	req           vehicleRequest
	startDate     time.Time
	endDate       *time.Time
	reservedFrom  *time.Time
	reservedUntil *time.Time
}

func parseOptDate(s *string) (*time.Time, error) {
	if s == nil || trim(*s) == "" {
		return nil, nil
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
	if req.CostOverride != nil && *req.CostOverride < 0 {
		return nil, errors.New("cost_override must not be negative")
	}
	if req.Rate != nil && *req.Rate < 0 {
		return nil, errors.New("rate must not be negative")
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

	start, err := time.Parse(dateLayout, trim(req.StartDate))
	if err != nil {
		return nil, errors.New("start_date must be YYYY-MM-DD")
	}
	endPtr, err := parseOptDate(req.EndDate)
	if err != nil {
		return nil, errors.New("end_date must be YYYY-MM-DD")
	}
	if endPtr != nil && endPtr.Before(start) {
		return nil, errors.New("end_date must not be before start_date")
	}
	resFrom, err := parseOptDate(req.ReservedFrom)
	if err != nil {
		return nil, errors.New("reserved_from must be YYYY-MM-DD")
	}
	resUntil, err := parseOptDate(req.ReservedUntil)
	if err != nil {
		return nil, errors.New("reserved_until must be YYYY-MM-DD")
	}
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

// rowQuerier is satisfied by both *pgxpool.Pool and pgx.Tx, so lookups can run
// standalone or inside a caller's transaction.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// categoryDefaultRate returns the current Tarif default for the billing period.
func categoryDefaultRate(ctx context.Context, q rowQuerier, categoryID int64, period string) (float64, error) {
	var monthly, yearly float64
	if err := q.QueryRow(ctx,
		`SELECT default_monthly_cost, default_yearly_cost FROM categories WHERE id=$1`, categoryID).
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
	var fallback float64
	if pv.req.Rate == nil && pv.req.CostOverride == nil {
		def, derr := categoryDefaultRate(r.Context(), h.Pool, pv.req.CategoryID, pv.req.BillingPeriod)
		if derr != nil {
			if errors.Is(derr, pgx.ErrNoRows) {
				writeError(w, http.StatusBadRequest, "category does not exist")
				return
			}
			writeError(w, http.StatusInternalServerError, "could not create vehicle")
			return
		}
		fallback = def
	}
	rate := effectiveRate(pv.req, fallback)
	var id int64
	err = h.Pool.QueryRow(r.Context(),
		`INSERT INTO vehicles (person_id, category_id, label, license_plate, notes,
		        billing_period, rate, cost_override, start_date, end_date, status,
		        reserved_from, reserved_until)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`,
		pv.req.PersonID, pv.req.CategoryID, pv.req.Label, pv.req.LicensePlate,
		pv.req.Notes, pv.req.BillingPeriod, rate, pv.req.CostOverride,
		pv.startDate, pv.endDate, pv.req.Status, pv.reservedFrom, pv.reservedUntil,
	).Scan(&id)
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "person or category does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create vehicle")
		return
	}
	h.recordStatus(r, id, "", pv.req.Status, "angelegt")
	h.audit(r, "create", "vehicle", id, "created vehicle "+vehicleLabel(pv.req.Label, pv.req.LicensePlate))
	h.writeVehicle(w, r.Context(), id, http.StatusCreated)
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
	var oldStatus string
	var oldRate float64
	var archived bool
	scanErr := h.Pool.QueryRow(r.Context(),
		`SELECT status, rate, archived FROM vehicles WHERE id=$1`, id).Scan(&oldStatus, &oldRate, &archived)
	if !ensureVehicleWritable(w, archived, scanErr) {
		return
	}
	// Fallback is the existing rate, so an omitted rate keeps the locked price.
	rate := effectiveRate(pv.req, oldRate)
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE vehicles SET person_id=$1, category_id=$2, label=$3, license_plate=$4,
		        notes=$5, billing_period=$6, rate=$7, cost_override=$8, start_date=$9,
		        end_date=$10, status=$11, reserved_from=$12, reserved_until=$13, updated_at=now()
		 WHERE id=$14`,
		pv.req.PersonID, pv.req.CategoryID, pv.req.Label, pv.req.LicensePlate,
		pv.req.Notes, pv.req.BillingPeriod, rate, pv.req.CostOverride,
		pv.startDate, pv.endDate, pv.req.Status, pv.reservedFrom, pv.reservedUntil, id)
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
		h.recordStatus(r, id, oldStatus, pv.req.Status, "über Bearbeitung geändert")
	}
	h.audit(r, "update", "vehicle", id, "updated vehicle "+vehicleLabel(pv.req.Label, pv.req.LicensePlate))
	// An edit can change status/paid-relevant state too — keep archival behavior
	// consistent with the status-slider endpoint.
	h.autoArchiveIfClosed(r, id)
	h.writeVehicle(w, r.Context(), id, http.StatusOK)
}

// DeleteVehicle removes a vehicle.
func (h *Handler) DeleteVehicle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM vehicles WHERE id = $1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete vehicle")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "vehicle not found")
		return
	}
	h.audit(r, "delete", "vehicle", id, "deleted vehicle")
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
	var archived bool
	scanErr := h.Pool.QueryRow(r.Context(),
		`SELECT status, archived FROM vehicles WHERE id=$1`, id).Scan(&oldStatus, &archived)
	if !ensureVehicleWritable(w, archived, scanErr) {
		return
	}
	// When collecting, set the pickup/end date: use the caller-supplied date if
	// given (allows back-dating), otherwise keep any existing end date or today.
	switch {
	case req.Status == models.StatusCollected && trim(req.Date) != "":
		end, perr := time.Parse(dateLayout, trim(req.Date))
		if perr != nil {
			writeError(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
			return
		}
		_, _ = h.Pool.Exec(r.Context(),
			`UPDATE vehicles SET status=$1, end_date=$2, updated_at=now() WHERE id=$3`,
			req.Status, end, id)
	case req.Status == models.StatusCollected:
		_, _ = h.Pool.Exec(r.Context(),
			`UPDATE vehicles SET status=$1, end_date=COALESCE(end_date, CURRENT_DATE), updated_at=now() WHERE id=$2`,
			req.Status, id)
	default:
		_, _ = h.Pool.Exec(r.Context(),
			`UPDATE vehicles SET status=$1, updated_at=now() WHERE id=$2`, req.Status, id)
	}
	h.recordStatus(r, id, oldStatus, req.Status, trim(req.Note))
	h.audit(r, "update", "vehicle", id, "Status "+h.vehicleDesc(r, id)+": "+oldStatus+" → "+req.Status)
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
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) recordStatus(r *http.Request, vehicleID int64, oldStatus, newStatus, note string) {
	by := ""
	if u, ok := auth.UserFrom(r.Context()); ok {
		by = u.Username
	}
	_, _ = h.Pool.Exec(r.Context(),
		`INSERT INTO vehicle_status_history (vehicle_id, old_status, new_status, note, changed_by)
		 VALUES ($1,$2,$3,$4,$5)`, vehicleID, oldStatus, newStatus, note, by)
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
	var archived bool
	scanErr := h.Pool.QueryRow(r.Context(),
		`SELECT archived FROM vehicles WHERE id=$1`, id).Scan(&archived)
	if !ensureVehicleWritable(w, archived, scanErr) {
		return
	}
	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE vehicles SET paid=$1, updated_at=now() WHERE id=$2`, req.Paid, id); err != nil {
		writeError(w, http.StatusInternalServerError, "could not update payment status")
		return
	}
	label := "offen"
	if req.Paid {
		label = "bezahlt"
	}
	h.audit(r, "update", "vehicle", id, "Zahlung "+h.vehicleDesc(r, id)+": "+label)
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
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE vehicles SET archived=false, updated_at=now() WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not reactivate vehicle")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "vehicle not found")
		return
	}
	h.audit(r, "update", "vehicle", id, "Gefährt reaktiviert: "+h.vehicleDesc(r, id))
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
	start := time.Now()
	if trim(req.StartDate) != "" {
		s, err := time.Parse(dateLayout, trim(req.StartDate))
		if err != nil {
			writeError(w, http.StatusBadRequest, "start_date must be YYYY-MM-DD")
			return
		}
		start = s
	}

	var newID int64
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO vehicles (person_id, category_id, label, license_plate, notes,
		        billing_period, rate, cost_override, start_date, status)
		 SELECT person_id, category_id, label, license_plate, notes,
		        billing_period, rate, cost_override, $2, 'stored'
		 FROM vehicles WHERE id = $1
		 RETURNING id`, id, start).Scan(&newID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "vehicle not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not duplicate vehicle")
		return
	}

	// Copy the photos so the new period reuses the existing images.
	_, _ = h.Pool.Exec(r.Context(),
		`INSERT INTO vehicle_photos (vehicle_id, filename, content_type, byte_size, data)
		 SELECT $1, filename, content_type, byte_size, data
		 FROM vehicle_photos WHERE vehicle_id = $2`, newID, id)

	h.recordStatus(r, newID, "", models.StatusStored, "erneut eingestellt")
	h.audit(r, "create", "vehicle", newID, "re-stored vehicle from #"+strconv.FormatInt(id, 10))
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
	now := time.Now()
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
	writeJSON(w, status, vs[0])
}

// rowScanner unifies pgx.Row and pgx.Rows for scanning helpers.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanVehicleRow(row rowScanner) (models.Vehicle, models.Category, error) {
	var v models.Vehicle
	var cat models.Category
	var firstName, lastName string
	err := row.Scan(&v.ID, &v.PersonID, &v.CategoryID, &v.Label, &v.LicensePlate,
		&v.Notes, &v.BillingPeriod, &v.Rate, &v.CostOverride, &v.StartDate, &v.EndDate,
		&v.Status, &v.ReservedFrom, &v.ReservedUntil, &v.Paid, &v.Archived, &v.CreatedAt, &v.UpdatedAt,
		&cat.Name, &cat.DefaultMonthlyCost, &cat.DefaultYearlyCost,
		&firstName, &lastName, &v.PhotoCount)
	if err != nil {
		return v, cat, err
	}
	cat.ID = v.CategoryID
	v.CategoryName = cat.Name
	v.PersonName = trim(firstName + " " + lastName)
	return v, cat, nil
}

// enrich fills in derived cost/status fields on a vehicle as of the given time.
func enrich(v *models.Vehicle, cat models.Category, now time.Time) {
	v.EffectiveRate = v.EffectiveRateFor(cat)
	v.AccruedCost = round2(v.AccruedCostAsOf(cat, now))
	v.IsActive = v.Status == models.StatusStored || v.Status == models.StatusReserved
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
	return float64(int64(f*100+0.5)) / 100
}
