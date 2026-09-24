package restorectl

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func (c *Controller) ServeStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	id := strings.TrimPrefix(r.URL.Path, "/api/restore/status/")
	if r.Method != http.MethodGet || len(id) != 32 {
		writeStatusError(w, http.StatusNotFound, "not found")
		return
	}
	if _, err := hex.DecodeString(id); err != nil {
		writeStatusError(w, http.StatusNotFound, "not found")
		return
	}
	if !c.acquireStatusSlot(time.Now()) {
		w.Header().Set("Retry-After", "1")
		writeStatusError(w, http.StatusTooManyRequests, "status busy")
		return
	}
	defer c.releaseStatusSlot()
	job, err := c.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeStatusError(w, http.StatusNotFound, "not found")
			return
		}
		writeStatusError(w, http.StatusServiceUnavailable, "status unavailable")
		return
	}
	_ = json.NewEncoder(w).Encode(job)
}

func writeStatusError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
