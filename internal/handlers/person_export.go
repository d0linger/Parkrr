package handlers

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Datenauskunft nach Art. 15 DSGVO (Ausbau 28): alles, was Parkrr zu einer Person
// speichert, in einem ZIP. Das Gegenstück zur Anonymisierung (Art. 17). Nur für Admins;
// der Vorgang steht im Änderungsprotokoll. Tabellen mit person_id sind in
// personExportTables aufgeführt, ein Test hält die Liste vollständig.

// personExportSection ist eine JSON-Datei im Paket. SQL liefert genau ein jsonb-Array.
// $1 ist die Person und $2 ihre Gefährte; filtert eine Abfrage NUR nach Gefährten, ist
// $1 die Liste der Gefährte (ein ungenutzter Parameter hätte für Postgres keinen Typ).
type personExportSection struct {
	File  string
	Table string
	SQL   string
}

// Spalten mit Geheimnissen oder Binärdaten fehlen im JSON (to_jsonb(t) - '…'); die
// Binärdaten (Fotos, Anhänge, PDFs) liegen als eigene Dateien im Paket.
var personExportSections = []personExportSection{
	{"stammdaten.json", "persons", `SELECT COALESCE(jsonb_agg(to_jsonb(t)), '[]') FROM persons t WHERE t.id = $1`},
	{"gefaehrte.json", "vehicles", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]') FROM vehicles t WHERE t.person_id = $1`},
	{"pauschalen.json", "flat_rate_periods", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]') FROM flat_rate_periods t WHERE t.person_id = $1`},
	{"zusatzkosten.json", "charges", `SELECT COALESCE(jsonb_agg(to_jsonb(t) - 'idempotency_key' - 'request_fingerprint' - 'idempotency_actor' ORDER BY t.id), '[]') FROM charges t WHERE t.person_id = $1`},
	{"wiederkehrende_kosten.json", "recurring_charges", `SELECT COALESCE(jsonb_agg(to_jsonb(t) - 'idempotency_key' - 'request_fingerprint' - 'idempotency_actor' ORDER BY t.id), '[]') FROM recurring_charges t WHERE t.person_id = $1`},
	{"zahlungen.json", "payments", `SELECT COALESCE(jsonb_agg(to_jsonb(t) - 'idempotency_key' - 'request_fingerprint' ORDER BY t.id), '[]') FROM payments t WHERE t.person_id = $1`},
	{"rechnungen.json", "invoices", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]') FROM invoices t WHERE t.person_id = $1`},
	{"rechnungspositionen.json", "invoice_items", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.invoice_id, t.pos), '[]') FROM invoice_items t WHERE t.invoice_id IN (SELECT id FROM invoices WHERE person_id = $1)`},
	{"zahlungserinnerungen.json", "invoice_reminders", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]') FROM invoice_reminders t WHERE t.invoice_id IN (SELECT id FROM invoices WHERE person_id = $1)`},
	{"uebergaben.json", "handover_protocols", `SELECT COALESCE(jsonb_agg(to_jsonb(t) - 'signature' || jsonb_build_object('hat_unterschrift', t.signature IS NOT NULL) ORDER BY t.id), '[]') FROM handover_protocols t WHERE t.person_id = $1`},
	{"fotos.json", "vehicle_photos", `SELECT COALESCE(jsonb_agg(to_jsonb(t) - 'data' ORDER BY t.id), '[]') FROM vehicle_photos t WHERE t.vehicle_id = ANY($1)`},
	{"anhaenge.json", "attachments", `SELECT COALESCE(jsonb_agg(to_jsonb(t) - 'data' ORDER BY t.id), '[]') FROM attachments t WHERE t.owner_person_id = $1 OR t.person_id = $1`},
	{"kundenwuensche.json", "portal_requests", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]') FROM portal_requests t WHERE t.person_id = $1`},
	{"kundenzugang.json", "self_service_tokens", `SELECT COALESCE(jsonb_agg(to_jsonb(t) - 'token_hash' ORDER BY t.id), '[]') FROM self_service_tokens t WHERE t.person_id = $1`},
	{"stellplatz_verlauf.json", "spot_occupancy_history", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]') FROM spot_occupancy_history t WHERE t.person_id = $1`},
	{"mail_warteschlange.json", "mail_outbox", `SELECT COALESCE(jsonb_agg(to_jsonb(t) - 'attachment' ORDER BY t.id), '[]') FROM mail_outbox t WHERE t.person_id = $1`},
	{"zugangsmittel.json", "access_media_loans", `SELECT COALESCE(jsonb_agg(to_jsonb(t) || jsonb_build_object('art', m.kind, 'nummer', m.label) ORDER BY t.id), '[]') FROM access_media_loans t JOIN access_media m ON m.id = t.medium_id WHERE t.person_id = $1`},
	{"kautionen.json", "deposits", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]') FROM deposits t WHERE t.person_id = $1`},
	{"kontobuchungen.json", "bank_transactions", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]') FROM bank_transactions t WHERE t.person_id = $1`},
	{"vertragsannahmen.json", "contract_acceptances", `SELECT COALESCE(jsonb_agg(to_jsonb(t) || jsonb_build_object('version', c.version, 'titel', c.title) ORDER BY t.id), '[]') FROM contract_acceptances t JOIN contract_templates c ON c.id = t.template_id WHERE t.person_id = $1`},
	{"warteliste.json", "waitlist", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]') FROM waitlist t WHERE t.person_id = $1`},
	{"status_verlauf.json", "vehicle_status_history", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]') FROM vehicle_status_history t WHERE t.vehicle_id = ANY($1)`},
	{"serviceaufgaben.json", "service_task_log", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]') FROM service_task_log t WHERE t.vehicle_id = ANY($1)`},
	{"vertragsende_hinweise.json", "contract_end_notices", `SELECT COALESCE(jsonb_agg(to_jsonb(t)), '[]') FROM contract_end_notices t WHERE t.vehicle_id = ANY($1)`},
	{"aenderungsprotokoll.json", "audit_log", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]') FROM audit_log t
		WHERE (t.entity = 'person' AND t.entity_id = $1) OR (t.entity = 'vehicle' AND t.entity_id = ANY($2))`},
}

// personExportTables nennt die Tabellen, die das Paket abdeckt — für den Vollständigkeitstest.
func personExportTables() []string {
	out := []string{"mail_log"} // per Adresse, siehe exportMailLog
	for _, s := range personExportSections {
		out = append(out, s.Table)
	}
	return out
}

// PersonDataExport: GET /api/persons/{id}/export.
func (h *Handler) PersonDataExport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	ctx := r.Context()
	var first, last, email string
	if err := h.Pool.QueryRow(ctx, `SELECT first_name, last_name, email FROM persons WHERE id = $1`, id).Scan(&first, &last, &email); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "person not found")
			return
		}
		serverError(w, r, "query failed", err)
		return
	}
	var vehicleIDs []int64
	if err := h.Pool.QueryRow(ctx, `SELECT COALESCE(array_agg(id ORDER BY id), '{}') FROM vehicles WHERE person_id = $1`, id).Scan(&vehicleIDs); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	// Erst alles lesen, dann schreiben: ein Fehler mitten im ZIP ließe sich nicht mehr
	// als Statuscode melden.
	files := map[string][]byte{}
	var order []string
	add := func(name string, data []byte) { files[name] = data; order = append(order, name) }
	counts := map[string]int{}
	for _, s := range personExportSections {
		var raw json.RawMessage
		args := []any{id}
		switch {
		case strings.Contains(s.SQL, "$2"):
			args = append(args, vehicleIDs)
		case strings.Contains(s.SQL, "ANY($1)"):
			args = []any{vehicleIDs}
		}
		if err := h.Pool.QueryRow(ctx, s.SQL, args...).Scan(&raw); err != nil {
			serverError(w, r, "export failed: "+s.Table, err)
			return
		}
		var rows []json.RawMessage
		_ = json.Unmarshal(raw, &rows)
		counts[s.File] = len(rows)
		pretty, _ := json.MarshalIndent(rows, "", "  ")
		add(s.File, pretty)
	}
	mails, err := h.exportMailLog(ctx, id, email)
	if err != nil {
		serverError(w, r, "export failed: mail_log", err)
		return
	}
	counts["mail_protokoll.json"] = len(mails)
	mailJSON, _ := json.MarshalIndent(mails, "", "  ")
	add("mail_protokoll.json", mailJSON)

	if err := h.exportBinaries(r, id, vehicleIDs, add); err != nil {
		serverError(w, r, "export failed: files", err)
		return
	}
	seller := ""
	if bs, err := h.loadBillingSettings(ctx); err == nil {
		seller = trim(bs.SellerName)
	}
	now := h.now()
	add("LIESMICH.txt", []byte(personExportReadme(strings.TrimSpace(first+" "+last), seller, now, counts)))
	manifest, _ := json.MarshalIndent(map[string]any{"person_id": id, "erstellt": now.Format(time.RFC3339), "tabellen": personExportTables(), "dateien": order}, "", "  ")
	add("manifest.json", manifest)

	h.audit(r, "export", "person", id, "Datenauskunft nach Art. 15 DSGVO erstellt ("+strconv.Itoa(len(order))+" Dateien)")
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="Datenauskunft-`+strconv.FormatInt(id, 10)+`-`+now.Format("2006-01-02")+`.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	zw := zip.NewWriter(w)
	for _, name := range order {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return
		}
		if _, err := f.Write(files[name]); err != nil {
			return
		}
	}
	_ = zw.Close()
}

type exportMail struct {
	SentAt     time.Time `json:"sent_at"`
	Recipients string    `json:"recipients"`
	Subject    string    `json:"subject"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error"`
}

// exportMailLog: das Mail-Protokoll kennt keine Person, nur Empfänger. Gesucht wird mit
// denselben Adressen, mit denen die Anonymisierung es leert (personKnownEmails).
func (h *Handler) exportMailLog(ctx context.Context, personID int64, current string) ([]exportMail, error) {
	out := []exportMail{}
	err := pgx.BeginTxFunc(ctx, h.Pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		addrs, err := personKnownEmails(ctx, tx, personID, current)
		if err != nil || len(addrs) == 0 {
			return err
		}
		rows, err := tx.Query(ctx,
			`SELECT sent_at, recipients, subject, ok, error FROM mail_log
			  WHERE EXISTS (SELECT 1 FROM unnest(string_to_array(recipients, ',')) AS x
			                 WHERE lower(btrim(x, E' \t\r\n')) = ANY ($1::text[]))
			  ORDER BY sent_at, id`, addrs)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m exportMail
			if err := rows.Scan(&m.SentAt, &m.Recipients, &m.Subject, &m.OK, &m.Error); err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

// exportBinaries legt Rechnungs- und Übergabe-PDFs, Fotos und Anhänge ins Paket.
func (h *Handler) exportBinaries(r *http.Request, personID int64, vehicleIDs []int64, add func(string, []byte)) error {
	ctx := r.Context()
	safe := func(s string) string {
		s = strings.Map(func(c rune) rune {
			if c == '/' || c == '\\' || c == ':' || c < 32 {
				return '_'
			}
			return c
		}, path.Base(s))
		if s == "." || s == "" {
			s = "datei"
		}
		return s
	}
	var invoiceIDs []int64
	if err := h.Pool.QueryRow(ctx, `SELECT COALESCE(array_agg(id ORDER BY id), '{}') FROM invoices WHERE person_id = $1`, personID).Scan(&invoiceIDs); err != nil {
		return err
	}
	for _, iid := range invoiceIDs {
		iv, found, err := h.fetchInvoice(ctx, iid)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		pdf, err := invoicePDFBytes(iv)
		if err != nil {
			return err
		}
		add("rechnungen/"+safe(iv.Number)+".pdf", pdf)
	}
	var handoverIDs []int64
	if err := h.Pool.QueryRow(ctx, `SELECT COALESCE(array_agg(id ORDER BY id), '{}') FROM handover_protocols WHERE person_id = $1`, personID).Scan(&handoverIDs); err != nil {
		return err
	}
	for _, hid := range handoverIDs {
		req := httptest.NewRequest(http.MethodGet, "/api/handovers/"+strconv.FormatInt(hid, 10)+"/pdf", nil).WithContext(ctx)
		req.SetPathValue("id", strconv.FormatInt(hid, 10))
		rec := httptest.NewRecorder()
		h.HandoverPDF(rec, req)
		if rec.Code != http.StatusOK {
			return fmt.Errorf("handover %d: status %d", hid, rec.Code)
		}
		add("uebergaben/Uebergabeprotokoll-"+strconv.FormatInt(hid, 10)+".pdf", rec.Body.Bytes())
	}
	rows, err := h.Pool.Query(ctx, `SELECT id, content_type, data FROM vehicle_photos WHERE vehicle_id = ANY($1) ORDER BY id`, vehicleIDs)
	if err != nil {
		return err
	}
	for rows.Next() {
		var pid int64
		var ct string
		var data []byte
		if err := rows.Scan(&pid, &ct, &data); err != nil {
			rows.Close()
			return err
		}
		ext := ".jpg"
		if ct == "image/png" {
			ext = ".png"
		}
		add("fotos/foto-"+strconv.FormatInt(pid, 10)+ext, data)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = h.Pool.Query(ctx, `SELECT id, filename, data FROM attachments WHERE owner_person_id = $1 OR person_id = $1 ORDER BY id`, personID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var aid int64
		var name string
		var data []byte
		if err := rows.Scan(&aid, &name, &data); err != nil {
			return err
		}
		add("anhaenge/"+strconv.FormatInt(aid, 10)+"-"+safe(name), data)
	}
	return rows.Err()
}

func personExportReadme(name, seller string, at time.Time, counts map[string]int) string {
	var b strings.Builder
	b.WriteString("Auskunft nach Art. 15 DSGVO\n")
	b.WriteString("===========================\n\n")
	if name != "" {
		b.WriteString("Betroffene Person: " + name + "\n")
	}
	if seller != "" {
		b.WriteString("Verantwortlich:    " + seller + "\n")
	}
	b.WriteString("Erstellt am:       " + at.Format("02.01.2006 15:04") + "\n\n")
	b.WriteString("Das Paket enthält alle Daten, die zu dieser Person in Parkrr gespeichert sind.\n")
	b.WriteString("Die JSON-Dateien geben die Datensätze vollständig wieder, ohne interne Schlüssel\n")
	b.WriteString("und ohne Zugangsgeheimnisse. Rechnungen und Übergabeprotokolle liegen als PDF bei,\n")
	b.WriteString("Fotos und Anhänge als Originaldateien.\n\n")
	b.WriteString("Zweck der Verarbeitung: Einstellung von Fahrzeugen, Abrechnung und Aufbewahrung\n")
	b.WriteString("der Belege. Rechnungen werden sieben Jahre aufbewahrt (§ 132 BAO).\n\n")
	b.WriteString("Inhalt (Datei: Einträge)\n")
	names := make([]string, 0, len(counts))
	for k := range counts {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Fprintf(&b, "  %-28s %d\n", k, counts[k])
	}
	return b.String()
}
