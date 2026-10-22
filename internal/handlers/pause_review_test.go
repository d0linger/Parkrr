package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func paidReturnedPause(t *testing.T, h *Handler) (int64, *vehiclePause) {
	t.Helper()
	_, vid := pauseFixture(t, h)
	h.Now = func() time.Time { return time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC) }
	if _, err := h.Pool.Exec(context.Background(), `UPDATE vehicles SET paid = true, paid_through = '2026-07-31' WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	p, code, msg := startPause(t, h, vid, `{"from":"2026-07-10"}`)
	if p == nil {
		t.Fatalf("start pause: %d %s", code, msg)
	}
	w := callJSONRaw(h, h.ReturnFromPause, http.MethodPost, "/api/vehicle-pauses/x/return", strconv.FormatInt(p.ID, 10), `{"on":"2026-07-20"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("return: %d %s", w.Code, w.Body.String())
	}
	return vid, p
}

func pauseRowJSON(t *testing.T, h *Handler, id int64) string {
	t.Helper()
	var snapshot string
	if err := h.Pool.QueryRow(context.Background(), `SELECT to_jsonb(p)::text FROM vehicle_pauses p WHERE id = $1`, id).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestPauseCollectRejectsEndedWithoutChangingCoverage(t *testing.T) {
	h := testHandler(t)
	vid, p := paidReturnedPause(t, h)
	before := pauseRowJSON(t, h, p.ID)
	w := callJSON(h, h.CollectFromPause, http.MethodPost, "/api/vehicle-pauses/x/collect", strconv.FormatInt(p.ID, 10), nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("collect returned pause: %d %s, want 409", w.Code, w.Body.String())
	}
	if after := pauseRowJSON(t, h, p.ID); after != before {
		t.Errorf("ended pause changed: before=%s after=%s", before, after)
	}
	var status string
	var end *time.Time
	var through time.Time
	if err := h.Pool.QueryRow(context.Background(), `SELECT status, end_date, paid_through FROM vehicles WHERE id = $1`, vid).Scan(&status, &end, &through); err != nil {
		t.Fatal(err)
	}
	if status != "stored" || end != nil || through.Format(dateLayout) != "2026-08-10" {
		t.Errorf("ended collect changed vehicle: status=%s end=%v paid_through=%s", status, end, through.Format(dateLayout))
	}
}

func TestPauseCollectFailurePreservesCompletePause(t *testing.T) {
	h := testHandler(t)
	_, vid := pauseFixture(t, h)
	p, code, msg := startPause(t, h, vid, `{"from":"2026-07-10","until":"2026-07-30","note":"Keep the complete original record"}`)
	if p == nil {
		t.Fatalf("start pause: %d %s", code, msg)
	}
	if _, err := h.Pool.Exec(context.Background(), `UPDATE vehicles SET archived = true WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	before := pauseRowJSON(t, h, p.ID)
	w := callJSON(h, h.CollectFromPause, http.MethodPost, "/api/vehicle-pauses/x/collect", strconv.FormatInt(p.ID, 10), nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("collect archived vehicle: %d %s, want 409", w.Code, w.Body.String())
	}
	if after := pauseRowJSON(t, h, p.ID); after != before {
		t.Errorf("failed collection changed pause: before=%s after=%s", before, after)
	}
	var history, deletedAudits int
	if err := h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM vehicle_status_history WHERE vehicle_id = $1 AND new_status = 'collected'`, vid).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if err := h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE entity = 'vehicle_pause' AND entity_id = $1 AND action = 'delete'`, p.ID).Scan(&deletedAudits); err != nil {
		t.Fatal(err)
	}
	if history != 0 || deletedAudits != 0 {
		t.Errorf("failed collection left history=%d deleted audits=%d", history, deletedAudits)
	}
}

func waitPauseRequestsLocked(t *testing.T, ctx context.Context, h *Handler, want int) {
	t.Helper()
	for {
		var waiting int
		if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("requests did not reach %d database locks", want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func pauseRequest(ctx context.Context, handler http.HandlerFunc, method string, id int64, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/vehicle-pauses/x", strings.NewReader(body)).WithContext(ctx)
	r.SetPathValue("id", strconv.FormatInt(id, 10))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func TestPauseConcurrentCollectDoesNotResurrectPause(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, vid := pauseFixture(t, h)
	p, code, msg := startPause(t, h, vid, `{"from":"2026-07-10"}`)
	if p == nil {
		t.Fatalf("start pause: %d %s", code, msg)
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
		go func() { results <- pauseRequest(ctx, h.CollectFromPause, http.MethodPost, p.ID, "") }()
	}
	waitPauseRequestsLocked(t, ctx, h, 2)
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	statuses := map[int]int{}
	for range 2 {
		select {
		case w := <-results:
			statuses[w.Code]++
		case <-ctx.Done():
			t.Fatal("collect requests did not finish")
		}
	}
	if statuses[http.StatusOK] != 1 || statuses[http.StatusNotFound]+statuses[http.StatusConflict] != 1 {
		t.Errorf("collect responses=%v, want one success and one missing/conflict", statuses)
	}
	var pauses, history int
	var status string
	var end time.Time
	if err := h.Pool.QueryRow(ctx, `SELECT status, end_date, (SELECT count(*) FROM vehicle_pauses WHERE vehicle_id = $1), (SELECT count(*) FROM vehicle_status_history WHERE vehicle_id = $1 AND new_status = 'collected') FROM vehicles WHERE id = $1`, vid).
		Scan(&status, &end, &pauses, &history); err != nil {
		t.Fatal(err)
	}
	if status != "collected" || end.Format(dateLayout) != "2026-07-10" || pauses != 0 || history != 1 {
		t.Errorf("collect result: status=%s end=%s pauses=%d history=%d", status, end.Format(dateLayout), pauses, history)
	}
}

func TestPauseConcurrentExtendCannotChangeRecordedReturn(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, vid := pauseFixture(t, h)
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET paid = true, paid_through = '2026-07-31' WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	p, code, msg := startPause(t, h, vid, `{"from":"2026-07-10"}`)
	if p == nil {
		t.Fatalf("start pause: %d %s", code, msg)
	}
	blocker, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `SELECT id FROM vehicles WHERE id = $1 FOR UPDATE`, vid); err != nil {
		t.Fatal(err)
	}
	back := make(chan *httptest.ResponseRecorder, 1)
	extend := make(chan *httptest.ResponseRecorder, 1)
	go func() { back <- pauseRequest(ctx, h.ReturnFromPause, http.MethodPost, p.ID, `{"on":"2026-07-20"}`) }()
	waitPauseRequestsLocked(t, ctx, h, 1)
	go func() { extend <- pauseRequest(ctx, h.ExtendPause, http.MethodPut, p.ID, `{"until":"2026-08-31"}`) }()
	waitPauseRequestsLocked(t, ctx, h, 2)
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for _, result := range []struct {
		name string
		ch   <-chan *httptest.ResponseRecorder
		want int
	}{{"return", back, http.StatusOK}, {"extend", extend, http.StatusConflict}} {
		select {
		case w := <-result.ch:
			if w.Code != result.want {
				t.Errorf("%s: %d %s, want %d", result.name, w.Code, w.Body.String(), result.want)
			}
		case <-ctx.Done():
			t.Fatal("pause requests did not finish")
		}
	}
	var ended bool
	var until, through time.Time
	if err := h.Pool.QueryRow(ctx, `SELECT p.ended, p.until, v.paid_through FROM vehicle_pauses p JOIN vehicles v ON v.id = p.vehicle_id WHERE p.id = $1`, p.ID).Scan(&ended, &until, &through); err != nil {
		t.Fatal(err)
	}
	if !ended || until.Format(dateLayout) != "2026-07-19" || through.Format(dateLayout) != "2026-08-10" {
		t.Errorf("return changed: ended=%v until=%s paid_through=%s", ended, until.Format(dateLayout), through.Format(dateLayout))
	}
}

func TestPauseDeletePreservesLaterPaidThroughChanges(t *testing.T) {
	for _, change := range []string{"later coverage", "paid toggle cleared"} {
		t.Run(change, func(t *testing.T) {
			h := testHandler(t)
			vid, p := paidReturnedPause(t, h)
			if change == "later coverage" {
				if _, err := h.Pool.Exec(context.Background(), `UPDATE vehicles SET paid_through = '2026-09-30' WHERE id = $1`, vid); err != nil {
					t.Fatal(err)
				}
			} else {
				w := callJSONRaw(h, h.MarkPaid, http.MethodPut, "/api/vehicles/x/paid", strconv.FormatInt(vid, 10), `{"paid":false}`)
				if w.Code != http.StatusOK {
					t.Fatalf("clear paid toggle: %d %s", w.Code, w.Body.String())
				}
			}
			before := pauseRowJSON(t, h, p.ID)
			w := callJSON(h, h.DeletePause, http.MethodDelete, "/api/vehicle-pauses/x", strconv.FormatInt(p.ID, 10), nil)
			if w.Code != http.StatusConflict {
				t.Fatalf("delete after %s: %d %s, want 409", change, w.Code, w.Body.String())
			}
			if after := pauseRowJSON(t, h, p.ID); after != before {
				t.Errorf("rejected delete changed pause: before=%s after=%s", before, after)
			}
			var through *time.Time
			if err := h.Pool.QueryRow(context.Background(), `SELECT paid_through FROM vehicles WHERE id = $1`, vid).Scan(&through); err != nil {
				t.Fatal(err)
			}
			if change == "later coverage" && (through == nil || through.Format(dateLayout) != "2026-09-30") {
				t.Errorf("later coverage overwritten: %v", through)
			}
			if change == "paid toggle cleared" && through != nil {
				t.Errorf("cleared coverage resurrected: %v", through)
			}
		})
	}
}
