package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// Der Verlauf (Hundert 56) sammelt die Ereignisse einer Person aus den
// Domänentabellen in einen absteigend sortierten Strom — und NUR ihre.
func TestPersonTimelineAggregatesOwnEvents(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, _ := mkSignedHandover(t, h) // Person + Gefährt + Übergabeprotokoll
	other, _ := mkSignedHandover(t, h)

	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO payments (person_id, amount, method) VALUES ($1, 55.00, 'bar')`, pid); err != nil {
		t.Fatalf("payment: %v", err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO charges (person_id, description, amount) VALUES ($1, 'Timeline-Posten', 12.00)`, pid); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO invoices (number, person_id, subtotal, total) VALUES ('TL-2026-0001', $1, 12, 12)`, pid); err != nil {
		t.Fatalf("invoice: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_ = purgeExec(c, h.Pool, `DELETE FROM invoices WHERE number='TL-2026-0001'`)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/persons/"+strconv.FormatInt(pid, 10)+"/timeline", nil)
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	rec := httptest.NewRecorder()
	h.PersonTimeline(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("timeline: %d %s", rec.Code, rec.Body.String())
	}
	var events []timelineEvent
	if err := json.Unmarshal(rec.Body.Bytes(), &events); err != nil {
		t.Fatalf("decode: %v", err)
	}
	kinds := map[string]int{}
	for _, e := range events {
		kinds[e.Kind]++
	}
	for _, want := range []string{"payment", "charge", "invoice", "handover"} {
		if kinds[want] == 0 {
			t.Errorf("Ereignisart %q fehlt im Verlauf: %+v", want, kinds)
		}
	}
	// Absteigend sortiert.
	for i := 1; i < len(events); i++ {
		if events[i].At.After(events[i-1].At) {
			t.Errorf("Verlauf nicht absteigend sortiert an Position %d", i)
			break
		}
	}

	// Die zweite Person sieht NUR ihr eigenes Übergabeprotokoll, nichts vom ersten.
	oreq := httptest.NewRequest(http.MethodGet, "/api/persons/"+strconv.FormatInt(other, 10)+"/timeline", nil)
	oreq.SetPathValue("id", strconv.FormatInt(other, 10))
	orec := httptest.NewRecorder()
	h.PersonTimeline(orec, oreq)
	var oevents []timelineEvent
	_ = json.Unmarshal(orec.Body.Bytes(), &oevents)
	for _, e := range oevents {
		if e.Kind == "payment" || e.Kind == "invoice" || e.Kind == "charge" {
			t.Errorf("fremdes Ereignis im Verlauf der zweiten Person: %+v", e)
		}
	}
}
