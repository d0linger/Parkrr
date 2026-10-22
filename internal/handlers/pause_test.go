package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func pauseFixture(t *testing.T, h *Handler) (pid, vid int64) {
	t.Helper()
	pid, vid, _ = seasonFixture(t, h, "")
	if _, err := h.Pool.Exec(context.Background(), `UPDATE vehicles SET rate = 62, start_date = '2026-01-01' WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	return pid, vid
}

func startPause(t *testing.T, h *Handler, vid int64, body string) (*vehiclePause, int, string) {
	t.Helper()
	w := callJSONRaw(h, h.StartPause, http.MethodPost, "/api/vehicles/x/pause", strconv.FormatInt(vid, 10), body)
	if w.Code != http.StatusCreated {
		return nil, w.Code, w.Body.String()
	}
	var p vehiclePause
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	return &p, w.Code, ""
}

func julyLine(t *testing.T, h *Handler, pid int64, kind string) float64 {
	t.Helper()
	iv := mustInvoice(t, h, pid)
	var total float64
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(sum(it.line_total), 0) FROM invoice_items it WHERE it.invoice_id = $1 AND it.description LIKE $2`,
		iv.ID, "%"+kind+"%2026-07%").Scan(&total); err != nil {
		t.Fatal(err)
	}
	return total
}

// Ruhezeit: 10 von 31 Julitagen ruhen Miete und gebundene Nebenkosten; die offene
// Ruhezeit ruht über den geplanten Tag hinaus, bis „Ist zurück“; der Rückkehrtag ist
// wieder Miettag.
func TestPauseBillingAndReturn(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, vid := pauseFixture(t, h)
	withAddress(t, h, pid)
	if _, err := h.Pool.Exec(ctx, `INSERT INTO recurring_charges (person_id, vehicle_id, description, amount, period, start_date)
		VALUES ($1, $2, 'Strom', 31, 'monthly', '2026-01-01')`, pid, vid); err != nil {
		t.Fatal(err)
	}
	h.Now = func() time.Time { return time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC) }
	p, code, msg := startPause(t, h, vid, `{"from":"2026-07-10","until":"2026-07-19"}`)
	if p == nil {
		t.Fatalf("pause: %d %s", code, msg)
	}
	if !p.HoldSpot || p.Ended {
		t.Fatalf("Standard: Platz halten, läuft noch: %+v", p)
	}
	if _, code, _ := startPause(t, h, vid, `{"from":"2026-07-25"}`); code != http.StatusConflict {
		t.Fatalf("zweite, überschneidende Ruhezeit (die erste ist offen): %d, want 409", code)
	}
	// Geplanter Tag verstrichen, niemand hat „Ist zurück“ getippt: die Ruhezeit läuft weiter.
	h.Now = func() time.Time { return time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC) }
	due := callJSON(h, h.PausesDue, http.MethodGet, "/api/pauses/due", "", nil)
	if !strings.Contains(due.Body.String(), `"pause_id":`+strconv.FormatInt(p.ID, 10)) {
		t.Fatalf("Rückkehr fällig in der Heute-Liste: %s", due.Body.String())
	}
	back := callJSONRaw(h, h.ReturnFromPause, http.MethodPost, "/api/vehicle-pauses/x/return", strconv.FormatInt(p.ID, 10), `{"on":"2026-07-20"}`)
	if back.Code != http.StatusOK {
		t.Fatalf("Ist zurück: %d %s", back.Code, back.Body.String())
	}
	h.Now = func() time.Time { return time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC) }
	if got := julyLine(t, h, pid, "Einstellplatz"); got != 42 {
		t.Errorf("Juli-Miete mit 10 Ruhetagen: %v, want 42 (62 × 21/31)", got)
	}
	var strom float64
	if err := h.Pool.QueryRow(ctx, `SELECT COALESCE(sum(line_total), 0) FROM invoice_items it JOIN invoices i ON i.id = it.invoice_id
		WHERE i.person_id = $1 AND it.description LIKE '%Strom%2026-07%'`, pid).Scan(&strom); err != nil {
		t.Fatal(err)
	}
	if strom != 21 {
		t.Errorf("gebundene Nebenkosten ruhen mit: Juli %v, want 21 (31 × 21/31)", strom)
	}
	// Jetzt ist Juli abgerechnet: eine neue Ruhezeit im Juli ist gesperrt.
	_, code, msg = startPause(t, h, vid, `{"from":"2026-07-25","until":"2026-07-28"}`)
	if code != http.StatusConflict || !strings.Contains(msg, "01.08.2026") {
		t.Fatalf("Ruhezeit in abgerechnetem Monat: %d %s", code, msg)
	}
}

// Vorab bezahlt: „bezahlt bis“ verschiebt sich um die Ruhetage; Löschen stellt es wieder her.
func TestPausePaidThroughShiftsAndRestores(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	_, vid := pauseFixture(t, h)
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET paid = true, paid_through = '2026-07-31' WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	h.Now = func() time.Time { return time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC) }
	p, code, msg := startPause(t, h, vid, `{"from":"2026-07-10"}`)
	if p == nil {
		t.Fatalf("pause: %d %s", code, msg)
	}
	back := callJSONRaw(h, h.ReturnFromPause, http.MethodPost, "/api/vehicle-pauses/x/return", strconv.FormatInt(p.ID, 10), `{"on":"2026-07-20"}`)
	if back.Code != http.StatusOK {
		t.Fatalf("Ist zurück: %d %s", back.Code, back.Body.String())
	}
	var through time.Time
	if err := h.Pool.QueryRow(ctx, `SELECT paid_through FROM vehicles WHERE id = $1`, vid).Scan(&through); err != nil {
		t.Fatal(err)
	}
	if through.Format(dateLayout) != "2026-08-10" {
		t.Fatalf("bezahlt bis nach 10 Ruhetagen: %s, want 2026-08-10", through.Format(dateLayout))
	}
	if c := callJSON(h, h.DeletePause, http.MethodDelete, "/api/vehicle-pauses/x", strconv.FormatInt(p.ID, 10), nil).Code; c != http.StatusOK {
		t.Fatalf("löschen: %d", c)
	}
	if err := h.Pool.QueryRow(ctx, `SELECT paid_through FROM vehicles WHERE id = $1`, vid).Scan(&through); err != nil || through.Format(dateLayout) != "2026-07-31" {
		t.Fatalf("bezahlt bis nach dem Löschen: %s %v", through.Format(dateLayout), err)
	}
}

func TestPauseConcurrentReturnShiftsPaidThroughOnce(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, vid := pauseFixture(t, h)
	h.Now = func() time.Time { return time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC) }
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET paid = true, paid_through = '2026-07-31' WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	p, code, msg := startPause(t, h, vid, `{"from":"2026-07-10"}`)
	if p == nil {
		t.Fatalf("pause: %d %s", code, msg)
	}
	blocker, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `SELECT id FROM vehicles WHERE id = $1 FOR UPDATE`, vid); err != nil {
		t.Fatal(err)
	}
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			r := httptest.NewRequest(http.MethodPost, "/api/vehicle-pauses/x/return", strings.NewReader(`{"on":"2026-07-20"}`)).WithContext(ctx)
			r.SetPathValue("id", strconv.FormatInt(p.ID, 10))
			w := httptest.NewRecorder()
			h.ReturnFromPause(w, r)
			results <- w
		}()
	}
	// Both requests must reach a database lock before releasing the vehicle.
	// This reproduces two operators returning the same vehicle simultaneously.
	for {
		var waiting int
		if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("return requests did not reach the locks")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	statuses := map[int]int{}
	for range 2 {
		select {
		case w := <-results:
			statuses[w.Code]++
		case <-ctx.Done():
			t.Fatal("return requests did not finish")
		}
	}
	if statuses[http.StatusOK] != 1 || statuses[http.StatusConflict] != 1 {
		t.Errorf("return statuses = %v, want one 200 and one 409", statuses)
	}
	var through time.Time
	if err := h.Pool.QueryRow(ctx, `SELECT paid_through FROM vehicles WHERE id = $1`, vid).Scan(&through); err != nil {
		t.Fatal(err)
	}
	if got := through.Format(dateLayout); got != "2026-08-10" {
		t.Errorf("paid through = %s, want 2026-08-10 after ten paused days", got)
	}
}

// Platz freigeben, Rückkehr belegt ihn wieder; „Doch abgeholt“ wird ein normales Abholen.
func TestPauseReleaseRelinkAndCollect(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	_, vid := pauseFixture(t, h)
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	var spot int64
	if err := h.Pool.QueryRow(ctx, `WITH g AS (INSERT INTO garages (name) VALUES ('Ruhe-'||$1) RETURNING id),
		hl AS (INSERT INTO halls (garage_id, name) SELECT id, 'Halle' FROM g RETURNING id)
		INSERT INTO spots (hall_id, label) SELECT id, 'B-07' FROM hl RETURNING id`, tag).Scan(&spot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM garages WHERE name = 'Ruhe-'||$1`, tag) })
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET spot_id = $2 WHERE id = $1`, vid, spot); err != nil {
		t.Fatal(err)
	}
	h.Now = func() time.Time { return time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC) }
	if _, code, _ := startPause(t, h, vid, `{"from":"2026-07-15","hold_spot":false}`); code != http.StatusBadRequest {
		t.Fatalf("Platz freigeben vor dem Abfahrtstag: %d, want 400", code)
	}
	p, code, msg := startPause(t, h, vid, `{"from":"2026-07-10","hold_spot":false}`)
	if p == nil {
		t.Fatalf("pause: %d %s", code, msg)
	}
	var cur *int64
	if err := h.Pool.QueryRow(ctx, `SELECT spot_id FROM vehicles WHERE id = $1`, vid).Scan(&cur); err != nil || cur != nil {
		t.Fatalf("freigegeben: spot %v %v", cur, err)
	}
	back := callJSONRaw(h, h.ReturnFromPause, http.MethodPost, "/api/vehicle-pauses/x/return", strconv.FormatInt(p.ID, 10), `{"on":"2026-07-12"}`)
	if back.Code != http.StatusOK || !strings.Contains(back.Body.String(), `"relinked":true`) {
		t.Fatalf("Rückkehr belegt den alten Platz: %d %s", back.Code, back.Body.String())
	}
	q, code, msg := startPause(t, h, vid, `{"from":"2026-07-20","until":"2026-08-10"}`)
	if q == nil {
		t.Fatalf("zweite Ruhezeit: %d %s", code, msg)
	}
	col := callJSON(h, h.CollectFromPause, http.MethodPost, "/api/vehicle-pauses/x/collect", strconv.FormatInt(q.ID, 10), nil)
	if col.Code != http.StatusOK {
		t.Fatalf("Doch abgeholt: %d %s", col.Code, col.Body.String())
	}
	var status string
	var end time.Time
	if err := h.Pool.QueryRow(ctx, `SELECT status, end_date FROM vehicles WHERE id = $1`, vid).Scan(&status, &end); err != nil {
		t.Fatal(err)
	}
	if status != "collected" || end.Format(dateLayout) != "2026-07-20" {
		t.Fatalf("Doch abgeholt: %s bis %s, want collected bis 2026-07-20", status, end.Format(dateLayout))
	}
	var left int
	_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM vehicle_pauses WHERE id = $1`, q.ID).Scan(&left)
	if left != 0 {
		t.Fatal("die Ruhezeit entfällt beim Abholen")
	}
}

// Portal: Kurz abholen nur für eigene, eingelagerte Gefährte und nicht rückwirkend.
func TestPortalPauseRequest(t *testing.T) {
	h := testHandler(t)
	pid, vid := pauseFixture(t, h)
	_, other := pauseFixture(t, h)
	h.Now = func() time.Time { return time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC) }
	token := createPortalLink(t, h, pid)
	send := func(body map[string]any) int { return portalRequestReq(t, h, token, body).Code }
	if c := send(map[string]any{"kind": "pause", "vehicle_id": vid, "date": "2026-07-01"}); c != http.StatusBadRequest {
		t.Fatalf("rückwirkend: %d, want 400", c)
	}
	if c := send(map[string]any{"kind": "pause", "vehicle_id": other, "date": "2026-07-20"}); c != http.StatusBadRequest {
		t.Fatalf("fremdes Gefährt: %d, want 400", c)
	}
	if c := send(map[string]any{"kind": "pause", "vehicle_id": vid, "date": "2026-07-20", "until": "2026-08-09", "note": "Wintercamping"}); c != http.StatusCreated {
		t.Fatalf("anfragen: %d", c)
	}
}
