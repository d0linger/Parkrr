package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/database"
)

// TestCSRFOK covers the signed double-submit check in isolation (no DB): the header
// must equal HMAC(csrfKey, session token) — a matching cookie alone is not enough,
// and the session cookie must be present. csrfToken is deterministic even with a nil
// key, so a bare Manager suffices.
func TestCSRFOK(t *testing.T) {
	m := &Manager{}
	const token = "session-token-abcdef"
	valid := m.csrfToken(token)
	cases := []struct {
		name       string
		hdr        string
		sessCookie string // "" = no session cookie
		want       bool
	}{
		{name: "valid signed token", hdr: valid, sessCookie: token, want: true},
		{name: "wrong header", hdr: "deadbeef", sessCookie: token, want: false},
		{name: "empty header", hdr: "", sessCookie: token, want: false},
		{name: "no session cookie", hdr: valid, sessCookie: "", want: false},
		{name: "token for a different session", hdr: m.csrfToken("other-token"), sessCookie: token, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/x", nil)
			if tc.sessCookie != "" {
				r.AddCookie(&http.Cookie{Name: SessionCookie, Value: tc.sessCookie})
			}
			if tc.hdr != "" {
				r.Header.Set(CSRFHeader, tc.hdr)
			}
			if got := m.csrfOK(r); got != tc.want {
				t.Errorf("csrfOK = %v, want %v", got, tc.want)
			}
		})
	}
}

func testAuthManager(t *testing.T) (*Manager, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set; skipping auth middleware integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("migrate: %v", err)
	}
	m, err := NewManager(pool, SessionConfig{MaxAge: 3600}, false, false, "a-sufficiently-long-test-secret")
	if err != nil {
		pool.Close()
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE username LIKE 'mwtest-%'`)
		pool.Close()
	})
	return m, pool
}

func mkAuthUser(t *testing.T, pool *pgxpool.Pool, role string, admin bool) int64 {
	t.Helper()
	var id int64
	uname := "mwtest-" + role + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, email, password_hash, role, is_admin) VALUES ($1,'','x',$2,$3) RETURNING id`,
		uname, role, admin).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// sessionCookies creates a real session for userID and returns its cookies.
func sessionCookies(t *testing.T, m *Manager, userID int64) []*http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if err := m.CreateSession(context.Background(), rec, req, userID); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return rec.Result().Cookies()
}

func withCookies(req *http.Request, cookies []*http.Cookie) *http.Request {
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return req
}

func csrfValue(cookies []*http.Cookie) string {
	for _, c := range cookies {
		if c.Name == CSRFCookie {
			return c.Value
		}
	}
	return ""
}

// ok200 is a trivial next-handler that proves the middleware let the request through.
func ok200() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

func TestRequireAuth(t *testing.T) {
	m, pool := testAuthManager(t)
	uid := mkAuthUser(t, pool, "editor", false)
	cookies := sessionCookies(t, m, uid)
	h := m.RequireAuth(ok200())

	// Anonymous GET → 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous GET: want 401, got %d", rec.Code)
	}

	// Valid session GET → 200.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, withCookies(httptest.NewRequest(http.MethodGet, "/api/x", nil), cookies))
	if rec.Code != http.StatusOK {
		t.Errorf("authed GET: want 200, got %d", rec.Code)
	}

	// State-changing without CSRF header → 403.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, withCookies(httptest.NewRequest(http.MethodPost, "/api/x", nil), cookies))
	if rec.Code != http.StatusForbidden {
		t.Errorf("authed POST without CSRF: want 403, got %d", rec.Code)
	}

	// State-changing WITH matching CSRF header → 200.
	rec = httptest.NewRecorder()
	req := withCookies(httptest.NewRequest(http.MethodPost, "/api/x", nil), cookies)
	req.Header.Set(CSRFHeader, csrfValue(cookies))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("authed POST with CSRF: want 200, got %d", rec.Code)
	}
}

func TestRequireAdminAndRole(t *testing.T) {
	m, pool := testAuthManager(t)
	admin := sessionCookies(t, m, mkAuthUser(t, pool, "admin", true))
	editor := sessionCookies(t, m, mkAuthUser(t, pool, "editor", false))
	reader := sessionCookies(t, m, mkAuthUser(t, pool, "reader", false))

	// RequireAdmin: admin passes, editor 403, anonymous 401.
	adminH := m.RequireAdmin(ok200())
	check := func(name string, h http.Handler, cookies []*http.Cookie, want int) {
		rec := httptest.NewRecorder()
		var req *http.Request
		if cookies == nil {
			req = httptest.NewRequest(http.MethodGet, "/api/x", nil)
		} else {
			req = withCookies(httptest.NewRequest(http.MethodGet, "/api/x", nil), cookies)
		}
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: want %d, got %d", name, want, rec.Code)
		}
	}
	check("admin→admin", adminH, admin, http.StatusOK)
	check("editor→admin", adminH, editor, http.StatusForbidden)
	check("anon→admin", adminH, nil, http.StatusUnauthorized)

	// RequireRole(editor): editor passes, reader 403, admin passes (admins always).
	roleH := m.RequireRole("editor")(ok200())
	check("editor→editorRole", roleH, editor, http.StatusOK)
	check("reader→editorRole", roleH, reader, http.StatusForbidden)
	check("admin→editorRole", roleH, admin, http.StatusOK)
}
