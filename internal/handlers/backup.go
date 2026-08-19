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
	"github.com/preining/parkrr/internal/database"
)

// clearWriteDeadline lifts the server's WriteTimeout for a long-running response
// (a large encrypted backup stream, an S3 upload, or a multi-minute pg_restore)
// so the write isn't aborted mid-flight — which would truncate a download into a
// corrupt archive or drop a restore's connection before its status is returned.
// The per-handler context still bounds the work; only the socket write deadline
// is cleared. Best-effort: a no-op if the writer doesn't support it.
func clearWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
}

// CreateBackup runs an encrypted pg_dump and streams it to the operator as a
// download (admin-only). Encrypted with PARKRR_BACKUP_KEY (AES-256-GCM).
func (h *Handler) CreateBackup(w http.ResponseWriter, r *http.Request) {
	clearWriteDeadline(w)
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

// BackupStatus reports the full Backup-tab state: what's configured, the
// GUI-editable cron schedule and retention, the runtime status (last runs,
// sizes, verify), the schema version, and the file listings (newest first).
func (h *Handler) BackupStatus(w http.ResponseWriter, r *http.Request) {
	type file struct {
		Name     string    `json:"name"`
		Size     int64     `json:"size"`
		Modified time.Time `json:"modified"`
	}
	resp := struct {
		Enabled       bool            `json:"enabled"`   // PARKRR_BACKUP_KEY is set
		Scheduled     bool            `json:"scheduled"` // a backup directory is configured
		Dir           string          `json:"dir"`
		SchemaVersion string          `json:"schema_version"`
		Settings      backup.Settings `json:"settings"`
		Status        backup.Status   `json:"status"`
		Files         []file          `json:"files"`
		S3            bool            `json:"s3"` // an S3 target is configured
		S3Bucket      string          `json:"s3_bucket"`
		S3Files       []file          `json:"s3_files"`
		Health        backup.Health   `json:"health"`
	}{Enabled: h.BackupKey != "", Scheduled: h.BackupDir != "", Dir: h.BackupDir, Files: []file{},
		S3: h.S3.Enabled(), S3Bucket: h.S3.Bucket, S3Files: []file{},
		SchemaVersion: backup.SchemaVersion(r.Context(), h.Pool)}

	if s, err := backup.LoadSettings(r.Context(), h.Pool); err == nil {
		resp.Settings = s
	} else {
		slog.Warn("backup: load settings failed", "err", err)
	}
	if s, err := backup.LoadStatus(r.Context(), h.Pool); err == nil {
		resp.Status = s
	} else {
		slog.Warn("backup: load status failed", "err", err)
	}

	if h.BackupDir != "" {
		matches, _ := filepath.Glob(filepath.Join(h.BackupDir, "parkrr-*.dump.enc"))
		sort.Sort(sort.Reverse(sort.StringSlice(matches))) // timestamped -> newest first
		for _, p := range matches {
			if fi, err := os.Stat(p); err == nil {
				resp.Files = append(resp.Files, file{Name: filepath.Base(p), Size: fi.Size(), Modified: fi.ModTime()})
			}
		}
	}
	if h.S3.Enabled() {
		// Bound the network round-trip so a slow/unreachable bucket can't hang the
		// status endpoint (which is otherwise a fast local read).
		s3ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if objs, err := backup.ListS3(s3ctx, h.S3); err == nil {
			for _, o := range objs {
				resp.S3Files = append(resp.S3Files, file{Name: o.Name, Size: o.Size, Modified: o.Modified})
			}
		} else {
			slog.Warn("backup: S3 list failed", "err", err)
		}
	}
	// Verdict last, once settings and status are both loaded. Computed server-side
	// because the threshold comes from the target's own cron expression, and the cron
	// parser lives here — the browser must not reimplement it.
	resp.Health = backup.BackupHealth(resp.Settings, resp.Status,
		h.BackupKey != "" && h.BackupDir != "", h.BackupKey != "" && h.S3.Enabled(), time.Now())
	writeJSON(w, http.StatusOK, resp)
}

// SaveBackupSchedule updates the GUI-editable cron schedule and retention. Crons
// are validated (empty = off); the running scheduler picks up the change on its
// next tick (it reloads the settings each minute).
func (h *Handler) SaveBackupSchedule(w http.ResponseWriter, r *http.Request) {
	var in backup.Settings
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in.VolumeCron = trim(in.VolumeCron)
	in.S3Cron = trim(in.S3Cron)
	if !validCronLength(in.VolumeCron) {
		writeError(w, http.StatusBadRequest, "volume cron is too long")
		return
	}
	if !validCronLength(in.S3Cron) {
		writeError(w, http.StatusBadRequest, "S3 cron is too long")
		return
	}
	if !backup.ValidCron(in.VolumeCron) {
		writeError(w, http.StatusBadRequest, "volume cron is not a valid 5-field cron expression")
		return
	}
	if !backup.ValidCron(in.S3Cron) {
		writeError(w, http.StatusBadRequest, "S3 cron is not a valid 5-field cron expression")
		return
	}
	if in.VolumeKeep < 0 || in.S3Keep < 0 {
		writeError(w, http.StatusBadRequest, "retention count must not be negative")
		return
	}
	// Read the previous schedule first so the trail carries the before/after values —
	// retention counts in particular decide how long backups survive.
	prev, prevErr := backup.LoadSettings(r.Context(), h.Pool)
	if prevErr != nil {
		// Do not save blind. backup_settings is a migration-seeded singleton pinned by
		// CHECK (id = 1), so a failed read is either a real database error — in which
		// case SaveSettings would fail next anyway — or the row is gone, and then
		// SaveSettings' `UPDATE … WHERE id = 1` touches zero rows, returns nil, and the
		// user is told "saved" while nothing was written. Refusing also keeps the
		// retention change from being recorded without the values it changed FROM.
		slog.Error("backup: cannot read current schedule, refusing to save", "err", prevErr)
		writeError(w, http.StatusInternalServerError, "could not read the current schedule")
		return
	}
	if err := backup.SaveSettings(r.Context(), h.Pool, in); err != nil {
		slog.Error("backup: save schedule failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not save the schedule")
		return
	}
	// prevErr is handled above (the request is refused), so the diff always has a real
	// before-state here.
	changes := diffFields(prev, in)
	// action "update" (not "backup"): this is a configuration change, and the
	// retention policy puts "backup" on the short window with the routine runs.
	// The retention counts decide how long backups survive — that must stay
	// provable for as long as any other settings change.
	h.auditChange(r, "update", "backup_settings", 0,
		"updated the backup schedule (volume '"+in.VolumeCron+"', S3 '"+in.S3Cron+"')", changes)
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

// RunScheduledBackup runs the configured scheduled targets (volume and/or S3)
// immediately, using the DB retention settings, and records the outcome. This is
// the "Jetzt sichern" action in the schedule section (distinct from the on-demand
// browser download in CreateBackup).
func (h *Handler) RunScheduledBackup(w http.ResponseWriter, r *http.Request) {
	if h.BackupKey == "" {
		writeError(w, http.StatusServiceUnavailable, "backup is not configured (set PARKRR_BACKUP_KEY)")
		return
	}
	if h.BackupDir == "" && !h.S3.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "no scheduled target configured (set PARKRR_BACKUP_DIR and/or S3)")
		return
	}
	settings, err := backup.LoadSettings(r.Context(), h.Pool)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load the schedule")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()

	ran := []string{}
	var firstErr error
	if h.BackupDir != "" {
		// `verified` must be honored, not discarded: RunVolume returns a nil error for
		// an archive that was written but failed its decrypt/restore-list check, so
		// treating nil-error as success would report "Volume gesichert" for a backup
		// that cannot be restored — and backup_status simultaneously records it as
		// failed. Same distinction the scheduler makes.
		switch _, verified, err := backup.RunVolume(ctx, h.Pool, h.DatabaseURL, h.BackupKey, h.BackupDir, settings.VolumeKeep); {
		case err != nil:
			slog.Error("run-now volume backup failed", "err", err)
			firstErr = err
		case !verified:
			slog.Error("run-now volume backup written but NOT verified")
			firstErr = errors.New("volume backup written but failed verification")
		default:
			ran = append(ran, "Volume")
		}
	}
	if h.S3.Enabled() {
		if _, err := backup.RunS3(ctx, h.Pool, h.DatabaseURL, h.BackupKey, h.S3, settings.S3Keep); err != nil {
			slog.Error("run-now S3 backup failed", "err", err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			ran = append(ran, "S3")
		}
	}
	if len(ran) == 0 {
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	h.audit(r, "backup", "system", 0, "ran scheduled backup now ("+strings.Join(ran, ", ")+")")
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "ran": ran, "partial": firstErr != nil})
}

// safeBackupName guards against path traversal: base name only, expected shape.
func safeBackupName(name string) bool {
	return name == filepath.Base(name) && name != "" &&
		strings.HasPrefix(name, "parkrr-") && strings.HasSuffix(name, ".dump.enc")
}

// BackupDownloadFile serves one scheduled backup from the backup directory.
func (h *Handler) BackupDownloadFile(w http.ResponseWriter, r *http.Request) {
	clearWriteDeadline(w)
	if h.BackupDir == "" {
		writeError(w, http.StatusNotFound, "no scheduled backup directory configured")
		return
	}
	name := r.PathValue("name")
	if !safeBackupName(name) {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	// os.Root confines the open to BackupDir: any traversal in `name` is rejected
	// by the OS layer (belt-and-braces with safeBackupName), which also satisfies
	// the path-traversal scanners.
	root, err := os.OpenRoot(h.BackupDir)
	if err != nil {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read backup")
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
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	info, err := backup.Validate(ctx, enc, key)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// BackupRestore restores an uploaded backup into the live database. DESTRUCTIVE:
// requires confirm=RESTORE and validates the archive first. The restore itself is
// reconcileSchemaAfterRestore brings the database back in step with THIS binary
// after a restore, so no manual restart is needed. A restored backup can be a
// schema version behind the running code (its schema_migrations, and thus the
// columns migrations added, are the backup's) — the app would then serve against a
// stale schema and fail every query touching the newer columns. Two steps:
//
//  1. Pool.Reset() — pg_restore --clean dropped and recreated every table, so any
//     pooled connection may hold cached statements bound to the old relations
//     ("cached plan must not change result type"). Discard them; the pool reopens
//     fresh connections on demand, and Migrate then runs on a clean one.
//  2. Migrate — re-apply whatever the backup lacked (the same embedded migrations
//     run at startup). Forward-only: a backup NEWER than this binary is left as-is
//     (there is no matching migration to apply and downgrading is never done).
func (h *Handler) reconcileSchemaAfterRestore(ctx context.Context) error {
	h.Pool.Reset()
	if err := database.Migrate(ctx, h.Pool); err != nil {
		return err
	}
	// The restored data may carry period settlements as off-book flags only (an older
	// backup, pre-migration 036). Book their real Zahlungseingänge now — idempotent,
	// exactly as at startup — so a legacy paid Pauschale/Nebenkosten shows its payment
	// without waiting for a restart.
	return h.BackfillPeriodPayments(ctx)
}

// atomic (pg_restore --single-transaction): a failure rolls back with no change.
func (h *Handler) BackupRestore(w http.ResponseWriter, r *http.Request) {
	clearWriteDeadline(w)
	enc, key, err := readBackupUpload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.FormValue("confirm") != "RESTORE" {
		writeError(w, http.StatusBadRequest, "type RESTORE to confirm the (destructive) restore")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	if _, err := backup.Validate(ctx, enc, key); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := backup.Restore(ctx, h.DatabaseURL, enc, key); err != nil {
		slog.Error("backup restore failed", "err", err)
		writeError(w, http.StatusInternalServerError, "restore failed")
		return
	}
	if err := h.reconcileSchemaAfterRestore(ctx); err != nil {
		slog.Error("post-restore migration failed", "err", err)
		writeError(w, http.StatusInternalServerError, "restored, but the schema upgrade failed — restart the app to complete it")
		return
	}
	h.audit(r, "restore", "system", 0, "restored the database from an uploaded backup")
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

// readBackupUpload pulls the encrypted file + the entered key out of a multipart
// request. The key is entered per-restore (it must match the file, which may have
// been made under an older PARKRR_BACKUP_KEY).
func readBackupUpload(r *http.Request) (enc []byte, key string, err error) {
	const maxUpload = 9 << 20 // matches server.maxRequestBody (the whole body is capped there)
	// #nosec G120 -- the request body is already bounded by limitRequestBody
	// (MaxBytesReader at maxRequestBody), so this in-memory parse limit cannot be
	// exceeded; it just sizes the buffer.
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		return nil, "", errors.New("upload too large or malformed (max ~9 MiB via the browser; use the CLI for larger)")
	}
	key = strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		return nil, "", errors.New("the backup key is required to decrypt the file")
	}
	if !validBackupKeyLength(key) {
		return nil, "", errors.New("the backup key is too long")
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		return nil, "", errors.New("missing backup file")
	}
	defer f.Close()
	// Read one byte past the limit so an over-large file is rejected explicitly
	// rather than silently truncated.
	enc, err = io.ReadAll(io.LimitReader(f, maxUpload+1))
	if err != nil {
		return nil, "", errors.New("could not read the uploaded file")
	}
	if len(enc) > maxUpload {
		return nil, "", errors.New("backup file exceeds the ~9 MiB browser limit; use the CLI (parkrr restore) for larger files")
	}
	return enc, key, nil
}

// BackupS3Test checks the configured bucket is reachable (read-only BucketExists) —
// the panel's "Verbindung testen". Admin-only; the diagnostic error is returned so
// the operator sees why it failed (missing bucket, bad credentials, unreachable
// endpoint). Modeled on Treckrr's S3Test.
func (h *Handler) BackupS3Test(w http.ResponseWriter, r *http.Request) {
	if !h.S3.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "S3 ist nicht konfiguriert (S3_* setzen).")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := backup.TestS3(ctx, h.S3); err != nil {
		slog.Warn("backup: S3 connection test failed", "err", err)
		writeError(w, http.StatusBadGateway, "S3-Verbindung fehlgeschlagen: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "bucket": h.S3.Bucket})
}

// CreateBackupS3 makes an encrypted backup and uploads it to the S3 bucket
// (no download). keep=0 here so a manual upload never prunes.
func (h *Handler) CreateBackupS3(w http.ResponseWriter, r *http.Request) {
	clearWriteDeadline(w)
	if h.BackupKey == "" || !h.S3.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "S3 backup is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	// keep=0: a manual upload never prunes. RunS3 records the status row.
	name, err := backup.RunS3(ctx, h.Pool, h.DatabaseURL, h.BackupKey, h.S3, 0)
	if err != nil {
		slog.Error("backup: S3 upload failed", "err", err)
		writeError(w, http.StatusBadGateway, "S3 upload failed")
		return
	}
	h.audit(r, "backup", "system", 0, "uploaded encrypted backup to S3 ("+name+")")
	writeJSON(w, http.StatusOK, map[string]string{"status": "uploaded", "name": name})
}

// BackupS3Download streams one backup object from the bucket.
func (h *Handler) BackupS3Download(w http.ResponseWriter, r *http.Request) {
	clearWriteDeadline(w)
	if !h.S3.Enabled() {
		writeError(w, http.StatusNotFound, "S3 is not configured")
		return
	}
	name := r.PathValue("name")
	if !safeBackupName(name) {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	data, err := backup.DownloadS3(r.Context(), h.S3, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	streamBackup(w, name, data)
}

// BackupRestoreS3 restores directly from an S3 object — no browser upload, so it
// handles any size. Requires the matching key and confirm=RESTORE; atomic.
func (h *Handler) BackupRestoreS3(w http.ResponseWriter, r *http.Request) {
	clearWriteDeadline(w)
	if !h.S3.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "S3 is not configured")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid form")
		return
	}
	name := r.FormValue("name")
	key := strings.TrimSpace(r.FormValue("key"))
	if !safeBackupName(name) {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	if key == "" {
		writeError(w, http.StatusBadRequest, "the backup key is required to decrypt the file")
		return
	}
	if !validBackupKeyLength(key) {
		writeError(w, http.StatusBadRequest, "the backup key is too long")
		return
	}
	if r.FormValue("confirm") != "RESTORE" {
		writeError(w, http.StatusBadRequest, "type RESTORE to confirm the (destructive) restore")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	enc, err := backup.DownloadS3(ctx, h.S3, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "backup not found in S3")
		return
	}
	if _, err := backup.Validate(ctx, enc, key); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := backup.Restore(ctx, h.DatabaseURL, enc, key); err != nil {
		slog.Error("backup restore (S3) failed", "err", err)
		writeError(w, http.StatusInternalServerError, "restore failed")
		return
	}
	if err := h.reconcileSchemaAfterRestore(ctx); err != nil {
		slog.Error("post-restore migration failed (S3)", "err", err)
		writeError(w, http.StatusInternalServerError, "restored, but the schema upgrade failed — restart the app to complete it")
		return
	}
	h.audit(r, "restore", "system", 0, "restored the database from S3 backup "+name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

func streamBackup(w http.ResponseWriter, name string, data []byte) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// #nosec G705 -- data is an encrypted backup blob streamed as an octet-stream
	// attachment (Content-Disposition + nosniff), never interpreted as HTML.
	_, _ = w.Write(data)
}
