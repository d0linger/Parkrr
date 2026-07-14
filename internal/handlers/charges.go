package handlers

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

// ---------- Service catalog ----------

// ListServiceTypes returns the catalog of chargeable extra services.
func (h *Handler) ListServiceTypes(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, name, default_amount, created_at, updated_at
		 FROM service_types ORDER BY name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []models.ServiceType{}
	for rows.Next() {
		var s models.ServiceType
		if err := rows.Scan(&s.ID, &s.Name, &s.DefaultAmount, &s.CreatedAt, &s.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, s)
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
	h.audit(r, "create", "service_type", id, "created service "+req.Name)
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
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE service_types SET name=$1, default_amount=$2, updated_at=now() WHERE id=$3`,
		req.Name, req.DefaultAmount, id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a service with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update service")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	h.audit(r, "update", "service_type", id, "updated service "+req.Name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteServiceType removes a catalog entry (admin only).
func (h *Handler) DeleteServiceType(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM service_types WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete service")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}
	h.audit(r, "delete", "service_type", id, "deleted service")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---------- Charges ----------

// ListCharges returns charges, optionally filtered by ?person_id=.
func (h *Handler) ListCharges(w http.ResponseWriter, r *http.Request) {
	var (
		rows pgx.Rows
		err  error
	)
	base := `SELECT c.id, c.person_id, c.vehicle_id, c.description, c.amount, c.quantity,
	                c.charged_on, c.created_at, trim(pe.first_name || ' ' || pe.last_name)
	         FROM charges c JOIN persons pe ON pe.id = c.person_id`
	limit, offset := pageParams(r, 1000, 1000)
	if pid := r.URL.Query().Get("person_id"); pid != "" {
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
			&c.Quantity, &c.ChargedOn, &c.CreatedAt, &c.PersonName); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		c.Total = round2(c.Amount * c.Quantity)
		out = append(out, c)
	}
	writeJSON(w, http.StatusOK, out)
}

type chargeRequest struct {
	PersonID    int64   `json:"person_id"`
	VehicleID   *int64  `json:"vehicle_id"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	Quantity    float64 `json:"quantity"`
	ChargedOn   string  `json:"charged_on"`
}

// CreateCharge adds an extra line item.
func (h *Handler) CreateCharge(w http.ResponseWriter, r *http.Request) {
	var req chargeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Description = trim(req.Description)
	if req.PersonID <= 0 || req.Description == "" {
		writeError(w, http.StatusBadRequest, "person_id and description are required")
		return
	}
	if !validNameLength(req.Description) {
		writeError(w, http.StatusBadRequest, "description is too long")
		return
	}
	if req.Quantity <= 0 {
		req.Quantity = 1
	}
	chargedOn := time.Now()
	if trim(req.ChargedOn) != "" {
		t, err := time.Parse(dateLayout, trim(req.ChargedOn))
		if err != nil {
			writeError(w, http.StatusBadRequest, "charged_on must be YYYY-MM-DD")
			return
		}
		chargedOn = t
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
	h.audit(r, "create", "charge", id, "added charge "+req.Description)
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// DeleteCharge removes an extra line item.
func (h *Handler) DeleteCharge(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM charges WHERE id=$1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete charge")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "charge not found")
		return
	}
	h.audit(r, "delete", "charge", id, "deleted charge")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
