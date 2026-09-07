package handlers

import (
	"net/http"
	"time"
)

// timelineEvent ist ein Eintrag im Verlauf einer Person (Hundert 56).
type timelineEvent struct {
	At    time.Time `json:"at"`
	Kind  string    `json:"kind"` // payment | invoice | charge | status | handover | agreement
	Text  string    `json:"text"`
	RefID int64     `json:"ref_id,omitempty"`
}

// PersonTimeline liefert die Geschichte einer Person in EINEM Strom: Zahlungen,
// Rechnungen, Zusatzkosten, Statuswechsel der Gefährte, Übergaben, Pauschalen
// (Hundert 56). Bisher steckten diese Ereignisse in fünf Reitern — "was ist bei
// Familie X zuletzt passiert?" hieß fünfmal blättern.
//
// Aus den DOMÄNENTABELLEN, nicht aus dem Audit-Log: das Audit ist rollenbeschränkt
// (admin), unterliegt Aufbewahrungsfenstern und spricht Technik ("update person");
// die Domänentabellen sind die Wahrheit, die auch Bearbeiter sehen dürfen.
func (h *Handler) PersonTimeline(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	limit, offset := pageParams(r, 100, 500)

	// Ein UNION über die Quellen, einheitlich (at, kind, text, ref): sortiert und
	// begrenzt IN der Datenbank, damit eine lange Kundengeschichte nicht komplett
	// in den Speicher wandert.
	rows, err := h.Pool.Query(r.Context(), `
		SELECT at, kind, text, ref FROM (
			SELECT p.paid_on::timestamptz AS at, 'payment' AS kind,
			       CASE WHEN p.reversed THEN 'Zahlung storniert: ' ELSE 'Zahlung erhalten: ' END
			         || to_char(p.amount, 'FM999G999G990D00') || ' € (' || p.method || ')' AS text,
			       p.id AS ref
			  FROM payments p WHERE p.person_id = $1
			UNION ALL
			SELECT i.issued_on::timestamptz, 'invoice',
			       CASE WHEN i.cancels_id IS NOT NULL THEN 'Storno-Rechnung ' ELSE 'Rechnung ' END
			         || i.number || ' über ' || to_char(i.total, 'FM999G999G990D00') || ' €',
			       i.id
			  FROM invoices i WHERE i.person_id = $1
			UNION ALL
			SELECT c.charged_on::timestamptz, 'charge',
			       'Zusatzkosten: ' || c.description || ' (' || to_char(c.amount * c.quantity, 'FM999G999G990D00') || ' €)',
			       c.id
			  FROM charges c WHERE c.person_id = $1
			UNION ALL
			SELECT vsh.created_at, 'status',
			       COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), 'Gefährt')
			         || ': ' || vsh.old_status || ' → ' || vsh.new_status
			         || CASE WHEN vsh.note <> '' THEN ' — ' || vsh.note ELSE '' END,
			       v.id
			  FROM vehicle_status_history vsh JOIN vehicles v ON v.id = vsh.vehicle_id
			 WHERE v.person_id = $1
			UNION ALL
			SELECT ho.created_at, 'handover',
			       CASE WHEN ho.direction = 'einlagerung' THEN 'Einlagerung: ' ELSE 'Auslagerung: ' END
			         || COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), 'Gefährt')
			         || CASE WHEN ho.signer_name <> '' THEN ' (unterschrieben: ' || ho.signer_name || ')' ELSE '' END,
			       ho.id
			  FROM handover_protocols ho JOIN vehicles v ON v.id = ho.vehicle_id
			 WHERE v.person_id = $1
			UNION ALL
			SELECT fp.start_date::timestamptz, 'agreement',
			       'Pauschale vereinbart: ' || to_char(fp.amount, 'FM999G999G990D00') || ' € / '
			         || CASE WHEN fp.period = 'monthly' THEN 'Monat' ELSE 'Jahr' END,
			       fp.id
			  FROM flat_rate_periods fp WHERE fp.person_id = $1
		) ev
		ORDER BY at DESC, kind, ref DESC
		LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	out := []timelineEvent{}
	for rows.Next() {
		var e timelineEvent
		if err := rows.Scan(&e.At, &e.Kind, &e.Text, &e.RefID); err != nil {
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
