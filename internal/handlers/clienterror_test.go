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
}
