package handlers

import (
	"net/http"

	"github.com/preining/parkrr/internal/models"
)

// ListPersons returns all persons.
func (h *Handler) ListPersons(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, first_name, last_name, email, phone, address, notes,
		        created_at, updated_at
		 FROM persons ORDER BY last_name, first_name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	persons := []models.Person{}
	for rows.Next() {
		var p models.Person
		if err := rows.Scan(&p.ID, &p.FirstName, &p.LastName, &p.Email, &p.Phone,
			&p.Address, &p.Notes, &p.CreatedAt, &p.UpdatedAt); err != nil {
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

// CreatePerson adds a new person.
func (h *Handler) CreatePerson(w http.ResponseWriter, r *http.Request) {
	var req personRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.normalize()
	if req.FirstName == "" && req.LastName == "" {
		writeError(w, http.StatusBadRequest, "first or last name is required")
		return
	}
	var p models.Person
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO persons (first_name, last_name, email, phone, address, notes)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING id, first_name, last_name, email, phone, address, notes,
		           created_at, updated_at`,
		req.FirstName, req.LastName, req.Email, req.Phone, req.Address, req.Notes,
	).Scan(&p.ID, &p.FirstName, &p.LastName, &p.Email, &p.Phone, &p.Address,
		&p.Notes, &p.CreatedAt, &p.UpdatedAt)
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
	if req.FirstName == "" && req.LastName == "" {
		writeError(w, http.StatusBadRequest, "first or last name is required")
		return
	}
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
	h.audit(r, "update", "person", id, "updated person "+trim(req.FirstName+" "+req.LastName))
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
