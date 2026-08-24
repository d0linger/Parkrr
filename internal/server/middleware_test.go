package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStatusRecorderUnwrap verifies that http.ResponseController reaches the
// wrapped writer's optional interfaces (Flush, SetWriteDeadline, ...) through
// the access-log wrapper — without Unwrap, clearWriteDeadline silently fails.
func TestStatusRecorderUnwrap(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: inner, status: http.StatusOK}
	if err := http.NewResponseController(rec).Flush(); err != nil {
		t.Fatalf("Flush through statusRecorder: %v", err)
	}
	if !inner.Flushed {
		t.Error("inner recorder should report flushed")
	}
}

// TestRedactPath: portal tokens are bearer-equivalent and live in the URL path;
// the access logger must never emit the raw token (finding SEC-01). Non-portal
// paths pass through unchanged.
func TestRedactPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/api/portal/abc123SECRET/summary", "/api/portal/REDACTED/summary"},
		{"/api/portal/abc123SECRET/invoices/7/pdf", "/api/portal/REDACTED/invoices/7/pdf"},
		{"/api/portal/abc123SECRET/invoices/7/pay-qr", "/api/portal/REDACTED/invoices/7/pay-qr"},
		{"/api/portal/abc123SECRET", "/api/portal/REDACTED"},
		{"/api/persons/5", "/api/persons/5"},
		{"/api/portal-links/9/revoke", "/api/portal-links/9/revoke"}, // not the /api/portal/ prefix
		{"/", "/"},
	}
	for _, c := range cases {
		if got := redactPath(c.in); got != c.want {
			t.Errorf("redactPath(%q) = %q, want %q", c.in, got, c.want)
		}
		if got := redactPath(c.in); c.in != c.want && got == c.in {
			t.Errorf("redactPath(%q) leaked the token unchanged", c.in)
		}
	}
}
