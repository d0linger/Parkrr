package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

func TestTOTPEnrollmentIsBoundToLatestCeremony(t *testing.T) {
	h := testHandler(t)
	mgr, err := auth.NewManager(h.Pool, auth.SessionConfig{MaxAge: 3600}, false, false,
		"a-sufficiently-long-totp-ceremony-secret")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	ah := NewAuthHandler(h, mgr, nil, stop)

	const username = "totp-ceremony-Integration"
	var userID int64
	if err := h.Pool.QueryRow(t.Context(),
		`INSERT INTO users (username,password_hash) VALUES ($1,'x') RETURNING id`, username).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, userID) })
	user := &models.User{ID: userID, Username: username}

	sessionRec := httptest.NewRecorder()
	if err := mgr.CreateSession(t.Context(), sessionRec,
		httptest.NewRequest(http.MethodPost, "/api/auth/login", nil), userID); err != nil {
		t.Fatal(err)
	}
	var sessionCookie *http.Cookie
	for _, cookie := range sessionRec.Result().Cookies() {
		if cookie.Name == auth.SessionCookie {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("session cookie missing")
	}

	type setupResponse struct {
		Secret  string `json:"secret"`
		SetupID string `json:"setup_id"`
	}
	setup := func() setupResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/setup", nil)
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		rec := httptest.NewRecorder()
		ah.TOTPSetup(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
		}
		var out setupResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := setup()
	second := setup()
	if first.SetupID == second.SetupID || first.Secret == second.Secret {
		t.Fatal("separate setup ceremonies reused identity or secret")
	}

	enable := func(s setupResponse) *httptest.ResponseRecorder {
		t.Helper()
		code, err := totp.GenerateCode(s.Secret, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]string{"setup_id": s.SetupID, "code": code})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/enable", bytes.NewReader(body))
		req.AddCookie(sessionCookie)
		req = req.WithContext(auth.ContextWithUser(req.Context(), user))
		rec := httptest.NewRecorder()
		ah.TOTPEnable(rec, req)
		return rec
	}
	if rec := enable(first); rec.Code != http.StatusConflict {
		t.Fatalf("superseded ceremony: got %d %s, want 409", rec.Code, rec.Body.String())
	}
	if rec := enable(second); rec.Code != http.StatusOK {
		t.Fatalf("current ceremony: got %d %s, want 200", rec.Code, rec.Body.String())
	}
	var enabled bool
	var pendingSecret, pendingNonce string
	var pendingExpiry *time.Time
	if err := h.Pool.QueryRow(t.Context(),
		`SELECT totp_enabled,pending_totp_secret,pending_totp_nonce,pending_totp_expires_at FROM users WHERE id=$1`,
		userID).Scan(&enabled, &pendingSecret, &pendingNonce, &pendingExpiry); err != nil {
		t.Fatal(err)
	}
	if !enabled || pendingSecret != "" || pendingNonce != "" || pendingExpiry != nil {
		t.Fatalf("ceremony was not atomically consumed: enabled=%v secret=%q nonce=%q expiry=%v",
			enabled, pendingSecret, pendingNonce, pendingExpiry)
	}
}
