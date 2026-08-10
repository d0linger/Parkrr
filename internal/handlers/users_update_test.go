package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUpdateUserMissingReturns404: editing a user id that doesn't exist must 404,
// not report a phantom success (the UPDATE would touch 0 rows yet return 200).
func TestUpdateUserMissingReturns404(t *testing.T) {
	h := testHandler(t)
	body, _ := json.Marshal(userRequest{Username: "ghost", Email: "ghost@example.com", Role: "admin"})
	req := httptest.NewRequest(http.MethodPut, "/api/users/999999", bytes.NewReader(body))
	req.SetPathValue("id", "999999")
	rec := httptest.NewRecorder()
	h.UpdateUser(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a nonexistent user, got %d %s", rec.Code, rec.Body.String())
	}
}
