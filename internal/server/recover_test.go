package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRecoverPanics verifies a panicking handler yields a logged 500 rather than
// a dropped connection (and does not crash the test).
func TestRecoverPanics(t *testing.T) {
	h := recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panicking handler: want 500, got %d", rec.Code)
	}
}
