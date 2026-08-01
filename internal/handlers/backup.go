package handlers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/preining/parkrr/internal/backup"
)

// CreateBackup runs an encrypted pg_dump and streams it to the operator as a
// download (admin-only). Encrypted with PARKRR_BACKUP_KEY (AES-256-GCM).
func (h *Handler) CreateBackup(w http.ResponseWriter, r *http.Request) {
	if h.BackupKey == "" {
		writeError(w, http.StatusServiceUnavailable, "backup is not configured (set PARKRR_BACKUP_KEY)")
		return
	}
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
	streamBackup(w, name, enc)
}

// BackupStatus reports whether backups are configured and lists the scheduled
// files in the backup directory (newest first).
func (h *Handler) BackupStatus(w http.ResponseWriter, r *http.Request) {
	type file struct {
		Name     string    `json:"name"`
		Size     int64     `json:"size"`
		Modified time.Time `json:"modified"`
	}
	resp := struct {
		Enabled   bool   `json:"enabled"`   // PARKRR_BACKUP_KEY is set
		Scheduled bool   `json:"scheduled"` // a backup directory is configured
		Dir       string `json:"dir"`
		Files     []file `json:"files"`
	}{Enabled: h.BackupKey != "", Scheduled: h.BackupDir != "", Dir: h.BackupDir, Files: []file{}}

	if h.BackupDir != "" {
		matches, _ := filepath.Glob(filepath.Join(h.BackupDir, "parkrr-*.dump.enc"))
		sort.Sort(sort.Reverse(sort.StringSlice(matches))) // timestamped -> newest first
		for _, p := range matches {
			if fi, err := os.Stat(p); err == nil {
				resp.Files = append(resp.Files, file{Name: filepath.Base(p), Size: fi.Size(), Modified: fi.ModTime()})
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// safeBackupName guards against path traversal: base name only, expected shape.
func safeBackupName(name string) bool {
	return name == filepath.Base(name) && name != "" &&
		strings.HasPrefix(name, "parkrr-") && strings.HasSuffix(name, ".dump.enc")
}

// BackupDownloadFile serves one scheduled backup from the backup directory.
func (h *Handler) BackupDownloadFile(w http.ResponseWriter, r *http.Request) {
	if h.BackupDir == "" {
		writeError(w, http.StatusNotFound, "no scheduled backup directory configured")
		return
	}
	name := r.PathValue("name")
	if !safeBackupName(name) {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	data, err := os.ReadFile(filepath.Join(h.BackupDir, name)) //nolint:gosec // name validated by safeBackupName
	if err != nil {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	streamBackup(w, name, data)
}

// BackupValidate decrypts + inspects an uploaded backup (no DB change), so the
// operator can confirm the key is right and the archive is intact before a restore.
func (h *Handler) BackupValidate(w http.ResponseWriter, r *http.Request) {
	enc, key, err := readBackupUpload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := backup.Validate(enc, key)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// BackupRestore restores an uploaded backup into the live database. DESTRUCTIVE:
// requires confirm=RESTORE and validates the archive first. The restore itself is
// atomic (pg_restore --single-transaction): a failure rolls back with no change.
func (h *Handler) BackupRestore(w http.ResponseWriter, r *http.Request) {
	enc, key, err := readBackupUpload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.FormValue("confirm") != "RESTORE" {
		writeError(w, http.StatusBadRequest, "type RESTORE to confirm the (destructive) restore")
		return
	}
	if _, err := backup.Validate(enc, key); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	if err := backup.Restore(ctx, h.DatabaseURL, enc, key); err != nil {
		slog.Error("backup restore failed", "err", err)
		writeError(w, http.StatusInternalServerError, "restore failed: "+err.Error())
		return
	}
	h.audit(r, "restore", "system", 0, "restored the database from an uploaded backup")
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

// readBackupUpload pulls the encrypted file + the entered key out of a multipart
// request. The key is entered per-restore (it must match the file, which may have
// been made under an older PARKRR_BACKUP_KEY).
func readBackupUpload(r *http.Request) (enc []byte, key string, err error) {
	if err := r.ParseMultipartForm(9 << 20); err != nil {
		return nil, "", errors.New("upload too large or malformed (max ~9 MiB via the browser; use the CLI for larger)")
	}
	key = strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		return nil, "", errors.New("the backup key is required to decrypt the file")
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		return nil, "", errors.New("missing backup file")
	}
	defer f.Close()
	enc, err = io.ReadAll(io.LimitReader(f, 9<<20))
	if err != nil {
		return nil, "", errors.New("could not read the uploaded file")
	}
	return enc, key, nil
}

func streamBackup(w http.ResponseWriter, name string, data []byte) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}
