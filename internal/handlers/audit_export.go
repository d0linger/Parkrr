package handlers

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

// auditExportRow ist eine Zeile des revisionssicheren Exports. Die Feldreihenfolge
// ist Teil des Formats: die Kettenprüfung hasht die kanonische JSON-Serialisierung
// GENAU dieser Struktur, eine Umsortierung wäre eine Formatänderung.
type auditExportRow struct {
	Type      string          `json:"type"` // "entry"
	ID        int64           `json:"id"`
	UserID    *int64          `json:"user_id"`
	Username  string          `json:"username"`
	Action    string          `json:"action"`
	Entity    string          `json:"entity"`
	EntityID  *int64          `json:"entity_id"`
	Summary   string          `json:"summary"`
	Changes   json.RawMessage `json:"changes"`
	CreatedAt time.Time       `json:"created_at"`
	// Chain = SHA-256(vorige Chain als Hex || kanonisches JSON der Zeile OHNE
	// dieses Feld). Jede Zeile versiegelt damit alle vorigen: eine nachträglich
	// veränderte, entfernte oder eingeschobene Zeile bricht die Kette ab genau
	// dieser Stelle (Hundert 43).
	Chain string `json:"chain"`
}

// auditExportManifest schließt den Export ab: Zeilenzahl und Endglied der Kette.
// Wer das Manifest (oder nur sein final_chain) getrennt ablegt, kann jeden
// späteren Export gegen diesen Stand prüfen.
type auditExportManifest struct {
	Type       string    `json:"type"` // "manifest"
	Rows       int64     `json:"rows"`
	FinalChain string    `json:"final_chain"`
	ExportedAt time.Time `json:"exported_at"`
}

// auditChainStep berechnet das nächste Kettenglied aus dem vorigen (Hex) und der
// Zeile ohne Chain-Feld. Als eigene Funktion, damit Prüfwerkzeuge und Tests
// dieselbe Rechnung teilen.
func auditChainStep(prevHex string, rowWithoutChain []byte) string {
	h := sha256.New()
	h.Write([]byte(prevHex))
	h.Write(rowWithoutChain)
	return hex.EncodeToString(h.Sum(nil))
}

// ExportAudit streamt das GESAMTE Änderungsprotokoll als JSON-Zeilen (JSONL) mit
// SHA-256-Hashkette (Hundert 43). Die Tabelle selbst ist per Trigger unveränderlich
// — aber ein Export davon war bisher nur eine Textdatei, der man jede Nachbearbeitung
// glauben musste. Mit der Kette ist jede Zeile an alle vorigen gebunden: prüfbar
// mit nichts als SHA-256, ohne Parkrr.
//
// Admin-only wie die Audit-Ansicht selbst. Ohne Limit: ein Revisionsexport, der
// still abschneidet, wäre schlimmer als keiner — deshalb gestreamt statt gepuffert.
func (h *Handler) ExportAudit(w http.ResponseWriter, r *http.Request) {
	// Der Export ist bewusst unbegrenzt — genau deshalb braucht er eine EIGENE
	// Verbindung ohne die 10-Sekunden-Bremse, unter der jede Poolverbindung läuft.
	// Sonst bricht PostgreSQL die Abfrage nach zehn Sekunden ab, NACHDEM Status 200
	// und ein Teil der Datei schon draußen sind: der Prüfer bekommt eine
	// abgeschnittene Datei, deren einziges Erkennungsmerkmal die fehlende
	// Manifest-Zeile ist. Bei einem Protokoll mit sieben Jahren Aufbewahrung ist das
	// nicht der Ausnahme-, sondern der Normalfall. Muster wie in database.go: Bremse
	// lösen, vor der Rückgabe wieder setzen, sonst die Verbindung verwerfen.
	conn, cerr := h.Pool.Acquire(r.Context())
	if cerr != nil {
		serverError(w, r, "query failed", cerr)
		return
	}
	defer conn.Release()
	if _, terr := conn.Exec(r.Context(), `SET statement_timeout = 0`); terr != nil {
		serverError(w, r, "query failed", terr)
		return
	}
	defer func() {
		resetCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, rerr := conn.Exec(resetCtx, `SET statement_timeout = 10000`); rerr != nil {
			_ = conn.Conn().Close(resetCtx)
		}
	}()

	rows, err := conn.Query(r.Context(),
		`SELECT id, user_id, username, action, entity, entity_id, summary, changes, created_at
		   FROM audit_log ORDER BY id ASC`)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="parkrr-audit-export-`+h.now().Format("2006-01-02")+`.jsonl"`)
	bw := bufio.NewWriterSize(w, 64<<10)

	chain := "" // Genesis: leere vorige Kette
	var count int64
	for rows.Next() {
		var row auditExportRow
		row.Type = "entry"
		if err := rows.Scan(&row.ID, &row.UserID, &row.Username, &row.Action, &row.Entity,
			&row.EntityID, &row.Summary, &row.Changes, &row.CreatedAt); err != nil {
			// Header sind raus; ein sauberer Abbruch mitten im Strom ist nicht mehr
			// möglich. Die Datei endet dann OHNE Manifest — genau daran erkennt ein
			// Prüfer den unvollständigen Export.
			serverError(w, r, "scan failed", err)
			return
		}
		// Erst ohne Chain serialisieren (das ist die gehashte Form), dann mit.
		row.Chain = ""
		bare, merr := json.Marshal(row)
		if merr != nil {
			serverError(w, r, "encode failed", merr)
			return
		}
		chain = auditChainStep(chain, bare)
		row.Chain = chain
		line, merr := json.Marshal(row)
		if merr != nil {
			serverError(w, r, "encode failed", merr)
			return
		}
		bw.Write(line)
		bw.WriteByte('\n')
		count++
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	manifest, _ := json.Marshal(auditExportManifest{
		Type: "manifest", Rows: count, FinalChain: chain, ExportedAt: h.now(),
	})
	bw.Write(manifest)
	bw.WriteByte('\n')
	_ = bw.Flush()
}
