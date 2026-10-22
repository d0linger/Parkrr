package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func seasonFixture(t *testing.T, h *Handler, endDate string) (pid, vid, catID int64) {
	t.Helper()
	ctx := context.Background()
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	pid = createIntegrationPerson(t, h)
	if err := h.Pool.QueryRow(ctx, `INSERT INTO categories (name, default_monthly_cost, default_yearly_cost) VALUES ($1, 70, 700) RETURNING id`, "Saison-"+tag).Scan(&catID); err != nil {
		t.Fatal(err)
	}
	var end any
	if endDate != "" {
		end = endDate
	}
	if err := h.Pool.QueryRow(ctx, `INSERT INTO vehicles (person_id, category_id, label, license_plate, billing_period, rate, start_date, end_date, status)
		VALUES ($1, $2, 'Eriba '||$3, 'S-'||right($3, 5), 'monthly', 60, '2025-11-01', $4, 'stored') RETURNING id`, pid, catID, tag, end).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	return
}

// Saisonwechsel: Vorjahrespreis bleibt Standard; auf Wunsch der heutige Tarif und eine
// Reservierung ab dem neuen Beginn.
func TestDuplicateSeasonOptions(t *testing.T) {
	h := testHandler(t)
	_, vid, _ := seasonFixture(t, h, "2026-03-31")
	dup := func(body string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+strconv.FormatInt(vid, 10)+"/duplicate", strings.NewReader(body))
		req.SetPathValue("id", strconv.FormatInt(vid, 10))
		w := httptest.NewRecorder()
		h.DuplicateVehicle(w, req)
		if w.Code >= 300 {
			t.Fatalf("duplicate %s: %d %s", body, w.Code, w.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	a := dup(`{"start_date":"2026-11-01"}`)
	if a["rate"].(float64) != 60 || a["status"] != "stored" {
		t.Errorf("default copy: rate=%v status=%v, want the agreed 60 and stored", a["rate"], a["status"])
	}
	b := dup(`{"start_date":"2026-11-01","current_tariff":true,"reserve":true}`)
	if b["rate"].(float64) != 70 || b["status"] != "reserved" || !strings.HasPrefix(b["reserved_from"].(string), "2026-11-01") {
		t.Errorf("season copy: rate=%v status=%v reserved_from=%v, want 70, reserved, 2026-11-01", b["rate"], b["status"], b["reserved_from"])
	}
}

// Zeitfenster: ohne Fenster bleibt das Wunschdatum; mit Fenstern ist die Uhrzeit
// Pflicht, und der letzte Platz eines Zeitpunkts wird nur einmal vergeben.
func TestPickupWindowsAndSlots(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM pickup_windows`) })
	h.Now = func() time.Time { return time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC) } // Mittwoch
	save := func(body string) int {
		w := httptest.NewRecorder()
		h.SavePickupWindows(w, httptest.NewRequest(http.MethodPut, "/api/pickup-windows", strings.NewReader(body)))
		return w.Code
	}
	if c := save(`[{"weekday":6,"start_min":600,"end_min":540,"slot_min":30,"capacity":1}]`); c != http.StatusBadRequest {
		t.Fatalf("end before start: %d, want 400", c)
	}
	// Samstag 9:00-10:00 in 30-Minuten-Schritten, je ein Platz.
	if c := save(`[{"weekday":6,"start_min":540,"end_min":600,"slot_min":30,"capacity":1}]`); c != http.StatusOK {
		t.Fatalf("save: %d", c)
	}
	slots, err := h.pickupSlots(ctx, h.Pool, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 2 || slots[0].Date != "2026-10-17" || slots[0].Time != "09:00" || slots[1].Time != "09:30" {
		t.Fatalf("slots = %+v, want Saturday 09:00 and 09:30", slots)
	}
	pA := createIntegrationPerson(t, h)
	pB := createIntegrationPerson(t, h)
	ta, tb := createPortalLink(t, h, pA), createPortalLink(t, h, pB)
	if w := portalRequestReq(t, h, ta, map[string]any{"kind": "pickup", "date": "2026-10-17"}); w.Code != http.StatusBadRequest {
		t.Errorf("without time while windows exist: %d, want 400", w.Code)
	}
	if w := portalRequestReq(t, h, ta, map[string]any{"kind": "pickup", "date": "2026-10-17", "time": "09:00"}); w.Code != http.StatusCreated {
		t.Fatalf("book 09:00: %d %s", w.Code, w.Body.String())
	}
	if w := portalRequestReq(t, h, tb, map[string]any{"kind": "pickup", "date": "2026-10-17", "time": "09:00"}); w.Code != http.StatusConflict {
		t.Errorf("second booking of a full slot: %d, want 409", w.Code)
	}
	if w := portalRequestReq(t, h, tb, map[string]any{"kind": "pickup", "date": "2026-10-17", "time": "11:00"}); w.Code != http.StatusConflict {
		t.Errorf("time outside any window: %d, want 409", w.Code)
	}
	slots, _ = h.pickupSlots(ctx, h.Pool, 7)
	if len(slots) != 1 || slots[0].Time != "09:30" {
		t.Errorf("after booking: %+v, want only 09:30", slots)
	}
	// Die Uhrzeit erscheint in der Terminquelle (Wochenliste, Kalender-Abo).
	events, _ := h.scheduleEvents(ctx, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC))
	var hasTime bool
	for _, e := range events {
		if e.Kind == "wish" && e.PersonID == pA && e.Time == "09:00" {
			hasTime = true
		}
	}
	if !hasTime {
		t.Error("scheduled wish lacks its time")
	}
}

// Verlängerungswunsch: nur für eigene, aktive Gefährte und nur nach dem bisherigen Ende.
func TestPortalExtendRequest(t *testing.T) {
	h := testHandler(t)
	pid, vid, _ := seasonFixture(t, h, "2026-11-30")
	_, foreign, _ := seasonFixture(t, h, "2026-11-30")
	h.Now = func() time.Time { return time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC) }
	token := createPortalLink(t, h, pid)
	if w := portalRequestReq(t, h, token, map[string]any{"kind": "extend", "vehicle_id": foreign, "until": "2027-03-31"}); w.Code != http.StatusBadRequest {
		t.Errorf("someone else's vehicle: %d, want 400", w.Code)
	}
	if w := portalRequestReq(t, h, token, map[string]any{"kind": "extend", "vehicle_id": vid, "until": "2026-11-15"}); w.Code != http.StatusBadRequest {
		t.Errorf("until before the current end: %d, want 400", w.Code)
	}
	w := portalRequestReq(t, h, token, map[string]any{"kind": "extend", "vehicle_id": vid, "until": "2027-03-31", "note": "bitte bis Ostern"})
	if w.Code != http.StatusCreated {
		t.Fatalf("extend: %d %s", w.Code, w.Body.String())
	}
	var payload map[string]string
	var raw []byte
	if err := h.Pool.QueryRow(context.Background(), `SELECT payload FROM portal_requests WHERE person_id=$1 AND kind='extend'`, pid).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &payload)
	if payload["until"] != "2027-03-31" || payload["previous_end"] != "2026-11-30" || payload["vehicle_id"] != strconv.FormatInt(vid, 10) {
		t.Errorf("payload = %v", payload)
	}
}

// Vertragsende-Erinnerung: aus, bis Tage gesetzt sind; dann genau einmal je Ende.
func TestContractEndNotices(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `UPDATE mail_automation SET end_reminder_days=0 WHERE id=1`)
	})
	pid, vid, _ := seasonFixture(t, h, time.Now().AddDate(0, 0, 10).Format("2006-01-02"))
	if _, err := h.Pool.Exec(ctx, `UPDATE persons SET email='ende@example.com' WHERE id=$1`, pid); err != nil {
		t.Fatal(err)
	}
	fake := &attachSender{}
	h.Mail = fake
	count := func() (n int) {
		_ = h.Pool.QueryRow(ctx, `SELECT count(*) FROM mail_outbox WHERE kind='contract_end' AND ref_id=$1`, vid).Scan(&n)
		return
	}
	if n, _ := h.QueueContractEndNotices(ctx); n != 0 || count() != 0 {
		t.Fatal("off by default")
	}
	_, _ = h.Pool.Exec(ctx, `UPDATE mail_automation SET end_reminder_days=14 WHERE id=1`)
	if _, err := h.QueueContractEndNotices(ctx); err != nil {
		t.Fatal(err)
	}
	_, _ = h.QueueContractEndNotices(ctx) // zweiter Lauf: nichts doppelt
	if count() != 1 {
		t.Fatalf("queued %d notices, want exactly 1", count())
	}
	h.ProcessOutbox(ctx)
	if len(fake.sent) != 1 || !strings.Contains(fake.sent[0].body, "endet am") || fake.sent[0].to[0] != "ende@example.com" {
		t.Fatalf("sent = %+v", fake.sent)
	}
}

// Warteliste: Einwilligung ist Pflicht, Übernehmen legt die Person an, Löschen
// protokolliert ohne Kontaktdaten.
func TestWaitlistLifecycle(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.CreateWaitlist(w, httptest.NewRequest(http.MethodPost, "/api/waitlist", strings.NewReader(body)))
		return w
	}
	if w := post(`{"name":"Lena Integration","email":"lena@example.com"}`); w.Code != http.StatusBadRequest {
		t.Errorf("without consent: %d, want 400", w.Code)
	}
	if w := post(`{"name":"Lena Integration","consent":true}`); w.Code != http.StatusBadRequest {
		t.Errorf("without any contact: %d, want 400", w.Code)
	}
	w := post(`{"name":"Lena Maria Integration","email":"lena@example.com","consent":true,"wanted_from":"2026-11-01","length_m":6.2}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var e waitlistEntry
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	conv := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/waitlist/"+strconv.FormatInt(e.ID, 10)+"/convert", nil)
		req.SetPathValue("id", strconv.FormatInt(e.ID, 10))
		rw := httptest.NewRecorder()
		h.ConvertWaitlist(rw, req)
		return rw
	}
	cw := conv()
	if cw.Code != http.StatusOK {
		t.Fatalf("convert: %d %s", cw.Code, cw.Body.String())
	}
	var res struct {
		PersonID int64 `json:"person_id"`
	}
	_ = json.Unmarshal(cw.Body.Bytes(), &res)
	var first, last, email string
	if err := h.Pool.QueryRow(ctx, `SELECT first_name, last_name, email FROM persons WHERE id=$1`, res.PersonID).Scan(&first, &last, &email); err != nil {
		t.Fatal(err)
	}
	if first != "Lena Maria" || last != "Integration" || email != "lena@example.com" {
		t.Errorf("person = %q %q %q", first, last, email)
	}
	if c := conv().Code; c != http.StatusConflict {
		t.Errorf("second convert: %d, want 409", c)
	}
	dreq := httptest.NewRequest(http.MethodDelete, "/api/waitlist/"+strconv.FormatInt(e.ID, 10), nil)
	dreq.SetPathValue("id", strconv.FormatInt(e.ID, 10))
	dw := httptest.NewRecorder()
	h.DeleteWaitlist(dw, dreq)
	if dw.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", dw.Code)
	}
	var changes []byte
	_ = h.Pool.QueryRow(ctx, `SELECT COALESCE(changes::text, '') FROM audit_log WHERE entity='waitlist' AND entity_id=$1 AND action='delete' ORDER BY id DESC LIMIT 1`, e.ID).Scan(&changes)
	if bytes.Contains(changes, []byte("lena@example.com")) {
		t.Error("the delete audit must not keep the contact")
	}
	sw := httptest.NewRecorder()
	h.WaitlistSummary(sw, httptest.NewRequest(http.MethodGet, "/api/waitlist/summary", nil))
	if sw.Code != http.StatusOK || !strings.Contains(sw.Body.String(), `"open":`) {
		t.Errorf("summary: %d %s", sw.Code, sw.Body.String())
	}
}
