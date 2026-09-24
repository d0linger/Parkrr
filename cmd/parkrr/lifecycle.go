package main

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
	"sync"

	"github.com/preining/parkrr/internal/restorectl"
	"github.com/preining/parkrr/internal/server"
)

// lifecycleHandler keeps the listener alive while the disposable application
// generation is drained for a restore. The mutex closes the admission gate before
// waiting, so no request can increment the in-flight count after draining starts.
type lifecycleHandler struct {
	mu          sync.Mutex
	active      http.Handler
	maintenance bool
	inFlight    int
	drained     chan struct{}
	drainClosed bool
	restores    *restorectl.Controller
}

func newLifecycleHandler(restores *restorectl.Controller) *lifecycleHandler {
	return &lifecycleHandler{restores: restores}
}

func (h *lifecycleHandler) Activate(next http.Handler) {
	h.mu.Lock()
	h.active = next
	h.maintenance = false
	h.drained = nil
	h.drainClosed = false
	h.mu.Unlock()
}

func (h *lifecycleHandler) EnterMaintenance(ctx context.Context, _ string) error {
	h.mu.Lock()
	if !h.maintenance {
		h.maintenance = true
		h.drained = make(chan struct{})
		if h.inFlight == 0 {
			close(h.drained)
			h.drainClosed = true
		}
	}
	drained := h.drained
	h.mu.Unlock()
	if drained == nil {
		return nil
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *lifecycleHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/restore/status/") && h.restores != nil {
		h.restores.ServeStatus(w, r)
		return
	}

	h.mu.Lock()
	if h.maintenance || h.active == nil {
		h.mu.Unlock()
		h.serveMaintenance(w, r)
		return
	}
	next := h.active
	h.inFlight++
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		h.inFlight--
		if h.maintenance && h.inFlight == 0 && !h.drainClosed && h.drained != nil {
			close(h.drained)
			h.drainClosed = true
		}
		h.mu.Unlock()
	}()
	next.ServeHTTP(w, r)
}

func (h *lifecycleHandler) serveMaintenance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.URL.Path == "/healthz" {
		writeLifecycleJSON(w, http.StatusOK, map[string]string{
			"status": "maintenance", "version": server.Version,
		})
		return
	}
	if r.URL.Path == "/readyz" || strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Retry-After", "5")
		writeLifecycleJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "maintenance",
		})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = maintenancePage.Execute(w, nil)
}

func writeLifecycleJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

var maintenancePage = template.Must(template.New("maintenance").Parse(`<!doctype html>
<html lang="de"><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<meta http-equiv="refresh" content="5">
<title>Parkrr wird wiederhergestellt</title>
<style>body{font:16px/1.5 system-ui;margin:0;background:#0b1520;color:#e8f1f5;display:grid;min-height:100vh;place-items:center}main{max-width:38rem;padding:2rem}code{color:#5ed4c9}</style>
<main><h1>Wiederherstellung läuft</h1><p>Parkrr hat den normalen Betrieb angehalten. Datenbank, Migrationen und Sitzungen werden sicher geprüft.</p><p>Diese Seite lädt automatisch neu, sobald die Anwendung wieder bereit ist.</p></main>
</html>`))
