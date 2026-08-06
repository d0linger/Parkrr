// Package server wires routes, middleware and static assets together.
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/backup"
	"github.com/preining/parkrr/internal/handlers"
	"github.com/preining/parkrr/internal/models"
	"github.com/preining/parkrr/web"
)

// New builds the top-level HTTP handler with all routes registered. Background
// goroutines started here (rate-limiter cleanup, login-throttle cleanup) run
// until stop is closed.
func New(pool *pgxpool.Pool, authMgr *auth.Manager, wa *auth.WebAuthnService, rateLimitPerMin int, metricsToken string, checkBreachedPasswords, failClosedOnBreach bool, backupKey, dbURL, backupDir string, s3 backup.S3Config, stop <-chan struct{}) (http.Handler, error) {
	h := handlers.New(pool)
	h.CheckBreachedPasswords = checkBreachedPasswords
	h.FailClosedOnBreach = failClosedOnBreach
	h.BackupKey = backupKey
	h.DatabaseURL = dbURL
	h.BackupDir = backupDir
	h.S3 = s3
	ah := handlers.NewAuthHandler(h, authMgr, wa, stop)

	// Archive vehicles of finished-and-settled Pauschalen in the background.
	go startFlatRateArchival(h, stop)

	// Idempotent one-shot: book real Zahlungseingänge for Pauschale/Nebenkosten
	// period settlements made before migration 036 (they only flipped an off-book
	// flag). Runs in the background so a large dataset never delays serving.
	go func() {
		if err := h.BackfillPeriodPayments(context.Background()); err != nil {
			slog.Error("period-payment backfill failed", "err", err)
		}
	}()

	mux := http.NewServeMux()

	// Middleware shortcuts.
	authed := authMgr.RequireAuth
	admin := authMgr.RequireAdmin
	// Editors may do everything except user management and the audit log
	// (admins always satisfy RequireRole as well).
	editor := authMgr.RequireRole(models.RoleEditor)
	hf := func(f func(http.ResponseWriter, *http.Request)) http.Handler { return http.HandlerFunc(f) }

	// --- Auth (public) ---
	mux.HandleFunc("POST /api/auth/login", ah.Login)
	mux.HandleFunc("GET /api/auth/capabilities", ah.Capabilities)
	mux.HandleFunc("POST /api/auth/passkey/login/begin", ah.PasskeyLoginBegin)
	mux.HandleFunc("POST /api/auth/passkey/login/finish", ah.PasskeyLoginFinish)

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

	// --- Passkeys / WebAuthn (protected: manage your own) ---
	mux.Handle("GET /api/passkeys", authed(hf(ah.ListPasskeys)))
	mux.Handle("POST /api/passkeys/register/begin", authed(hf(ah.PasskeyRegisterBegin)))
	mux.Handle("POST /api/passkeys/register/finish", authed(hf(ah.PasskeyRegisterFinish)))
	mux.Handle("DELETE /api/passkeys/{id}", authed(hf(ah.DeletePasskey)))

	// --- Persons (read: any; write: manager/admin) ---
	mux.Handle("GET /api/persons", authed(hf(h.ListPersons)))
	mux.Handle("GET /api/persons/outstanding", authed(hf(h.OutstandingByPerson)))
	mux.Handle("POST /api/persons", editor(hf(h.CreatePerson)))
	mux.Handle("PUT /api/persons/{id}", editor(hf(h.UpdatePerson)))
	mux.Handle("DELETE /api/persons/{id}", editor(hf(h.DeletePerson)))
	mux.Handle("GET /api/persons/{id}/stats", authed(hf(h.PersonStats)))

	// --- Payments (recorded money-in / Kontoauszug) ---
	mux.Handle("GET /api/persons/{id}/payments", authed(hf(h.ListPayments)))
	mux.Handle("POST /api/persons/{id}/payments", editor(hf(h.CreatePayment)))
	mux.Handle("DELETE /api/payments/{id}", editor(hf(h.DeletePayment)))
	mux.Handle("GET /api/persons/{id}/open-items", authed(hf(h.OpenItems)))
	mux.Handle("POST /api/persons/{id}/apply-credit", editor(hf(h.ApplyCredit)))

	// --- Invoicing (Rechnungen) ---
	mux.Handle("GET /api/billing/settings", admin(hf(h.GetBillingSettings)))
	mux.Handle("POST /api/billing/settings", admin(hf(h.SaveBillingSettings)))
	mux.Handle("GET /api/persons/{id}/invoices", authed(hf(h.ListInvoices)))
	mux.Handle("POST /api/persons/{id}/invoices", editor(hf(h.CreateInvoice)))
	mux.Handle("GET /api/invoices/{id}", authed(hf(h.GetInvoice)))
	mux.Handle("POST /api/invoices/{id}/cancel", editor(hf(h.CancelInvoice)))
	mux.Handle("POST /api/persons/{id}/pay-invoices", editor(hf(h.PayInvoices)))
	mux.Handle("GET /api/invoices/overdue", authed(hf(h.OverdueInvoices)))

	// --- Flat-rate agreements (Pauschale-Einträge) ---
	mux.Handle("GET /api/persons/{id}/agreements", authed(hf(h.ListAgreements)))
	mux.Handle("POST /api/persons/{id}/agreements", editor(hf(h.CreateAgreement)))
	mux.Handle("PUT /api/agreements/{id}", editor(hf(h.UpdateAgreement)))
	mux.Handle("DELETE /api/agreements/{id}", editor(hf(h.DeleteAgreement)))
	mux.Handle("POST /api/agreements/{id}/paid", editor(hf(h.SetAgreementPaid)))
	mux.Handle("POST /api/agreements/{id}/period-paid", editor(hf(h.SetAgreementPeriodPaid)))

	// --- Recurring extra costs (Wiederkehrende Nebenkosten) ---
	mux.Handle("GET /api/persons/{id}/recurring", authed(hf(h.ListRecurringCharges)))
	mux.Handle("POST /api/persons/{id}/recurring", editor(hf(h.CreateRecurringCharge)))
	mux.Handle("PUT /api/recurring/{id}", editor(hf(h.UpdateRecurringCharge)))
	mux.Handle("DELETE /api/recurring/{id}", editor(hf(h.DeleteRecurringCharge)))
	mux.Handle("POST /api/recurring/{id}/paid", editor(hf(h.SetRecurringChargePaid)))
	mux.Handle("POST /api/recurring/{id}/period-paid", editor(hf(h.SetRecurringChargePeriodPaid)))

	// --- Vehicles ---
	mux.Handle("GET /api/vehicles", authed(hf(h.ListVehicles)))
	mux.Handle("POST /api/vehicles", editor(hf(h.CreateVehicle)))
	mux.Handle("PUT /api/vehicles/{id}", editor(hf(h.UpdateVehicle)))
	mux.Handle("DELETE /api/vehicles/{id}", editor(hf(h.DeleteVehicle)))
	mux.Handle("POST /api/vehicles/{id}/status", editor(hf(h.ChangeVehicleStatus)))
	mux.Handle("POST /api/vehicles/{id}/paid", editor(hf(h.MarkPaid)))
	mux.Handle("POST /api/vehicles/{id}/reactivate", editor(hf(h.ReactivateVehicle)))
	mux.Handle("POST /api/vehicles/{id}/duplicate", editor(hf(h.DuplicateVehicle)))
	mux.Handle("GET /api/vehicles/{id}/history", authed(hf(h.VehicleHistory)))

	// --- Vehicle photos ---
	mux.Handle("GET /api/vehicles/{id}/photos", authed(hf(h.ListPhotos)))
	mux.Handle("POST /api/vehicles/{id}/photos", editor(hf(h.UploadPhoto)))
	mux.Handle("GET /api/photos/{id}", authed(hf(h.GetPhoto)))
	mux.Handle("DELETE /api/photos/{id}", editor(hf(h.DeletePhoto)))

	// --- Categories (tariffs) ---
	mux.Handle("GET /api/categories", authed(hf(h.ListCategories)))
	mux.Handle("POST /api/categories", editor(hf(h.CreateCategory)))
	mux.Handle("PUT /api/categories/{id}", editor(hf(h.UpdateCategory)))
	mux.Handle("POST /api/categories/{id}/archived", editor(hf(h.SetCategoryArchived)))
	mux.Handle("DELETE /api/categories/{id}", editor(hf(h.DeleteCategory)))

	// --- Service catalog ---
	mux.Handle("GET /api/services", authed(hf(h.ListServiceTypes)))
	mux.Handle("POST /api/services", editor(hf(h.CreateServiceType)))
	mux.Handle("PUT /api/services/{id}", editor(hf(h.UpdateServiceType)))
	mux.Handle("POST /api/services/{id}/archived", editor(hf(h.SetServiceArchived)))
	mux.Handle("DELETE /api/services/{id}", editor(hf(h.DeleteServiceType)))

	// --- Charges ---
	mux.Handle("GET /api/charges", authed(hf(h.ListCharges)))
	mux.Handle("POST /api/charges", editor(hf(h.CreateCharge)))
	mux.Handle("PUT /api/charges/{id}", editor(hf(h.UpdateCharge)))
	mux.Handle("POST /api/charges/{id}/paid", editor(hf(h.SetChargePaid)))
	mux.Handle("DELETE /api/charges/{id}", editor(hf(h.DeleteCharge)))

	// --- Stats ---
	mux.Handle("GET /api/overview", authed(hf(h.Overview)))

	// --- Users & audit (admin only) ---
	mux.Handle("GET /api/users", admin(hf(h.ListUsers)))
	mux.Handle("POST /api/users", admin(hf(h.CreateUser)))
	mux.Handle("PUT /api/users/{id}", admin(hf(h.UpdateUser)))
	mux.Handle("DELETE /api/users/{id}", admin(hf(h.DeleteUser)))
	mux.Handle("POST /api/users/{id}/reset-2fa", admin(hf(h.ResetUserTOTP)))
	mux.Handle("GET /api/audit", admin(hf(h.ListAudit)))
	mux.Handle("POST /api/backup", admin(hf(h.CreateBackup)))
	mux.Handle("GET /api/backup/status", admin(hf(h.BackupStatus)))
	mux.Handle("POST /api/backup/schedule", admin(hf(h.SaveBackupSchedule)))
	mux.Handle("POST /api/backup/run", admin(hf(h.RunScheduledBackup)))
	mux.Handle("GET /api/backup/file/{name}", admin(hf(h.BackupDownloadFile)))
	mux.Handle("POST /api/backup/validate", admin(hf(h.BackupValidate)))
	mux.Handle("POST /api/backup/restore", admin(hf(h.BackupRestore)))
	mux.Handle("POST /api/backup/s3/test", admin(hf(h.BackupS3Test)))
	mux.Handle("POST /api/backup/s3", admin(hf(h.CreateBackupS3)))
	mux.Handle("GET /api/backup/s3/file/{name}", admin(hf(h.BackupS3Download)))
	mux.Handle("POST /api/backup/restore-s3", admin(hf(h.BackupRestoreS3)))

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
	// Asset fingerprinting: append a content hash to the JS/CSS references so a
	// changed asset gets a new URL (cache-bust) while unchanged assets stay
	// cacheable forever. Hashes are computed once from the embedded files.
	indexHTML = fingerprintAsset(indexHTML, staticFS, "/js/app.js", "js/app.js")
	indexHTML = fingerprintAsset(indexHTML, staticFS, "/css/style.css", "css/style.css")

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
				// Fingerprinted assets (carrying ?v=) are immutable: safe to cache
				// aggressively because the URL changes whenever the content does.
				if r.URL.Query().Get("v") != "" {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		// Otherwise serve the SPA shell.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(indexHTML)
	})

	return buildChain(authMgr, mux, rateLimitPerMin, stop), nil
}

// buildChain assembles the middleware stack around the router (outermost first:
// access log -> metrics -> rate limit -> security headers -> body limit ->
// routes). Extracted from New so the wiring — including the request-body limit —
// is testable without a live database pool.
func buildChain(authMgr *auth.Manager, mux *http.ServeMux, rateLimitPerMin int, stop <-chan struct{}) http.Handler {
	// recoverPanics wraps the router directly (innermost) so a handler panic is
	// turned into a normal 500 return — which the outer metrics/log middleware
	// then record — instead of an unlogged, unmeasured dropped connection.
	chain := securityHeaders(authMgr, limitRequestBody(recoverPanics(mux)))
	chain = rateLimit(authMgr, rateLimitPerMin, stop, chain)
	chain = metricsMiddleware(mux, chain)
	chain = requestLogger(authMgr, chain)
	return chain
}

// recoverPanics converts a handler panic into a logged 500 (with stack + request
// context) instead of a silently aborted connection that emits no log or metric.
// It only emits the 500 body when the handler hasn't already started the response
// (a panic mid-stream, e.g. in streamBackup, must not append a JSON error or
// re-send headers). Unwrap() exposes the underlying writer so http.Response
// Controller can still reach optional interfaces (Flusher/Hijacker).
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &panicResponseWriter{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered in handler", "err", rec,
					"method", r.Method, "path", r.URL.Path, "stack", string(debug.Stack()))
				if !rw.started {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal server error"}`))
				}
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

// panicResponseWriter tracks whether the response has started so recoverPanics
// can tell an unstarted request (safe to send a 500) from one already streaming.
type panicResponseWriter struct {
	http.ResponseWriter
	started bool
}

func (w *panicResponseWriter) WriteHeader(code int) {
	w.started = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *panicResponseWriter) Write(b []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController find optional interfaces on the wrapped writer.
func (w *panicResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// maxRequestBody caps every request body as a DoS backstop. It sits ABOVE the
// 8 MiB photo-upload cap (handlers.maxPhotoBytes) so legitimate uploads still
// pass, while JSON bodies stay further limited to 1 MiB in decodeJSON. A request
// that declares more is rejected with 413 before any read; MaxBytesReader caps
// the actual read for chunked/undeclared bodies.
const maxRequestBody = 9 << 20 // 9 MiB

// limitRequestBody rejects over-large request bodies with 413 and caps the read.
func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxRequestBody {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		}
		next.ServeHTTP(w, r)
	})
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

// fingerprintAsset appends "?v=<hash>" to every occurrence of ref in html,
// where hash is a short content hash of the embedded file at fsPath. If the
// file cannot be read, html is returned unchanged.
func fingerprintAsset(html []byte, fsys fs.FS, ref, fsPath string) []byte {
	b, err := fs.ReadFile(fsys, fsPath)
	if err != nil {
		return html
	}
	sum := sha256.Sum256(b)
	v := hex.EncodeToString(sum[:])[:10]
	return bytes.ReplaceAll(html, []byte(ref+`"`), []byte(ref+`?v=`+v+`"`))
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
