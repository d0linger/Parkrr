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
	// The proxy's own peer address must be in the trusted CIDR allowlist for its
	// forwarded headers to be honored at all (fail-closed; finding H-06).
	if err := proxied.SetTrustedProxyCIDRs([]string{"10.0.0.0/8"}); err != nil {
		t.Fatalf("SetTrustedProxyCIDRs: %v", err)
	}
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

// A proxy may append its hop as a SEPARATE X-Forwarded-For header line rather
// than comma-appending. All lines must be joined before taking the rightmost
// entry, otherwise the client-supplied first line wins.
func TestClientIP_MultipleForwardedForFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	// Attacker sends their own XFF line; the trusted proxy adds a second,
	// separate line carrying the real client address.
	req.Header.Add("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
	req.Header.Add("X-Forwarded-For", "203.0.113.7")

	m := &Manager{trustProxy: true}
	if err := m.SetTrustedProxyCIDRs([]string{"10.0.0.0/8"}); err != nil {
		t.Fatalf("SetTrustedProxyCIDRs: %v", err)
	}
	if got := m.ClientIP(req); got != "203.0.113.7" {
		t.Errorf("multi-field XFF: got %q, want rightmost hop 203.0.113.7", got)
	}
}

// H-06: with trusted-proxy ON but NO CIDR allowlist, no direct peer is trusted, so
// forwarded headers are ignored and the socket peer is authoritative. (Startup also
// refuses this combination; this guards the ClientIP path directly.)
func TestClientIP_TrustProxyWithoutCIDRsFailsClosed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")

	m := &Manager{trustProxy: true} // no SetTrustedProxyCIDRs -> trust nobody
	if got := m.ClientIP(req); got != "10.0.0.1" {
		t.Errorf("no CIDRs: got %q, want socket peer 10.0.0.1 (XFF must be ignored)", got)
	}
}

// With a trusted-proxy CIDR allowlist configured, forwarded headers are honored
// only when the DIRECT peer (RemoteAddr) is inside the allowlist. A client that
// reaches the backend directly (outside the allowlist) cannot spoof its IP via
// X-Forwarded-For (finding SH-05).
func TestClientIP_ForwardedForRequiresTrustedPeerCIDR(t *testing.T) {
	newReq := func(remote string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", "203.0.113.7")
		return r
	}
	m := &Manager{trustProxy: true}
	if err := m.SetTrustedProxyCIDRs([]string{"10.0.0.0/8"}); err != nil {
		t.Fatalf("SetTrustedProxyCIDRs: %v", err)
	}
	// Peer inside the allowlist -> XFF honored.
	if got := m.ClientIP(newReq("10.0.0.1:5555")); got != "203.0.113.7" {
		t.Errorf("trusted peer: got %q, want forwarded 203.0.113.7", got)
	}
	// Peer outside the allowlist -> XFF ignored, socket peer wins.
	if got := m.ClientIP(newReq("198.51.100.9:5555")); got != "198.51.100.9" {
		t.Errorf("untrusted peer: got %q, want socket peer 198.51.100.9 (XFF ignored)", got)
	}
	// An invalid CIDR is a configuration error (fail-closed at startup).
	if err := m.SetTrustedProxyCIDRs([]string{"not-a-cidr"}); err == nil {
		t.Error("expected an error for an invalid CIDR")
	}
}
