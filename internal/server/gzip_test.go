package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGzipStatic(t *testing.T) {
	jsBody := "console.log('a reasonably long javascript payload to compress');"
	js := gzipStatic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write([]byte(jsBody))
	}))

	// compressible + client accepts gzip → encoded
	r := httptest.NewRequest("GET", "/js/app.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	js.ServeHTTP(w, r)
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected Content-Encoding gzip, got %q", w.Header().Get("Content-Encoding"))
	}
	if !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("compressed response must Vary on Accept-Encoding, got %q", w.Header().Get("Vary"))
	}
	gr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, _ := io.ReadAll(gr)
	if string(got) != jsBody {
		t.Fatalf("round-trip mismatch: %q", got)
	}

	// no Accept-Encoding → untouched
	w = httptest.NewRecorder()
	js.ServeHTTP(w, httptest.NewRequest("GET", "/js/app.js", nil))
	if w.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("must not gzip without Accept-Encoding")
	}
	if !strings.Contains(w.Body.String(), "javascript payload") {
		t.Fatal("passthrough body corrupted")
	}

	// "gzip;q=0" explicitly refuses gzip → must not compress, but still Vary.
	r = httptest.NewRequest("GET", "/js/app.js", nil)
	r.Header.Set("Accept-Encoding", "gzip;q=0")
	w = httptest.NewRecorder()
	js.ServeHTTP(w, r)
	if w.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("gzip;q=0 must not be compressed")
	}
	if !strings.Contains(w.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatal("uncompressed gzippable response must still Vary on Accept-Encoding")
	}

	// non-compressible type (woff2) → never gzipped
	font := gzipStatic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "font/woff2")
		_, _ = w.Write([]byte("BINARYFONTDATA"))
	}))
	r = httptest.NewRequest("GET", "/fonts/x.woff2", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w = httptest.NewRecorder()
	font.ServeHTTP(w, r)
	if w.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("woff2 must not be gzipped")
	}
}

func TestAcceptsGzip(t *testing.T) {
	cases := map[string]bool{
		"gzip":                true,
		"gzip, deflate, br":   true,
		"deflate, gzip;q=0.8": true,
		"gzip;q=0":            false,
		"gzip;q=0.0":          false,
		"identity":            false,
		"":                    false,
		"*":                   true,
		"deflate, *;q=0":      false,
		"br, gzip ; q=0 , *":  false, // gzip explicitly q=0 wins over the trailing *
	}
	for h, want := range cases {
		if got := acceptsGzip(h); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", h, got, want)
		}
	}
}
