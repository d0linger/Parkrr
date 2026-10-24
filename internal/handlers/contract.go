package handlers

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

// Einstellvertrag als PDF, Annahme im Portal (Ausbau 30). Die Vorlage ist versioniert:
// Speichern legt eine neue Version an, frühere Annahmen bleiben an ihrer Version.
// Platzhalter in doppelten geschweiften Klammern werden je Gefährt gefüllt. Die Annahme
// im Kundenzugang hält Zeitpunkt und Version fest; das PDF wird Anhang der Person.

var contractPlaceholders = []string{"name", "anschrift", "gefaehrt", "kennzeichen", "tarif", "preis", "abrechnung",
	"beginn", "ende", "halle", "stellplatz", "betreiber", "betreiber_anschrift", "datum"}

const maxContractBody = 30000

type contractTemplate struct {
	ID        int64     `json:"id"`
	Version   int       `json:"version"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handler) currentContract(ctx context.Context) (*contractTemplate, error) {
	var c contractTemplate
	err := h.Pool.QueryRow(ctx, `SELECT id, version, title, body, created_at FROM contract_templates ORDER BY version DESC LIMIT 1`).
		Scan(&c.ID, &c.Version, &c.Title, &c.Body, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetContractTemplate: GET /api/contract-template → {template|null, placeholders}.
func (h *Handler) GetContractTemplate(w http.ResponseWriter, r *http.Request) {
	c, err := h.currentContract(r.Context())
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"template": c, "placeholders": contractPlaceholders})
}

// SaveContractTemplate: POST /api/contract-template {title, body} → neue Version.
func (h *Handler) SaveContractTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.Title, in.Body = trim(in.Title), strings.TrimSpace(in.Body)
	if in.Title == "" || in.Body == "" {
		writeError(w, http.StatusBadRequest, "Titel und Text sind nötig")
		return
	}
	if len([]rune(in.Title)) > 120 || len(in.Body) > maxContractBody {
		writeError(w, http.StatusBadRequest, "Eingabe ist zu lang")
		return
	}
	var createdBy *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		createdBy = &u.ID
	}
	var c contractTemplate
	err := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		// Versionen vergibt die Sperre, nicht der Zufall zweier gleichzeitiger Speicherungen.
		if _, err := tx.Exec(r.Context(), `LOCK TABLE contract_templates IN EXCLUSIVE MODE`); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(),
			`INSERT INTO contract_templates (version, title, body, created_by)
			 SELECT COALESCE(max(version), 0) + 1, $1, $2, $3 FROM contract_templates
			 RETURNING id, version, title, body, created_at`, in.Title, in.Body, createdBy).
			Scan(&c.ID, &c.Version, &c.Title, &c.Body, &c.CreatedAt)
	})
	if err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	h.auditCreated(r, "contract_template", c.ID, "Einstellvertrag Version "+strconv.Itoa(c.Version)+" gespeichert",
		map[string]any{"version": c.Version, "title": c.Title, "chars": len(c.Body)})
	writeJSON(w, http.StatusCreated, c)
}

// contractFields liefert die Platzhalterwerte für ein Gefährt und dessen Halter.
func (h *Handler) contractFields(ctx context.Context, vehicleID int64) (map[string]string, int64, error) {
	var (
		pid                                   int64
		name, addr, label, plate, cat, period string
		rate                                  float64
		start                                 time.Time
		end                                   *time.Time
		hall, spot                            string
	)
	err := h.Pool.QueryRow(ctx,
		`SELECT v.person_id, trim(p.first_name || ' ' || p.last_name), p.address,
		        COALESCE(NULLIF(v.label,''), c.name), v.license_plate, c.name, v.billing_period,
		        CASE WHEN v.rate <> 0 THEN v.rate WHEN v.billing_period = 'yearly' THEN c.default_yearly_cost ELSE c.default_monthly_cost END,
		        v.start_date, v.end_date, COALESCE(hl.name, ''), COALESCE(s.label, '')
		   FROM vehicles v
		   JOIN persons p ON p.id = v.person_id
		   JOIN categories c ON c.id = v.category_id
		   LEFT JOIN spots s ON s.id = v.spot_id
		   LEFT JOIN halls hl ON hl.id = s.hall_id
		  WHERE v.id = $1`, vehicleID).
		Scan(&pid, &name, &addr, &label, &plate, &cat, &period, &rate, &start, &end, &hall, &spot)
	if err != nil {
		return nil, 0, err
	}
	bs, _ := h.loadBillingSettings(ctx)
	f := map[string]string{
		"name": name, "anschrift": strings.TrimSpace(addr), "gefaehrt": label, "kennzeichen": plate, "tarif": cat,
		"preis": pdfMoney(rate), "abrechnung": map[string]string{"yearly": "jährlich", "monthly": "monatlich"}[period],
		"beginn": start.Format("02.01.2006"), "ende": "unbefristet", "halle": hall, "stellplatz": spot,
		"betreiber": strings.TrimSpace(bs.SellerName), "betreiber_anschrift": strings.TrimSpace(bs.SellerAddress),
		"datum": h.now().Format("02.01.2006"),
	}
	if end != nil {
		f["ende"] = end.Format("02.01.2006")
	}
	for k, v := range f {
		if strings.TrimSpace(v) == "" {
			f[k] = "–"
		}
	}
	return f, pid, nil
}

func fillContract(body string, f map[string]string) string {
	pairs := make([]string, 0, len(f)*4)
	for k, v := range f {
		pairs = append(pairs, "{{"+k+"}}", v, "{{ "+k+" }}", v)
	}
	return strings.NewReplacer(pairs...).Replace(body)
}

func renderContractPDF(c *contractTemplate, text, stamp string) ([]byte, error) {
	pdf, tr := newPDF()
	pdf.SetMargins(20, 18, 20)
	pdf.SetAutoPageBreak(true, 20)
	pdf.SetFooterFunc(func() {
		pdf.SetY(-14)
		pdf.SetFont(pdfFontName, "", 8)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(0, 5, tr("Vorlage Version "+strconv.Itoa(c.Version)+" · Seite "+strconv.Itoa(pdf.PageNo())), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()
	pdf.SetFont(pdfFontName, "B", 16)
	pdf.SetTextColor(20, 20, 20)
	pdf.MultiCell(0, 8, tr(c.Title), "", "L", false)
	pdf.Ln(3)
	pdf.SetFont(pdfFontName, "", 10.5)
	pdf.SetTextColor(30, 30, 30)
	pdf.MultiCell(0, 5.4, tr(text), "", "L", false)
	pdf.Ln(8)
	if stamp != "" {
		pdf.SetFont(pdfFontName, "B", 10)
		pdf.MultiCell(0, 5.4, tr(stamp), "1", "L", false)
	} else {
		pdf.SetDrawColor(120, 120, 120)
		y := pdf.GetY() + 14
		pdf.Line(20, y, 90, y)
		pdf.Line(120, y, 190, y)
		pdf.SetXY(20, y+1)
		pdf.SetFont(pdfFontName, "", 9)
		pdf.CellFormat(70, 5, tr("Ort, Datum, Unterschrift Betreiber"), "", 0, "L", false, 0, "")
		pdf.SetX(120)
		pdf.CellFormat(70, 5, tr("Ort, Datum, Unterschrift Kunde"), "", 1, "L", false, 0, "")
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeContractPDF(w http.ResponseWriter, data []byte, name string) {
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Disposition", `inline; filename="`+name+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// VehicleContractPDF: GET /api/vehicles/{id}/contract.pdf — zum Ausdrucken und Unterschreiben.
func (h *Handler) VehicleContractPDF(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	h.serveContract(w, r, id, 0)
}

// serveContract rendert die aktuelle Vorlage für ein Gefährt; mustOwner > 0 verlangt,
// dass es dieser Person gehört (Portal).
func (h *Handler) serveContract(w http.ResponseWriter, r *http.Request, vehicleID, mustOwner int64) {
	c, err := h.currentContract(r.Context())
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if c == nil {
		writeError(w, http.StatusNotFound, "Es gibt noch keine Vertragsvorlage")
		return
	}
	f, pid, err := h.contractFields(r.Context(), vehicleID)
	if err != nil || (mustOwner > 0 && pid != mustOwner) {
		writeError(w, http.StatusNotFound, "Gefährt nicht gefunden")
		return
	}
	data, err := renderContractPDF(c, fillContract(c.Body, f), "")
	if err != nil {
		serverError(w, r, "pdf failed", err)
		return
	}
	writeContractPDF(w, data, "Einstellvertrag-v"+strconv.Itoa(c.Version)+".pdf")
}

type contractAcceptance struct {
	ID           int64     `json:"id"`
	VehicleID    *int64    `json:"vehicle_id"`
	VehicleLabel string    `json:"vehicle_label"`
	Version      int       `json:"version"`
	AcceptedAt   time.Time `json:"accepted_at"`
	Via          string    `json:"via"`
	AttachmentID *int64    `json:"attachment_id"`
}

// PersonContracts: GET /api/persons/{id}/contracts → Annahmen der Person.
func (h *Handler) PersonContracts(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT a.id, a.vehicle_id, COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), ''), t.version, a.accepted_at, a.via, a.attachment_id
		   FROM contract_acceptances a
		   JOIN contract_templates t ON t.id = a.template_id
		   LEFT JOIN vehicles v ON v.id = a.vehicle_id
		  WHERE a.person_id = $1 ORDER BY a.accepted_at DESC, a.id DESC`, id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	out := []contractAcceptance{}
	for rows.Next() {
		var a contractAcceptance
		if err := rows.Scan(&a.ID, &a.VehicleID, &a.VehicleLabel, &a.Version, &a.AcceptedAt, &a.Via, &a.AttachmentID); err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// PortalContractPDF: GET /api/portal/contract?vehicle_id= — der Vertrag zum Lesen.
func (h *Handler) PortalContractPDF(w http.ResponseWriter, r *http.Request) {
	pid, ok := h.portalPerson(r)
	if !ok {
		writeError(w, http.StatusNotFound, "Link ungültig oder abgelaufen")
		return
	}
	rawVid := r.URL.Query().Get("vehicle_id")
	if !validNumericParamLength(rawVid) {
		writeError(w, http.StatusBadRequest, "vehicle_id is too long")
		return
	}
	vid, err := strconv.ParseInt(rawVid, 10, 64)
	if err != nil || vid <= 0 {
		writeError(w, http.StatusBadRequest, "vehicle_id fehlt")
		return
	}
	h.serveContract(w, r, vid, pid)
}

// PortalAcceptContract: POST /api/portal/contract/accept {vehicle_id, version}.
// Version ist die gelesene: hat der Betreiber inzwischen eine neue gespeichert, wird
// nichts angenommen (409), damit niemand einen Text annimmt, den er nicht gesehen hat.
func (h *Handler) PortalAcceptContract(w http.ResponseWriter, r *http.Request) {
	pid, ok := h.portalPerson(r)
	if !ok {
		writeError(w, http.StatusNotFound, "Link ungültig oder abgelaufen")
		return
	}
	var in struct {
		VehicleID int64 `json:"vehicle_id"`
		Version   int   `json:"version"`
	}
	if err := decodeJSON(r, &in); err != nil || in.VehicleID <= 0 || in.Version <= 0 {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c, err := h.currentContract(r.Context())
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	if c == nil || c.Version != in.Version {
		writeError(w, http.StatusConflict, "Der Vertrag wurde inzwischen geändert. Bitte die neue Fassung lesen.")
		return
	}
	f, owner, err := h.contractFields(r.Context(), in.VehicleID)
	if err != nil || owner != pid {
		writeError(w, http.StatusNotFound, "Gefährt nicht gefunden")
		return
	}
	now := h.now()
	stamp := "Gelesen und angenommen im Kundenzugang am " + now.Format("02.01.2006") + " um " + now.Format("15:04") +
		" Uhr (Vorlage Version " + strconv.Itoa(c.Version) + ")."
	data, err := renderContractPDF(c, fillContract(c.Body, f), stamp)
	if err != nil {
		serverError(w, r, "pdf failed", err)
		return
	}
	fileName := "Einstellvertrag-v" + strconv.Itoa(c.Version) + "-" + safeFileWord(f["gefaehrt"]) + "-" + now.Format("2006-01-02") + ".pdf"
	var accID int64
	var dup bool
	err = pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `SELECT 1 FROM persons WHERE id = $1 FOR UPDATE`, pid); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM contract_acceptances WHERE person_id = $1 AND vehicle_id = $2 AND template_id = $3)`,
			pid, in.VehicleID, c.ID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			dup = true
			return nil
		}
		var attID int64
		if err := tx.QueryRow(r.Context(),
			`INSERT INTO attachments (person_id, owner_person_id, filename, content_type, byte_size, data)
			 VALUES ($1, $1, $2, 'application/pdf', $3, $4) RETURNING id`, pid, fileName, len(data), data).Scan(&attID); err != nil {
			return err
		}
		return tx.QueryRow(r.Context(),
			`INSERT INTO contract_acceptances (person_id, vehicle_id, template_id, accepted_at, via, attachment_id)
			 VALUES ($1, $2, $3, $4, 'portal', $5) RETURNING id`, pid, in.VehicleID, c.ID, now, attID).Scan(&accID)
	})
	if err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	if dup {
		writeError(w, http.StatusConflict, "Schon angenommen")
		return
	}
	h.AuditSystem(r.Context(), "create", "contract_acceptance", accID, "Einstellvertrag im Kundenzugang angenommen",
		auditSnapshot(map[string]any{"person_id": pid, "vehicle_id": in.VehicleID, "version": c.Version}))
	writeJSON(w, http.StatusCreated, map[string]any{"id": accID, "version": c.Version, "accepted_at": now})
}

func safeFileWord(s string) string {
	s = strings.Map(func(c rune) rune {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-':
			return c
		case c == ' ' || c == '_':
			return '-'
		}
		return -1
	}, s)
	if s == "" {
		return "Gefaehrt"
	}
	return truncRunes(s, 40)
}
