package handlers

import (
	"encoding/csv"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CSV export for accounting/backups. Written with a semicolon delimiter and a
// UTF-8 BOM so German Excel/LibreOffice open it with correct columns and umlauts;
// amounts use a decimal comma so they re-import as numbers.

func csvMoney(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 2, 64), ".", ",", 1)
}

func csvDate(t time.Time) string { return t.Format("2006-01-02") }

func csvDateP(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

// csvSafe neutralizes spreadsheet formula injection (CWE-1236): a cell whose
// first byte is a formula trigger (= + - @) or a control char (TAB/CR) is
// prefixed with a single apostrophe, which Excel/LibreOffice render as a text
// literal. Parkrr's own CSV import strips this guard again (see csvUnguard) so
// an exported file re-imports losslessly.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// ExportCSV streams one dataset as CSV. The entity is a path value:
// outstanding | payments | persons | vehicles. Read-only (any authenticated
// user), since it only re-serves data the caller can already see in the UI.
func (h *Handler) ExportCSV(w http.ResponseWriter, r *http.Request) {
	entity := r.PathValue("entity")
	var name string
	var header []string
	var rows [][]string

	switch entity {
	case "outstanding":
		name = "offene-posten"
		header = []string{"person_id", "name", "offen_eur"}
		bal, err := h.outstandingByPerson(r) // map[personID]outstanding, names not included
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Export fehlgeschlagen")
			return
		}
		names, err := h.personNames(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Export fehlgeschlagen")
			return
		}
		type ob struct {
			pid  int64
			name string
			amt  float64
		}
		var obs []ob
		for pid, amt := range bal {
			if amt > 0.005 { // only genuinely open balances
				obs = append(obs, ob{pid, names[pid], amt})
			}
		}
		// Most-owed first: a dunning-ready order.
		sort.Slice(obs, func(i, j int) bool { return obs[i].amt > obs[j].amt })
		for _, o := range obs {
			rows = append(rows, []string{strconv.FormatInt(o.pid, 10), o.name, csvMoney(o.amt)})
		}

	case "payments":
		name = "zahlungen"
		header = []string{"datum", "person", "betrag_eur", "methode", "gefaehrt_id", "storniert", "notiz"}
		rr, err := h.Pool.Query(r.Context(),
			`SELECT p.paid_on, per.first_name, per.last_name, p.amount, p.method, p.vehicle_id, p.reversed, p.note
			   FROM payments p JOIN persons per ON per.id = p.person_id
			  ORDER BY p.paid_on DESC, p.id DESC`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Export fehlgeschlagen")
			return
		}
		defer rr.Close()
		for rr.Next() {
			var paidOn time.Time
			var fn, ln, method, note string
			var amount float64
			var vid *int64
			var reversed bool
			if err := rr.Scan(&paidOn, &fn, &ln, &amount, &method, &vid, &reversed, &note); err != nil {
				writeError(w, http.StatusInternalServerError, "Export fehlgeschlagen")
				return
			}
			veh := ""
			if vid != nil {
				veh = strconv.FormatInt(*vid, 10)
			}
			rows = append(rows, []string{
				csvDate(paidOn), strings.TrimSpace(fn + " " + ln), csvMoney(amount),
				method, veh, boolJaNein(reversed), note,
			})
		}
		if rr.Err() != nil {
			writeError(w, http.StatusInternalServerError, "Export fehlgeschlagen")
			return
		}

	case "persons":
		name = "personen"
		header = []string{"id", "vorname", "nachname", "email", "telefon", "adresse"}
		rr, err := h.Pool.Query(r.Context(),
			`SELECT id, first_name, last_name, email, phone, address FROM persons ORDER BY last_name, first_name`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Export fehlgeschlagen")
			return
		}
		defer rr.Close()
		for rr.Next() {
			var id int64
			var fn, ln, email, phone, addr string
			if err := rr.Scan(&id, &fn, &ln, &email, &phone, &addr); err != nil {
				writeError(w, http.StatusInternalServerError, "Export fehlgeschlagen")
				return
			}
			rows = append(rows, []string{strconv.FormatInt(id, 10), fn, ln, email, phone, addr})
		}
		if rr.Err() != nil {
			writeError(w, http.StatusInternalServerError, "Export fehlgeschlagen")
			return
		}

	case "vehicles":
		name = "gefaehrte"
		header = []string{"id", "bezeichnung", "kennzeichen", "kategorie", "person", "status", "abrechnung", "tarif_eur", "beginn", "ende", "archiviert"}
		rr, err := h.Pool.Query(r.Context(),
			`SELECT v.id, v.label, v.license_plate, c.name, per.first_name, per.last_name,
			        v.status, v.billing_period, v.rate, v.start_date, v.end_date, v.archived
			   FROM vehicles v
			   JOIN persons per ON per.id = v.person_id
			   JOIN categories c ON c.id = v.category_id
			  ORDER BY per.last_name, v.label`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Export fehlgeschlagen")
			return
		}
		defer rr.Close()
		for rr.Next() {
			var id int64
			var label, plate, cat, fn, ln, status, period string
			var rate float64
			var start time.Time
			var end *time.Time
			var archived bool
			if err := rr.Scan(&id, &label, &plate, &cat, &fn, &ln, &status, &period, &rate, &start, &end, &archived); err != nil {
				writeError(w, http.StatusInternalServerError, "Export fehlgeschlagen")
				return
			}
			rows = append(rows, []string{
				strconv.FormatInt(id, 10), label, plate, cat, strings.TrimSpace(fn + " " + ln),
				status, period, csvMoney(rate), csvDate(start), csvDateP(end), boolJaNein(archived),
			})
		}
		if rr.Err() != nil {
			writeError(w, http.StatusInternalServerError, "Export fehlgeschlagen")
			return
		}

	default:
		writeError(w, http.StatusNotFound, "unbekannter Export")
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		`attachment; filename="parkrr-`+name+`-`+time.Now().Format("2006-01-02")+`.csv"`)
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM for Excel
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	// Guard every data cell against formula injection (header is developer-controlled).
	for i := range rows {
		for j := range rows[i] {
			rows[i][j] = csvSafe(rows[i][j])
		}
	}
	_ = cw.Write(header)
	_ = cw.WriteAll(rows)
	cw.Flush()
}

func boolJaNein(b bool) string {
	if b {
		return "ja"
	}
	return ""
}

// personNames returns id -> "Vorname Nachname" for every person.
func (h *Handler) personNames(r *http.Request) (map[int64]string, error) {
	names := map[int64]string{}
	rows, err := h.Pool.Query(r.Context(), `SELECT id, first_name, last_name FROM persons`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var fn, ln string
		if err := rows.Scan(&id, &fn, &ln); err != nil {
			return nil, err
		}
		names[id] = strings.TrimSpace(fn + " " + ln)
	}
	return names, rows.Err()
}
