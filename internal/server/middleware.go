package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/preining/parkrr/internal/auth"
)

// statusRecorder captures the response status code for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController find optional interfaces on the wrapped writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func requestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

// requestLogger assigns a request ID and logs one structured line per request.
func requestLogger(mgr *auth.Manager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := requestID()
		w.Header().Set("X-Request-ID", id)
		// Install a request-log record so auth middleware can add the user and any
		// handler can stash the 5xx cause. Die Anfrage-Kennung selbst wandert NICHT
		// hinein: sie steht im Antwortkopf (X-Request-ID) und wird unten aus der
		// lokalen Variablen geloggt — der Ablageplatz im Kontext hatte nie einen Leser.
		ctx := auth.WithRequestLog(r.Context())
		r = r.WithContext(ctx)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		attrs := []any{
			"id", id,
			// Portal tokens are no longer carried in the URL path (they moved to the
			// Authorization header, finding SEC-01), so the raw path is safe to log.
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"ip", mgr.ClientIP(r),
			"dur_ms", time.Since(start).Milliseconds(),
		}
		if user, uid := auth.RequestLogUser(ctx); user != "" {
			attrs = append(attrs, "user", user, "user_id", uid)
		}
		// Surface the underlying cause of a 5xx that a handler stashed (OPS-01), so a
		// DB fault is never invisible even where the handler wrote only a generic body.
		if err := auth.RequestError(ctx); err != nil {
			attrs = append(attrs, "err", err.Error())
		}
		// Escalate the level by status so a 500 doesn't read like a 200 (OPS-03).
		switch {
		// Der Client hat die Anfrage ABGEBROCHEN (weggeblättert, Tab zu, Reload
		// mittendrin). Der Handler sieht dann "context canceled" und quittiert 500,
		// aber es ist niemand mehr da, dem geantwortet würde — das als Serverfehler zu
		// protokollieren erzeugt genau das Rauschen, in dem ein echter 500 untergeht.
		// NUR Canceled: ein DeadlineExceeded ist unsere eigene Zeitgrenze und bleibt
		// ein Fehler (Hundert 01, gefunden beim Messen des a11y-Laufs).
		case errors.Is(ctx.Err(), context.Canceled):
			slog.Info("request aborted by client", attrs...)
		case rec.status >= 500:
			slog.Error("request", attrs...)
		case rec.status >= 400:
			slog.Warn("request", attrs...)
		default:
			slog.Info("request", attrs...)
		}
	})
}

// ipLimiter is a per-IP token-bucket rate limiter.
type ipLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	capacity float64
	refill   float64 // tokens per second
	// aggBuckets counts the coarse-prefix buckets (aggKeyPrefix) in buckets.
	aggBuckets int
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newIPLimiter(perMin int) *ipLimiter {
	return &ipLimiter{
		buckets:  make(map[string]*bucket),
		capacity: float64(perMin),
		refill:   float64(perMin) / 60.0,
	}
}

// maxRateBuckets deckelt die Bucket-Tabelle (PRT-04). Ein Eintrag lebt nach
// seiner letzten Anfrage noch mindestens zehn Minuten (cleanup); ohne Deckel
// wächst die Tabelle mit jeder neuen Quelladresse, bis der Speicher ausgeht.
const maxRateBuckets = 50000

// Ist die Tabelle voll, bekommen NEUE Schlüssel keinen eigenen Bucket mehr,
// sondern teilen sich einen gröberen: IPv6 je /48, IPv4 je /16. So trifft eine
// Flut aus einem Netz nur dieses Netz, nicht jeden neuen Kunden. Diese
// Sammel-Buckets sind ihrerseits auf maxAggregateBuckets gedeckelt; erst danach
// teilen sich alle übrigen Neuen den einen overflowBucketKey. Bestehende Clients
// behalten ihren eigenen Bucket.
const (
	aggKeyPrefix        = "agg:"
	maxAggregateBuckets = 1024
	overflowBucketKey   = "overflow"
)

// aggregateKey bildet eine Adresse auf ihr grobes Netz ab (IPv6 /48, IPv4 /16).
func aggregateKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	addr = addr.WithZone("").Unmap()
	bits := 48
	if addr.Is4() {
		bits = 16
	}
	pfx, err := addr.Prefix(bits)
	if err != nil {
		return ""
	}
	return aggKeyPrefix + pfx.String()
}

// rateLimitKey bildet die Client-Adresse auf ihren Bucket ab. IPv4 (auch als
// ::ffff:a.b.c.d) bleibt die volle Adresse; IPv6 wird auf das /64 gekürzt, weil
// ein einzelner Anschluss üblicherweise ein ganzes /64 bekommt und daraus beliebig
// viele Quelladressen wählen kann — pro Adresse ein eigener, voller Bucket hob die
// Grenze für jeden IPv6-Client auf (PRT-04). Nicht parsbare Werte bleiben
// unverändert.
func rateLimitKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is4() {
		return addr.String()
	}
	pfx, err := addr.Prefix(64)
	if err != nil {
		return ip
	}
	return pfx.String()
}

func (l *ipLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	key := rateLimitKey(ip)
	b := l.buckets[key]
	if b == nil && len(l.buckets) >= maxRateBuckets {
		key = overflowBucketKey
		if agg := aggregateKey(ip); agg != "" {
			if ab := l.buckets[agg]; ab != nil || l.aggBuckets < maxAggregateBuckets {
				key, b = agg, ab
			}
		}
		if key == overflowBucketKey {
			b = l.buckets[key]
		}
	}
	if b == nil {
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[key] = b
		if strings.HasPrefix(key, aggKeyPrefix) {
			l.aggBuckets++
		}
	}
	b.tokens += now.Sub(b.last).Seconds() * l.refill
	if b.tokens > l.capacity {
		b.tokens = l.capacity
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// cleanup drops idle buckets so the map does not grow unbounded.
func (l *ipLimiter) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-10 * time.Minute)
	for ip, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, ip)
			if strings.HasPrefix(ip, aggKeyPrefix) {
				l.aggBuckets--
			}
		}
	}
}

// rateLimit wraps a handler with per-IP throttling. perMin <= 0 disables it.
// The background bucket-cleanup goroutine runs until stop is closed.
func rateLimit(mgr *auth.Manager, perMin int, stop <-chan struct{}, next http.Handler) http.Handler {
	if perMin <= 0 {
		return next
	}
	lim := newIPLimiter(perMin)
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				lim.cleanup()
			}
		}
	}()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !lim.allow(mgr.ClientIP(r)) {
			w.Header().Set("Retry-After", "10")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
