package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestChargeQuantityPresenceSemantics(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	post := func(quantity any, include bool) *httptest.ResponseRecorder {
		body := map[string]any{"person_id": pid, "description": "Presence", "amount": 10}
		if include {
			body["quantity"] = quantity
		}
		encoded, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		h.CreateCharge(rec, httptest.NewRequest(http.MethodPost, "/api/charges", bytes.NewReader(encoded)))
		return rec
	}
	omitted := post(nil, false)
	if omitted.Code != http.StatusCreated {
		t.Fatalf("omitted quantity should default to 1: %d %s", omitted.Code, omitted.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(omitted.Body.Bytes(), &created)
	var quantity float64
	if err := h.Pool.QueryRow(t.Context(), `SELECT quantity FROM charges WHERE id=$1`, created.ID).Scan(&quantity); err != nil {
		t.Fatal(err)
	}
	if quantity != 1 {
		t.Fatalf("omitted quantity = %.2f, want 1", quantity)
	}
	for _, quantity := range []float64{0, -1} {
		if rec := post(quantity, true); rec.Code != http.StatusBadRequest {
			t.Errorf("explicit quantity %.2f: got %d %s, want 400", quantity, rec.Code, rec.Body.String())
		}
	}
}

func TestSettledChargeCannotBeMutatedOrDeleted(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	chargeID := mkChargeP(t, h, pid, "settled immutable", 25, "2026-06-10")
	payment := postPayment(t, h, pid, map[string]any{
		"amount": 25, "method": "bar",
		"allocations": []map[string]any{{"kind": "charge", "id": chargeID}},
	})
	if payment.Code != http.StatusCreated {
		t.Fatalf("settle charge: %d %s", payment.Code, payment.Body.String())
	}

	updateBody, _ := json.Marshal(map[string]any{
		"person_id": pid, "description": "changed", "amount": 99, "quantity": 1, "charged_on": "2026-06-10",
	})
	updateReq := httptest.NewRequest(http.MethodPut, "/api/charges/"+strconv.FormatInt(chargeID, 10), bytes.NewReader(updateBody))
	updateReq.SetPathValue("id", strconv.FormatInt(chargeID, 10))
	updateRec := httptest.NewRecorder()
	h.UpdateCharge(updateRec, updateReq)
	if updateRec.Code != http.StatusConflict {
		t.Fatalf("update settled charge: got %d %s, want 409", updateRec.Code, updateRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/charges/"+strconv.FormatInt(chargeID, 10), nil)
	deleteReq.SetPathValue("id", strconv.FormatInt(chargeID, 10))
	deleteRec := httptest.NewRecorder()
	h.DeleteCharge(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusConflict {
		t.Fatalf("delete settled charge: got %d %s, want 409", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestFinancialVehicleCannotTransferOwnerOrBeDeleted(t *testing.T) {
	h := testHandler(t)
	owner := createIntegrationPerson(t, h)
	newOwner := createIntegrationPerson(t, h)
	vehicleID := mkStoredVehicle(t, h, owner, 30, "2026-01-01")
	var paymentID int64
	if err := h.Pool.QueryRow(t.Context(),
		`INSERT INTO payments (person_id,amount,paid_on,method,note) VALUES ($1,30,CURRENT_DATE,'bar','vehicle lock') RETURNING id`,
		owner).Scan(&paymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(t.Context(),
		`INSERT INTO payment_allocations (payment_id,kind,ref_id,amount) VALUES ($1,'vehicle',$2,30)`,
		paymentID, vehicleID); err != nil {
		t.Fatal(err)
	}

	if rec := updateVehicleFields(t, h, owner, vehicleID, map[string]any{"person_id": newOwner}); rec.Code != http.StatusConflict {
		t.Fatalf("transfer financially referenced vehicle: got %d %s, want 409", rec.Code, rec.Body.String())
	}
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/vehicles/"+strconv.FormatInt(vehicleID, 10), nil)
	deleteReq.SetPathValue("id", strconv.FormatInt(vehicleID, 10))
	deleteRec := httptest.NewRecorder()
	h.DeleteVehicle(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusConflict {
		t.Fatalf("delete financially referenced vehicle: got %d %s, want 409", deleteRec.Code, deleteRec.Body.String())
	}
}
