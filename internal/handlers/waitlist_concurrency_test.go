package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestWaitlistConcurrentConversionCreatesOnePerson(t *testing.T) {
	h := testHandler(t)
	if h.Pool.Config().MaxConns < 4 {
		t.Skip("concurrent conversion regression requires four database connections")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	email := fmt.Sprintf("waitlist-%d@example.com", time.Now().UnixNano())
	var id int64
	if err := h.Pool.QueryRow(ctx, `INSERT INTO waitlist (name,email,consent)
		VALUES ('Concurrent Integration',$1,true) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := h.Pool.Exec(context.Background(), `DELETE FROM waitlist WHERE id=$1`, id); err != nil {
			t.Errorf("cleanup waitlist: %v", err)
		}
	})
	lock, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(ctx, `SELECT id FROM waitlist WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			req := httptest.NewRequest(http.MethodPost, "/api/waitlist/"+strconv.FormatInt(id, 10)+"/convert", nil).WithContext(ctx)
			req.SetPathValue("id", strconv.FormatInt(id, 10))
			w := httptest.NewRecorder()
			h.ConvertWaitlist(w, req)
			results <- w
		}()
	}
	// Holding the row makes both requests overlap deterministically. Without the
	// conversion lock both insert a person before their final UPDATE blocks here.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting int
		if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%waitlist%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("both conversions did not reach the held waitlist row")
		case <-ticker.C:
		}
	}
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	statuses := map[int]int{}
	for range 2 {
		select {
		case w := <-results:
			statuses[w.Code]++
			if w.Code != http.StatusOK && w.Code != http.StatusConflict {
				t.Errorf("convert: %d %s", w.Code, w.Body.String())
			}
		case <-ctx.Done():
			t.Fatal("conversions did not finish")
		}
	}
	if statuses[http.StatusOK] != 1 || statuses[http.StatusConflict] != 1 {
		t.Errorf("concurrent responses=%v, want one 200 and one 409", statuses)
	}
	var persons int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM persons WHERE email=$1`, email).Scan(&persons); err != nil {
		t.Fatal(err)
	}
	if persons != 1 {
		t.Errorf("created %d persons for one waitlist entry, want 1", persons)
	}
	var linked bool
	if err := h.Pool.QueryRow(ctx, `SELECT status='uebernommen' AND person_id IS NOT NULL FROM waitlist WHERE id=$1`, id).Scan(&linked); err != nil || !linked {
		t.Errorf("conversion link missing: linked=%v err=%v", linked, err)
	}
}
