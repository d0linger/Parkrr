package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
)

// Finding SH-03: WebAuthn ceremony state lives server-side (webauthn_ceremonies),
// expiring and single-use, with the cookie carrying only an opaque id. A ceremony
// must load exactly once (replay fails) and never load after it has expired.
//
// Runs only when PARKRR_TEST_DATABASE_URL is set (testHandler skips otherwise).
func TestWebAuthnCeremonySingleUseAndExpiry(t *testing.T) {
	h := testHandler(t)
	ah := &AuthHandler{Handler: h}
	ctx := context.Background()
	t.Cleanup(func() { _, _ = h.Pool.Exec(ctx, `DELETE FROM webauthn_ceremonies`) })

	// Begin: store a ceremony and capture the opaque cookie it sets.
	rec := httptest.NewRecorder()
	if err := ah.storeCeremony(ctx, rec, webauthn.SessionData{}, "my-key"); err != nil {
		t.Fatalf("storeCeremony: %v", err)
	}
	var cookieVal string
	for _, c := range rec.Result().Cookies() {
		if c.Name == waCookie {
			cookieVal = c.Value
		}
	}
	if cookieVal == "" {
		t.Fatal("no ceremony cookie was set")
	}
	// The cookie must NOT carry the ceremony data itself — only an id.
	if len(cookieVal) > 100 {
		t.Errorf("ceremony cookie looks like it carries state (len=%d), expected a short id", len(cookieVal))
	}

	reqWithCookie := func(v string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/passkeys/login/finish", nil)
		r.AddCookie(&http.Cookie{Name: waCookie, Value: v})
		return r
	}

	// First finish: loads successfully and round-trips our data.
	cer, err := ah.loadCeremony(ctx, reqWithCookie(cookieVal))
	if err != nil {
		t.Fatalf("first loadCeremony: %v", err)
	}
	if cer.Name != "my-key" {
		t.Fatalf("ceremony round-trip mismatch: got name %q", cer.Name)
	}

	// Replay: the same cookie must not load a second time (single-use).
	if _, err := ah.loadCeremony(ctx, reqWithCookie(cookieVal)); err == nil {
		t.Error("replay: a consumed ceremony must not load again")
	}

	// Expiry: a row past expires_at must not load.
	id, err := newCeremonyID()
	if err != nil {
		t.Fatalf("newCeremonyID: %v", err)
	}
	blob, _ := json.Marshal(waCeremony{})
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO webauthn_ceremonies (id, data, expires_at) VALUES ($1,$2, now() - interval '1 minute')`,
		id, blob); err != nil {
		t.Fatalf("insert expired ceremony: %v", err)
	}
	if _, err := ah.loadCeremony(ctx, reqWithCookie(id)); err == nil {
		t.Error("an expired ceremony must not load")
	}
}
