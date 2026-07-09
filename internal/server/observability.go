package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/preining/parkrr/internal/database"
	"github.com/preining/parkrr/internal/handlers"
)

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Build-time version, injected via -ldflags "-X ...Version=...". Surfaced at
// /healthz so a deployment can confirm exactly what is running.
var Version = "dev"

var (
	httpRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "parkrr_http_requests_total",
			Help: "Total HTTP requests by route, method and status.",
		},
		[]string{"route", "method", "status"},
	)
	httpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "parkrr_http_request_duration_seconds",
			Help:    "HTTP request latency by route and method.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"route", "method"},
	)
)

// registerMetrics registers all collectors on a fresh registry and returns it.
// A dedicated registry (rather than the global default) keeps the metrics
// deterministic and avoids duplicate-registration panics in tests.
func registerMetrics(pool *pgxpool.Pool) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		httpRequests,
		httpDuration,
		collectors(pool),
	)
	return reg
}

// collectors exposes pgx pool statistics as gauges.
func collectors(pool *pgxpool.Pool) prometheus.Collector {
	return prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "parkrr_db_pool_acquired_conns",
			Help: "Currently acquired database connections.",
		},
		func() float64 { return float64(pool.Stat().AcquiredConns()) },
	)
}

// metricsMiddleware records request count and latency, labeled by the matched
// route pattern (low cardinality — path IDs are collapsed to "{id}").
func metricsMiddleware(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := "other"
		if _, pattern := mux.Handler(r); pattern != "" {
			route = pattern
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		httpDuration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
		httpRequests.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()
	})
}

// health wires liveness, readiness and metrics endpoints onto the mux.
// metricsToken, when non-empty, is required as a Bearer token to scrape
// /metrics; when empty the endpoint is open (suitable for a private network).
func registerObservability(mux *http.ServeMux, pool *pgxpool.Pool, metricsToken string) {
	// Liveness: the process is up. Never touches the database.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSONStatus(w, http.StatusOK, map[string]string{"status": "ok", "version": Version})
	})

	// Readiness: can we serve traffic? Pings the database.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			writeJSONStatus(w, http.StatusServiceUnavailable,
				map[string]string{"status": "unavailable", "reason": "database unreachable"})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	reg := registerMetrics(pool)
	promHandler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	mux.Handle("GET /metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if metricsToken != "" && r.Header.Get("Authorization") != "Bearer "+metricsToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		promHandler.ServeHTTP(w, r)
	}))
}

// startFlatRateArchival runs a background loop that archives vehicles whose
// Pauschalen have all ended AND been fully paid, tidying finished-and-settled
// agreements out of the active lists. It sweeps once at startup and hourly.
func startFlatRateArchival(h *handlers.Handler, stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	sweep := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if n, err := h.ArchiveSettledExpiredVehicles(ctx); err != nil {
			slog.Warn("flat-rate archival failed", "err", err)
		} else if n > 0 {
			slog.Info("archived vehicles of finished Pauschalen", "count", n)
		}
	}
	sweep() // once at startup
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			sweep()
		}
	}
}

// StartAuditRetention periodically prunes audit entries older than keep. It runs
// until stop is closed. keep <= 0 disables retention (keep forever).
func StartAuditRetention(pool *pgxpool.Pool, keep time.Duration, stop <-chan struct{}) {
	if keep <= 0 {
		return
	}
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	prune := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = database.PruneAuditLog(ctx, pool, keep)
	}
	prune() // once at startup
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			prune()
		}
	}
}
