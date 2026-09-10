package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/preining/parkrr/internal/auth"
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics must remain bounded even for extension methods rejected by the limiter.
func TestMetricsMiddlewareBoundsArbitraryMethods(t *testing.T) {
	httpRequests.Reset()
	httpDuration.Reset()
	defer httpRequests.Reset()
	defer httpDuration.Reset()
	reg := prometheus.NewRegistry()
	reg.MustRegister(httpRequests, httpDuration)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	stop := make(chan struct{})
	defer close(stop)
	wrapped := metricsMiddleware(mux, rateLimit(&auth.Manager{}, 1, stop, mux))
	throttled := 0
	for i := 0; i < 200; i++ {
		r := httptest.NewRequest(fmt.Sprintf("CUSTOM%d", i), "http://localhost/", nil)
		r.RemoteAddr = "192.0.2.1:12345"
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			throttled++
		}
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 2 || throttled == 0 {
		t.Fatalf("missing metrics or limiter coverage: families=%d throttled=%d", len(families), throttled)
	}
	for _, f := range families {
		t.Logf("%s: %d unique label combinations (%d requests rate-limited)",
			f.GetName(), len(f.Metric), throttled)
		if len(f.Metric) > 2 {
			t.Errorf("untrusted HTTP methods created unbounded metric cardinality: %d", len(f.Metric))
		}
	}
}
