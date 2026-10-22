package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

func TestDeletePasskeyRequestValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		body        string
		contentType string
		id          string
		want        int
	}{
		{name: "empty body requests step up", id: "1", want: http.StatusForbidden},
		{name: "whitespace body requests step up", id: "1", body: " \n\t", want: http.StatusForbidden},
		{name: "empty object requests step up", id: "1", body: `{}`, want: http.StatusForbidden},
		{name: "invalid id", id: "wrong", want: http.StatusBadRequest},
		{name: "incomplete JSON", id: "1", body: `{"password":`, want: http.StatusBadRequest},
		{name: "wrong password type", id: "1", body: `{"password":123}`, want: http.StatusBadRequest},
		{name: "unknown field", id: "1", body: `{"unexpected":true}`, want: http.StatusBadRequest},
		{name: "trailing JSON document", id: "1", body: `{} {}`, want: http.StatusBadRequest},
		{name: "trailing garbage", id: "1", body: `{} invalid`, want: http.StatusBadRequest},
		{name: "non JSON content type", id: "1", body: `{}`, contentType: "text/plain", want: http.StatusBadRequest},
		{name: "oversized body", id: "1", body: strings.Repeat(" ", maxJSONBody+1), want: http.StatusBadRequest},
		{name: "overlong password", id: "1", body: `{"password":"` + strings.Repeat("a", maxPasswordLen+1) + `"}`, want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wa, err := auth.NewWebAuthnService(nil, "example.com", "Example", []string{"https://example.com"})
			if err != nil {
				t.Fatal(err)
			}
			ah := &AuthHandler{
				Handler:     &Handler{},
				Auth:        &auth.Manager{},
				WebAuthn:    wa,
				Limiter:     auth.NewLoginLimiter(3, time.Minute, time.Minute),
				UserLimiter: auth.NewStickyLoginLimiter(3, time.Minute, time.Minute),
			}
			u := &models.User{ID: 1, Username: "Delete-Validation"}
			r := httptest.NewRequest(http.MethodDelete, "/api/passkeys/"+tc.id, strings.NewReader(tc.body))
			r.SetPathValue("id", tc.id)
			r.RemoteAddr = "192.0.2.8:1234"
			if tc.contentType != "" {
				r.Header.Set("Content-Type", tc.contentType)
			}
			r = r.WithContext(auth.ContextWithUser(r.Context(), u))
			w := httptest.NewRecorder()
			ah.DeletePasskey(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s, want %d", w.Code, w.Body.String(), tc.want)
			}
			// No credential was checked (except for overlong password attempts,
			// which record a re-authentication failure): neither malformed requests nor
			// step-up prompts without credentials spend the user's failed-authentication budget.
			wantBudget := 3
			if tc.name == "overlong password" {
				wantBudget = 2
			}
			assertDeletePasskeyBudget(t, ah, u.Username, "192.0.2.8", wantBudget)
		})
	}
}

func TestDeletePasskeyDisabled(t *testing.T) {
	t.Parallel()
	ah := &AuthHandler{Handler: &Handler{}}
	w := httptest.NewRecorder()
	ah.DeletePasskey(w, httptest.NewRequest(http.MethodDelete, "/api/passkeys/1", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled passkeys: got %d, want 404", w.Code)
	}
}

// Probe each independent budget through its public API. The final admitted
// attempt locks it; this catches both leaked reservations and accidental resets.
func assertDeletePasskeyBudget(t *testing.T, ah *AuthHandler, username, ip string, remaining int) {
	t.Helper()
	for _, budget := range []struct {
		name    string
		limiter *auth.LoginLimiter
		key     string
	}{
		{"username and IP", ah.Limiter, strings.ToLower(username) + "|" + ip},
		{"account", ah.UserLimiter, strings.ToLower(username)},
	} {
		for i := 0; i < remaining; i++ {
			if ok, _ := budget.limiter.Consume(budget.key); !ok {
				t.Fatalf("%s budget rejected remaining attempt %d of %d", budget.name, i+1, remaining)
			}
		}
		if ok, _ := budget.limiter.Allowed(budget.key); ok {
			t.Errorf("%s budget allowed more than %d remaining attempts", budget.name, remaining)
		}
	}
}
