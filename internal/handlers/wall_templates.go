package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Server-side planner wall templates (AR3): shared, reusable wall layouts stored
// in the DB instead of the browser's localStorage, so they survive a browser
// change and are visible team-wide. `walls` is opaque JSON (the planner's node
// graph), stored and returned verbatim.
const maxWallTemplates = 100

type wallTemplate struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Walls     json.RawMessage `json:"walls"`
	CreatedAt time.Time       `json:"created_at"`
}

type wallTemplateReq struct {
	Name  string          `json:"name"`
	Walls json.RawMessage `json:"walls"`
}

// ListWallTemplates returns the stored templates, newest first.
func (h *Handler) ListWallTemplates(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, name, walls, created_at FROM wall_templates ORDER BY created_at DESC LIMIT $1`, maxWallTemplates)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer rows.Close()
	out := []wallTemplate{}
	for rows.Next() {
		var t wallTemplate
		if err := rows.Scan(&t.ID, &t.Name, &t.Walls, &t.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateWallTemplate stores a template (name + opaque walls JSON) and trims the
// collection to the newest maxWallTemplates.
func (h *Handler) CreateWallTemplate(w http.ResponseWriter, r *http.Request) {
	var req wallTemplateReq
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
	if len(req.Walls) == 0 || !json.Valid(req.Walls) {
		writeError(w, http.StatusBadRequest, "walls payload is required")
		return
	}
	if len(req.Walls) > maxGeometryLen { // same cap the hall/spot geometry endpoints enforce
		writeError(w, http.StatusBadRequest, "walls payload is too large")
		return
	}
	var t wallTemplate
	if err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO wall_templates (name, walls) VALUES ($1,$2) RETURNING id, name, walls, created_at`,
		req.Name, req.Walls,
	).Scan(&t.ID, &t.Name, &t.Walls, &t.CreatedAt); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save template")
		return
	}
	// Ringpuffer: alles jenseits der jüngsten maxWallTemplates fällt weg. Die
	// verdrängten Namen werden zurückgegeben und protokolliert — vorher lief das als
	// stilles `_, _ =`, sodass Vorlagen spurlos verschwanden, während DeleteWallTemplate
	// nebenan großen Aufwand gegen Phantom-Löschungen treibt (Hundert API-84).
	trimmed, terr := h.Pool.Query(r.Context(),
		`DELETE FROM wall_templates WHERE id IN (
		     SELECT id FROM wall_templates ORDER BY created_at DESC OFFSET $1)
		 RETURNING id, name`, maxWallTemplates)
	if terr != nil {
		slog.Warn("wall-template trim failed", "err", terr)
	} else {
		type droppedTpl struct {
			id   int64
			name string
		}
		var dropped []droppedTpl
		for trimmed.Next() {
			var d droppedTpl
			if err := trimmed.Scan(&d.id, &d.name); err != nil {
				slog.Warn("wall-template trim scan failed", "err", err)
				break
			}
			dropped = append(dropped, d)
		}
		trimmed.Close()
		if err := trimmed.Err(); err != nil {
			slog.Warn("wall-template trim read failed", "err", err)
		}
		for _, d := range dropped {
			// auditDeleted (nicht h.audit) ist Pflicht: der Eintrag muss die entfernte
			// Zeile mitschreiben, damit er auflösbar bleibt, wenn sie weg ist. Genau
			// darauf besteht TestAuditDeletesCarryASnapshot.
			h.auditDeleted(r, "wall_template", d.id,
				"Wand-Vorlage verdrängt (Limit "+strconv.Itoa(maxWallTemplates)+"): "+d.name,
				map[string]any{"name": d.name})
		}
	}
	// The walls payload is opaque planner geometry, not business data — record the
	// identifying fields only, so the trail stays readable.
	h.auditCreated(r, "wall_template", t.ID, "Wand-Vorlage angelegt: "+t.Name,
		map[string]any{"name": t.Name})
	writeJSON(w, http.StatusCreated, t)
}

// DeleteWallTemplate removes a template by id.
func (h *Handler) DeleteWallTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	// RETURNING both names what was deleted AND proves a row existed: the previous
	// form audited unconditionally after a plain Exec, so deleting a nonexistent id
	// wrote a phantom deletion into an append-only table.
	var name string
	if err := h.Pool.QueryRow(r.Context(),
		`DELETE FROM wall_templates WHERE id=$1 RETURNING name`, id).Scan(&name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "template not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not delete template")
		return
	}
	h.auditDeleted(r, "wall_template", id, "Wand-Vorlage gelöscht: "+name,
		map[string]any{"name": name})
	w.WriteHeader(http.StatusNoContent)
}
