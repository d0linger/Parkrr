package handlers

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

// ListCategories returns all centrally-managed vehicle categories.
func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, name, default_monthly_cost, default_yearly_cost, rates_synced,
		        archived, created_at, updated_at
		 FROM categories ORDER BY archived, name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()

	cats := []models.Category{}
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.DefaultMonthlyCost,
			&c.DefaultYearlyCost, &c.RatesSynced, &c.Archived, &c.CreatedAt, &c.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, cats)
}

type categoryRequest struct {
	Name               string  `json:"name"`
	DefaultMonthlyCost float64 `json:"default_monthly_cost"`
	DefaultYearlyCost  float64 `json:"default_yearly_cost"`
	RatesSynced        bool    `json:"rates_synced"`
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
	if !validNameLength(req.Name) {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	if req.DefaultMonthlyCost < 0 || req.DefaultYearlyCost < 0 {
		writeError(w, http.StatusBadRequest, "costs must not be negative")
		return
	}
	var c models.Category
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO categories (name, default_monthly_cost, default_yearly_cost, rates_synced)
		 VALUES ($1,$2,$3,$4)
		 RETURNING id, name, default_monthly_cost, default_yearly_cost, rates_synced,
		           created_at, updated_at`,
		req.Name, req.DefaultMonthlyCost, req.DefaultYearlyCost, req.RatesSynced,
	).Scan(&c.ID, &c.Name, &c.DefaultMonthlyCost, &c.DefaultYearlyCost, &c.RatesSynced,
		&c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a category with that name already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not create category")
		return
	}
	h.auditCreated(r, "category", c.ID, "created tariff "+c.Name, map[string]any{
		"name": c.Name, "default_monthly_cost": c.DefaultMonthlyCost,
		"default_yearly_cost": c.DefaultYearlyCost,
	})
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
	if !validNameLength(req.Name) {
		writeError(w, http.StatusBadRequest, "name is too long")
		return
	}
	if req.DefaultMonthlyCost < 0 || req.DefaultYearlyCost < 0 {
		writeError(w, http.StatusBadRequest, "costs must not be negative")
		return
	}
	var old models.Category
	_ = h.Pool.QueryRow(r.Context(),
		`SELECT id, name, default_monthly_cost, default_yearly_cost, rates_synced
		 FROM categories WHERE id=$1`, id).
		Scan(&old.ID, &old.Name, &old.DefaultMonthlyCost, &old.DefaultYearlyCost, &old.RatesSynced)
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE categories SET name=$1, default_monthly_cost=$2,
		        default_yearly_cost=$3, rates_synced=$4, updated_at=now()
		 WHERE id=$5`,
		req.Name, req.DefaultMonthlyCost, req.DefaultYearlyCost, req.RatesSynced, id)
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
	newC := old
	newC.Name, newC.DefaultMonthlyCost = req.Name, req.DefaultMonthlyCost
	newC.DefaultYearlyCost, newC.RatesSynced = req.DefaultYearlyCost, req.RatesSynced
	changes := diffFields(old, newC, "created_at", "updated_at", "id")
	h.auditChange(r, "update", "category", id, "updated tariff "+req.Name, changes)
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
	// RETURNING names the removed tariff; an id alone is unresolvable afterwards.
	var delName string
	var delMonthly, delYearly float64
	err := h.Pool.QueryRow(r.Context(),
		`DELETE FROM categories WHERE id = $1
		 RETURNING name, default_monthly_cost, default_yearly_cost`, id).
		Scan(&delName, &delMonthly, &delYearly)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "category not found")
			return
		}
		// A vehicle referencing this tariff can be inserted between the count and
		// the DELETE (ON DELETE RESTRICT) — report that as a clean 409, not a 500.
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusConflict, "category is still used by vehicles")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not delete category")
		return
	}
	h.auditDeleted(r, "category", id, "deleted tariff "+delName, map[string]any{
		"name": delName, "default_monthly_cost": delMonthly, "default_yearly_cost": delYearly,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// SetCategoryArchived archives/reactivates a tariff. Archived tariffs stay valid
// for existing vehicles (whose rate is locked) but drop out of the pickers.
func (h *Handler) SetCategoryArchived(w http.ResponseWriter, r *http.Request) {
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
	var catName string
	err := h.Pool.QueryRow(r.Context(),
		`WITH prev AS (SELECT archived, name FROM categories WHERE id=$2)
		 UPDATE categories SET archived=$1, updated_at=now() WHERE id=$2
		 RETURNING (SELECT archived FROM prev), (SELECT name FROM prev)`,
		req.Archived, id).Scan(&prevArchived, &catName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "category not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not update category")
		return
	}
	verb := "reactivated tariff"
	if req.Archived {
		verb = "archived tariff"
	}
	h.auditChange(r, "update", "category", id, verb+" "+catName,
		diffFields(map[string]any{"archived": prevArchived}, map[string]any{"archived": req.Archived}))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
