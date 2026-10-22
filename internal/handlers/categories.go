package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

// ListCategories returns all centrally-managed vehicle categories.
func (h *Handler) ListCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, name, default_monthly_cost, default_yearly_cost, rates_synced,
		        archived, default_length_m, default_width_m, default_height_m, default_weight_t,
		        created_at, updated_at
		 FROM categories ORDER BY archived, name`)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()

	cats := []models.Category{}
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.DefaultMonthlyCost,
			&c.DefaultYearlyCost, &c.RatesSynced, &c.Archived,
			&c.DefaultLengthM, &c.DefaultWidthM, &c.DefaultHeightM, &c.DefaultWeightT,
			&c.CreatedAt, &c.UpdatedAt); err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if hallStaff(r) { // Ausbau 35: Maße ja, Preise nein
		for i := range cats {
			cats[i].DefaultMonthlyCost, cats[i].DefaultYearlyCost = 0, 0
		}
	}
	writeJSON(w, http.StatusOK, cats)
}

type categoryRequest struct {
	Name               string  `json:"name"`
	DefaultMonthlyCost float64 `json:"default_monthly_cost"`
	DefaultYearlyCost  float64 `json:"default_yearly_cost"`
	RatesSynced        bool    `json:"rates_synced"`
	// Standardmaße (Hundert 80): nil = keine Vorgabe. Zeiger, damit "weggelassen"
	// und "gelöscht" unterscheidbar bleiben — beide bedeuten hier: keine Vorgabe.
	DefaultLengthM *float64 `json:"default_length_m"`
	DefaultWidthM  *float64 `json:"default_width_m"`
	DefaultHeightM *float64 `json:"default_height_m"`
	DefaultWeightT *float64 `json:"default_weight_t"`
}

// validCategoryDims prüft die Standardmaße mit denselben Grenzen wie der
// Maße-Endpunkt der Gefährte (dimPos/dimInRange) — eine Vorgabe, die kein
// Gefährt tragen dürfte, wäre sinnlos.
func validCategoryDims(req *categoryRequest) bool {
	return dimPos(req.DefaultLengthM, 60) && dimPos(req.DefaultWidthM, 15) &&
		dimPos(req.DefaultHeightM, 15) && dimInRange(req.DefaultWeightT, 200)
}

func validCategoryPrices(req *categoryRequest) bool {
	return req.DefaultMonthlyCost >= 0 && req.DefaultMonthlyCost <= maxMoneyAmount &&
		req.DefaultYearlyCost >= 0 && req.DefaultYearlyCost <= maxMoneyAmount
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
	if !validCategoryPrices(&req) {
		writeError(w, http.StatusBadRequest, "costs are out of range")
		return
	}
	if !validCategoryDims(&req) {
		writeError(w, http.StatusBadRequest, "dimension out of range")
		return
	}
	var c models.Category
	err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO categories (name, default_monthly_cost, default_yearly_cost, rates_synced,
		                         default_length_m, default_width_m, default_height_m, default_weight_t)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 RETURNING id, name, default_monthly_cost, default_yearly_cost, rates_synced,
		           default_length_m, default_width_m, default_height_m, default_weight_t,
		           created_at, updated_at`,
		req.Name, req.DefaultMonthlyCost, req.DefaultYearlyCost, req.RatesSynced,
		req.DefaultLengthM, req.DefaultWidthM, req.DefaultHeightM, req.DefaultWeightT,
	).Scan(&c.ID, &c.Name, &c.DefaultMonthlyCost, &c.DefaultYearlyCost, &c.RatesSynced,
		&c.DefaultLengthM, &c.DefaultWidthM, &c.DefaultHeightM, &c.DefaultWeightT,
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
	if !validCategoryPrices(&req) {
		writeError(w, http.StatusBadRequest, "costs are out of range")
		return
	}
	if !validCategoryDims(&req) {
		writeError(w, http.StatusBadRequest, "dimension out of range")
		return
	}
	ctx := r.Context()
	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		serverError(w, r, "could not update category", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var old models.Category
	err = tx.QueryRow(ctx,
		`SELECT id, name, default_monthly_cost, default_yearly_cost, rates_synced,
		        default_length_m, default_width_m, default_height_m, default_weight_t
		 FROM categories WHERE id=$1 FOR UPDATE`, id).
		Scan(&old.ID, &old.Name, &old.DefaultMonthlyCost, &old.DefaultYearlyCost, &old.RatesSynced,
			&old.DefaultLengthM, &old.DefaultWidthM, &old.DefaultHeightM, &old.DefaultWeightT)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "category not found")
			return
		}
		serverError(w, r, "could not read category", err)
		return
	}
	// Compare the amounts PostgreSQL will persist. A sub-cent input that rounds
	// to the same tariff must not be mistaken for an effective price change.
	req.DefaultMonthlyCost = round2(req.DefaultMonthlyCost)
	req.DefaultYearlyCost = round2(req.DefaultYearlyCost)
	monthlyChanged := req.DefaultMonthlyCost != old.DefaultMonthlyCost
	yearlyChanged := req.DefaultYearlyCost != old.DefaultYearlyCost
	if monthlyChanged || yearlyChanged {
		var dependent int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM vehicles
			 WHERE category_id=$1 AND rate=0 AND cost_override IS NULL
			   AND ((billing_period='yearly' AND $2) OR (billing_period<>'yearly' AND $3))`,
			id, yearlyChanged, monthlyChanged).Scan(&dependent); err != nil {
			serverError(w, r, "could not check existing booking prices", err)
			return
		}
		if dependent > 0 {
			// Never reinterpret or backfill legacy prices while editing a tariff.
			// Archived and future bookings count too: their agreement still exists.
			writeError(w, http.StatusConflict, fmt.Sprintf(
				"Tarifpreis nicht geändert: %d bestehende Buchung(en) beziehen ihren Preis noch aus diesem Tarif. "+
					"Bestätige deren vereinbarten Preis einzeln oder lege einen neuen Tarif für neue Buchungen an.", dependent))
			return
		}
	}
	_, err = tx.Exec(ctx,
		`UPDATE categories SET name=$1, default_monthly_cost=$2,
		        default_yearly_cost=$3, rates_synced=$4,
		        default_length_m=$5, default_width_m=$6, default_height_m=$7, default_weight_t=$8,
		        updated_at=now()
		 WHERE id=$9`,
		req.Name, req.DefaultMonthlyCost, req.DefaultYearlyCost, req.RatesSynced,
		req.DefaultLengthM, req.DefaultWidthM, req.DefaultHeightM, req.DefaultWeightT, id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "a category with that name already exists")
			return
		}
		serverError(w, r, "could not update category", err)
		return
	}
	newC := old
	newC.Name, newC.DefaultMonthlyCost = req.Name, req.DefaultMonthlyCost
	newC.DefaultYearlyCost, newC.RatesSynced = req.DefaultYearlyCost, req.RatesSynced
	newC.DefaultLengthM, newC.DefaultWidthM = req.DefaultLengthM, req.DefaultWidthM
	newC.DefaultHeightM, newC.DefaultWeightT = req.DefaultHeightM, req.DefaultWeightT
	changes := diffFields(old, newC, "created_at", "updated_at", "id")
	if err := h.auditChangeTx(ctx, tx, r, "update", "category", id, "updated tariff "+req.Name, changes); err != nil {
		serverError(w, r, "could not record tariff change", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, r, "could not update category", err)
		return
	}
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
