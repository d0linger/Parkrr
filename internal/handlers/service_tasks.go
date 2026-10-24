package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

// Serviceaufgaben während der Einlagerung (Ausbau 25): Batterie laden, Reifendruck,
// lüften. Eine Aufgabe gilt für ein Gefährt, einen Tarif, alle eingelagerten oder alle
// mit Ladebedarf; fällig wird sie je Gefährt nach ihrem Intervall. „Erledigt“ ist ein
// Tipp; hängt eine Leistung daran, entsteht auf Wunsch die Zusatzkosten-Position über
// denselben Weg wie im Formular (CreateCharge).

const (
	maxServiceTitleRunes = 120
	serviceDueDefaultDay = 7
	serviceDueMaxRows    = 500
)

type serviceTask struct {
	ID            int64     `json:"id"`
	Title         string    `json:"title"`
	Scope         string    `json:"scope"`
	VehicleID     *int64    `json:"vehicle_id"`
	VehicleLabel  string    `json:"vehicle_label,omitempty"`
	CategoryID    *int64    `json:"category_id"`
	CategoryName  string    `json:"category_name,omitempty"`
	IntervalDays  int       `json:"interval_days"`
	ServiceTypeID *int64    `json:"service_type_id"`
	ServiceName   string    `json:"service_name,omitempty"`
	ServiceAmount *float64  `json:"service_amount,omitempty"`
	Note          string    `json:"note"`
	Active        bool      `json:"active"`
	CreatedAt     time.Time `json:"created_at"`
}

type serviceTaskIn struct {
	Title         string `json:"title"`
	Scope         string `json:"scope"`
	VehicleID     *int64 `json:"vehicle_id"`
	CategoryID    *int64 `json:"category_id"`
	IntervalDays  int    `json:"interval_days"`
	ServiceTypeID *int64 `json:"service_type_id"`
	Note          string `json:"note"`
	Active        *bool  `json:"active"`
}

const serviceTaskCols = `t.id, t.title, t.scope, t.vehicle_id,
	COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), vc.name, ''), t.category_id, COALESCE(c.name, ''),
	t.interval_days, t.service_type_id, COALESCE(st.name, ''), st.default_amount, t.note, t.active, t.created_at`

const serviceTaskFrom = ` FROM service_tasks t
	LEFT JOIN vehicles v ON v.id = t.vehicle_id
	LEFT JOIN categories vc ON vc.id = v.category_id
	LEFT JOIN categories c ON c.id = t.category_id
	LEFT JOIN service_types st ON st.id = t.service_type_id`

func scanServiceTask(row rowScanner) (serviceTask, error) {
	var t serviceTask
	err := row.Scan(&t.ID, &t.Title, &t.Scope, &t.VehicleID, &t.VehicleLabel, &t.CategoryID, &t.CategoryName,
		&t.IntervalDays, &t.ServiceTypeID, &t.ServiceName, &t.ServiceAmount, &t.Note, &t.Active, &t.CreatedAt)
	return t, err
}

func (h *Handler) getServiceTask(ctx context.Context, id int64) (serviceTask, error) {
	return scanServiceTask(h.Pool.QueryRow(ctx, `SELECT `+serviceTaskCols+serviceTaskFrom+` WHERE t.id = $1`, id))
}

// parseServiceTask prüft die Eingabe; der Rückgabetext ist die deutsche Fehlermeldung.
func parseServiceTask(in *serviceTaskIn) string {
	in.Title, in.Note = trim(in.Title), trim(in.Note)
	if in.Title == "" {
		return "Titel fehlt"
	}
	if len([]rune(in.Title)) > maxServiceTitleRunes || !validNoteLength(in.Note) {
		return "Eingabe ist zu lang"
	}
	if in.IntervalDays < 1 || in.IntervalDays > 730 {
		return "Intervall muss zwischen 1 und 730 Tagen liegen"
	}
	switch in.Scope {
	case "vehicle":
		if in.VehicleID == nil {
			return "Gefährt fehlt"
		}
		in.CategoryID = nil
	case "category":
		if in.CategoryID == nil {
			return "Tarif fehlt"
		}
		in.VehicleID = nil
	case "all", "power":
		in.VehicleID, in.CategoryID = nil, nil
	default:
		return "Unbekannter Geltungsbereich"
	}
	return ""
}

// ListServiceTasks: GET /api/service-tasks.
func (h *Handler) ListServiceTasks(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(), `SELECT `+serviceTaskCols+serviceTaskFrom+` ORDER BY NOT t.active, lower(t.title), t.id`)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	out := []serviceTask{}
	for rows.Next() {
		t, err := scanServiceTask(rows)
		if err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		if hallStaff(r) {
			t.ServiceAmount = nil
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// CreateServiceTask: POST /api/service-tasks.
func (h *Handler) CreateServiceTask(w http.ResponseWriter, r *http.Request) {
	var in serviceTaskIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if msg := parseServiceTask(&in); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	active := in.Active == nil || *in.Active
	var id int64
	if err := h.Pool.QueryRow(r.Context(),
		`INSERT INTO service_tasks (title, scope, vehicle_id, category_id, interval_days, service_type_id, note, active)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		in.Title, in.Scope, in.VehicleID, in.CategoryID, in.IntervalDays, in.ServiceTypeID, in.Note, active).Scan(&id); err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "Gefährt, Tarif oder Leistung gibt es nicht")
			return
		}
		serverError(w, r, "could not create task", err)
		return
	}
	h.auditCreated(r, "service_task", id, "Serviceaufgabe angelegt: "+in.Title, map[string]any{
		"title": in.Title, "scope": in.Scope, "vehicle_id": in.VehicleID, "category_id": in.CategoryID,
		"interval_days": in.IntervalDays, "service_type_id": in.ServiceTypeID, "active": active,
	})
	t, err := h.getServiceTask(r.Context(), id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// UpdateServiceTask: PUT /api/service-tasks/{id}.
func (h *Handler) UpdateServiceTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in serviceTaskIn
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if msg := parseServiceTask(&in); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	old, err := h.getServiceTask(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	active := old.Active
	if in.Active != nil {
		active = *in.Active
	}
	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE service_tasks SET title=$2, scope=$3, vehicle_id=$4, category_id=$5, interval_days=$6,
		        service_type_id=$7, note=$8, active=$9, updated_at=now() WHERE id=$1`,
		id, in.Title, in.Scope, in.VehicleID, in.CategoryID, in.IntervalDays, in.ServiceTypeID, in.Note, active); err != nil {
		if isForeignKeyViolation(err) {
			writeError(w, http.StatusBadRequest, "Gefährt, Tarif oder Leistung gibt es nicht")
			return
		}
		serverError(w, r, "could not save", err)
		return
	}
	next := serviceTask{ID: id, Title: in.Title, Scope: in.Scope, VehicleID: in.VehicleID, CategoryID: in.CategoryID,
		IntervalDays: in.IntervalDays, ServiceTypeID: in.ServiceTypeID, Note: in.Note, Active: active, CreatedAt: old.CreatedAt}
	h.auditChange(r, "update", "service_task", id, "Serviceaufgabe geändert: "+in.Title,
		diffFields(old, next, "vehicle_label", "category_name", "service_name", "service_amount"))
	t, err := h.getServiceTask(r.Context(), id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// DeleteServiceTask: DELETE /api/service-tasks/{id}. Die Erledigungen gehen mit;
// abgerechnete Positionen bleiben, wo sie sind (charge_id ist nur ein Verweis).
func (h *Handler) DeleteServiceTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var title, scope string
	var interval int
	err := h.Pool.QueryRow(r.Context(),
		`DELETE FROM service_tasks WHERE id=$1 RETURNING title, scope, interval_days`, id).Scan(&title, &scope, &interval)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		serverError(w, r, "could not delete", err)
		return
	}
	h.auditDeleted(r, "service_task", id, "Serviceaufgabe gelöscht: "+title,
		map[string]any{"title": title, "scope": scope, "interval_days": interval})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

type serviceDue struct {
	TaskID        int64     `json:"task_id"`
	Title         string    `json:"title"`
	VehicleID     int64     `json:"vehicle_id"`
	VehicleLabel  string    `json:"vehicle_label"`
	LicensePlate  string    `json:"license_plate"`
	PersonID      int64     `json:"person_id"`
	PersonName    string    `json:"person_name"`
	HallName      string    `json:"hall_name"`
	DueOn         time.Time `json:"due_on"`
	LastDone      *string   `json:"last_done"`
	ServiceTypeID *int64    `json:"service_type_id"`
	ServiceName   string    `json:"service_name,omitempty"`
	ServiceAmount *float64  `json:"service_amount,omitempty"`
}

// serviceAppliesSQL: welche Gefährte eine Aufgabe betrifft (Alias t und v).
const serviceAppliesSQL = `(t.scope = 'all'
	OR (t.scope = 'vehicle' AND v.id = t.vehicle_id)
	OR (t.scope = 'category' AND v.category_id = t.category_id)
	OR (t.scope = 'power' AND v.needs_power))`

func (h *Handler) serviceDueList(ctx context.Context, until time.Time) ([]serviceDue, error) {
	rows, err := h.Pool.Query(ctx,
		`WITH app AS (
		   SELECT t.id AS task_id, t.title, t.interval_days, t.service_type_id,
		          v.id AS vehicle_id, COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), c.name) AS label,
		          v.license_plate, v.person_id, trim(p.first_name || ' ' || p.last_name) AS person_name,
		          COALESCE(hl.name, '') AS hall_name,
		          (SELECT max(l.done_on) FROM service_task_log l WHERE l.task_id = t.id AND l.vehicle_id = v.id) AS last_done,
		          GREATEST(v.start_date, t.created_at::date) AS since
		     FROM service_tasks t
		     JOIN vehicles v ON NOT v.archived AND v.status = 'stored' AND `+serviceAppliesSQL+`
		     JOIN categories c ON c.id = v.category_id
		     JOIN persons p ON p.id = v.person_id
		     LEFT JOIN spots s ON s.id = v.spot_id
		     LEFT JOIN halls hl ON hl.id = s.hall_id
		    WHERE t.active)
		 SELECT a.task_id, a.title, a.vehicle_id, a.label, a.license_plate, a.person_id, a.person_name, a.hall_name,
		        COALESCE(a.last_done, a.since) + a.interval_days, to_char(a.last_done, 'YYYY-MM-DD'),
		        a.service_type_id, COALESCE(st.name, ''), st.default_amount
		   FROM app a LEFT JOIN service_types st ON st.id = a.service_type_id
		  WHERE COALESCE(a.last_done, a.since) + a.interval_days <= $1
		  ORDER BY 9, lower(a.label), a.task_id
		  LIMIT `+strconv.Itoa(serviceDueMaxRows), until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []serviceDue{}
	for rows.Next() {
		var d serviceDue
		if err := rows.Scan(&d.TaskID, &d.Title, &d.VehicleID, &d.VehicleLabel, &d.LicensePlate, &d.PersonID, &d.PersonName,
			&d.HallName, &d.DueOn, &d.LastDone, &d.ServiceTypeID, &d.ServiceName, &d.ServiceAmount); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ServiceTasksDue: GET /api/service-tasks/due?days=7. Dazu der Vorschlag „Erhaltungs-
// ladung prüfen“, solange Gefährte mit Ladebedarf eingelagert sind und keine aktive
// Aufgabe für sie existiert.
func (h *Handler) ServiceTasksDue(w http.ResponseWriter, r *http.Request) {
	days := serviceDueDefaultDay
	if s := r.URL.Query().Get("days"); s != "" {
		if !validNumericParamLength(s) {
			writeError(w, http.StatusBadRequest, "days is too long")
			return
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 365 {
			writeError(w, http.StatusBadRequest, "days must be 0..365")
			return
		}
		days = n
	}
	today := startOfDayUTC(h.now())
	items, err := h.serviceDueList(r.Context(), today.AddDate(0, 0, days))
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	var powerCount int
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT count(*) FROM vehicles v
		  WHERE NOT v.archived AND v.status = 'stored' AND v.needs_power
		    AND NOT EXISTS (SELECT 1 FROM service_tasks t WHERE t.active AND t.scope = 'power')`).Scan(&powerCount); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	overdue := 0
	for i, d := range items {
		if d.DueOn.Before(today) {
			overdue++
		}
		if hallStaff(r) {
			items[i].ServiceAmount = nil
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "overdue": overdue, "power_without_task": powerCount})
}

// CompleteServiceTask: POST /api/service-tasks/{id}/done {vehicle_id, done_on?, note?, bill?}.
func (h *Handler) CompleteServiceTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		VehicleID int64   `json:"vehicle_id"`
		DoneOn    *string `json:"done_on"`
		Note      string  `json:"note"`
		Bill      bool    `json:"bill"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.Note = trim(in.Note)
	if in.VehicleID <= 0 || !validNoteLength(in.Note) {
		writeError(w, http.StatusBadRequest, "vehicle_id fehlt oder Notiz zu lang")
		return
	}
	doneOn := startOfDayUTC(h.now())
	if in.DoneOn != nil && trim(*in.DoneOn) != "" {
		d, err := parseOptDate(in.DoneOn)
		if err != nil || d == nil {
			writeError(w, http.StatusBadRequest, "done_on must be YYYY-MM-DD")
			return
		}
		if d.After(doneOn) {
			writeError(w, http.StatusBadRequest, "Erledigt kann nicht in der Zukunft liegen")
			return
		}
		doneOn = *d
	}
	var (
		title, serviceName string
		serviceID          *int64
		amount             *float64
		personID           int64
		applies            bool
	)
	err := h.Pool.QueryRow(r.Context(),
		`SELECT t.title, t.service_type_id, COALESCE(st.name, ''), st.default_amount, v.person_id, `+serviceAppliesSQL+`
		   FROM service_tasks t
		   JOIN vehicles v ON v.id = $2 AND NOT v.archived
		   LEFT JOIN service_types st ON st.id = t.service_type_id
		  WHERE t.id = $1 AND t.active`, id, in.VehicleID).Scan(&title, &serviceID, &serviceName, &amount, &personID, &applies)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Aufgabe oder Gefährt nicht gefunden")
		return
	}
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if !applies {
		writeError(w, http.StatusBadRequest, "Die Aufgabe gilt nicht für dieses Gefährt")
		return
	}
	if in.Bill && hallStaff(r) {
		writeError(w, http.StatusForbidden, "Abrechnen macht das Büro; als erledigt vermerken geht.")
		return
	}
	if in.Bill && (serviceID == nil || amount == nil) {
		writeError(w, http.StatusBadRequest, "An der Aufgabe hängt keine Leistung zum Abrechnen")
		return
	}
	var doneBy *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		doneBy = &u.ID
	}
	var logID int64
	err = h.Pool.QueryRow(r.Context(),
		`INSERT INTO service_task_log (task_id, vehicle_id, done_on, note, done_by) VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (task_id, vehicle_id, done_on) DO NOTHING RETURNING id`,
		id, in.VehicleID, doneOn, in.Note, doneBy).Scan(&logID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "An diesem Tag schon erledigt")
		return
	}
	if err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	var chargeID *int64
	if in.Bill {
		desc := serviceName
		if title != serviceName {
			desc += " – " + title
		}
		cid, status, msg := h.chargeViaHandler(r, logID, chargeRequest{PersonID: personID, VehicleID: &in.VehicleID,
			Description: desc, Amount: *amount, Quantity: 1, quantitySet: true, ChargedOn: doneOn.Format(dateLayout)})
		if cid == 0 {
			// Ohne Position kein halber Vorgang: die Erledigung wird zurückgenommen.
			if _, derr := h.Pool.Exec(r.Context(), `DELETE FROM service_task_log WHERE id = $1`, logID); derr != nil {
				serverError(w, r, "could not roll back", derr)
				return
			}
			writeError(w, status, "Position nicht angelegt: "+msg)
			return
		}
		chargeID = &cid
		if _, err := h.Pool.Exec(r.Context(), `UPDATE service_task_log SET charge_id = $2 WHERE id = $1`, logID, cid); err != nil {
			serverError(w, r, "could not save", err)
			return
		}
	}
	h.auditCreated(r, "service_task_log", logID, "Serviceaufgabe erledigt: "+title, map[string]any{
		"task_id": id, "vehicle_id": in.VehicleID, "done_on": doneOn.Format(dateLayout), "charge_id": chargeID,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"id": logID, "done_on": doneOn.Format(dateLayout), "charge_id": chargeID})
}

// chargeViaHandler legt die Position über CreateCharge an (gleiche Prüfungen, Sperren
// und Idempotenz wie das Formular). Der Schlüssel hängt an der Erledigung, damit eine
// Wiederholung keine zweite Position erzeugt.
func (h *Handler) chargeViaHandler(r *http.Request, logID int64, req chargeRequest) (int64, int, string) {
	body, err := json.Marshal(map[string]any{
		"person_id": req.PersonID, "vehicle_id": req.VehicleID, "description": req.Description,
		"amount": req.Amount, "quantity": req.Quantity, "charged_on": req.ChargedOn,
	})
	if err != nil {
		return 0, http.StatusInternalServerError, err.Error()
	}
	sub := httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(body)).WithContext(r.Context())
	sub.Header.Set("Content-Type", "application/json")
	sub.Header.Set("Idempotency-Key", "service-task-log-"+strconv.FormatInt(logID, 10))
	rec := httptest.NewRecorder()
	h.CreateCharge(rec, sub)
	var out struct {
		ID    int64  `json:"id"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		if out.Error == "" {
			out.Error = http.StatusText(rec.Code)
		}
		return 0, rec.Code, out.Error
	}
	return out.ID, rec.Code, ""
}

// UndoServiceTask: DELETE /api/service-task-log/{id}. Nimmt die Erledigung zurück; eine
// dabei angelegte Position löscht die Oberfläche danach über den normalen Weg (dort
// greifen die Sperren für abgerechnete Positionen).
func (h *Handler) UndoServiceTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var taskID, vehicleID int64
	var doneOn time.Time
	var chargeID *int64
	err := h.Pool.QueryRow(r.Context(),
		`DELETE FROM service_task_log WHERE id = $1 RETURNING task_id, vehicle_id, done_on, charge_id`, id).
		Scan(&taskID, &vehicleID, &doneOn, &chargeID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		serverError(w, r, "could not delete", err)
		return
	}
	h.auditDeleted(r, "service_task_log", id, "Erledigung zurückgenommen", map[string]any{
		"task_id": taskID, "vehicle_id": vehicleID, "done_on": doneOn.Format(dateLayout), "charge_id": chargeID,
	})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "charge_id": chargeID})
}
