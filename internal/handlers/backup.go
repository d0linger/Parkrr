package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/preining/parkrr/internal/backup"
)

// CreateBackup runs an encrypted pg_dump and streams it to the operator as a
// download (admin-only). The file is AES-256-GCM encrypted with PARKRR_BACKUP_KEY.
func (h *Handler) CreateBackup(w http.ResponseWriter, r *http.Request) {
	if h.BackupKey == "" {
		writeError(w, http.StatusServiceUnavailable, "backup is not configured (set PARKRR_BACKUP_KEY)")
		return
	}
	// pg_dump can take a while on a large DB; bound it generously.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	dump, err := backup.Dump(ctx, h.DatabaseURL)
	if err != nil {
		slog.Error("backup: pg_dump failed", "err", err)
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	enc, err := backup.Encrypt(dump, h.BackupKey)
	if err != nil {
		slog.Error("backup: encrypt failed", "err", err)
		writeError(w, http.StatusInternalServerError, "backup encryption failed")
		return
	}

	name := "parkrr-" + time.Now().Format("2006-01-02-150405") + ".dump.enc"
	h.audit(r, "backup", "system", 0, "created encrypted database backup ("+name+")")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(enc)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(enc)
}
