package handlers

import (
	"bytes"
	"encoding/base64"
	"errors"
	"html"
	"image/png"
	"net/http"
	"strconv"
	"strings"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
	"github.com/jackc/pgx/v5"
)

// qrDataURI renders content as a QR-code PNG data URI (reuses boombuler/barcode,
// already a dependency via the TOTP QR).
func qrDataURI(content string, size int) (string, error) {
	code, err := qr.Encode(content, qr.M, qr.Auto)
	if err != nil {
		return "", err
	}
	scaled, err := barcode.Scale(code, size, size)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, scaled); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// VehicleLabel serves a self-contained, printable HTML label for a vehicle: a QR
// that opens the vehicle page plus its name, plate and current spot — to stick on
// the vehicle or the spot. Served with its own tight CSP (this static page needs
// inline <style>/print button, which the app-wide style-src 'self' forbids); all
// interpolated fields are HTML-escaped so the relaxation cannot become an XSS.
func (h *Handler) VehicleLabel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var label, plate, person, spot, hall string
	err := h.Pool.QueryRow(r.Context(),
		`SELECT v.label, v.license_plate, trim(p.first_name || ' ' || p.last_name),
		        COALESCE(s.label, ''), COALESCE(hh.name, '')
		   FROM vehicles v
		   JOIN persons p ON p.id = v.person_id
		   LEFT JOIN spots s ON s.id = v.spot_id
		   LEFT JOIN halls hh ON hh.id = s.hall_id
		  WHERE v.id = $1`, id).Scan(&label, &plate, &person, &spot, &hall)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "vehicle not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	qrURI, err := qrDataURI(h.vehicleLink(r, id), 260)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not render QR")
		return
	}

	setLabelHeaders(w)
	// Every interpolated field is HTML-escaped and the QR is a server-generated
	// data: URI; together with the tight per-response CSP above, no untrusted
	// markup can reach the output.
	page := renderVehicleLabel(
		html.EscapeString(label), html.EscapeString(plate),
		html.EscapeString(person), html.EscapeString(labelLocation(hall, spot)), qrURI)
	_, _ = w.Write([]byte(page)) // #nosec G705 -- inputs HTML-escaped above (taint false positive)
}

// vehicleLink is the address a label's QR opens. Prefer the configured public base
// URL (canonical, not attacker-influenced); fall back to the request scheme+Host only
// when it is unset.
func (h *Handler) vehicleLink(r *http.Request, id int64) string {
	if base := strings.TrimRight(h.PublicBaseURL, "/"); base != "" {
		return base + "/#/vehicles/" + strconv.FormatInt(id, 10)
	}
	scheme := "http"
	if h.Auth != nil && h.Auth.RequestIsHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/#/vehicles/" + strconv.FormatInt(id, 10)
}

func labelLocation(hall, spot string) string {
	loc := hall
	if spot != "" {
		if loc != "" {
			loc += " · "
		}
		loc += "Platz " + spot
	}
	if loc == "" {
		loc = "nicht platziert"
	}
	return loc
}

// setLabelHeaders: tight, label-only CSP for these static print pages (they need
// inline <style> and the print button, which the app-wide style-src 'self' forbids).
func setLabelHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// HallLabels serves one printable A4 sheet with a label for every placed, active
// Gefährt of a hall (Ausbau 12), sorted by spot: ten per page, two columns, each the
// same card and QR as the single label. Same tight CSP, every field HTML-escaped.
func (h *Handler) HallLabels(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var hallName string
	if err := h.Pool.QueryRow(r.Context(), `SELECT name FROM halls WHERE id=$1`, id).Scan(&hallName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "hall not found")
			return
		}
		serverError(w, r, "query failed", err)
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT v.id, v.label, v.license_plate, trim(p.first_name || ' ' || p.last_name), s.label
		   FROM vehicles v
		   JOIN persons p ON p.id = v.person_id
		   JOIN spots s ON s.id = v.spot_id
		  WHERE s.hall_id = $1 AND NOT v.archived
		  ORDER BY s.label, v.id`, id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	var cards strings.Builder
	n := 0
	for rows.Next() {
		var vid int64
		var label, plate, person, spot string
		if err := rows.Scan(&vid, &label, &plate, &person, &spot); err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		qrURI, err := qrDataURI(h.vehicleLink(r, vid), 260)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not render QR")
			return
		}
		cards.WriteString(labelCard(html.EscapeString(label), html.EscapeString(plate),
			html.EscapeString(person), html.EscapeString(labelLocation(hallName, spot)), qrURI))
		n++
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	setLabelHeaders(w)
	_, _ = w.Write([]byte(renderLabelSheet(html.EscapeString(hallName), n, cards.String()))) // #nosec G705 -- inputs HTML-escaped above
}

// labelCard is one label: QR left, name, plate, person and location right. All
// arguments must already be HTML-escaped (qrURI is a server-built data: URI).
func labelCard(label, plate, person, loc, qrURI string) string {
	plateRow := ""
	if plate != "" {
		plateRow = `<div class="plate">` + plate + `</div>`
	}
	return `<div class="label">
      <img src="` + qrURI + `" alt="QR">
      <div class="meta">
        <div class="brand">Parkrr</div>
        <div class="name">` + label + `</div>
        ` + plateRow + `
        <div class="loc">` + person + `</div>
        <div class="loc">` + loc + `</div>
      </div>
    </div>`
}

// labelStyle is shared by the single label and the A4 sheet.
const labelStyle = `
  :root { color-scheme: light; }
  * { box-sizing: border-box; }
  body { margin: 0; background: #eef1f4; color: #17222e; font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
  .sheet { display: flex; flex-direction: column; align-items: center; gap: 14px; padding: 24px; }
  .label { width: 320px; background: #fff; border: 1px solid #cdd5dd; border-radius: 14px; padding: 18px; display: flex; gap: 16px; align-items: center; }
  .label img { width: 120px; height: 120px; image-rendering: pixelated; }
  .meta { min-width: 0; }
  .brand { font-size: 11px; letter-spacing: .14em; text-transform: uppercase; color: #109a8c; font-weight: 700; }
  .name { font-size: 20px; font-weight: 750; line-height: 1.15; margin: 2px 0 4px; word-break: break-word; }
  .plate { display: inline-block; font-family: ui-monospace, monospace; font-weight: 700; border: 1.5px solid #17222e; border-radius: 5px; padding: 1px 7px; margin-bottom: 5px; }
  .loc { font-size: 13px; color: #5a6b7b; }
  .print { border: 0; background: #109a8c; color: #fff; font: inherit; font-weight: 600; padding: 9px 18px; border-radius: 9px; cursor: pointer; }
  @media print {
    body { background: #fff; }
    .sheet { padding: 0; }
    .label { border: none; }
    .print { display: none; }
  }`

// renderLabelSheet lays the cards out as an A4 sheet: two columns and five rows
// per page. hall is HTML-escaped; cards come from labelCard.
func renderLabelSheet(hall string, count int, cards string) string {
	empty := ""
	if count == 0 {
		empty = `<p class="empty">In dieser Halle steht noch kein Gefährt.</p>`
	}
	return `<!doctype html><html lang="de"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Etiketten · ` + hall + `</title>
<style>` + labelStyle + `
  .head { display: flex; justify-content: space-between; align-items: center; gap: 12px; width: min(100%, 186mm); }
  .head h1 { font-size: 18px; margin: 0; }
  .grid { display: grid; grid-template-columns: repeat(2, 90mm); gap: 5mm 6mm; justify-content: center; }
  .grid .label { width: 90mm; height: 50mm; break-inside: avoid; }
  .empty { color: #5a6b7b; }
  @page { size: A4; margin: 12mm; }
  @media print { .sheet { display: block; } .head { display: none; } .grid .label { border: 1px dashed #cdd5dd; } }
</style></head>
<body>
  <div class="sheet">
    <div class="head"><h1>` + hall + ` · ` + strconv.Itoa(count) + ` Etiketten</h1>
      <button class="print" onclick="window.print()">Bogen drucken</button></div>
    ` + empty + `<div class="grid">` + cards + `</div>
  </div>
</body></html>`
}

func renderVehicleLabel(label, plate, person, loc, qrURI string) string {
	return `<!doctype html><html lang="de"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Label · ` + label + `</title>
<style>` + labelStyle + `
</style></head>
<body>
  <div class="sheet">
    ` + labelCard(label, plate, person, loc, qrURI) + `
    <button class="print" onclick="window.print()">Label drucken</button>
  </div>
</body></html>`
}
