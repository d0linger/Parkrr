package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

// ---------- Service catalog ----------

// ListServiceTypes returns the catalog of chargeable extra services.
func (h *Handler) ListServiceTypes(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, name, default_amount, archived, created_at, updated_at
		 FROM service_types ORDER BY archived, name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []models.ServiceType{}
	for rows.Next() {
		var s models.ServiceType
		if err := rows.Scan(&s.ID, &s.Name, &s.DefaultAmount, &s.Archived, &s.CreatedAt, &s.UpdatedAt); err != nil {
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

type serviceTypeRequest struct {
	Name          string  `json:"name"`
	DefaultAmount float64 `json:"default_amount"`
}

// CreateServiceType adds a catalog entry (admin only).
func (h *Handler) CreateServiceType(w http.ResponseWriter, r *http.Request) {
	var req serviceTypeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = trim(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validNameLength(req.Name) {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	var id int64
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO service_types (name, default_amount) VALUES ($1,$2) RETURNING id`,
		req.Name, req.DefaultAmount).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a service with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create service")
		return
	}
	h.auditCreated(r, "service_type", id, "created service "+req.Name,
		map[string]any{"name": req.Name, "default_amount": req.DefaultAmount})
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// UpdateServiceType edits a catalog entry (admin only).
func (h *Handler) UpdateServiceType(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req serviceTypeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = trim(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if !validNameLength(req.Name) {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	var prev serviceTypeRequest
	err := h.Pool.QueryRow(r.Context(),
		`WITH prev AS (SELECT name, default_amount FROM service_types WHERE id=$3)
		 UPDATE service_types SET name=$1, default_amount=$2, updated_at=now() WHERE id=$3
		 RETURNING (SELECT name FROM prev), (SELECT default_amount FROM prev)`,
		req.Name, req.DefaultAmount, id).Scan(&prev.Name, &prev.DefaultAmount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "service not found")
			return
		}
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a service with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update service")
		return
	}
	h.auditChange(r, "update", "service_type", id, "updated service "+req.Name, diffFields(prev, req))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteServiceType removes a catalog entry (admin only).
func (h *Handler) DeleteServiceType(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var delName string
	var delAmount float64
	err := h.Pool.QueryRow(r.Context(),
		`DELETE FROM service_types WHERE id=$1 RETURNING name, default_amount`, id).Scan(&delName, &delAmount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "service not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not delete service")
		return
	}
	h.auditDeleted(r, "service_type", id, "deleted service "+delName,
		map[string]any{"name": delName, "default_amount": delAmount})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// SetServiceArchived archives/reactivates a catalog service. Archived services
// drop out of the "Aus Katalog" picker; existing charges are snapshots.
func (h *Handler) SetServiceArchived(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Archived bool `json:"archived"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var prevArchived bool
	var name string
	err := h.Pool.QueryRow(r.Context(),
		`WITH prev AS (SELECT archived, name FROM service_types WHERE id=$2)
		 UPDATE service_types SET archived=$1, updated_at=now() WHERE id=$2
		 RETURNING (SELECT archived FROM prev), (SELECT name FROM prev)`,
		req.Archived, id).Scan(&prevArchived, &name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "service not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update service")
		return
	}
	verb := "reactivated service"
	if req.Archived {
		verb = "archived service"
	}
	// Name the object, not just its id, so the trail stays readable.
	h.auditChange(r, "update", "service_type", id, verb+" "+name,
		diffFields(map[string]any{"archived": prevArchived}, map[string]any{"archived": req.Archived}))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- Charges ----------

// ListCharges returns charges, optionally filtered by ?person_id=.
func (h *Handler) ListCharges(w http.ResponseWriter, r *http.Request) {
	var (
		rows pgx.Rows
		err  error
	)
	base := `SELECT c.id, c.person_id, c.vehicle_id, c.description, c.amount, c.quantity,
	                c.charged_on, c.created_at, trim(pe.first_name || ' ' || pe.last_name),
	                c.paid, COALESCE(v.paid, false),
	                COALESCE(NULLIF(v.label, ''), NULLIF(v.license_plate, ''), cat.name, '')
	         FROM charges c
	         JOIN persons pe ON pe.id = c.person_id
	         LEFT JOIN vehicles v ON v.id = c.vehicle_id
	         LEFT JOIN categories cat ON cat.id = v.category_id`
	limit, offset := pageParams(r, 1000, 1000)
	if raw := r.URL.Query().Get("person_id"); raw != "" {
		pid, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid person_id")
			return
		}
		rows, err = h.Pool.Query(r.Context(),
			base+` WHERE c.person_id=$1 ORDER BY c.charged_on DESC, c.id DESC LIMIT $2 OFFSET $3`, pid, limit, offset)
	} else {
		rows, err = h.Pool.Query(r.Context(),
			base+` ORDER BY c.charged_on DESC, c.id DESC LIMIT $1 OFFSET $2`, limit, offset)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []models.Charge{}
	for rows.Next() {
		var c models.Charge
		if err := rows.Scan(&c.ID, &c.PersonID, &c.VehicleID, &c.Description, &c.Amount,
			&c.Quantity, &c.ChargedOn, &c.CreatedAt, &c.PersonName,
			&c.Paid, &c.VehiclePaid, &c.VehicleLabel); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		c.Total = round2(c.Amount * c.Quantity)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	rows.Close()

	// A bound Zusatzkosten is billed separately from the flat rate (Option A): a
	// covering Pauschale settles only the base rent, so it does NOT fold into the
	// charge's displayed paid state. VehiclePaid stays the vehicle's own paid flag
	// (as scanned) — the explicit settlement path — matching invoiceLines.
	if err := h.setChargeInvoiceStatus(r.Context(), out); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// setChargeInvoiceStatus derives each charge's Invoiced / InvoiceOpen flags from the
// invoices that bill it (invoice_source, kind='charge'), the charge-level twin of
// setVehicleInvoiceStatus. A bound Zusatzkosten settled through a paid Rechnung then
// shows "bezahlt · Rechnung" instead of a stale "offen" — PayInvoices settles the
// invoice, not the underlying charge's raw flag, so the state must be derived here.
func (h *Handler) setChargeInvoiceStatus(ctx context.Context, charges []models.Charge) error {
	if len(charges) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(charges))
	for i := range charges {
		ids = append(ids, charges[i].ID)
	}
	rows, err := h.Pool.Query(ctx,
		`SELECT s.ref_id, count(*) FILTER (WHERE (i.total - i.paid_amount) > 0.005) AS open_n
		   FROM invoice_source s JOIN invoices i ON i.id = s.invoice_id
		  WHERE s.kind='charge' AND NOT i.canceled AND s.ref_id = ANY($1)
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
	for i := range charges {
		if n, ok := openN[charges[i].ID]; ok {
			charges[i].Invoiced = true
			charges[i].InvoiceOpen = n > 0
		}
	}
	return nil
}

type chargeRequest struct {
	PersonID    int64   `json:"person_id"`
	VehicleID   *int64  `json:"vehicle_id"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	Quantity    float64 `json:"quantity"`
	ChargedOn   string  `json:"charged_on"`
}

// chargeChartData returns a person's one-off charges (by charge date) both per
// calendar month of the given year (index 0 = January) and summed per year, in a
// single round trip. Charges dated on or after until are excluded so future-dated
// charges don't show up before they are due, matching the rent/recurring cutoff.
func (h *Handler) chargeChartData(ctx context.Context, personID int64, year int, until time.Time) ([]float64, map[int]float64, error) {
	monthly := make([]float64, 12)
	yearly := map[int]float64{}
	rows, err := h.Pool.Query(ctx,
		`SELECT charged_on, amount*quantity FROM charges WHERE person_id=$1 AND charged_on < $2`, personID, until)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var chargedOn time.Time
		var s float64
		if err := rows.Scan(&chargedOn, &s); err != nil {
			return nil, nil, err
		}
		s = round2(s) // per-line cent rounding, consistent with the balance/invoice
		yearly[chargedOn.Year()] += s
		if chargedOn.Year() == year {
			monthly[int(chargedOn.Month())-1] += s
		}
	}
	return monthly, yearly, rows.Err()
}

// validateCharge normalizes and validates a charge request and returns the parsed
// charged_on. badMsg carries a 400 reason (empty when valid); a non-nil error
// signals a 500. A bound vehicle must belong to the same person.
func (h *Handler) validateCharge(ctx context.Context, req *chargeRequest) (time.Time, string, error) {
	req.Description = trim(req.Description)
	if req.PersonID <= 0 || req.Description == "" {
		return time.Time{}, "person_id and description are required", nil
	}
	if !validNameLength(req.Description) {
		return time.Time{}, "description is too long", nil
	}
	if req.Quantity <= 0 {
		req.Quantity = 1
	}
	// Bound amount and line total so out-of-range input is a clean 400 rather than a
	// NUMERIC(12,2) overflow 500 on insert.
	if req.Amount < 0 || req.Amount > maxMoneyAmount || req.Quantity > maxQuantity {
		return time.Time{}, "amount or quantity is out of range", nil
	}
	if lt := req.Amount * req.Quantity; lt > maxMoneyAmount {
		return time.Time{}, "amount × quantity exceeds the allowed maximum", nil
	}
	if req.VehicleID != nil {
		var owner int64
		err := h.Pool.QueryRow(ctx, `SELECT person_id FROM vehicles WHERE id=$1`, *req.VehicleID).Scan(&owner)
		if err == pgx.ErrNoRows || (err == nil && owner != req.PersonID) {
			return time.Time{}, "vehicle does not belong to that person", nil
		}
		if err != nil {
			return time.Time{}, "", err
		}
	}
	chargedOn := time.Now()
	if trim(req.ChargedOn) != "" {
		if !validDateLength(trim(req.ChargedOn)) {
			return time.Time{}, "charged_on is too long", nil
		}
		t, perr := time.Parse(dateLayout, trim(req.ChargedOn))
		if perr != nil {
			return time.Time{}, "charged_on must be YYYY-MM-DD", nil
		}
		chargedOn = t
	}
	return chargedOn, "", nil
}

// CreateCharge adds an extra line item.
func (h *Handler) CreateCharge(w http.ResponseWriter, r *http.Request) {
	var req chargeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	chargedOn, badMsg, serr := h.validateCharge(r.Context(), &req)
	if serr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if badMsg != "" {
		writeError(w, http.StatusBadRequest, badMsg)
		return
	}
	var id int64
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO charges (person_id, vehicle_id, description, amount, quantity, charged_on)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		req.PersonID, req.VehicleID, req.Description, req.Amount, req.Quantity, chargedOn,
	).Scan(&id)
	if err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "person or vehicle does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create charge")
		return
	}
	h.auditCreated(r, "charge", id, "added charge "+req.Description, map[string]any{
		"description": req.Description, "amount": req.Amount, "quantity": req.Quantity,
		"person_id": req.PersonID, "charged_on": req.ChargedOn,
	})
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// UpdateCharge edits an extra line item; the paid state is left unchanged.
func (h *Handler) UpdateCharge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req chargeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	chargedOn, badMsg, serr := h.validateCharge(r.Context(), &req)
	if serr != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if badMsg != "" {
		writeError(w, http.StatusBadRequest, badMsg)
		return
	}
	// The own paid flag only governs standalone charges; a bound charge follows its
	// vehicle/Pauschale. So when the binding changes (bind, unbind or rebind) the
	// old flag is stale — reset it to open. Edits that keep the binding keep paid.
	// (In UPDATE, the RHS vehicle_id refers to the pre-update value.)
	// `prev` captures the pre-update row in the SAME statement, so the money diff is
	// atomic with the change (amounts must be provable after the fact).
	var prev chargeRequest
	var prevChargedOn time.Time
	err := h.Pool.QueryRow(r.Context(),
		`WITH prev AS (SELECT person_id, vehicle_id, description, amount, quantity, charged_on
		                 FROM charges WHERE id=$7)
		 UPDATE charges SET person_id=$1, vehicle_id=$2, description=$3, amount=$4,
		        quantity=$5, charged_on=$6,
		        paid = CASE WHEN vehicle_id IS DISTINCT FROM $2 THEN false ELSE paid END
		 WHERE id=$7
		 RETURNING (SELECT person_id FROM prev), (SELECT vehicle_id FROM prev),
		           (SELECT description FROM prev), (SELECT amount FROM prev),
		           (SELECT quantity FROM prev), (SELECT charged_on FROM prev)`,
		req.PersonID, req.VehicleID, req.Description, req.Amount, req.Quantity, chargedOn, id).
		Scan(&prev.PersonID, &prev.VehicleID, &prev.Description, &prev.Amount, &prev.Quantity, &prevChargedOn)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "charge not found")
			return
		}
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "person or vehicle does not exist")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update charge")
		return
	}
	prev.ChargedOn = prevChargedOn.Format("2006-01-02")
	h.auditChange(r, "update", "charge", id, "updated charge "+req.Description, diffFields(prev, req))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteCharge removes an extra line item.
func (h *Handler) DeleteCharge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if inv, ierr := h.refInvoiced(r.Context(), h.Pool, "charge", id); ierr != nil {
		writeError(w, http.StatusInternalServerError, "could not delete charge")
		return
	} else if inv {
		writeError(w, http.StatusConflict, "Zusatzkosten sind Teil einer ausgestellten Rechnung und können nicht gelöscht werden (Storno statt Löschen).")
		return
	}
	// Money row: the trail must retain what was removed (amount, who, when).
	var dDesc string
	var dAmount, dQty float64
	var dPerson int64
	var dOn time.Time
	err := h.Pool.QueryRow(r.Context(),
		`DELETE FROM charges WHERE id=$1 RETURNING description, amount, quantity, person_id, charged_on`, id).
		Scan(&dDesc, &dAmount, &dQty, &dPerson, &dOn)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "charge not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not delete charge")
		return
	}
	h.auditDeleted(r, "charge", id, "deleted charge "+dDesc, map[string]any{
		"description": dDesc, "amount": dAmount, "quantity": dQty,
		"person_id": dPerson, "charged_on": dOn.Format("2006-01-02"),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// SetChargePaid toggles a standalone charge's own paid flag. A charge bound to a
// vehicle derives its paid state from that vehicle, so the flag is ignored there.
func (h *Handler) SetChargePaid(w http.ResponseWriter, r *http.Request) {
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
	// A charge bound to a vehicle is settled via that vehicle; its own flag is
	// meaningless there. Only standalone charges carry their own paid flag.
	var personID int64
	var curPaid bool
	switch e := h.Pool.QueryRow(r.Context(),
		`SELECT person_id, paid FROM charges WHERE id=$1 AND vehicle_id IS NULL`, id).Scan(&personID, &curPaid); {
	case e == pgx.ErrNoRows:
		// Missing (404) or bound to a vehicle (409).
		var exists bool
		if h.Pool.QueryRow(r.Context(), `SELECT true FROM charges WHERE id=$1`, id).Scan(&exists) == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "charge not found")
		} else {
			writeError(w, http.StatusConflict, "charge is settled via its vehicle")
		}
		return
	case e != nil:
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	// P2.3: record the auto-payment while still open, then flip the flag — all in
	// ONE transaction so a failure between them can't leave a phantom
	// payment/credit. The owed amount is read BEFORE the tx (openOwedItems uses
	// the pool; reading it while holding the tx's connection can deadlock the
	// pool under concurrency).
	var amt float64
	var bound []boundCharge
	if req.Paid && !curPaid {
		var err error
		if amt, bound, err = h.toggleOwed(r, "charge", id, personID); err != nil {
			writeError(w, http.StatusInternalServerError, "could not record payment")
			return
		}
	}
	failMsg := "could not update charge"
	if err := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if req.Paid && !curPaid {
			if err := h.syncTogglePaymentTx(r.Context(), tx, "charge", id, personID, true, amt, bound); err != nil {
				failMsg = "could not record payment"
				return err
			}
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE charges SET paid=$1 WHERE id=$2 AND vehicle_id IS NULL`, req.Paid, id); err != nil {
			return err
		}
		if !req.Paid && curPaid {
			// Don't swallow: a discarded error would leave a phantom auto-payment
			// while the charge shows open, inflating the balance.
			if err := h.syncTogglePaymentTx(r.Context(), tx, "charge", id, personID, false, 0, nil); err != nil {
				failMsg = "could not reverse payment"
				return err
			}
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, failMsg)
		return
	}
	verb := "charge marked open"
	if req.Paid {
		verb = "charge marked paid"
	}
	// curPaid was read above for the bound-charge check, so the settlement diff is free.
	h.auditChange(r, "update", "charge", id, verb,
		diffFields(map[string]any{"paid": curPaid}, map[string]any{"paid": req.Paid}))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
