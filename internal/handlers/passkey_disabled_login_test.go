package handlers

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
)

// softPasskey is a software authenticator for one P-256 credential: it stores its
// public key the way a registration would and signs real assertions, so a test can
// reach the code that runs AFTER the cryptographic check.
type softPasskey struct {
	key *ecdsa.PrivateKey
	id  []byte
}

func newSoftPasskey(t *testing.T, name string) *softPasskey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &softPasskey{key: key, id: []byte("soft-credential-" + name)}
}

// cose encodes the public key as a COSE_Key (EC2, ES256, P-256) in CBOR.
func (p *softPasskey) cose(t *testing.T) []byte {
	t.Helper()
	pub, err := p.key.PublicKey.ECDH()
	if err != nil {
		t.Fatal(err)
	}
	raw := pub.Bytes() // 0x04 || X || Y
	out := []byte{0xA5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21, 0x58, 0x20}
	out = append(out, raw[1:33]...)
	out = append(out, 0x22, 0x58, 0x20)
	return append(out, raw[33:65]...)
}

func (p *softPasskey) register(t *testing.T, f *authFixture, uid int64) {
	t.Helper()
	if _, err := f.h.Pool.Exec(context.Background(),
		`INSERT INTO webauthn_credentials (user_id, credential_id, public_key, attestation_type, sign_count)
		 VALUES ($1, $2, $3, 'none', 0)`, uid, p.id, p.cose(t)); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
}

// login runs begin + finish with a correctly signed assertion and returns the
// finish status.
func (p *softPasskey) login(t *testing.T, f *authFixture, uid int64) int {
	t.Helper()
	w := httptest.NewRecorder()
	f.ah.PasskeyLoginBegin(w, httptest.NewRequest(http.MethodPost, "/api/auth/passkey/login/begin", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("begin: %d %s", w.Code, w.Body.String())
	}
	var opts struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &opts); err != nil {
		t.Fatal(err)
	}
	var cer *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == waCookie {
			cer = c
		}
	}
	if cer == nil {
		t.Fatal("no ceremony cookie")
	}

	clientData, _ := json.Marshal(map[string]string{
		"type": "webauthn.get", "challenge": opts.PublicKey.Challenge, "origin": "https://example.com",
	})
	rpHash := sha256.Sum256([]byte("example.com"))
	authData := append(rpHash[:], 0x05, 0, 0, 0, 1) // UP|UV, counter 1
	cdHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), cdHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, p.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	handle := make([]byte, 8)
	binary.BigEndian.PutUint64(handle, uint64(uid))
	b64 := base64.RawURLEncoding.EncodeToString
	body, _ := json.Marshal(map[string]any{
		"id": b64(p.id), "rawId": b64(p.id), "type": "public-key",
		"response": map[string]string{
			"clientDataJSON":    b64(clientData),
			"authenticatorData": b64(authData),
			"signature":         b64(sig),
			"userHandle":        b64(handle),
		},
	})
	fin := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/login/finish", bytes.NewReader(body))
	fin.Header.Set("Content-Type", "application/json")
	fin.AddCookie(cer)
	w = httptest.NewRecorder()
	f.ah.PasskeyLoginFinish(w, fin)
	return w.Code
}

// A passkey login on a disabled account is a failed login, exactly like the
// password path (Authenticate rejects disabled accounts as bad credentials): 401,
// and the throttle attempt stays spent. Refunding it let a valid key for a
// disabled account retry without limit (PR 212).
func TestPasskeyLoginDisabledAccountKeepsThrottle(t *testing.T) {
	f := newAuthFixture(t)
	wa, err := auth.NewWebAuthnService(f.h.Pool, "example.com", "Example", []string{"https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	f.ah.WebAuthn = wa
	t.Cleanup(func() { _, _ = f.h.Pool.Exec(context.Background(), `DELETE FROM webauthn_ceremonies`) })

	// Control: the same software authenticator logs an ENABLED account in. Without
	// it, the 401 below could come from a broken signature and prove nothing.
	on := f.createUser(t, "passkey-enabled", "control-password-1")
	pOn := newSoftPasskey(t, "enabled")
	pOn.register(t, f, on)
	f.ah.Limiter = auth.NewLoginLimiter(1, time.Minute, time.Minute)
	if code := pOn.login(t, f, on); code != http.StatusOK {
		t.Fatalf("control: a valid assertion for an enabled account must log in, got %d", code)
	}

	off := f.createUser(t, "passkey-disabled", "control-password-2")
	pOff := newSoftPasskey(t, "disabled")
	pOff.register(t, f, off)
	if _, err := f.h.Pool.Exec(context.Background(), `UPDATE users SET disabled = TRUE WHERE id = $1`, off); err != nil {
		t.Fatal(err)
	}
	// One attempt per window: a refund would let the next begin through.
	f.ah.Limiter = auth.NewLoginLimiter(1, time.Minute, time.Minute)
	if code := pOff.login(t, f, off); code != http.StatusUnauthorized {
		t.Fatalf("disabled account: want 401, got %d", code)
	}
	w := httptest.NewRecorder()
	f.ah.PasskeyLoginBegin(w, httptest.NewRequest(http.MethodPost, "/api/auth/passkey/login/begin", nil))
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("the disabled-account attempt was refunded: next begin got %d, want 429", w.Code)
	}
}

func TestPasskeyLoginFinish_RecordUserLimiterFailure(t *testing.T) {
	f := newAuthFixture(t)
	wa, err := auth.NewWebAuthnService(f.h.Pool, "example.com", "Example", []string{"https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	f.ah.WebAuthn = wa
	t.Cleanup(func() { _, _ = f.h.Pool.Exec(context.Background(), `DELETE FROM webauthn_ceremonies`) })

	off := f.createUser(t, "disabled-user", "password-123")
	pOff := newSoftPasskey(t, "disabled-user-key")
	pOff.register(t, f, off)
	if _, err := f.h.Pool.Exec(context.Background(), `UPDATE users SET disabled = TRUE WHERE id = $1`, off); err != nil {
		t.Fatal(err)
	}

	// Configure a UserLimiter with capacity 1 to observe the failure recording.
	f.ah.UserLimiter = auth.NewStickyLoginLimiter(1, time.Minute, time.Minute)

	// Attempt passkey login for the disabled user.
	if code := pOff.login(t, f, off); code != http.StatusUnauthorized {
		t.Fatalf("disabled account: want 401, got %d", code)
	}

	// UserLimiter should now be exhausted for "disabled-user".
	if allowed, _ := f.ah.UserLimiter.Allowed("disabled-user"); allowed {
		t.Errorf("expected UserLimiter to be throttled after passkey login on disabled account, but allowed is true")
	}
}
