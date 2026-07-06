// Package server wires routes, middleware and static assets together.
package server

import (
	"context"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/handlers"
	"github.com/preining/parkrr/internal/models"
	"github.com/preining/parkrr/web"
)

// New builds the top-level HTTP handler with all routes registered. Background
// goroutines started here (rate-limiter cleanup, login-throttle cleanup) run
// until stop is closed.
func New(pool *pgxpool.Pool, authMgr *auth.Manager, rateLimitPerMin int, metricsToken string, checkBreachedPasswords bool, stop <-chan struct{}) (http.Handler, error) {
	h := handlers.New(pool)
	h.CheckBreachedPasswords = checkBreachedPasswords
	ah := handlers.NewAuthHandler(h, authMgr, stop)

	mux := http.NewServeMux()

	// Middleware shortcuts.
	authed := authMgr.RequireAuth
	admin := authMgr.RequireAdmin
	manager := authMgr.RequireRole(models.RoleManager)
	billing := authMgr.RequireRole(models.RoleManager, models.RoleAccounting)
	hf := func(f func(http.ResponseWriter, *http.Request)) http.Handler { return http.HandlerFunc(f) }

	// --- Auth (public) ---
	mux.HandleFunc("POST /api/auth/login", ah.Login)

	// --- Auth (protected) ---
	mux.Handle("POST /api/auth/logout", authed(hf(ah.Logout)))
	mux.Handle("GET /api/auth/me", authed(hf(ah.Me)))
	mux.Handle("POST /api/auth/change-password", authed(hf(ah.ChangePassword)))
	mux.Handle("GET /api/auth/sessions", authed(hf(ah.ListSessions)))
	mux.Handle("DELETE /api/auth/sessions/{handle}", authed(hf(ah.RevokeSession)))
	mux.Handle("POST /api/auth/sessions/revoke-others", authed(hf(ah.RevokeOtherSessions)))
	mux.Handle("POST /api/auth/2fa/setup", authed(hf(ah.TOTPSetup)))
	mux.Handle("POST /api/auth/2fa/enable", authed(hf(ah.TOTPEnable)))
	mux.Handle("POST /api/auth/2fa/disable", authed(hf(ah.TOTPDisable)))
	mux.Handle("GET /api/auth/2fa/backup-codes", authed(hf(ah.TOTPBackupCount)))
	mux.Handle("POST /api/auth/2fa/backup-codes/regenerate", authed(hf(ah.TOTPRegenerateBackup)))

	// --- Persons (read: any; write: manager/admin) ---
	mux.Handle("GET /api/persons", authed(hf(h.ListPersons)))
	mux.Handle("POST /api/persons", manager(hf(h.CreatePerson)))
	mux.Handle("PUT /api/persons/{id}", manager(hf(h.UpdatePerson)))
	mux.Handle("DELETE /api/persons/{id}", manager(hf(h.DeletePerson)))
	mux.Handle("GET /api/persons/{id}/stats", authed(hf(h.PersonStats)))
	mux.Handle("PUT /api/persons/{id}/flatrate", billing(hf(h.SetFlatRate)))
	mux.Handle("POST /api/persons/{id}/flatrate/paid", billing(hf(h.SetFlatRatePaid)))

	// --- Vehicles ---
	mux.Handle("GET /api/vehicles", authed(hf(h.ListVehicles)))
	mux.Handle("POST /api/vehicles", manager(hf(h.CreateVehicle)))
	mux.Handle("PUT /api/vehicles/{id}", manager(hf(h.UpdateVehicle)))
	mux.Handle("DELETE /api/vehicles/{id}", manager(hf(h.DeleteVehicle)))
	mux.Handle("POST /api/vehicles/{id}/status", manager(hf(h.ChangeVehicleStatus)))
	mux.Handle("POST /api/vehicles/{id}/paid", billing(hf(h.MarkPaid)))
	mux.Handle("POST /api/vehicles/{id}/duplicate", manager(hf(h.DuplicateVehicle)))
	mux.Handle("GET /api/vehicles/{id}/history", authed(hf(h.VehicleHistory)))

	// --- Vehicle photos ---
	mux.Handle("GET /api/vehicles/{id}/photos", authed(hf(h.ListPhotos)))
	mux.Handle("POST /api/vehicles/{id}/photos", manager(hf(h.UploadPhoto)))
	mux.Handle("GET /api/photos/{id}", authed(hf(h.GetPhoto)))
	mux.Handle("DELETE /api/photos/{id}", manager(hf(h.DeletePhoto)))

	// --- Categories (tariffs) ---
	mux.Handle("GET /api/categories", authed(hf(h.ListCategories)))
	mux.Handle("POST /api/categories", admin(hf(h.CreateCategory)))
	mux.Handle("PUT /api/categories/{id}", admin(hf(h.UpdateCategory)))
	mux.Handle("DELETE /api/categories/{id}", admin(hf(h.DeleteCategory)))

	// --- Service catalog ---
	mux.Handle("GET /api/services", authed(hf(h.ListServiceTypes)))
	mux.Handle("POST /api/services", admin(hf(h.CreateServiceType)))
	mux.Handle("PUT /api/services/{id}", admin(hf(h.UpdateServiceType)))
	mux.Handle("DELETE /api/services/{id}", admin(hf(h.DeleteServiceType)))

	// --- Charges ---
	mux.Handle("GET /api/charges", authed(hf(h.ListCharges)))
	mux.Handle("POST /api/charges", billing(hf(h.CreateCharge)))
	mux.Handle("DELETE /api/charges/{id}", billing(hf(h.DeleteCharge)))

	// --- Stats ---
	mux.Handle("GET /api/overview", authed(hf(h.Overview)))

	// --- Users & audit (admin only) ---
	mux.Handle("GET /api/users", admin(hf(h.ListUsers)))
	mux.Handle("POST /api/users", admin(hf(h.CreateUser)))
	mux.Handle("PUT /api/users/{id}", admin(hf(h.UpdateUser)))
	mux.Handle("DELETE /api/users/{id}", admin(hf(h.DeleteUser)))
	mux.Handle("POST /api/users/{id}/reset-2fa", admin(hf(h.ResetUserTOTP)))
	mux.Handle("GET /api/audit", admin(hf(h.ListAudit)))

	// --- Health, readiness and metrics ---
	registerObservability(mux, pool, metricsToken)

	// --- Static assets and SPA shell ---
	staticFS, err := fs.Sub(web.StaticFS, "static")
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(staticFS))

	indexHTML, err := web.StaticFS.ReadFile("static/index.html")
	if err != nil {
		return nil, err
	}
	swJS, err := web.StaticFS.ReadFile("static/sw.js")
	if err != nil {
		return nil, err
	}

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		// Service worker must be served from the root scope.
		if path == "sw.js" {
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Service-Worker-Allowed", "/")
			_, _ = w.Write(swJS)
			return
		}
		// Unknown API paths must not fall through to the SPA shell.
		if strings.HasPrefix(path, "api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		}
		// Serve real static files if they exist.
		if path != "" {
			if f, err := staticFS.Open(path); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// Otherwise serve the SPA shell.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(indexHTML)
	})

	// Middleware chain (outermost first): access log -> metrics -> rate limit ->
	// security headers -> routes.
	chain := securityHeaders(authMgr, mux)
	chain = rateLimit(authMgr, rateLimitPerMin, stop, chain)
	chain = metricsMiddleware(mux, chain)
	chain = requestLogger(authMgr, chain)
	return chain, nil
}

// securityHeaders wraps a handler with sensible default security headers.
// HSTS is only emitted when the original request is HTTPS.
func securityHeaders(authMgr *auth.Manager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy",
			"geolocation=(), camera=(), microphone=(), payment=(), usb=(), interest-cohort=()")
		csp := "default-src 'self'; img-src 'self' data:; style-src 'self'; " +
			"script-src 'self'; connect-src 'self'; manifest-src 'self'; " +
			"base-uri 'self'; form-action 'self'; frame-ancestors 'none'; " +
			"object-src 'none'; frame-src 'none'; child-src 'none'; worker-src 'self'"
		if authMgr.RequestIsHTTPS(r) {
			// Only over HTTPS: forcing upgrades on plain-HTTP local dev would break
			// same-origin API calls (upgraded to an https port that isn't served).
			csp += "; upgrade-insecure-requests"
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// StartSessionCleanup runs a background loop pruning expired sessions.
func StartSessionCleanup(authMgr *auth.Manager, stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			_ = authMgr.CleanupExpired(context.Background())
		}
	}
}
