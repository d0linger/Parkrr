//go:build integration

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"

	"github.com/preining/parkrr/internal/auth"
)

func TestTOTPLoginCannotAdvanceReplacementCredentialReplayStep(t *testing.T) {
	f := newAuthFixture(t)
	const username, password = "totp-credential-race-Integration", "credential-race-password"
	uid, oldSecret := f.totpUser(t, username, password)
	newKey, err := auth.GenerateTOTP(username)
	if err != nil {
		t.Fatal(err)
	}
	newSecret, err := f.mgr.EncryptTOTPSecret(newKey.Secret())
	if err != nil {
		t.Fatal(err)
	}
	// Pending material is only fixture storage for the simulated replacement;
	// the trigger makes rotation occur after Login has loaded the old credential.
	if _, err := f.h.Pool.Exec(t.Context(),
		`UPDATE users SET pending_totp_secret=$2 WHERE id=$1`, uid, newSecret); err != nil {
		t.Fatal(err)
	}
	name := pgx.Identifier{"totp_credential_rotation_" + strconv.FormatInt(uid, 10)}.Sanitize()
	if _, err := f.h.Pool.Exec(t.Context(), `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			NEW.totp_secret := NEW.pending_totp_secret;
			NEW.pending_totp_secret := '';
			NEW.totp_failures := 0;
			NEW.totp_locked_until := NULL;
			NEW.last_totp_step := 0;
			RETURN NEW;
		END $$`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := f.h.Pool.Exec(ctx,
			`DROP TRIGGER IF EXISTS `+name+` ON users; DROP FUNCTION IF EXISTS `+name+`() `); err != nil {
			t.Errorf("remove credential rotation fixture: %v", err)
		}
	})
	if _, err := f.h.Pool.Exec(t.Context(), `CREATE TRIGGER `+name+` BEFORE UPDATE OF totp_failures ON users
		FOR EACH ROW WHEN (OLD.id = `+strconv.FormatInt(uid, 10)+`
			AND OLD.pending_totp_secret <> '' AND NEW.totp_failures = OLD.totp_failures + 1)
		EXECUTE FUNCTION `+name+`() `); err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(oldSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.ah.Login(w, authJSONReq(t, http.MethodPost, "/api/auth/login",
		loginRequest{Username: username, Password: password, TOTPCode: code}))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("login with replaced credential: got %d, want 401", w.Code)
	}
	state := readTOTPLifecycleState(t, f, uid)
	if !state.Enabled || state.Secret != newSecret || state.PendingSecret != "" {
		t.Fatal("fixture did not rotate the factor during the login reservation")
	}
	if state.LastStep != 0 || state.Failures != 0 || state.LockedUntil != nil {
		t.Fatalf("old login changed replacement state: step=%d failures=%d lock=%v",
			state.LastStep, state.Failures, state.LockedUntil)
	}
	if state.Sessions != 0 {
		t.Fatalf("old credential created %d sessions", state.Sessions)
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == auth.SessionCookie && cookie.Value != "" {
			t.Error("rejected login returned a session cookie")
		}
	}
}
