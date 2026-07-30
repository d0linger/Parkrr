package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/preining/parkrr/internal/auth"
)

// TestBuildChainEnforcesBodyLimit checks the wiring, not just the middleware in
// isolation: a request routed through the full chain New assembles is still body-
// limited (so the test fails if the body-limit stops being wired into the chain),
// while a normal request reaches the handler.
func TestBuildChainEnforcesBodyLimit(t *testing.T) {
	mux := http.NewServeMux()
	var reached bool
	mux.HandleFunc("POST /x", func(w http.ResponseWriter, r *http.Request) {
		reached = true
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	})
	stop := make(chan struct{})
	defer close(stop)
	// A zero Manager is enough: the chain only calls pool-free methods
	// (RequestIsHTTPS / ClientIP) before the body limit and handler.
	h := buildChain(&auth.Manager{}, mux, 100000, stop)

	// Oversized declared body → 413, and the handler must not run.
	reached = false
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(bytes.Repeat([]byte("a"), maxRequestBody+1)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || reached {
		t.Fatalf("oversized: want 413 without reaching handler, got %d (reached=%v)", rec.Code, reached)
	}

	// A normal request flows through and reaches the handler.
	reached = false
	req = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader([]byte("ok")))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !reached {
		t.Fatalf("normal: want 200 reaching handler, got %d (reached=%v)", rec.Code, reached)
	}
}

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

		// Unknown-length body (ContentLength = -1): the upfront check can't fire, so
		// the handler runs and it's the capped read (MaxBytesReader) that rejects it.
		reached = false
		req = httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(bytes.Repeat([]byte("a"), maxRequestBody+1)))
		req.ContentLength = -1
		req.Header.Set("Content-Type", ct)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge || !reached {
			t.Fatalf("%s: unknown-length max+1: want handler-read 413, got %d (reached=%v)", ct, rec.Code, reached)
		}
	}
}
