package handlers

import (
	"bytes"
	"encoding/base64"
	"errors"
	"html"
	"image/png"
	"net/http"
	"strconv"

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

	scheme := "http"
	if h.Auth != nil && h.Auth.RequestIsHTTPS(r) {
		scheme = "https"
	}
	target := scheme + "://" + r.Host + "/#/vehicles/" + strconv.FormatInt(id, 10)
	qrURI, err := qrDataURI(target, 260)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not render QR")
		return
	}

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

	// Tight, label-only CSP for this one static page.
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; img-src data:; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(renderVehicleLabel(
		html.EscapeString(label), html.EscapeString(plate),
		html.EscapeString(person), html.EscapeString(loc), qrURI)))
}

func renderVehicleLabel(label, plate, person, loc, qrURI string) string {
	plateRow := ""
	if plate != "" {
		plateRow = `<div class="plate">` + plate + `</div>`
	}
	return `<!doctype html><html lang="de"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Label · ` + label + `</title>
<style>
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
  }
</style></head>
<body>
  <div class="sheet">
    <div class="label">
      <img src="` + qrURI + `" alt="QR">
      <div class="meta">
        <div class="brand">Parkrr</div>
        <div class="name">` + label + `</div>
        ` + plateRow + `
        <div class="loc">` + person + `</div>
        <div class="loc">` + loc + `</div>
      </div>
    </div>
    <button class="print" onclick="window.print()">Label drucken</button>
  </div>
</body></html>`
}
