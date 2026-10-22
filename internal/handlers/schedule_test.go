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

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

func TestWeekStart(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-16": "2026-10-12", // Freitag
		"2026-10-12": "2026-10-12", // Montag
		"2026-10-18": "2026-10-12", // Sonntag gehört zur Woche davor
		"2027-01-01": "2026-12-28", // über den Jahreswechsel
	} {
		d, _ := time.Parse("2006-01-02", in)
		if got := weekStart(d).Format("2006-01-02"); got != want {
			t.Errorf("weekStart(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestICSTextAndFold(t *testing.T) {
	if got := icsText("Halle B; Platz 7, links\nhinten\\"); got != `Halle B\; Platz 7\, links\nhinten\\` {
		t.Errorf("icsText = %q", got)
	}
	long := "SUMMARY:" + strings.Repeat("ä", 60)
	folded := icsFold(long)
	for _, ln := range strings.Split(strings.TrimSuffix(folded, "\r\n"), "\r\n") {
		if len(ln) > 75 {
			t.Fatalf("Zeile mit %d Bytes: %q", len(ln), ln)
		}
	}
	if strings.ReplaceAll(strings.TrimSuffix(folded, "\r\n"), "\r\n ", "") != long {
		t.Error("Entfalten ergibt nicht den Originaltext (UTF-8 zerschnitten?)")
	}
}

func TestICSRepeatedEventsHaveUniqueStableUIDs(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "http://parkrr.test/ical/test", nil)
	day := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	events := []scheduleEvent{
		{Kind: "pickup", VehicleID: 7, Date: day},
		{Kind: "arrival", VehicleID: 7, Date: day},
		{Kind: "leave", VehicleID: 7, SourceID: 11, Date: day},
		{Kind: "return", VehicleID: 7, SourceID: 11, Date: day},
		{Kind: "leave", VehicleID: 7, SourceID: 12, Date: day},
		{Kind: "return", VehicleID: 7, SourceID: 12, Date: day},
		{Kind: "wish", PersonID: 5, SourceID: 21, Date: day, Time: "09:00"},
		{Kind: "wish", PersonID: 5, SourceID: 22, Date: day, Time: "09:00"},
	}
	uids := func(body string) []string {
		var result []string
		for _, line := range strings.Split(body, "\r\n") {
			if strings.HasPrefix(line, "UID:") {
				result = append(result, line)
			}
		}
		return result
	}
	before := uids(h.renderICS(req, events))
	seen := map[string]bool{}
	for _, uid := range before {
		if seen[uid] {
			t.Fatalf("calendar events share UID %q", uid)
		}
		seen[uid] = true
	}
	if len(before) != len(events) {
		t.Fatalf("got %d event UIDs, want %d", len(before), len(events))
	}
	for i := range events {
		events[i].Date = events[i].Date.AddDate(0, 0, 7)
		events[i].Time = "09:30"
	}
	after := uids(h.renderICS(req, events))
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("rescheduling changes event identities: %v -> %v", before, after)
	}
}

// Termine kommen aus Vertragsende, Reservierungsbeginn und offenen Abholwünschen; die
// Wochenliste ist ein PDF, das Abo liefert dieselben Termine ohne Namen und ist
// widerrufbar.
func TestScheduleWeekPDFAndCalendarFeed(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid := createIntegrationPerson(t, h)
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	h.Now = func() time.Time { return time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC) } // Mittwoch
	var catID, vid int64
	if err := h.Pool.QueryRow(ctx, `INSERT INTO categories (name, default_monthly_cost, default_yearly_cost) VALUES ($1, 20, 200) RETURNING id`, "Sched-"+tag).Scan(&catID); err != nil {
		t.Fatal(err)
	}
	if err := h.Pool.QueryRow(ctx, `INSERT INTO vehicles (person_id, category_id, label, license_plate, billing_period, rate, start_date, end_date, status)
		VALUES ($1, $2, 'Eriba '||$3, 'BN-627 CK', 'monthly', 20, '2026-01-01', '2026-10-17', 'stored') RETURNING id`, pid, catID, tag).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	wishIDs := make(map[int64]bool)
	for range 2 {
		var id int64
		if err := h.Pool.QueryRow(ctx, `INSERT INTO portal_requests (person_id, kind, payload) VALUES ($1, 'pickup', '{"date":"2026-10-15","note":"vormittags"}') RETURNING id`, pid).Scan(&id); err != nil {
			t.Fatal(err)
		}
		wishIDs[id] = true
	}
	pauseIDs := make(map[int64]int)
	for _, dates := range [][2]string{{"2026-10-12", "2026-10-13"}, {"2026-10-15", "2026-10-16"}} {
		var id int64
		if err := h.Pool.QueryRow(ctx, `INSERT INTO vehicle_pauses (vehicle_id, start_date, until, ended) VALUES ($1,$2,$3,true) RETURNING id`, vid, dates[0], dates[1]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		pauseIDs[id] = 0
	}

	events, err := h.scheduleEvents(ctx, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var pickup, wish bool
	for _, e := range events {
		if e.Kind == "pickup" && e.VehicleID == vid && e.Date.Format("2006-01-02") == "2026-10-17" {
			pickup = true
		}
		if e.Kind == "wish" && e.PersonID == pid && e.Note == "vormittags" {
			wish = true
			if !wishIDs[e.SourceID] {
				t.Fatalf("wish lost its database identity: %+v", e)
			}
			delete(wishIDs, e.SourceID)
		}
		if (e.Kind == "leave" || e.Kind == "return") && e.VehicleID == vid {
			if _, found := pauseIDs[e.SourceID]; !found {
				t.Fatalf("pause event lost its database identity: %+v", e)
			}
			pauseIDs[e.SourceID]++
		}
	}
	if !pickup || !wish {
		t.Fatalf("pickup=%v wish=%v in %+v", pickup, wish, events)
	}
	if len(wishIDs) != 0 {
		t.Fatalf("missing wish events: %v", wishIDs)
	}
	for id, count := range pauseIDs {
		if count != 2 {
			t.Fatalf("pause %d has %d events, want departure and return", id, count)
		}
	}

	rec := httptest.NewRecorder()
	h.WeekSchedulePDF(rec, httptest.NewRequest(http.MethodGet, "/api/reports/week.pdf?from=2026-10-16", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "%PDF") {
		t.Fatalf("week.pdf: %d %q", rec.Code, rec.Body.String()[:min(20, rec.Body.Len())])
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "parkrr-woche-2026-10-12.pdf") {
		t.Errorf("Content-Disposition = %q, want the Monday of the week", cd)
	}
	bad := httptest.NewRecorder()
	h.WeekSchedulePDF(bad, httptest.NewRequest(http.MethodGet, "/api/reports/week.pdf?from=morgen", nil))
	if bad.Code != http.StatusBadRequest {
		t.Errorf("invalid from: %d, want 400", bad.Code)
	}

	// Kalender-Abo für einen Benutzer.
	var uid int64
	if err := h.Pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, role) VALUES ($1, 'x', 'editor') RETURNING id`, "feed-"+tag).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
	asUser := func(r *http.Request) *http.Request {
		return r.WithContext(auth.ContextWithUser(r.Context(), &models.User{ID: uid, Username: "feed-" + tag, Role: "editor"}))
	}
	crec := httptest.NewRecorder()
	h.CalendarFeedCreate(crec, asUser(httptest.NewRequest(http.MethodPost, "/api/calendar/feed", nil)))
	if crec.Code != http.StatusCreated {
		t.Fatalf("create feed: %d %s", crec.Code, crec.Body.String())
	}
	var created struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(crec.Body.Bytes(), &created)
	token := created.URL[strings.LastIndex(created.URL, "/ical/")+len("/ical/"):]
	var stored []byte
	if err := h.Pool.QueryRow(ctx, `SELECT token_hash FROM calendar_feeds WHERE user_id=$1`, uid).Scan(&stored); err != nil || string(stored) == token {
		t.Fatalf("stored token must be a hash, got err=%v", err)
	}

	feed := func(tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/ical/"+tok, nil)
		req.SetPathValue("token", tok)
		w := httptest.NewRecorder()
		h.CalendarFeed(w, req)
		return w
	}
	f := feed(token)
	if f.Code != http.StatusOK || !strings.HasPrefix(f.Header().Get("Content-Type"), "text/calendar") {
		t.Fatalf("feed: %d %s", f.Code, f.Header().Get("Content-Type"))
	}
	ics := f.Body.String()
	for _, want := range []string{"BEGIN:VCALENDAR", "UID:pickup-" + strconv.FormatInt(vid, 10) + "@parkrr", "DTSTART;VALUE=DATE:20261017", "Abholung: Eriba", "BN-627 CK", "Abholwunsch aus dem Portal"} {
		if !strings.Contains(ics, want) {
			t.Errorf("feed is missing %q", want)
		}
	}
	if strings.Contains(ics, "Integration") {
		t.Error("feed must not contain customer names")
	}

	rrec := httptest.NewRecorder()
	h.CalendarFeedRevoke(rrec, asUser(httptest.NewRequest(http.MethodDelete, "/api/calendar/feed", nil)))
	if rrec.Code != http.StatusNoContent {
		t.Fatalf("revoke: %d", rrec.Code)
	}
	if f := feed(token); f.Code != http.StatusNotFound {
		t.Errorf("revoked feed: %d, want 404", f.Code)
	}
}
