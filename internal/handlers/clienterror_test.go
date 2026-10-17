package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientError(t *testing.T) {
	h := &Handler{}
	call := func(body string) int {
		r := httptest.NewRequest("POST", "/api/client-error", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ClientError(w, r)
		return w.Code
	}
	if got := call(`{"message":"boom","stack":"x","url":"/plan","version":"v180"}`); got != http.StatusNoContent {
		t.Errorf("valid body: got %d, want 204", got)
	}
	if got := call(`{"message":""}`); got != http.StatusBadRequest {
		t.Errorf("empty message: got %d, want 400", got)
	}
	if got := call(`not json`); got != http.StatusBadRequest {
		t.Errorf("garbage: got %d, want 400", got)
	}
	// an over-limit MESSAGE (but sub-8KiB body) is truncated, not rejected (still 204).
	if got := call(`{"message":"` + strings.Repeat("A", 5000) + `"}`); got != http.StatusNoContent {
		t.Errorf("long message: got %d, want 204", got)
	}
	// a request BODY exceeding the 8 KiB MaxBytesReader is rejected with 400.
	if got := call(`{"message":"` + strings.Repeat("A", 9000) + `"}`); got != http.StatusBadRequest {
		t.Errorf("oversized body: got %d, want 400", got)
	}
	// unknown fields in body should be rejected with 400 via decodeJSON.
	if got := call(`{"message":"boom","unknown_field":"value"}`); got != http.StatusBadRequest {
		t.Errorf("unknown field in body: got %d, want 400", got)
	}

	// invalid Content-Type should be rejected with 400 via decodeJSON.
	r := httptest.NewRequest("POST", "/api/client-error", strings.NewReader(`{"message":"boom"}`))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ClientError(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid content-type: got %d, want 400", w.Code)
	}
}
