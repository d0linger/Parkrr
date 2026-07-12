package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// When behind a trusted proxy, ClientIP must use the RIGHTMOST X-Forwarded-For
// entry (the one the proxy appended). Trusting the leftmost, client-supplied
// entry would let an attacker forge it to rotate the rate-limit key and evade
// the login lockout.
func TestClientIP_ForwardedForUsesRightmostHop(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.1:5555" // the proxy's own socket address
	// Attacker forges spoofed hops on the left; the real client IP is the
	// rightmost entry, added by our trusted proxy.
	req.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2, 203.0.113.7")

	proxied := &Manager{trustProxy: true}
	if got := proxied.ClientIP(req); got != "203.0.113.7" {
		t.Errorf("trusted proxy: got %q, want the rightmost hop 203.0.113.7", got)
	}

	// Without trustProxy, the header is ignored entirely and the socket peer
	// address is authoritative.
	direct := &Manager{trustProxy: false}
	if got := direct.ClientIP(req); got != "10.0.0.1" {
		t.Errorf("untrusted: got %q, want socket peer 10.0.0.1 (header ignored)", got)
	}
}
