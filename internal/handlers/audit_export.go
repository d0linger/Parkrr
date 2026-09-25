package handlers

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// Grenzen des Exports (PRT-02). Der Export ist unbegrenzt in der ZEILENZAHL, aber
// nicht in der Zeit: der allgemeine WriteTimeout des Servers (30 s) würde eine
// große Datei still abschneiden, und eine Abfrage ohne statement_timeout hielte
// eine der zehn Poolverbindungen beliebig lange fest. Deshalb ein eigenes,
// großzügiges, aber endliches Fenster für Socket UND Abfrage — und höchstens ein
// Export gleichzeitig, wie backupDownloadSlots bei den Sicherungen.
const (
	auditExportDeadline = 30 * time.Minute
	// Etwas UNTER dem Schreibfenster, damit PostgreSQL sauber abbricht, bevor der
	// Kontext die Verbindung hart kappt.
	auditExportStatementTimeout = `SET statement_timeout = '29min'`
	auditExportBufSize          = 64 << 10
)

var auditExportSlots = make(chan struct{}, 1)

// errAuditExportWrite markiert einen Schreibfehler zum Abrufer (Verbindung weg,
// Schreibfenster abgelaufen) — im Unterschied zu einem Fehler der Datenbank.
var errAuditExportWrite = errors.New("audit export: write failed")

// auditRowSource ist der Teil von pgx.Rows, den die Schleife braucht — als
// Schnittstelle, damit sich der Abbruch beim ersten Schreibfehler ohne
// Datenbank prüfen lässt.
type auditRowSource interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// writeAuditExportRows schreibt alle Zeilen samt Kette nach bw und bricht beim
// ERSTEN Schreibfehler ab: der Abrufer ist dann weg, und jede weitere Zeile wäre
// nur noch ein sinnloser Scan über das ganze Protokoll auf einer Poolverbindung.
func writeAuditExportRows(rows auditRowSource, bw *bufio.Writer) (count int64, chain string, err error) {
	for rows.Next() {
		var row auditExportRow
		row.Type = "entry"
		if err := rows.Scan(&row.ID, &row.UserID, &row.Username, &row.Action, &row.Entity,
			&row.EntityID, &row.Summary, &row.Changes, &row.CreatedAt); err != nil {
			return count, chain, fmt.Errorf("scan: %w", err)
		}
		// Erst ohne Chain serialisieren (das ist die gehashte Form), dann mit.
		row.Chain = ""
		bare, merr := json.Marshal(row)
		if merr != nil {
			return count, chain, fmt.Errorf("encode: %w", merr)
		}
		chain = auditChainStep(chain, bare)
		row.Chain = chain
		line, merr := json.Marshal(row)
		if merr != nil {
			return count, chain, fmt.Errorf("encode: %w", merr)
		}
		if _, werr := bw.Write(line); werr != nil {
			return count, chain, fmt.Errorf("%w: %w", errAuditExportWrite, werr)
		}
		if werr := bw.WriteByte('\n'); werr != nil {
			return count, chain, fmt.Errorf("%w: %w", errAuditExportWrite, werr)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return count, chain, fmt.Errorf("query: %w", err)
	}
	return count, chain, nil
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
	select {
	case auditExportSlots <- struct{}{}:
	case <-r.Context().Done():
		return
	default:
		writeError(w, http.StatusTooManyRequests, "Es läuft bereits ein Protokollexport – bitte später erneut versuchen")
		return
	}
	defer func() { <-auditExportSlots }()
	// Der WriteTimeout des Servers bricht NICHT den Request-Kontext ab — ohne das
	// eigene Fenster schlügen nach 30 s alle Schreibvorgänge fehl, während die
	// Abfrage weiterläuft.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(auditExportDeadline))
	ctx, cancel := context.WithTimeout(r.Context(), auditExportDeadline)
	defer cancel()

	// Der Export ist bewusst unbegrenzt — genau deshalb braucht er eine EIGENE
	// Verbindung ohne die 10-Sekunden-Bremse, unter der jede Poolverbindung läuft.
	// Sonst bricht PostgreSQL die Abfrage nach zehn Sekunden ab, NACHDEM Status 200
	// und ein Teil der Datei schon draußen sind: der Prüfer bekommt eine
	// abgeschnittene Datei, deren einziges Erkennungsmerkmal die fehlende
	// Manifest-Zeile ist. Bei einem Protokoll mit sieben Jahren Aufbewahrung ist das
	// nicht der Ausnahme-, sondern der Normalfall. Die Bremse wird gelockert, nicht
	// gelöst (PRT-02): ein endlicher Wert passend zum Schreibfenster. Muster wie in
	// database.go: vor der Rückgabe wieder setzen, sonst die Verbindung verwerfen.
	conn, cerr := h.Pool.Acquire(ctx)
	if cerr != nil {
		serverError(w, r, "query failed", cerr)
		return
	}
	defer conn.Release()
	if _, terr := conn.Exec(ctx, auditExportStatementTimeout); terr != nil {
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

	rows, err := conn.Query(ctx,
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
	bw := bufio.NewWriterSize(w, auditExportBufSize)

	count, chain, err := writeAuditExportRows(rows, bw)
	if err != nil {
		// Header sind raus; ein sauberer Abbruch mitten im Strom ist nicht mehr
		// möglich. Die Datei endet dann OHNE Manifest — genau daran erkennt ein
		// Prüfer den unvollständigen Export. cancel() (per defer) bricht die
		// Abfrage ab, statt sie für einen verschwundenen Abrufer zu Ende zu lesen.
		if errors.Is(err, errAuditExportWrite) {
			slog.Warn("audit export aborted: client write failed", "rows", count, "err", err)
			// Before the deferred rows.Close runs: it would otherwise read the rest
			// of the result for a client that is gone.
			cancel()
			return
		}
		serverError(w, r, "audit export failed", err)
		return
	}
	manifest, _ := json.Marshal(auditExportManifest{
		Type: "manifest", Rows: count, FinalChain: chain, ExportedAt: h.now(),
	})
	_, _ = bw.Write(manifest)
	_ = bw.WriteByte('\n')
	if ferr := bw.Flush(); ferr != nil {
		slog.Warn("audit export aborted: final flush failed", "rows", count, "err", ferr)
	}
}
