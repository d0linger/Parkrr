package auth

import (
	"context"
	"crypto/sha1" // #nosec G505 -- test mirrors the HIBP protocol hashing.
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hibpStub serves a canned range response for the prefix of the given password.
func hibpStub(t *testing.T, password string, count int) *httptest.Server {
	t.Helper()
	sum := sha1.Sum([]byte(password))
	full := strings.ToUpper(hex.EncodeToString(sum[:]))
	suffix := full[5:]
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a couple of decoys plus the real suffix (when count > 0).
		fmt.Fprintf(w, "00000000000000000000000000000000000:3\r\n")
		if count > 0 {
			fmt.Fprintf(w, "%s:%d\r\n", suffix, count)
		}
	}))
}

func withEndpoint(url string, fn func()) {
	orig := hibpEndpoint
	hibpEndpoint = url + "/"
	defer func() { hibpEndpoint = orig }()
	fn()
}

func TestBreachedPasswordFound(t *testing.T) {
	srv := hibpStub(t, "password123", 4242)
	defer srv.Close()
	withEndpoint(srv.URL, func() {
		n, err := BreachedPasswordCount(context.Background(), srv.Client(), "password123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 4242 {
			t.Fatalf("count: got %d want 4242", n)
		}
	})
}

func TestBreachedPasswordNotFound(t *testing.T) {
	srv := hibpStub(t, "unique-strong-passphrase", 0)
	defer srv.Close()
	withEndpoint(srv.URL, func() {
		n, err := BreachedPasswordCount(context.Background(), srv.Client(), "unique-strong-passphrase")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 0 {
			t.Fatalf("count: got %d want 0", n)
		}
	})
}

func TestBreachedPasswordServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	withEndpoint(srv.URL, func() {
		if _, err := BreachedPasswordCount(context.Background(), srv.Client(), "x"); err == nil {
			t.Fatal("expected error on 500 so caller can fail open")
		}
	})
}
