package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestDeleteParentsPreservesHandover(t *testing.T) {
	h := testHandler(t)
	pid, hid := mkSignedHandover(t, h)
	var vid int64
	if err := h.Pool.QueryRow(t.Context(),
		`SELECT vehicle_id FROM handover_protocols WHERE id=$1`, hid).Scan(&vid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		id      int64
		handler http.HandlerFunc
	}{
		{name: "vehicle", id: vid, handler: h.DeleteVehicle},
		{name: "person", id: pid, handler: h.DeletePerson},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodDelete, "/", nil)
			req.SetPathValue("id", strconv.FormatInt(tc.id, 10))
			rec := httptest.NewRecorder()
			tc.handler(rec, req)
			if rec.Code != http.StatusConflict {
				t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
			}
			var remains bool
			if err := h.Pool.QueryRow(t.Context(),
				`SELECT EXISTS(SELECT 1 FROM handover_protocols WHERE id=$1)`, hid).Scan(&remains); err != nil {
				t.Fatal(err)
			}
			if !remains {
				t.Fatal("parent deletion removed protected handover")
			}
		})
	}
}
