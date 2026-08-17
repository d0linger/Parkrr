package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// ClientError records a browser-side JavaScript error reported by the SPA
// (window.onerror / unhandledrejection). It is deliberately minimal: it accepts a
// small JSON body, truncates every field, logs it with NO PII (no cookies, no
// headers, no request body echo), and returns 204. The global rate limiter and
// request-body limit already bound abuse; the MaxBytesReader is a second guard.
func (h *Handler) ClientError(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Message string `json:"message"`
		Stack   string `json:"stack"`
		URL     string `json:"url"`
		Version string `json:"version"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	msg := truncRunes(in.Message, 500)
	if msg == "" {
		writeError(w, http.StatusBadRequest, "message required")
		return
	}
	slog.Warn("client-error",
		"message", msg,
		"stack", truncRunes(in.Stack, 4000),
		"url", truncRunes(in.URL, 500),
		"version", truncRunes(in.Version, 40),
	)
	w.WriteHeader(http.StatusNoContent)
}
