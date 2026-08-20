package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/models"
)

// personColumns is the shared column list for reading persons. has_flat_rate is
// derived live from the agreement records — the legacy persons.flat_rate_*
// columns are frozen (no writer remains) and must not drive the badge.
const personColumns = `id, first_name, last_name, email, phone, address, notes,
	created_at, updated_at, anonymized,
	EXISTS(SELECT 1 FROM flat_rate_periods fp WHERE fp.person_id = persons.id)`

func scanPerson(row rowScanner) (models.Person, error) {
	var p models.Person
	err := row.Scan(&p.ID, &p.FirstName, &p.LastName, &p.Email, &p.Phone, &p.Address,
		&p.Notes, &p.CreatedAt, &p.UpdatedAt, &p.Anonymized, &p.HasFlatRate)
	return p, err
}

// personLabel returns a person's display name for audit messages ("" on error).
func (h *Handler) personLabel(r *http.Request, id int64) string {
	return personLabelTx(r.Context(), h.Pool, id)
}

// personLabelTx resolves a person's display label through the given querier —
// pass the caller's tx when one is open, so this doesn't grab a second pooled
// connection while the tx is held (which would deadlock the pool).
func personLabelTx(ctx context.Context, q rowQuerier, id int64) string {
	var name string
	_ = q.QueryRow(ctx,
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
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
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
	h.auditCreated(r, "person", p.ID, "created person "+trim(p.FirstName+" "+p.LastName), map[string]any{
		"first_name": p.FirstName, "last_name": p.LastName, "email": p.Email,
		"phone": p.Phone, "address": p.Address,
	})
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
		// AND NOT anonymized ist der serverseitige Riegel gegen einen gebastelten
		// PUT, der gelöschte Personendaten wieder einträgt (DSGVO Art. 17). Die
		// Oberfläche blendet das Formular aus; darauf allein darf man sich nicht
		// verlassen.
		`UPDATE persons SET first_name=$1, last_name=$2, email=$3, phone=$4,
		        address=$5, notes=$6, updated_at=now()
		 WHERE id=$7 AND NOT anonymized`,
		req.FirstName, req.LastName, req.Email, req.Phone, req.Address, req.Notes, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update person")
		return
	}
	if ct.RowsAffected() == 0 {
		// Kein Treffer heißt hier zweierlei: Person gibt es nicht ODER sie ist
		// anonymisiert und der Riegel oben hat gegriffen. Ein pauschales 404 wäre im
		// zweiten Fall falsch und verwirrend — die Person existiert ja, sie darf nur
		// nicht wiederbefüllt werden.
		var anon bool
		if qerr := h.Pool.QueryRow(r.Context(),
			`SELECT anonymized FROM persons WHERE id=$1`, id).Scan(&anon); qerr == nil && anon {
			writeError(w, http.StatusConflict,
				"person is anonymized (DSGVO Art. 17) and cannot be edited")
			return
		}
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
	// BAO §132: recorded payments are money-in Grundaufzeichnungen and must be kept.
	// The payments/charges FKs cascade, so a person with a booked payment would have
	// it silently wiped on delete — block that (invoices are already FK-RESTRICTed).
	var hasPayments bool
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM payments WHERE person_id=$1)`, id).Scan(&hasPayments); err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete person")
		return
	}
	if hasPayments {
		writeError(w, http.StatusConflict,
			"Person hat erfasste Zahlungen und kann nicht gelöscht werden (Aufbewahrungspflicht).")
		return
	}
	// RETURNING captures who was deleted, atomically with the delete — once the row
	// is gone an id-only audit entry could never be resolved back to a person.
	var delFirst, delLast, delEmail string
	err := h.Pool.QueryRow(r.Context(),
		`DELETE FROM persons WHERE id = $1 RETURNING first_name, last_name, email`, id).
		Scan(&delFirst, &delLast, &delEmail)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "person not found")
			return
		}
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
	name := strings.TrimSpace(delFirst + " " + delLast)
	h.auditDeleted(r, "person", id, "deleted person "+name, map[string]any{
		"first_name": delFirst, "last_name": delLast, "email": delEmail,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// AnonymizePerson löscht die personenbezogenen Daten im lebenden Stammsatz
// (DSGVO Art. 17), lässt die Zeile und alles daran Hängende aber bestehen.
//
// Warum nicht einfach löschen: invoices.person_id ist FK-RESTRICTed und Belege sind
// sieben Jahre aufbewahrungspflichtig (BAO §132) — DeletePerson lehnt eine Person mit
// Rechnungen darum ab. Anonymisieren ist der Ausweg aus dieser Sackgasse: die
// Buchhaltung bleibt vollständig, die Personenbeziehung verschwindet.
//
// Die eingefrorenen Rechnungs-Snapshots (invoices.buyer_snapshot, beim Ausstellen mit
// Name und Adresse festgeschrieben) bleiben ABSICHTLICH unangetastet. Sie sind Teil
// des Belegs; sie nachträglich zu ändern hieße, ein aufbewahrungspflichtiges Dokument
// zu verfälschen. Art. 17 Abs. 3 lit. b DSGVO nimmt genau solche gesetzlichen
// Aufbewahrungspflichten von der Löschpflicht aus.
//
// Unumkehrbar. Wirkt nur einmal (WHERE NOT anonymized).
func (h *Handler) AnonymizePerson(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	// Der Platzhalter bleibt eindeutig und stabil, damit Listen und Verweise weiter
	// etwas Anzeigbares haben, ohne wieder auf eine Person zu zeigen.
	var wasAnon bool
	err := h.Pool.QueryRow(r.Context(),
		`WITH prev AS (SELECT anonymized FROM persons WHERE id=$1)
		 UPDATE persons
		    SET first_name = 'Anonymisiert', last_name = '#' || id,
		        email = '', phone = '', address = '', notes = '',
		        anonymized = TRUE, updated_at = now()
		  WHERE id = $1 AND NOT anonymized
		  RETURNING (SELECT anonymized FROM prev)`, id).Scan(&wasAnon)
	if errors.Is(err, pgx.ErrNoRows) {
		// Kein Treffer heißt: Person existiert nicht ODER war schon anonymisiert.
		// Für den Aufrufer sind das zwei verschiedene Antworten.
		var exists, already bool
		if qerr := h.Pool.QueryRow(r.Context(),
			`SELECT TRUE, anonymized FROM persons WHERE id=$1`, id).Scan(&exists, &already); qerr != nil {
			writeError(w, http.StatusNotFound, "person not found")
			return
		}
		if already {
			writeError(w, http.StatusConflict, "person is already anonymized")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not anonymize person")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not anonymize person")
		return
	}
	// Der Eintrag hält NUR fest, DASS anonymisiert wurde — bewusst ohne die
	// gelöschten Werte. Ein Trail, der beim Löschen den Namen mitschreibt, hebt die
	// Löschung auf: audit_log ist append-only und wird sieben Jahre aufbewahrt.
	// (Treckrr schreibt an dieser Stelle den alten Namen in die Zusammenfassung; das
	// ist hier bewusst nicht übernommen.)
	h.auditChange(r, "anonymize", "person", id,
		"Personendaten anonymisiert (DSGVO Art. 17) — Belege bleiben aufbewahrungspflichtig erhalten",
		auditSnapshot(map[string]any{"anonymized": true}))
	p, gerr := h.getPerson(r.Context(), id)
	if gerr != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "anonymized"})
		return
	}
	writeJSON(w, http.StatusOK, p)
}
