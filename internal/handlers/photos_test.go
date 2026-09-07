package handlers

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// mkPhotoVehicle legt Person + Tarif + Gefährt an — das Minimum, an dem ein Foto
// hängen kann — und räumt alles wieder ab.
func mkPhotoVehicle(t *testing.T, h *Handler) int64 {
	t.Helper()
	ctx := context.Background()
	var pid, catID, vid int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name) VALUES ('Foto','Integration') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("person: %v", err)
	}
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO categories (name) VALUES ('FotoTarif-'||clock_timestamp()::text) RETURNING id`).Scan(&catID); err != nil {
		t.Fatalf("category: %v", err)
	}
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO vehicles (person_id, category_id, status, label) VALUES ($1,$2,'stored','FotoAuto') RETURNING id`,
		pid, catID).Scan(&vid); err != nil {
		t.Fatalf("vehicle: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = h.Pool.Exec(c, `DELETE FROM vehicle_photos WHERE vehicle_id=$1`, vid)
		_ = purgeExec(c, h.Pool, `DELETE FROM vehicles WHERE id=$1`, vid)
		_, _ = h.Pool.Exec(c, `DELETE FROM categories WHERE id=$1`, catID)
		_ = purgeExec(c, h.Pool, `DELETE FROM persons WHERE id=$1`, pid)
	})
	return vid
}

// tinyJPEG erzeugt ein echtes, dekodierbares JPEG (kein Byte-Gefrickel: die
// Sanitisierung dekodiert wirklich, ein gefälschter Header fällt durch).
func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for x := 0; x < 8; x++ {
		for y := 0; y < 6; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 40), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func uploadPhotoReq(t *testing.T, h *Handler, vehicleID int64, field, filename string, payload []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := fw.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(vehicleID, 10)+"/photos", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetPathValue("id", strconv.FormatInt(vehicleID, 10))
	rec := httptest.NewRecorder()
	h.UploadPhoto(rec, req)
	return rec
}

// Der Kernvertrag des Uploads (Hundert 91): ein echtes JPEG wird angenommen,
// NEU KODIERT gespeichert (Metadaten weg) und ist danach abrufbar.
func TestPhotoUploadRoundTrip(t *testing.T) {
	h := testHandler(t)
	vid := mkPhotoVehicle(t, h)

	rec := uploadPhotoReq(t, h, vid, "photo", "auto.jpg", tinyJPEG(t))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"content_type":"image/jpeg"`) {
		t.Errorf("Antwort ohne JPEG-Typ: %s", rec.Body.String())
	}

	// Gespeichert wurden die NEU kodierten Bytes, nicht die Originaldatei: die
	// gespeicherten Bytes müssen selbst wieder ein gültiges JPEG sein.
	var data []byte
	var photoID int64
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT id, data FROM vehicle_photos WHERE vehicle_id=$1`, vid).Scan(&photoID, &data); err != nil {
		t.Fatalf("read stored: %v", err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		t.Errorf("die gespeicherten Bytes sind kein dekodierbares JPEG: %v", err)
	}

	// GetPhoto liefert sie mit nosniff und einem erlaubten Content-Type aus.
	greq := httptest.NewRequest(http.MethodGet, "/api/photos/"+strconv.FormatInt(photoID, 10), nil)
	greq.SetPathValue("id", strconv.FormatInt(photoID, 10))
	grec := httptest.NewRecorder()
	h.GetPhoto(grec, greq)
	if grec.Code != http.StatusOK {
		t.Fatalf("get: %d", grec.Code)
	}
	if ct := grec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type %q", ct)
	}
	if grec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff fehlt — ein manipulierter Datensatz dürfte sonst als HTML gerendert werden")
	}
}

// Die Sanitisierung DEKODIERT wirklich: ein Nicht-Bild mit Bilddateinamen und ein
// GIF (nicht erlaubt) müssen abgelehnt werden, ein fehlendes Feld ist ein 400.
func TestPhotoUploadRejectsNonImages(t *testing.T) {
	h := testHandler(t)
	vid := mkPhotoVehicle(t, h)

	if rec := uploadPhotoReq(t, h, vid, "photo", "nicht-bild.jpg", []byte("ich bin kein bild")); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("Nicht-Bild: %d, erwartet 415", rec.Code)
	}
	// Kleinstes gültiges GIF — echtes Bildformat, aber nicht auf der Erlaubnisliste.
	gif := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x00\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")
	if rec := uploadPhotoReq(t, h, vid, "photo", "bild.gif", gif); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("GIF: %d, erwartet 415", rec.Code)
	}
	if rec := uploadPhotoReq(t, h, vid, "falschesfeld", "auto.jpg", tinyJPEG(t)); rec.Code != http.StatusBadRequest {
		t.Errorf("fehlendes Feld: %d, erwartet 400", rec.Code)
	}
}

// Ein Upload auf ein Gefährt, das es nicht gibt, ist ein 404 über die
// FK-Verletzung — kein 500 und keine verwaiste Zeile.
func TestPhotoUploadUnknownVehicleIs404(t *testing.T) {
	h := testHandler(t)
	rec := uploadPhotoReq(t, h, 99999999, "photo", "auto.jpg", tinyJPEG(t))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unbekanntes Gefährt: %d, erwartet 404", rec.Code)
	}
}

// Löschen entfernt die Zeile und quittiert ein zweites Löschen mit 404 — sonst
// entstünde ein Phantom-Audit-Eintrag für etwas, das gar nicht mehr da war.
func TestPhotoDeleteTwiceIs404(t *testing.T) {
	h := testHandler(t)
	vid := mkPhotoVehicle(t, h)
	if rec := uploadPhotoReq(t, h, vid, "photo", "auto.jpg", tinyJPEG(t)); rec.Code != http.StatusCreated {
		t.Fatalf("upload: %d", rec.Code)
	}
	var photoID int64
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT id FROM vehicle_photos WHERE vehicle_id=$1`, vid).Scan(&photoID); err != nil {
		t.Fatalf("photo id: %v", err)
	}
	del := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/photos/"+strconv.FormatInt(photoID, 10), nil)
		req.SetPathValue("id", strconv.FormatInt(photoID, 10))
		rec := httptest.NewRecorder()
		h.DeletePhoto(rec, req)
		return rec
	}
	if rec := del(); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := del(); rec.Code != http.StatusNotFound {
		t.Errorf("zweites Löschen: %d, erwartet 404", rec.Code)
	}
}

// Ein Bild jenseits der Maximalmaße muss am HEADER abgelehnt werden, bevor die
// Pixel dekodiert werden. Ein echtes, schmales PNG knapp über der Achsen-Grenze
// (8001×1) ist billig zu erzeugen und trifft genau diesen Wächter.
//
// (Ein manipulierter IHDR mit erfundenen Riesenmaßen taugt als Testvehikel NICHT:
// Gos png-Decoder prüft die Chunk-CRC schon in DecodeConfig, die Fälschung fällt
// als "could not read image" durch und der Größen-Wächter wird nie erreicht —
// ausprobiert, deshalb dieser Weg.)
func TestSanitizeImageRejectsOversizedDimensions(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, maxPhotoDimension+1, 1))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	_, _, err := sanitizeImage(buf.Bytes())
	if err == nil {
		t.Fatalf("%d×1 muss abgelehnt werden (Grenze %d je Achse)", maxPhotoDimension+1, maxPhotoDimension)
	}
	if !strings.Contains(err.Error(), "dimensions") {
		t.Errorf("unerwarteter Grund: %v", err)
	}
}
