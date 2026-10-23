//go:build integration

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

func TestTOTPDisableClearsLifecycleState(t *testing.T) {
	f := newAuthFixture(t)
	const name, password = "totp-disable-lifecycle-Integration", "lifecycle-password"
	uid, _ := f.totpUser(t, name, password)
	seedTOTPLifecycleState(t, f, uid)
	before := readTOTPLifecycleState(t, f, uid)
	u := &models.User{ID: uid, Username: name, TOTPEnabled: true}
	if w := disableTOTPLifecycle(t, f, u, "incorrect-password"); w.Code != http.StatusForbidden {
		t.Fatalf("wrong password: %d %s", w.Code, w.Body.String())
	}
	if after := readTOTPLifecycleState(t, f, uid); !reflect.DeepEqual(before, after) {
		t.Fatal("rejected disable changed the factor, pending setup, lock or replay state")
	}
	if w := disableTOTPLifecycle(t, f, u, password); w.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	assertTOTPLifecycleCleared(t, readTOTPLifecycleState(t, f, uid))
}

func TestTOTPDisableRollsBackWhenRecoveryCodeDeletionFails(t *testing.T) {
	f := newAuthFixture(t)
	const name, password = "totp-disable-rollback-Integration", "lifecycle-password"
	uid, _ := f.totpUser(t, name, password)
	seedTOTPLifecycleState(t, f, uid)
	before := readTOTPLifecycleState(t, f, uid)
	removeFailure := failTOTPRecoveryCodeDeletion(t, f, uid)
	u := &models.User{ID: uid, Username: name, TOTPEnabled: true}
	if w := disableTOTPLifecycle(t, f, u, password); w.Code != http.StatusInternalServerError {
		t.Fatalf("injected recovery-code deletion failure: %d %s", w.Code, w.Body.String())
	}
	if after := readTOTPLifecycleState(t, f, uid); !reflect.DeepEqual(before, after) {
		t.Fatal("failed disable did not roll back the factor, setup, counters and recovery codes")
	}
	var audited int
	if err := f.h.Pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log
		WHERE entity='user' AND entity_id=$1 AND summary LIKE '%disabled two-factor authentication'`, uid).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 0 {
		t.Errorf("failed disable wrote %d success audit entries", audited)
	}
	removeFailure()
	if w := disableTOTPLifecycle(t, f, u, password); w.Code != http.StatusOK {
		t.Fatalf("retry disable: %d %s", w.Code, w.Body.String())
	}
	assertTOTPLifecycleCleared(t, readTOTPLifecycleState(t, f, uid))
}

func TestResetUserTOTPClearsLifecycleStateAndRevokesSessions(t *testing.T) {
	f := newAuthFixture(t)
	uid, _ := f.totpUser(t, "totp-reset-lifecycle-Integration", "lifecycle-password")
	otherID, _ := f.totpUser(t, "totp-reset-other-Integration", "other-lifecycle-password")
	for _, id := range []int64{uid, otherID} {
		seedTOTPLifecycleState(t, f, id)
		if _, err := f.h.Pool.Exec(t.Context(), `INSERT INTO webauthn_credentials (user_id,credential_id,public_key,name)
			VALUES ($1,$2,$3,'lifecycle-key')`, id, []byte(fmt.Sprintf("lifecycle-key-%d", id)), []byte("fixture-public-key")); err != nil {
			t.Fatal(err)
		}
	}
	issueSession := func(id int64) []*http.Cookie {
		t.Helper()
		w := httptest.NewRecorder()
		if err := f.mgr.CreateVerifiedSession(t.Context(), w, httptest.NewRequest(http.MethodPost, "/login", nil), id); err != nil {
			t.Fatal(err)
		}
		return w.Result().Cookies()
	}
	first, second, other := issueSession(uid), issueSession(uid), issueSession(otherID)
	probe := func(cookies []*http.Cookie) int {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/probe", nil)
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		f.mgr.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(w, r)
		return w.Code
	}
	for _, cookies := range [][]*http.Cookie{first, second, other} {
		if got := probe(cookies); got != http.StatusNoContent {
			t.Fatalf("fixture session rejected: %d", got)
		}
	}
	otherBefore := readTOTPLifecycleState(t, f, otherID)
	r := httptest.NewRequest(http.MethodPost, "/api/users/x/reset-2fa", nil)
	r.SetPathValue("id", strconv.FormatInt(uid, 10))
	w := httptest.NewRecorder()
	f.h.ResetUserTOTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("admin reset: %d %s", w.Code, w.Body.String())
	}
	after := readTOTPLifecycleState(t, f, uid)
	assertTOTPLifecycleCleared(t, after)
	if after.Passkeys != 0 || after.Sessions != 0 {
		t.Errorf("reset retained %d passkeys and %d sessions", after.Passkeys, after.Sessions)
	}
	for _, cookies := range [][]*http.Cookie{first, second} {
		if got := probe(cookies); got != http.StatusUnauthorized {
			t.Errorf("reset target session remains usable: %d", got)
		}
	}
	if got := probe(other); got != http.StatusNoContent {
		t.Errorf("reset revoked another account's session: %d", got)
	}
	if otherAfter := readTOTPLifecycleState(t, f, otherID); !reflect.DeepEqual(otherBefore, otherAfter) {
		t.Error("reset changed another account's factor, lock, replay state or credentials")
	}
}

func TestTOTPEnrollmentReplacesOldReplayStateAndConsumesAcceptedCode(t *testing.T) {
	f := newAuthFixture(t)
	const name, password = "totp-reenroll-lifecycle-Integration", "lifecycle-password"
	uid := f.createUser(t, name, password)
	seedTOTPLifecycleState(t, f, uid)
	u := &models.User{ID: uid, Username: name}
	cookie := f.sessionCookie(t, uid)
	setup := setupTOTPLifecycle(t, f, u)
	enrolledAt := time.Now()
	code, err := totp.GenerateCode(setup.Secret, enrolledAt)
	if err != nil {
		t.Fatal(err)
	}
	r := authJSONReq(t, http.MethodPost, "/api/auth/2fa/enable", totpVerifyRequest{Code: code, SetupID: setup.SetupID})
	r.AddCookie(cookie)
	r = r.WithContext(auth.ContextWithUser(r.Context(), u))
	w := httptest.NewRecorder()
	f.ah.TOTPEnable(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("enroll with stale prior-factor step: %d %s", w.Code, w.Body.String())
	}
	after := readTOTPLifecycleState(t, f, uid)
	wantStep := enrolledAt.Unix() / 30
	if !after.Enabled || after.LastStep != wantStep || after.Failures != 0 || after.LockedUntil != nil {
		t.Fatalf("new enrollment state: enabled=%v step=%d failures=%d lock=%v; want accepted step %d and cleared lock",
			after.Enabled, after.LastStep, after.Failures, after.LockedUntil, wantStep)
	}
	if after.PendingSecret != "" || after.PendingNonce != "" || after.PendingExpiresAt != nil {
		t.Error("successful enrollment left a reusable pending ceremony")
	}
	if after.Secret == "" || after.BackupCodes != 10 {
		t.Errorf("new enrollment did not retain its encrypted secret and recovery codes: codes=%d", after.BackupCodes)
	}
	login := func(code string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		f.ah.Login(w, authJSONReq(t, http.MethodPost, "/api/auth/login",
			loginRequest{Username: name, Password: password, TOTPCode: code}))
		return w
	}
	if w := login(code); w.Code != http.StatusUnauthorized {
		t.Fatalf("enrollment OTP reused at login: %d %s", w.Code, w.Body.String())
	}
	if state := readTOTPLifecycleState(t, f, uid); state.Sessions != after.Sessions || state.LastStep != wantStep {
		t.Error("replayed enrollment OTP created a session or changed the consumed step")
	}
	// The accepted +1 skew window gives us a genuinely newer code without sleeps.
	// Generate it at login time, so crossing a boundary during enrollment is safe.
	freshAt := time.Now().Add(30 * time.Second)
	freshCode, err := totp.GenerateCode(setup.Secret, freshAt)
	if err != nil {
		t.Fatal(err)
	}
	if w := login(freshCode); w.Code != http.StatusOK {
		t.Fatalf("fresh code from new factor: %d %s", w.Code, w.Body.String())
	}
	state := readTOTPLifecycleState(t, f, uid)
	if state.LastStep != freshAt.Unix()/30 || state.Sessions != after.Sessions+1 || state.Failures != 0 || state.LockedUntil != nil {
		t.Fatalf("fresh login did not consume its new step and create one session: step=%d sessions=%d failures=%d lock=%v",
			state.LastStep, state.Sessions, state.Failures, state.LockedUntil)
	}
	if w := login(freshCode); w.Code != http.StatusUnauthorized {
		t.Fatalf("fresh code reused at login: %d %s", w.Code, w.Body.String())
	}
	if replayed := readTOTPLifecycleState(t, f, uid); replayed.Sessions != state.Sessions || replayed.LastStep != state.LastStep {
		t.Error("reused login OTP created another session or changed the consumed step")
	}
}

func TestTOTPConcurrentLoginsRedeemOneCodeOnce(t *testing.T) {
	f := newAuthFixture(t)
	const name, password = "totp-concurrent-redeem-Integration", "concurrent-redeem-password"
	uid, secret := f.totpUser(t, name, password)
	before := readTOTPLifecycleState(t, f, uid)
	generatedAt := time.Now()
	code, err := totp.GenerateCode(secret, generatedAt)
	if err != nil {
		t.Fatal(err)
	}
	reqs := make([]*http.Request, 2)
	for i := range reqs {
		reqs[i] = authJSONReq(t, http.MethodPost, "/api/auth/login",
			loginRequest{Username: name, Password: password, TOTPCode: code})
	}
	// Both logins wait at one barrier, so they race for the same unconsumed step.
	start := make(chan struct{})
	results := make(chan int, len(reqs))
	var ready sync.WaitGroup
	for _, r := range reqs {
		ready.Add(1)
		go func(r *http.Request) {
			w := httptest.NewRecorder()
			ready.Done()
			<-start
			f.ah.Login(w, r)
			results <- w.Code
		}(r)
	}
	ready.Wait()
	close(start)
	got := map[int]int{}
	for range reqs {
		got[<-results]++
	}
	if got[http.StatusOK] != 1 || got[http.StatusUnauthorized] != 1 {
		t.Fatalf("concurrent redemption of one code: %v, want exactly one 200 and one 401", got)
	}
	after := readTOTPLifecycleState(t, f, uid)
	if after.Sessions != before.Sessions+1 {
		t.Fatalf("concurrent redemption created %d sessions, want 1", after.Sessions-before.Sessions)
	}
	if want := generatedAt.Unix() / 30; after.LastStep != want {
		t.Fatalf("consumed step %d, want %d", after.LastStep, want)
	}
}

func TestTOTPRejectedEnrollmentPreservesLifecycleState(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int
	}{
		{name: "password rejected", want: http.StatusForbidden},
		{name: "code rejected", want: http.StatusBadRequest},
		{name: "ceremony replaced", want: http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuthFixture(t)
			const password = "lifecycle-password"
			name := "totp-enable-rejected-" + strconv.FormatInt(time.Now().UnixNano(), 10)
			uid := f.createUser(t, name, password)
			seedTOTPLifecycleState(t, f, uid)
			u := &models.User{ID: uid, Username: name}
			setup := setupTOTPLifecycle(t, f, u)
			code, err := totp.GenerateCode(setup.Secret, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			in := totpVerifyRequest{Code: code, SetupID: setup.SetupID, Password: password}
			switch tc.name {
			case "password rejected":
				in.Password = "incorrect-password"
			case "code rejected":
				in.Code = "abcdef"
			case "ceremony replaced":
				_ = setupTOTPLifecycle(t, f, u)
			}
			before := readTOTPLifecycleState(t, f, uid)
			r := authJSONReq(t, http.MethodPost, "/api/auth/2fa/enable", in)
			r = r.WithContext(auth.ContextWithUser(r.Context(), u))
			w := httptest.NewRecorder()
			f.ah.TOTPEnable(w, r)
			if w.Code != tc.want {
				t.Fatalf("rejected enrollment: %d %s; want %d", w.Code, w.Body.String(), tc.want)
			}
			if after := readTOTPLifecycleState(t, f, uid); !reflect.DeepEqual(before, after) {
				t.Fatal("rejected enrollment changed the factor, ceremony, counters, replay step or recovery codes")
			}
		})
	}
}

type totpLifecycleState struct {
	Enabled          bool
	Secret           string
	PendingSecret    string
	PendingNonce     string
	PendingExpiresAt *time.Time
	Failures         int
	LockedUntil      *time.Time
	LastStep         int64
	BackupCodes      int
	Passkeys         int
	Sessions         int
}

func readTOTPLifecycleState(t *testing.T, f *authFixture, uid int64) totpLifecycleState {
	t.Helper()
	var state totpLifecycleState
	if err := f.h.Pool.QueryRow(t.Context(), `SELECT totp_enabled,totp_secret,pending_totp_secret,pending_totp_nonce,
		pending_totp_expires_at,totp_failures,totp_locked_until,last_totp_step,
		(SELECT count(*) FROM totp_backup_codes WHERE user_id=$1),
		(SELECT count(*) FROM webauthn_credentials WHERE user_id=$1),
		(SELECT count(*) FROM sessions WHERE user_id=$1) FROM users WHERE id=$1`, uid).
		Scan(&state.Enabled, &state.Secret, &state.PendingSecret, &state.PendingNonce, &state.PendingExpiresAt,
			&state.Failures, &state.LockedUntil, &state.LastStep, &state.BackupCodes, &state.Passkeys, &state.Sessions); err != nil {
		t.Fatal(err)
	}
	return state
}

func seedTOTPLifecycleState(t *testing.T, f *authFixture, uid int64) {
	t.Helper()
	if _, err := f.h.Pool.Exec(t.Context(), `UPDATE users SET pending_totp_secret='pending-lifecycle-secret',
		pending_totp_nonce='pending-lifecycle-nonce',pending_totp_expires_at=now()+interval '10 minutes',
		totp_failures=9,totp_locked_until=now()+interval '1 hour',last_totp_step=$2 WHERE id=$1`,
		uid, time.Now().Unix()/30+1_000_000); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.Pool.Exec(t.Context(), `INSERT INTO totp_backup_codes (user_id,code_hash) VALUES ($1,'old-lifecycle-code')`, uid); err != nil {
		t.Fatal(err)
	}
}

func assertTOTPLifecycleCleared(t *testing.T, state totpLifecycleState) {
	t.Helper()
	if state.Enabled || state.Secret != "" || state.PendingSecret != "" || state.PendingNonce != "" || state.PendingExpiresAt != nil {
		t.Error("active or pending TOTP credential remains after removing the factor")
	}
	if state.Failures != 0 || state.LockedUntil != nil || state.LastStep != 0 {
		t.Errorf("removed factor retained failures=%d lock=%v replay step=%d", state.Failures, state.LockedUntil, state.LastStep)
	}
	if state.BackupCodes != 0 {
		t.Errorf("removed factor retained %d recovery codes", state.BackupCodes)
	}
}

func disableTOTPLifecycle(t *testing.T, f *authFixture, user *models.User, password string) *httptest.ResponseRecorder {
	t.Helper()
	r := authJSONReq(t, http.MethodPost, "/api/auth/2fa/disable", totpDisableRequest{Password: password})
	r = r.WithContext(auth.ContextWithUser(r.Context(), user))
	w := httptest.NewRecorder()
	f.ah.TOTPDisable(w, r)
	return w
}

type totpLifecycleSetup struct {
	Secret  string `json:"secret"`
	SetupID string `json:"setup_id"`
}

func setupTOTPLifecycle(t *testing.T, f *authFixture, user *models.User) totpLifecycleSetup {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/setup", nil)
	r = r.WithContext(auth.ContextWithUser(r.Context(), user))
	w := httptest.NewRecorder()
	f.ah.TOTPSetup(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	var setup totpLifecycleSetup
	if err := json.Unmarshal(w.Body.Bytes(), &setup); err != nil {
		t.Fatal(err)
	}
	if setup.Secret == "" || setup.SetupID == "" {
		t.Fatal("setup returned no factor or ceremony identifier")
	}
	return setup
}

func failTOTPRecoveryCodeDeletion(t *testing.T, f *authFixture, uid int64) func() {
	t.Helper()
	name := pgx.Identifier{"totp_code_delete_failure_" + strconv.FormatInt(time.Now().UnixNano(), 10)}.Sanitize()
	if _, err := f.h.Pool.Exec(t.Context(), `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected recovery-code deletion failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := f.h.Pool.Exec(ctx, `DROP TRIGGER IF EXISTS `+name+` ON totp_backup_codes; DROP FUNCTION IF EXISTS `+name+`() `); err != nil {
			t.Errorf("remove recovery-code deletion failure: %v", err)
		}
	}
	t.Cleanup(cleanup)
	if _, err := f.h.Pool.Exec(t.Context(), `CREATE TRIGGER `+name+` BEFORE DELETE ON totp_backup_codes
		FOR EACH ROW WHEN (OLD.user_id = `+strconv.FormatInt(uid, 10)+`) EXECUTE FUNCTION `+name+`() `); err != nil {
		t.Fatal(err)
	}
	return cleanup
}
