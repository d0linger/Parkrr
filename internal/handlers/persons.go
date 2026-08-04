package handlers

import (
	"context"
	"net/http"

	"github.com/preining/parkrr/internal/models"
)

// personColumns is the shared column list for reading persons. has_flat_rate is
// derived live from the agreement records — the legacy persons.flat_rate_*
// columns are frozen (no writer remains) and must not drive the badge.
const personColumns = `id, first_name, last_name, email, phone, address, notes,
	flat_rate, flat_rate_period, flat_rate_start, flat_rate_end, flat_rate_paid,
	created_at, updated_at,
	EXISTS(SELECT 1 FROM flat_rate_periods fp WHERE fp.person_id = persons.id)`

func scanPerson(row rowScanner) (models.Person, error) {
	var p models.Person
	err := row.Scan(&p.ID, &p.FirstName, &p.LastName, &p.Email, &p.Phone, &p.Address,
		&p.Notes, &p.FlatRate, &p.FlatRatePeriod, &p.FlatRateStart, &p.FlatRateEnd,
		&p.FlatRatePaid, &p.CreatedAt, &p.UpdatedAt, &p.HasFlatRate)
	return p, err
}

// personLabel returns a person's display name for audit messages ("" on error).
func (h *Handler) personLabel(r *http.Request, id int64) string {
	var name string
	_ = h.Pool.QueryRow(r.Context(),
		`SELECT trim(first_name || ' ' || last_name) FROM persons WHERE id=$1`, id).Scan(&name)
	return name
}

// getPerson loads a single person by id.
func (h *Handler) getPerson(ctx context.Context, id int64) (models.Person, error) {
	return scanPerson(h.Pool.QueryRow(ctx,
		`SELECT `+personColumns+` FROM persons WHERE id=$1`, id))
}

// ListPersons returns all persons.
func (h *Handler) ListPersons(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r, 1000, 1000)
	rows, err := h.Pool.Query(r.Context(),
		`SELECT `+personColumns+` FROM persons ORDER BY last_name, first_name LIMIT $1 OFFSET $2`,
		limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	persons := []models.Person{}
	for rows.Next() {
		p, err := scanPerson(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		persons = append(persons, p)
	}
	writeJSON(w, http.StatusOK, persons)
}

type personRequest struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	Address   string `json:"address"`
	Notes     string `json:"notes"`
}

func (req *personRequest) normalize() {
	req.FirstName = trim(req.FirstName)
	req.LastName = trim(req.LastName)
	req.Email = trim(req.Email)
	req.Phone = trim(req.Phone)
	req.Address = trim(req.Address)
	req.Notes = trim(req.Notes)
}

func (req *personRequest) validate() string {
	if req.FirstName == "" && req.LastName == "" {
		return "first or last name is required"
	}
	if !validNameLength(req.FirstName) || !validNameLength(req.LastName) {
		return "name is too long"
	}
	if !validEmailLength(req.Email) {
		return "email is too long"
	}
	if !validPhoneLength(req.Phone) {
		return "phone is too long"
	}
	if !validAddressLength(req.Address) {
		return "address is too long"
	}
	if !validNoteLength(req.Notes) {
		return "notes is too long"
	}
	return ""
}

// CreatePerson adds a new person.
func (h *Handler) CreatePerson(w http.ResponseWriter, r *http.Request) {
	var req personRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.normalize()
	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	p, err := scanPerson(h.Pool.QueryRow(r.Context(),
		`INSERT INTO persons (first_name, last_name, email, phone, address, notes)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING `+personColumns,
		req.FirstName, req.LastName, req.Email, req.Phone, req.Address, req.Notes))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create person")
		return
	}
	h.audit(r, "create", "person", p.ID, "created person "+trim(p.FirstName+" "+p.LastName))
	writeJSON(w, http.StatusCreated, p)
}

// UpdatePerson edits an existing person.
func (h *Handler) UpdatePerson(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req personRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.normalize()
	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	old, _ := h.getPerson(r.Context(), id)
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE persons SET first_name=$1, last_name=$2, email=$3, phone=$4,
		        address=$5, notes=$6, updated_at=now()
		 WHERE id=$7`,
		req.FirstName, req.LastName, req.Email, req.Phone, req.Address, req.Notes, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update person")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}
	newP, _ := h.getPerson(r.Context(), id)
	changes := diffFields(old, newP, "updated_at", "created_at", "has_flat_rate")
	h.auditChange(r, "update", "person", id, "updated person "+trim(req.FirstName+" "+req.LastName), changes)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeletePerson removes a person and (via cascade) their vehicles.
func (h *Handler) DeletePerson(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM persons WHERE id = $1`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			// Invoices reference the person (ON DELETE RESTRICT) for immutability —
			// a person with issued invoices must be kept (storniere statt löschen).
			writeError(w, http.StatusConflict,
				"Person hat ausgestellte Rechnungen und kann nicht gelöscht werden (Storno statt Löschen).")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not delete person")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "person not found")
		return
	}
	h.audit(r, "delete", "person", id, "deleted person")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
