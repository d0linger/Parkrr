package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// TestAgreementVehicleCapCountsResult: maxAgreementVehicles gilt für die fertige
// Pauschale, gebundene und neu angelegte Gefährte zusammen. Was angelegt werden darf,
// bleibt so bearbeitbar; eine vor der Grenze größer gespeicherte Pauschale behält
// beim Bearbeiten ihren Umfang, wächst aber nicht weiter.
func TestAgreementVehicleCapCountsResult(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid := createIntegrationPerson(t, h)
	start := firstOfMonthMonthsAgo(1).Format("2006-01-02")
	extra := mkStoredVehicle(t, h, pid, 30, start)
	var cat int64
	if err := h.Pool.QueryRow(ctx, `SELECT category_id FROM vehicles WHERE id=$1`, extra).Scan(&cat); err != nil {
		t.Fatal(err)
	}
	news := func(n int) []newVehicleReq {
		out := make([]newVehicleReq, n)
		for i := range out {
			out[i] = newVehicleReq{CategoryID: cat, Label: "Cap " + strconv.Itoa(i)}
		}
		return out
	}
	send := func(id int64, ids []int64, nv []newVehicleReq) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(agreementRequest{Amount: floatPtr(50), Period: "monthly", StartDate: start, VehicleIDs: ids, NewVehicles: nv})
		rec := httptest.NewRecorder()
		if id == 0 {
			req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/agreements", bytes.NewReader(body))
			req.SetPathValue("id", strconv.FormatInt(pid, 10))
			h.CreateAgreement(rec, req)
		} else {
			req := httptest.NewRequest(http.MethodPut, "/api/agreements/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
			req.SetPathValue("id", strconv.FormatInt(id, 10))
			h.UpdateAgreement(rec, req)
		}
		return rec
	}
	expectRejected := func(step string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte("too many vehicles specified")) {
			t.Fatalf("%s: want 400 too many vehicles, got %d %s", step, rec.Code, rec.Body.String())
		}
	}

	// Eins über der Grenze, verteilt auf gebundene und neue Gefährte.
	expectRejected("create 1+100", send(0, []int64{extra}, news(maxAgreementVehicles)))

	rec := send(0, nil, news(maxAgreementVehicles))
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create 0+100: %d %s", rec.Code, rec.Body.String())
	}
	var list []struct {
		ID         int64   `json:"id"`
		VehicleIDs []int64 `json:"vehicle_ids"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 || len(list[0].VehicleIDs) != maxAgreementVehicles {
		t.Fatalf("want one agreement with %d vehicles, got %s (err %v)", maxAgreementVehicles, rec.Body.String(), err)
	}
	aid, ids := list[0].ID, list[0].VehicleIDs

	if rec := send(aid, ids, nil); rec.Code != http.StatusOK {
		t.Fatalf("edit at the cap: %d %s", rec.Code, rec.Body.String())
	}
	expectRejected("edit 100+1", send(aid, ids, news(1)))

	// Eine vor der Grenze gespeicherte, größere Pauschale.
	if _, err := h.Pool.Exec(ctx, `INSERT INTO flat_rate_period_vehicles (period_id, vehicle_id) VALUES ($1,$2)`, aid, extra); err != nil {
		t.Fatal(err)
	}
	legacy := append(append([]int64{}, ids...), extra)
	if rec := send(aid, legacy, nil); rec.Code != http.StatusOK {
		t.Fatalf("edit of a larger legacy agreement must keep working: %d %s", rec.Code, rec.Body.String())
	}
	expectRejected("legacy edit 101+1", send(aid, legacy, news(1)))
}
