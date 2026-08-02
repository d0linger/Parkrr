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
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("want Content-Type application/json, got %q", ct)
	}
	if body := rec.Body.String(); body != `{"error":"internal server error"}` {
		t.Errorf("unexpected recovery body: %q", body)
	}
}

// TestRecoverPanicsAfterResponseStarted verifies that a panic AFTER the handler
// has begun streaming does not append a second (JSON) response or re-send headers.
func TestRecoverPanicsAfterResponseStarted(t *testing.T) {
	h := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("boom mid-stream")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("started response: want 200 preserved, got %d", rec.Code)
	}
	if body := rec.Body.String(); body != "partial" {
		t.Errorf("recovery must not append to a started response, got %q", body)
	}
}
