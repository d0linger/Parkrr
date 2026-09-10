package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRotateSessionPreservesAssurance(t *testing.T) {
	for _, verified := range []bool{false, true} {
		name := "unverified"
		if verified {
			name = "verified"
		}
		t.Run(name, func(t *testing.T) {
			m, pool := testAuthManager(t)
			id := mkAuthUser(t, pool, "editor", false)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if err := m.createSession(t.Context(), rec, req, id, verified); err != nil {
				t.Fatal(err)
			}
			req = withCookies(req, rec.Result().Cookies())
			u, err := m.userFromRequest(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			ctx := ContextWithUser(t.Context(), u)
			rotated := httptest.NewRecorder()
			if err := m.RotateSession(ctx, rotated, req, id); err != nil {
				t.Fatal(err)
			}
			next := withCookies(httptest.NewRequest(http.MethodGet, "/", nil), rotated.Result().Cookies())
			got, err := m.userFromRequest(t.Context(), next)
			if err != nil {
				t.Fatal(err)
			}
			if got.FactorVerified != verified {
				t.Fatalf("assurance changed: got %v", got.FactorVerified)
			}
			if _, err := m.userFromRequest(t.Context(), req); err == nil {
				t.Fatal("old session survived rotation")
			}
		})
	}
}
