package main

import (
	"context"
	"crypto/sha1" // #nosec G505 -- HIBP range protocol, test stub only.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/config"
	"github.com/preining/parkrr/internal/database"
)

type bootstrapRoundTripFunc func(*http.Request) (*http.Response, error)

func (f bootstrapRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// All bootstrap tests use an in-memory transport; no HIBP requests leave the process.
func bootstrapBreachStub(password string, count int, offline bool, calls *int) *http.Client {
	sum := sha1.Sum([]byte(password)) // #nosec G401 -- HIBP protocol fixture, not password storage.
	suffix := strings.ToUpper(hex.EncodeToString(sum[:]))[5:]
	return &http.Client{Transport: bootstrapRoundTripFunc(func(*http.Request) (*http.Response, error) {
		*calls++
		if offline {
			return nil, errors.New("test breach service unavailable")
		}
		return &http.Response{StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(fmt.Sprintf("%s:%d\r\n", suffix, count)))}, nil
	})}
}

func TestCheckBootstrapPassword(t *testing.T) {
	cases := []struct {
		name         string
		password     string
		check, optIn bool
		failClosed   bool
		breaches     int
		offline      bool
		wantErr      bool
		wantLookups  int
	}{
		{name: "too short", password: "1234567", check: true, optIn: true, wantErr: true},
		{name: "minimum bytes", password: "12345678"},
		{name: "maximum bytes", password: strings.Repeat("x", 72)},
		{name: "too long", password: strings.Repeat("x", 73), check: true, optIn: true, wantErr: true},
		{name: "unicode minimum bytes", password: strings.Repeat("ä", 4)},
		{name: "unicode too many bytes", password: strings.Repeat("ä", 37), wantErr: true},
		{name: "bootstrap check defaults off", password: "password123", check: true, breaches: 42},
		{name: "UI check disabled", password: "password123", optIn: true, breaches: 42},
		{name: "known breach", password: "password123", check: true, optIn: true, breaches: 42, wantErr: true, wantLookups: 1},
		{name: "not breached", password: "test-unique-bootstrap-password", check: true, optIn: true, wantLookups: 1},
		{name: "unavailable fail open", password: "test-unique-bootstrap-password", check: true, optIn: true, offline: true, wantLookups: 1},
		{name: "unavailable fail closed", password: "test-unique-bootstrap-password", check: true, optIn: true, offline: true, failClosed: true, wantErr: true, wantLookups: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			cfg := &config.Config{AdminPassword: tc.password, CheckBreachedPasswords: tc.check,
				CheckBreachedBootstrapPasswords: tc.optIn, FailClosedOnBreach: tc.failClosed}
			err := checkBootstrapPassword(context.Background(), cfg, bootstrapBreachStub(tc.password, tc.breaches, tc.offline, &calls))
			if (err != nil) != tc.wantErr || calls != tc.wantLookups {
				t.Errorf("err=%v lookups=%d, want error=%v lookups=%d", err, calls, tc.wantErr, tc.wantLookups)
			}
		})
	}
}

func bootstrapTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set; skipping bootstrap integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func bootstrapTestConfig(t *testing.T, pool *pgxpool.Pool) *config.Config {
	t.Helper()
	cfg := &config.Config{AdminUsername: "bootstrap-test-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		AdminEmail: "bootstrap-test@example.invalid", AdminPassword: "test-original-admin-password",
		CheckBreachedPasswords: true, CheckBreachedBootstrapPasswords: true, FailClosedOnBreach: true}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE username = $1`, cfg.AdminUsername); err != nil {
			t.Errorf("cleanup bootstrap user: %v", err)
		}
	})
	return cfg
}

func useBootstrapBreachStub(t *testing.T, client *http.Client) {
	t.Helper()
	original := bootstrapHIBPClient
	bootstrapHIBPClient = client
	t.Cleanup(func() { bootstrapHIBPClient = original })
}

func TestBootstrapAdminCreationPasswordPolicy(t *testing.T) {
	pool := bootstrapTestPool(t)
	for _, tc := range []struct {
		name, password string
		breaches       int
		offline        bool
		failClosed     bool
		wantErr        bool
	}{
		{name: "short", password: "short", wantErr: true},
		{name: "overlong", password: strings.Repeat("x", 73), wantErr: true},
		{name: "minimum", password: "12345678"},
		{name: "maximum", password: strings.Repeat("x", 72)},
		{name: "breached", password: "password123", breaches: 42, wantErr: true},
		{name: "offline fail closed", password: "test-bootstrap-password", offline: true, failClosed: true, wantErr: true},
		{name: "offline fail open", password: "test-bootstrap-password", offline: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := bootstrapTestConfig(t, pool)
			cfg.AdminPassword, cfg.FailClosedOnBreach = tc.password, tc.failClosed
			calls := 0
			useBootstrapBreachStub(t, bootstrapBreachStub(tc.password, tc.breaches, tc.offline, &calls))
			err := bootstrapAdmin(context.Background(), pool, cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("bootstrap: %v, want error=%v", err, tc.wantErr)
			}
			var users int
			if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE username = $1`, cfg.AdminUsername).Scan(&users); err != nil {
				t.Fatal(err)
			}
			if tc.wantErr {
				if users != 0 {
					t.Errorf("rejected password created %d users", users)
				}
				return
			}
			var hash, role string
			var admin, disabled bool
			if err := pool.QueryRow(context.Background(), `SELECT password_hash, role, is_admin, disabled FROM users WHERE username = $1`, cfg.AdminUsername).
				Scan(&hash, &role, &admin, &disabled); err != nil {
				t.Fatal(err)
			}
			if !auth.CheckPassword(hash, cfg.AdminPassword) || role != "admin" || !admin || disabled {
				t.Errorf("created credentials/role invalid: role=%s admin=%v disabled=%v", role, admin, disabled)
			}
		})
	}
}

func seedBootstrapAdmin(t *testing.T, pool *pgxpool.Pool, cfg *config.Config) (int64, string) {
	t.Helper()
	hash, err := auth.HashPassword("test-original-admin-password")
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO users (username, email, password_hash, is_admin, role, disabled)
		VALUES ($1, 'old@example.invalid', $2, false, 'buchhaltung', true) RETURNING id`, cfg.AdminUsername, hash).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, now() + interval '1 day')`, cfg.AdminUsername+"-session", id); err != nil {
		t.Fatal(err)
	}
	return id, hash
}

func TestBootstrapAdminRestartPreservesPasswordAndDisableState(t *testing.T) {
	pool := bootstrapTestPool(t)
	for _, password := range []string{"short", strings.Repeat("x", 73), "password123"} {
		t.Run(strconv.Itoa(len(password))+" bytes", func(t *testing.T) {
			cfg := bootstrapTestConfig(t, pool)
			id, beforeHash := seedBootstrapAdmin(t, pool, cfg)
			cfg.AdminPassword = password
			calls := 0
			useBootstrapBreachStub(t, bootstrapBreachStub(password, 42, true, &calls))
			if err := bootstrapAdmin(context.Background(), pool, cfg); err != nil {
				t.Fatal(err)
			}
			var hash, email, role string
			var disabled, admin bool
			var sessions int
			if err := pool.QueryRow(context.Background(), `SELECT password_hash, email, role, disabled, is_admin,
				(SELECT count(*) FROM sessions WHERE user_id = $1) FROM users WHERE id = $1`, id).
				Scan(&hash, &email, &role, &disabled, &admin, &sessions); err != nil {
				t.Fatal(err)
			}
			if hash != beforeHash || !disabled || sessions != 1 || calls != 0 {
				t.Errorf("restart changed credentials: hash preserved=%v disabled=%v sessions=%d lookups=%d", hash == beforeHash, disabled, sessions, calls)
			}
			if email != cfg.AdminEmail || role != "admin" || !admin {
				t.Errorf("restart failed to refresh metadata: email=%s role=%s admin=%v", email, role, admin)
			}
		})
	}
}

func TestBootstrapAdminForcedRotationPolicyAndSessionRevocation(t *testing.T) {
	pool := bootstrapTestPool(t)
	cfg := bootstrapTestConfig(t, pool)
	id, beforeHash := seedBootstrapAdmin(t, pool, cfg)
	cfg.AdminPasswordForce = true
	for _, rejected := range []struct {
		password string
		breaches int
		offline  bool
	}{{password: "short"}, {password: strings.Repeat("x", 73)}, {password: "password123", breaches: 42}, {password: "test-offline-password", offline: true}} {
		cfg.AdminPassword = rejected.password
		calls := 0
		useBootstrapBreachStub(t, bootstrapBreachStub(rejected.password, rejected.breaches, rejected.offline, &calls))
		if err := bootstrapAdmin(context.Background(), pool, cfg); err == nil {
			t.Fatal("invalid forced password accepted")
		}
		var hash string
		var disabled bool
		var sessions int
		if err := pool.QueryRow(context.Background(), `SELECT password_hash, disabled, (SELECT count(*) FROM sessions WHERE user_id = $1) FROM users WHERE id = $1`, id).
			Scan(&hash, &disabled, &sessions); err != nil {
			t.Fatal(err)
		}
		if hash != beforeHash || !disabled || sessions != 1 {
			t.Errorf("rejected force mutated account: hash preserved=%v disabled=%v sessions=%d", hash == beforeHash, disabled, sessions)
		}
	}
	cfg.AdminPassword = "test-new-admin-password"
	calls := 0
	useBootstrapBreachStub(t, bootstrapBreachStub(cfg.AdminPassword, 0, false, &calls))
	if err := bootstrapAdmin(context.Background(), pool, cfg); err != nil {
		t.Fatal(err)
	}
	var hash, role string
	var disabled, admin bool
	var sessions int
	if err := pool.QueryRow(context.Background(), `SELECT password_hash, role, disabled, is_admin,
		(SELECT count(*) FROM sessions WHERE user_id = $1) FROM users WHERE id = $1`, id).
		Scan(&hash, &role, &disabled, &admin, &sessions); err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword(hash, cfg.AdminPassword) || hash == beforeHash || disabled || !admin || role != "admin" || sessions != 0 || calls != 1 {
		t.Errorf("forced rotation incorrect: new hash=%v disabled=%v admin=%v role=%s sessions=%d lookups=%d", hash != beforeHash, disabled, admin, role, sessions, calls)
	}
}
