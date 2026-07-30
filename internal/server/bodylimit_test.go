package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLimitRequestBody covers the boundary of the global request-body cap: a body
// of exactly maxRequestBody reaches the handler and is fully readable, while one
// byte more is rejected with 413 before the handler runs. Checked for both a JSON
// and a form content type — the cap bounds raw bytes and is content-type agnostic.
func TestLimitRequestBody(t *testing.T) {
	var reached bool
	var readN int
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusRequestEntityTooLarge)
			return
		}
		readN = int(n)
		w.WriteHeader(http.StatusOK)
	})
	h := limitRequestBody(stub)

	for _, ct := range []string{"application/json", "application/x-www-form-urlencoded"} {
		// Exactly at the limit: passes through and the full body is readable.
		reached, readN = false, 0
		req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(bytes.Repeat([]byte("a"), maxRequestBody)))
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: exactly max: want 200, got %d", ct, rec.Code)
		}
		if !reached || readN != maxRequestBody {
			t.Fatalf("%s: exactly max: handler read %d bytes (reached=%v), want %d", ct, readN, reached, maxRequestBody)
		}

		// One byte over: rejected with 413 before the handler runs.
		reached = false
		req = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(bytes.Repeat([]byte("a"), maxRequestBody+1)))
		req.Header.Set("Content-Type", ct)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s: max+1: want 413, got %d", ct, rec.Code)
		}
		if reached {
			t.Fatalf("%s: max+1: handler should not have run", ct)
		}
	}
}
