package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

// Kundenwünsche aus dem Portal (Hundert 85 + 87). Grundsatz: das Portal SCHREIBT
// NIE in Stammdaten — es hinterlegt einen Wunsch, der Betreiber übernimmt ihn
// (oder lehnt ab), und beides steht im Protokoll. Der einzige öffentliche
// Schreibweg bleibt damit ein Briefkasten, kein Stift.

// maxOpenPortalRequests deckelt offene Wünsche je Person: der Endpunkt hängt an
// einem Bearer-Link, und ein Briefkasten ohne Deckel ist eine Spam-Fläche.
const maxOpenPortalRequests = 5

type portalRequestIn struct {
	Kind string `json:"kind"` // contact_update | pickup
	// contact_update: gewünschte neue Werte (leere Felder = unverändert lassen)
	Email   string `json:"email"`
	Phone   string `json:"phone"`
	Address string `json:"address"`
	// pickup: Wunschtermin + Freitext
	Date string `json:"date"` // YYYY-MM-DD
	Note string `json:"note"`
}

// PortalCreateRequest nimmt einen Wunsch entgegen (PUBLIC, token-scoped).
func (h *Handler) PortalCreateRequest(w http.ResponseWriter, r *http.Request) {
	pid, ok := h.portalPerson(r)
	if !ok {
		writeError(w, http.StatusNotFound, "Link ungültig oder abgelaufen")
		return
	}
	var req portalRequestIn
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Email, req.Phone, req.Address = trim(req.Email), trim(req.Phone), trim(req.Address)
	req.Date, req.Note = trim(req.Date), trim(req.Note)

	payload := map[string]string{}
	switch req.Kind {
	case "contact_update":
		if req.Email == "" && req.Phone == "" && req.Address == "" {
			writeError(w, http.StatusBadRequest, "nothing to change")
			return
		}
		// Dieselben Längen-/Syntaxregeln wie die Stammdaten selbst: ein Wunsch, den
		// die Übernahme ablehnen müsste, wird gar nicht erst angenommen.
		if req.Email != "" && !validEmail(req.Email) {
			writeError(w, http.StatusBadRequest, "E-Mail ist ungültig oder zu lang")
			return
		}
		if !validPhoneLength(req.Phone) || !validAddressLength(req.Address) {
			writeError(w, http.StatusBadRequest, "Eingabe ist zu lang")
			return
		}
		if req.Email != "" {
			payload["email"] = req.Email
		}
		if req.Phone != "" {
			payload["phone"] = req.Phone
		}
		if req.Address != "" {
			payload["address"] = req.Address
		}
	case "pickup":
		if req.Date == "" {
			writeError(w, http.StatusBadRequest, "date is required")
			return
		}
		d, err := time.Parse(dateLayout, req.Date)
		if err != nil {
			writeError(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
			return
		}
		// Ein Abholtermin in der Vergangenheit ist ein Tippfehler, keine Absicht.
		if d.Before(startOfDayUTC(h.now())) {
			writeError(w, http.StatusBadRequest, "date must not be in the past")
			return
		}
		if !validNoteLength(req.Note) {
			writeError(w, http.StatusBadRequest, "note is too long")
			return
		}
		payload["date"] = req.Date
		if req.Note != "" {
			payload["note"] = req.Note
		}
	default:
		writeError(w, http.StatusBadRequest, "unknown kind")
		return
	}

	// Zählen und Einfügen gehören in EINE Transaktion, serialisiert über eine
	// transaktionsgebundene Sperre je Person. Als zwei getrennte Abfragen war der
	// Deckel eine Prüf-dann-Handle-Lücke: zwanzig gleichzeitige Einreichungen mit
	// demselben gültigen Portal-Link lasen alle 0 offene Anfragen und legten alle
	// zwanzig an — samt zwanzig Protokolleinträgen. Dasselbe Muster wie beim
	// Zahlungsabgleich (payments.go) und bei den Planer-Icons.
	raw, _ := json.Marshal(payload)
	var reqID int64
	var capped bool
	txErr := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		// Über advisoryKey, nicht über `hashtext($1)::bigint`: hashtext liefert einen
		// vorzeichenbehafteten int4 und schnitte den Schlüsselraum von 2^64 auf 2^32
		// zusammen. Da sich alle Sperren dieser Datenbank EINEN Namensraum teilen,
		// könnte eine Kollision auch eine fremde Sperre treffen — die Einreichung eines
		// Kunden bliebe grundlos hängen, hinter etwas, das nichts mit ihr zu tun hat.
		if _, e := tx.Exec(r.Context(), `SELECT pg_advisory_xact_lock($1)`,
			advisoryKey("parkrr.portal_request."+strconv.FormatInt(pid, 10))); e != nil {
			return e
		}
		var open int
		if e := tx.QueryRow(r.Context(),
			`SELECT count(*) FROM portal_requests WHERE person_id=$1 AND status='offen'`, pid).Scan(&open); e != nil {
			return e
		}
		if open >= maxOpenPortalRequests {
			capped = true
			return nil
		}
		return tx.QueryRow(r.Context(),
			`INSERT INTO portal_requests (person_id, kind, payload) VALUES ($1,$2,$3) RETURNING id`,
			pid, req.Kind, raw).Scan(&reqID)
	})
	if txErr != nil {
		serverError(w, r, "could not store request", txErr)
		return
	}
	if capped {
		writeError(w, http.StatusTooManyRequests, "zu viele offene Anfragen — bitte warten Sie auf eine Antwort")
		return
	}
	// Der Wunsch ist eine Handlung von außen und gehört ins Protokoll — als
	// System-Eintrag, denn hinter dem Token steht kein angemeldeter Benutzer.
	h.AuditSystem(r.Context(), "create", "portal_request", reqID,
		"Kundenwunsch aus dem Portal ("+req.Kind+")",
		// auditSnapshot, nicht die rohe Map: die Audit-Ansicht liest je Feld {old,new}.
		// Ein blanker Wert erschien dort als "∅ → ∅" — der Eintrag verlor genau die
		// Angaben, für die er geschrieben wurde.
		auditSnapshot(map[string]any{"person_id": pid, "kind": req.Kind}))
	writeJSON(w, http.StatusCreated, map[string]any{"status": "eingereicht", "id": reqID})
}

// startOfDayUTC: Mitternacht des heutigen Kalendertags, UTC-verankert wie die
// Trägerform der DATE-Spalten (siehe models.DayAfter) — "heute" zählt noch als
// gültiger Abholtermin. Der frühere Name models_DayBefore täuschte eine
// Zugehörigkeit zum models-Paket vor, die es nicht gibt.
func startOfDayUTC(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// portalRequestOut ist die Betreibersicht eines Wunschs.
type portalRequestOut struct {
	ID         int64           `json:"id"`
	PersonID   int64           `json:"person_id"`
	PersonName string          `json:"person_name"`
	Kind       string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
	Status     string          `json:"status"`
	CreatedAt  time.Time       `json:"created_at"`
}

// ListPortalRequests zeigt dem Betreiber die Wünsche (editor+), offene zuerst.
func (h *Handler) ListPortalRequests(w http.ResponseWriter, r *http.Request) {
	limit, offset := pageParams(r, 100, 500)
	rows, err := h.Pool.Query(r.Context(),
		`SELECT pr.id, pr.person_id, trim(p.first_name || ' ' || p.last_name), pr.kind, pr.payload, pr.status, pr.created_at
		   FROM portal_requests pr JOIN persons p ON p.id = pr.person_id
		  ORDER BY (pr.status = 'offen') DESC, pr.created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	out := []portalRequestOut{}
	for rows.Next() {
		var e portalRequestOut
		if err := rows.Scan(&e.ID, &e.PersonID, &e.PersonName, &e.Kind, &e.Payload, &e.Status, &e.CreatedAt); err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ResolvePortalRequest erledigt einen Wunsch (editor+). action "apply" übernimmt
// bei contact_update die gewünschten Felder in die Stammdaten (validiert, mit
// Vorher/Nachher im Protokoll); "reject" lehnt ab; "done" schließt ohne Änderung
// (für pickup: der Termin wurde außerhalb vereinbart).
func (h *Handler) ResolvePortalRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Action string `json:"action"` // apply | done | reject
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.Action != "apply" && in.Action != "done" && in.Action != "reject" {
		writeError(w, http.StatusBadRequest, "unknown action")
		return
	}

	var uid *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		uid = &u.ID
	}
	var notFound, alreadyDone bool
	txErr := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var pid int64
		var kind, status string
		var payload []byte
		if err := tx.QueryRow(r.Context(),
			`SELECT person_id, kind, payload, status FROM portal_requests WHERE id=$1 FOR UPDATE`, id).
			Scan(&pid, &kind, &payload, &status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				notFound = true
				return nil
			}
			return err
		}
		if status != "offen" {
			alreadyDone = true
			return nil
		}
		if in.Action == "apply" {
			if kind != "contact_update" {
				return errApplyNotContact
			}
			var want map[string]string
			if err := json.Unmarshal(payload, &want); err != nil {
				return err
			}
			var oldE, oldP, oldA string
			var anonymized bool
			if err := tx.QueryRow(r.Context(),
				`SELECT email, phone, address, anonymized FROM persons WHERE id=$1 FOR UPDATE`, pid).
				Scan(&oldE, &oldP, &oldA, &anonymized); err != nil {
				return err
			}
			// Ein Wunsch kann älter sein als die Löschung der Person. Ihn danach zu
			// übernehmen schriebe genau die Kontaktdaten zurück, die eine Auskunfts-
			// sperre bzw. Art.-17-Löschung entfernt hat — die Löschung wäre rückgängig
			// gemacht, ohne dass es jemandem auffällt. Der Wunsch wird deshalb nicht
			// angewandt; abweisen und erledigen bleiben möglich.
			if anonymized {
				return errApplyAnonymized
			}
			newE, newP, newA := oldE, oldP, oldA
			if v, ok := want["email"]; ok {
				newE = v
			}
			if v, ok := want["phone"]; ok {
				newP = v
			}
			if v, ok := want["address"]; ok {
				newA = v
			}
			if _, err := tx.Exec(r.Context(),
				`UPDATE persons SET email=$1, phone=$2, address=$3, updated_at=now() WHERE id=$4`,
				newE, newP, newA, pid); err != nil {
				return err
			}
			if err := h.auditChangeTx(r.Context(), tx, r, "update", "person", pid,
				"Kundenwunsch übernommen: Kontaktdaten aktualisiert",
				diffFields(map[string]any{"email": oldE, "phone": oldP, "address": oldA},
					map[string]any{"email": newE, "phone": newP, "address": newA})); err != nil {
				return err
			}
		}
		status = "erledigt"
		if in.Action == "reject" {
			status = "abgelehnt"
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE portal_requests SET status=$1, resolved_at=now(), resolved_by=$2 WHERE id=$3`,
			status, uid, id); err != nil {
			return err
		}
		return h.auditChangeTx(r.Context(), tx, r, "update", "portal_request", id,
			"Kundenwunsch "+status+" ("+kind+")", auditSnapshot(map[string]any{"action": in.Action, "person_id": pid}))
	})
	switch {
	case errors.Is(txErr, errApplyNotContact):
		writeError(w, http.StatusBadRequest, "apply gilt nur für Kontaktdaten-Wünsche")
		return
	case errors.Is(txErr, errApplyAnonymized):
		writeError(w, http.StatusConflict,
			"Person ist anonymisiert — der Wunsch kann nicht übernommen werden (nur ablehnen oder erledigen)")
		return
	case txErr != nil:
		serverError(w, r, "could not resolve request", txErr)
		return
	case notFound:
		writeError(w, http.StatusNotFound, "request not found")
		return
	case alreadyDone:
		writeError(w, http.StatusConflict, "request already resolved")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

var errApplyNotContact = errors.New("apply is only for contact_update")

// errApplyAnonymized: die Person wurde inzwischen anonymisiert. Das Übernehmen
// eines älteren Kontaktdaten-Wunsches würde die Löschung rückgängig machen.
var errApplyAnonymized = errors.New("person is anonymized")
