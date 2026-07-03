package handlers

import (
	"net/http"

	"github.com/preining/parkrr/internal/models"
)

// ListCategories returns all centrally-managed vehicle categories.
func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, name, default_monthly_cost, default_yearly_cost,
		        created_at, updated_at
		 FROM categories ORDER BY name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	cats := []models.Category{}
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.DefaultMonthlyCost,
			&c.DefaultYearlyCost, &c.CreatedAt, &c.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		cats = append(cats, c)
	}
	writeJSON(w, http.StatusOK, cats)
}

type categoryRequest struct {
	Name               string  `json:"name"`
	DefaultMonthlyCost float64 `json:"default_monthly_cost"`
	DefaultYearlyCost  float64 `json:"default_yearly_cost"`
}

// CreateCategory adds a new vehicle category (admin only).
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	var req categoryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = trim(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.DefaultMonthlyCost < 0 || req.DefaultYearlyCost < 0 {
		writeError(w, http.StatusBadRequest, "costs must not be negative")
		return
	}
	var c models.Category
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO categories (name, default_monthly_cost, default_yearly_cost)
		 VALUES ($1,$2,$3)
		 RETURNING id, name, default_monthly_cost, default_yearly_cost,
		           created_at, updated_at`,
		req.Name, req.DefaultMonthlyCost, req.DefaultYearlyCost,
	).Scan(&c.ID, &c.Name, &c.DefaultMonthlyCost, &c.DefaultYearlyCost,
		&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a category with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create category")
		return
	}
	h.audit(r, "create", "category", c.ID, "created tariff "+c.Name)
	writeJSON(w, http.StatusCreated, c)
}

// UpdateCategory edits a vehicle category (admin only).
func (h *Handler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req categoryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = trim(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.DefaultMonthlyCost < 0 || req.DefaultYearlyCost < 0 {
		writeError(w, http.StatusBadRequest, "costs must not be negative")
		return
	}
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE categories SET name=$1, default_monthly_cost=$2,
		        default_yearly_cost=$3, updated_at=now()
		 WHERE id=$4`,
		req.Name, req.DefaultMonthlyCost, req.DefaultYearlyCost, id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a category with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update category")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "category not found")
		return
	}
	h.audit(r, "update", "category", id, "updated tariff "+req.Name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DeleteCategory removes a category if no vehicles reference it (admin only).
func (h *Handler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var inUse int
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT count(*) FROM vehicles WHERE category_id = $1`, id).Scan(&inUse); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	if inUse > 0 {
		writeError(w, http.StatusConflict, "category is still used by vehicles")
		return
	}
	ct, err := h.Pool.Exec(r.Context(), `DELETE FROM categories WHERE id = $1`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete category")
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "category not found")
		return
	}
	h.audit(r, "delete", "category", id, "deleted tariff")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
