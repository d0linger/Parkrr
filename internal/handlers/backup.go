package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/backup"
	"github.com/preining/parkrr/internal/database"
	"github.com/preining/parkrr/internal/restorectl"
)

const backupResponseDeadline = 30 * time.Minute

const (
	MaxBrowserRestoreBytes       = int64(1 << 30)
	MaxBrowserRestoreRequestBody = MaxBrowserRestoreBytes + (1 << 20)
	restoreStepUpWindow          = 10 * time.Minute
)

var backupDownloadSlots = make(chan struct{}, 2)
var browserRestoreSlots = make(chan struct{}, 1)

// setBackupWriteDeadline replaces the short general WriteTimeout with a bounded
// backup-specific window. It is long enough for operational archives but cannot
// leave a stalled client holding a connection forever.
func setBackupWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(backupResponseDeadline))
}

func setBrowserRestoreDeadlines(w http.ResponseWriter) {
	deadline := time.Now().Add(backupResponseDeadline)
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
}

func acquireBackupDownload(w http.ResponseWriter, r *http.Request) bool {
	select {
	case backupDownloadSlots <- struct{}{}:
		return true
	case <-r.Context().Done():
		return false
	default:
		writeError(w, http.StatusTooManyRequests, "Zu viele Sicherungsdownloads – bitte später erneut versuchen")
		return false
	}
}

func acquireBrowserRestoreSlot(w http.ResponseWriter, r *http.Request) bool {
	select {
	case browserRestoreSlots <- struct{}{}:
		return true
	case <-r.Context().Done():
		return false
	default:
		writeError(w, http.StatusTooManyRequests,
			"Eine Sicherung wird bereits für die Wiederherstellung verarbeitet")
		return false
	}
}

// CreateBackup runs an encrypted pg_dump and streams it to the operator as a
// download (admin-only). Encrypted with PARKRR_BACKUP_KEY (AES-256-GCM).
func (h *Handler) CreateBackup(w http.ResponseWriter, r *http.Request) {
	setBackupWriteDeadline(w)
	if !acquireBackupDownload(w, r) {
		return
	}
	defer func() { <-backupDownloadSlots }()
	if h.BackupKey == "" {
		writeError(w, http.StatusServiceUnavailable, "backup is not configured (set PARKRR_BACKUP_KEY)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	// Im Arbeitsverzeichnis unter PARKRR_BACKUP_DIR, nicht in /tmp: das Archiv hat
	// volle Datenbankgröße (BAK-02).
	tmp, err := backup.CreateWorkFile("parkrr-download-", ".dump.enc")
	if err != nil {
		slog.Error("backup: create temporary archive failed", "err", err)
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := backup.DumpEncrypted(ctx, h.DatabaseURL, h.BackupKey, tmp); err != nil {
		_ = tmp.Close()
		slog.Error("backup: streamed pg_dump failed", "err", err)
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	if err := tmp.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	name := "parkrr-" + time.Now().Format("2006-01-02-150405") + ".dump.enc"
	h.audit(r, "backup", "system", 0, "created encrypted database backup ("+name+")")
	if err := streamBackupFile(w, r, name, tmpPath); err != nil {
		slog.Error("backup: stream temporary archive failed", "err", err)
	}
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
		Enabled        bool            `json:"enabled"`   // PARKRR_BACKUP_KEY is set
		Scheduled      bool            `json:"scheduled"` // a backup directory is configured
		Dir            string          `json:"dir"`
		SchemaVersion  string          `json:"schema_version"`
		Settings       backup.Settings `json:"settings"`
		Status         backup.Status   `json:"status"`
		Files          []file          `json:"files"`
		S3             bool            `json:"s3"` // an S3 target is configured
		S3Bucket       string          `json:"s3_bucket"`
		S3Files        []file          `json:"s3_files"`
		Health         backup.Health   `json:"health"`
		BrowserRestore bool            `json:"browser_restore"`
	}{Enabled: h.BackupKey != "", Scheduled: h.BackupDir != "", Dir: h.BackupDir, Files: []file{},
		S3: h.S3.Enabled(), S3Bucket: h.S3.Bucket, S3Files: []file{},
		SchemaVersion:  backup.SchemaVersion(r.Context(), h.Pool),
		BrowserRestore: h.Restore != nil && h.Restore.Enabled()}

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
	// Eine negative Tagesgrenze wäre eine Grenze in der ZUKUNFT: dann wäre nie etwas
	// alt genug und das Aufräumen stünde still, ohne dass jemand es merkt. Die
	// Obergrenze ist keine Willkür, sondern hält die Zahl in einem Bereich, in dem
	// "mindestens so lange aufbewahren" noch eine Aussage ist (Hundert 09).
	const maxKeepDays = 3650 // 10 Jahre
	if in.VolumeKeepDays < 0 || in.S3KeepDays < 0 {
		writeError(w, http.StatusBadRequest, "Mindestalter darf nicht negativ sein")
		return
	}
	if in.VolumeKeepDays > maxKeepDays || in.S3KeepDays > maxKeepDays {
		writeError(w, http.StatusBadRequest, "Mindestalter darf höchstens 3650 Tage betragen")
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
	busy := []string{}
	var firstErr error
	if h.BackupDir != "" {
		// `verified` must be honored, not discarded: RunVolume returns a nil error for
		// an archive that was written but failed its decrypt/restore-list check, so
		// treating nil-error as success would report "Volume gesichert" for a backup
		// that cannot be restored — and backup_status simultaneously records it as
		// failed. Same distinction the scheduler makes.
		switch _, verified, err := backup.RunVolume(ctx, h.Pool, h.DatabaseURL, h.BackupKey, h.BackupDir, settings.VolumeRetention()); {
		case errors.Is(err, backup.ErrBackupBusy):
			// Eine andere Instanz sichert gerade dasselbe Ziel — kein Fehlschlag.
			busy = append(busy, "Volume")
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
		switch _, err := backup.RunS3(ctx, h.Pool, h.DatabaseURL, h.BackupKey, h.S3, settings.S3Retention()); {
		case errors.Is(err, backup.ErrBackupBusy):
			busy = append(busy, "S3")
		case err != nil:
			slog.Error("run-now S3 backup failed", "err", err)
			if firstErr == nil {
				firstErr = err
			}
		default:
			ran = append(ran, "S3")
		}
	}
	if len(ran) == 0 && firstErr == nil && len(busy) > 0 {
		writeError(w, http.StatusConflict, "Eine Sicherung läuft bereits auf einer anderen Instanz")
		return
	}
	if len(ran) == 0 {
		writeError(w, http.StatusInternalServerError, "backup failed")
		return
	}
	h.audit(r, "backup", "system", 0, "ran scheduled backup now ("+strings.Join(ran, ", ")+")")
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "ran": ran, "partial": firstErr != nil || len(busy) > 0})
}

// safeBackupName guards against path traversal: base name only, expected shape.
func safeBackupName(name string) bool {
	return name == filepath.Base(name) && name != "" &&
		strings.HasPrefix(name, "parkrr-") && strings.HasSuffix(name, ".dump.enc")
}

// BackupDownloadFile serves one scheduled backup from the backup directory.
func (h *Handler) BackupDownloadFile(w http.ResponseWriter, r *http.Request) {
	setBackupWriteDeadline(w)
	if !acquireBackupDownload(w, r) {
		return
	}
	defer func() { <-backupDownloadSlots }()
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
	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read backup")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// BackupValidate decrypts + inspects an uploaded backup (no DB change), so the
// operator can confirm the key is right and the archive is intact before a restore.
func (h *Handler) BackupValidate(w http.ResponseWriter, r *http.Request) {
	if h.Restore == nil || !h.Restore.Enabled() {
		writeError(w, http.StatusConflict,
			"Browser-Wiederherstellung ist nicht aktiviert. PARKRR_BROWSER_RESTORE=true setzen oder die CLI verwenden.")
		return
	}
	setBrowserRestoreDeadlines(w)
	if !acquireBrowserRestoreSlot(w, r) {
		return
	}
	defer func() { <-browserRestoreSlots }()
	path, key, _, _, err := h.stageBackupUpload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer os.Remove(path)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	info, err := backup.ValidateFile(ctx, path, key)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	checksum, _, err := backup.ChecksumFile(path)
	if err != nil {
		serverError(w, r, "checksum uploaded backup", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"created": info.Created, "entries": info.Entries,
		"checksum_sha256": checksum, "confirmation": restoreConfirmation(checksum),
	})
}

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
	// Backups intentionally omit session data, and older archives may still
	// contain it. Purge after every restore so logout/password revocation can
	// never be rolled back by restoring an earlier database snapshot.
	if _, err := h.Pool.Exec(ctx, `DELETE FROM sessions`); err != nil {
		return fmt.Errorf("purge restored sessions: %w", err)
	}
	// The restored data may carry period settlements as off-book flags only (an older
	// backup, pre-migration 036). Book their real Zahlungseingänge now — idempotent,
	// exactly as at startup — so a legacy paid Pauschale/Nebenkosten shows its payment
	// without waiting for a restart.
	return h.BackfillPeriodPayments(ctx)
}

// BackupRestore validates and queues an uploaded archive for the process-level
// restore coordinator. Confirmation is bound to the validated archive checksum.
func (h *Handler) BackupRestore(w http.ResponseWriter, r *http.Request) {
	setBrowserRestoreDeadlines(w)
	if h.Restore == nil || !h.Restore.Enabled() {
		writeError(w, http.StatusConflict,
			"Browser-Wiederherstellung ist nicht aktiviert. PARKRR_BROWSER_RESTORE=true setzen oder die CLI verwenden.")
		return
	}
	if !h.restoreStepUpOK(w, r) {
		return
	}
	if !acquireBrowserRestoreSlot(w, r) {
		return
	}
	defer func() { <-browserRestoreSlots }()
	path, key, confirm, name, err := h.stageBackupUpload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path) // #nosec G703 -- path comes from os.CreateTemp in the server staging dir, not from the request
		}
	}()
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	info, err := backup.ValidateFile(ctx, path, key)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	checksum, _, err := backup.ChecksumFile(path)
	if err != nil {
		serverError(w, r, "checksum uploaded backup", err)
		return
	}
	if confirm != restoreConfirmation(checksum) {
		writeError(w, http.StatusBadRequest, "Bestätigung stimmt nicht mit der geprüften Sicherung überein")
		return
	}
	u, _ := auth.UserFrom(r.Context())
	requestedBy := "admin"
	if u != nil {
		requestedBy = u.Username
	}
	job, err := h.Restore.Submit(r.Context(), restorectl.SubmitRequest{
		SourcePath: path, SourceName: name, BackupCreated: info.Created,
		ArchiveEntries: info.Entries, ChecksumSHA256: checksum,
		RequestedBy: requestedBy, Key: key,
	})
	if err != nil {
		if errors.Is(err, restorectl.ErrBusy) {
			writeError(w, http.StatusConflict, "Eine Wiederherstellung läuft bereits")
			return
		}
		serverError(w, r, "queue browser restore", err)
		return
	}
	keep = true
	h.audit(r, "restore", "system", 0, "queued browser database restore ("+name+", sha256 "+checksum[:12]+")")
	writeJSON(w, http.StatusAccepted, job)
}

func restoreConfirmation(checksum string) string {
	const suffixLength = 12
	if len(checksum) < suffixLength {
		return "RESTORE"
	}
	return "RESTORE " + strings.ToUpper(checksum[len(checksum)-suffixLength:])
}

func (h *Handler) restoreStepUpOK(w http.ResponseWriter, r *http.Request) bool {
	if h.Auth == nil {
		writeError(w, http.StatusForbidden, "reauth_required")
		return false
	}
	created, ok := h.Auth.SessionCreatedAt(r.Context(), r)
	age := time.Since(created)
	if !ok || age < 0 || age >= restoreStepUpWindow {
		writeError(w, http.StatusForbidden, "reauth_required")
		return false
	}
	return true
}

func readSmallUploadPart(part *multipart.Part) (string, error) {
	const maxField = 4096
	b, err := io.ReadAll(io.LimitReader(part, maxField+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxField {
		return "", errors.New("restore form field is too long")
	}
	return strings.TrimSpace(string(b)), nil
}

// stageBackupUpload streams multipart input into a private file. The request body
// and the file are both bounded; no client-controlled filename reaches the disk.
func (h *Handler) stageBackupUpload(r *http.Request) (path, key, confirm, sourceName string, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", "", "", "", errors.New("invalid multipart upload")
	}
	base := h.BackupDir
	if base == "" {
		base = os.TempDir()
	}
	stagingDir := filepath.Join(base, ".restore-staging")
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return "", "", "", "", errors.New("restore staging directory is unavailable")
	}

	cleanup := func() {
		if path != "" {
			_ = os.Remove(path)
		}
	}
	for {
		part, nextErr := mr.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			cleanup()
			return "", "", "", "", errors.New("malformed multipart upload")
		}
		name := part.FormName()
		switch name {
		case "file":
			if path != "" {
				_ = part.Close()
				cleanup()
				return "", "", "", "", errors.New("only one backup file is allowed")
			}
			sourceName = filepath.Base(part.FileName())
			if sourceName == "." || sourceName == "" || len(sourceName) > 255 || !strings.HasSuffix(strings.ToLower(sourceName), ".enc") {
				_ = part.Close()
				cleanup()
				return "", "", "", "", errors.New("backup file must have an .enc filename")
			}
			f, createErr := os.CreateTemp(stagingDir, backup.StagingFilePattern())
			if createErr != nil {
				_ = part.Close()
				cleanup()
				return "", "", "", "", errors.New("could not create restore staging file")
			}
			path = f.Name()
			if chmodErr := f.Chmod(0o600); chmodErr != nil {
				_ = f.Close()
				_ = part.Close()
				cleanup()
				return "", "", "", "", errors.New("could not secure restore staging file")
			}
			n, copyErr := io.Copy(f, io.LimitReader(part, MaxBrowserRestoreBytes+1))
			syncErr := f.Sync()
			closeErr := f.Close()
			partErr := part.Close()
			if copyErr != nil || syncErr != nil || closeErr != nil || partErr != nil {
				cleanup()
				return "", "", "", "", errors.New("could not write uploaded backup")
			}
			if n > MaxBrowserRestoreBytes {
				cleanup()
				return "", "", "", "", errors.New("backup file exceeds the 1 GiB browser limit")
			}
		case "key", "confirm":
			value, readErr := readSmallUploadPart(part)
			closeErr := part.Close()
			if readErr != nil || closeErr != nil {
				cleanup()
				return "", "", "", "", errors.New("could not read restore form")
			}
			if name == "key" {
				key = value
			} else {
				confirm = value
			}
		default:
			if closeErr := part.Close(); closeErr != nil {
				cleanup()
				return "", "", "", "", errors.New("could not read restore form")
			}
		}
	}
	if path == "" {
		return "", "", "", "", errors.New("missing backup file")
	}
	if key == "" {
		cleanup()
		return "", "", "", "", errors.New("the backup key is required to decrypt the file")
	}
	if !validBackupKeyLength(key) {
		cleanup()
		return "", "", "", "", errors.New("the backup key is too long")
	}
	return path, key, confirm, sourceName, nil
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
	setBackupWriteDeadline(w)
	if h.BackupKey == "" || !h.S3.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "S3 backup is not configured")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	// Leere Retention: ein manueller Upload räumt nie auf. RunS3 schreibt die Statuszeile.
	name, err := backup.RunS3(ctx, h.Pool, h.DatabaseURL, h.BackupKey, h.S3, backup.Retention{})
	if errors.Is(err, backup.ErrBackupBusy) {
		writeError(w, http.StatusConflict, "Ein S3-Backup läuft bereits auf einer anderen Instanz")
		return
	}
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
	setBackupWriteDeadline(w)
	if !acquireBackupDownload(w, r) {
		return
	}
	defer func() { <-backupDownloadSlots }()
	if !h.S3.Enabled() {
		writeError(w, http.StatusNotFound, "S3 is not configured")
		return
	}
	name := r.PathValue("name")
	if !safeBackupName(name) {
		writeError(w, http.StatusBadRequest, "invalid backup name")
		return
	}
	path, _, err := backup.DownloadS3File(r.Context(), h.S3, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "backup not found")
		return
	}
	defer os.Remove(path)
	if err := streamBackupFile(w, r, name, path); err != nil {
		slog.Error("backup: stream S3 archive failed", "err", err)
	}
}

// BackupValidateS3 downloads and validates an S3 archive without modifying the
// database. Its checksum-bound confirmation must be repeated to BackupRestoreS3.
func (h *Handler) BackupValidateS3(w http.ResponseWriter, r *http.Request) {
	setBackupWriteDeadline(w)
	if h.Restore == nil || !h.Restore.Enabled() {
		writeError(w, http.StatusConflict,
			"Browser-Wiederherstellung ist nicht aktiviert. PARKRR_BROWSER_RESTORE=true setzen oder die CLI verwenden.")
		return
	}
	if !h.S3.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "S3 is not configured")
		return
	}
	if !acquireBrowserRestoreSlot(w, r) {
		return
	}
	defer func() { <-browserRestoreSlots }()
	var in struct {
		Name string `json:"name"`
		Key  string `json:"key"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !safeBackupName(in.Name) || in.Key == "" || !validBackupKeyLength(in.Key) {
		writeError(w, http.StatusBadRequest, "invalid restore request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	path, _, err := backup.DownloadS3File(ctx, h.S3, in.Name)
	if err != nil {
		writeError(w, http.StatusBadGateway, "S3 backup could not be downloaded")
		return
	}
	defer os.Remove(path)
	info, err := backup.ValidateFile(ctx, path, in.Key)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	checksum, _, err := backup.ChecksumFile(path)
	if err != nil {
		serverError(w, r, "checksum S3 backup", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"created": info.Created, "entries": info.Entries,
		"checksum_sha256": checksum, "confirmation": restoreConfirmation(checksum),
	})
}

// BackupRestoreS3 validates and queues an S3 archive. The process-level
// coordinator stops all application generations before it changes the database.
func (h *Handler) BackupRestoreS3(w http.ResponseWriter, r *http.Request) {
	setBackupWriteDeadline(w)
	if h.Restore == nil || !h.Restore.Enabled() {
		writeError(w, http.StatusConflict,
			"Browser-Wiederherstellung ist nicht aktiviert. PARKRR_BROWSER_RESTORE=true setzen oder die CLI verwenden.")
		return
	}
	if !h.restoreStepUpOK(w, r) {
		return
	}
	if !h.S3.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "S3 is not configured")
		return
	}
	if !acquireBrowserRestoreSlot(w, r) {
		return
	}
	defer func() { <-browserRestoreSlots }()
	var in struct {
		Name    string `json:"name"`
		Key     string `json:"key"`
		Confirm string `json:"confirm"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !safeBackupName(in.Name) || in.Key == "" || !validBackupKeyLength(in.Key) {
		writeError(w, http.StatusBadRequest, "invalid restore request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	path, _, err := backup.DownloadS3File(ctx, h.S3, in.Name)
	if err != nil {
		writeError(w, http.StatusBadGateway, "S3 backup could not be downloaded")
		return
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()
	info, err := backup.ValidateFile(ctx, path, in.Key)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	checksum, _, err := backup.ChecksumFile(path)
	if err != nil {
		serverError(w, r, "checksum S3 backup", err)
		return
	}
	if in.Confirm != restoreConfirmation(checksum) {
		writeError(w, http.StatusBadRequest, "Bestätigung stimmt nicht mit der geprüften Sicherung überein")
		return
	}
	u, _ := auth.UserFrom(r.Context())
	requestedBy := "admin"
	if u != nil {
		requestedBy = u.Username
	}
	job, err := h.Restore.Submit(r.Context(), restorectl.SubmitRequest{
		SourcePath: path, SourceName: in.Name, BackupCreated: info.Created,
		ArchiveEntries: info.Entries, ChecksumSHA256: checksum,
		RequestedBy: requestedBy, Key: in.Key,
	})
	if err != nil {
		if errors.Is(err, restorectl.ErrBusy) {
			writeError(w, http.StatusConflict, "Eine Wiederherstellung läuft bereits")
			return
		}
		serverError(w, r, "queue S3 restore", err)
		return
	}
	keep = true
	h.audit(r, "restore", "system", 0, "queued browser S3 restore ("+in.Name+", sha256 "+checksum[:12]+")")
	writeJSON(w, http.StatusAccepted, job)
}

// streamBackupFile sends a backup work file as a download named name.
func streamBackupFile(w http.ResponseWriter, r *http.Request, name, path string) error {
	// path is either this request's backup.CreateWorkFile result or DownloadS3File's
	// own work file. name is generated by Parkrr or passed through
	// safeBackupName before this helper is called.
	f, err := os.Open(path) // #nosec G304,G703 -- application-created temporary path; no request path reaches this sink
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, info.ModTime(), f)
	return nil
}

// backupHealthView is the compact header indicator: enough for an editor to see
// that backups are alive, and deliberately nothing more.
//
// It exists as its own view because GET /api/backup/status is admin-only for good
// reason — it carries the backup directory, the bucket name and the full archive
// listing. None of that belongs in a header dot, so this returns only the verdict,
// the age and two booleans. Modeled on Treckrr's header indicator, which draws the
// same line between "may see the state" and "may see the storage".
type backupHealthView struct {
	Tone      string `json:"tone"`  // "ok" | "warn" | "bad"
	Title     string `json:"title"` // "Backup aktuell", "Backup veraltet", …
	AgeLabel  string `json:"age_label"`
	S3        string `json:"s3"` // "ok" | "fehlgeschlagen" | "" (nicht konfiguriert)
	Encrypted bool   `json:"encrypted"`
}

// backupToneOf maps one target's state onto the three header tones. Overdue and
// never-run are "warn" (etwas einrichten), a failed run is "bad" (etwas reparieren).
func backupToneOf(st string) string {
	switch st {
	case "ok":
		return "ok"
	case "failed":
		return "bad"
	default:
		return "warn"
	}
}

// BackupHealth reports backup health for the header indicator (editor+).
//
// Editors cannot open the backup panel, but they are the ones working in the app
// all day — if the nightly backup died, they should not have to be an admin to
// notice. Viewers are excluded: they cannot act on it.
func (h *Handler) BackupHealth(w http.ResponseWriter, r *http.Request) {
	if h.BackupKey == "" {
		writeJSON(w, http.StatusOK, backupHealthView{Tone: "warn", Title: "Backups nicht aktiviert"})
		return
	}
	settings, serr := backup.LoadSettings(r.Context(), h.Pool)
	status, sterr := backup.LoadStatus(r.Context(), h.Pool)
	if serr != nil || sterr != nil {
		// Unknown is not "fine": a status we cannot read must not show green.
		writeJSON(w, http.StatusOK, backupHealthView{Tone: "warn", Title: "Backup-Status unklar"})
		return
	}
	hh := backup.BackupHealth(settings, status,
		h.BackupDir != "", h.S3.Enabled(), time.Now())

	volCfg, s3Cfg := hh.Volume.Configured, hh.S3.Configured
	if !volCfg && !s3Cfg {
		writeJSON(w, http.StatusOK, backupHealthView{
			Tone: "warn", Title: "Kein Backup-Ziel eingerichtet", Encrypted: true})
		return
	}
	// Worst of the configured targets, so a healthy volume cannot mask a dead S3.
	states := []string{}
	if volCfg {
		states = append(states, backupStateOf(hh.Volume))
	}
	if s3Cfg {
		states = append(states, backupStateOf(hh.S3))
	}
	worst := worstBackupState(states)

	v := backupHealthView{Tone: backupToneOf(worst), Encrypted: true}
	switch worst {
	case "ok":
		v.Title = "Backup aktuell"
	case "failed":
		v.Title = "Letztes Backup fehlgeschlagen"
	case "never":
		v.Title = "Noch kein automatisches Backup"
	case "off":
		v.Title = "Kein Zeitplan aktiv"
	default:
		v.Title = "Backup veraltet"
	}
	// Age of the NEWEST successful run across targets — "wie alt ist mein neuester
	// Wiederherstellungspunkt?" is the question the dot answers.
	var newest *int64
	for _, th := range []backup.TargetHealth{hh.Volume, hh.S3} {
		if th.Configured && th.AgeSeconds != nil && (newest == nil || *th.AgeSeconds < *newest) {
			newest = th.AgeSeconds
		}
	}
	if newest != nil {
		v.AgeLabel = humanAgeDE(*newest)
	}
	if s3Cfg {
		v.S3 = "ok"
		if backupStateOf(hh.S3) != "ok" {
			v.S3 = "fehlgeschlagen"
		}
	}
	writeJSON(w, http.StatusOK, v)
}

// backupStateOf classifies one target: failed beats never beats stale beats ok.
func backupStateOf(th backup.TargetHealth) string {
	// Reihenfolge ist entscheidend und war falsch herum: last_volume_ok hat in der
	// Migration den Default FALSE, ein noch nie gelaufenes Backup kam also über
	// !LastOK zuerst und wurde als "fehlgeschlagen" gemeldet — jede frische
	// Installation zeigte Rot für etwas, das schlicht noch nicht dran war.
	// Never (= es gibt keinen einzigen Lauf) muss vorher greifen; LastOK sagt nur
	// dann etwas aus, wenn überhaupt ein Versuch stattgefunden hat.
	switch {
	case !th.Configured:
		return "off"
	case th.Never:
		return "never"
	case !th.LastOK:
		return "failed"
	case th.Overdue:
		return "stale"
	default:
		return "ok"
	}
}

// Rangfolge für "das schlechteste Ziel gewinnt". "off" steht bewusst UNTER "never":
// ein nicht eingerichtetes Ziel ist eine Einrichtungslücke, ein eingerichtetes ohne
// einen einzigen Lauf ist dringender. Vorher war es umgekehrt.
var backupStateRank = map[string]int{"ok": 0, "off": 1, "stale": 2, "never": 3, "failed": 4}

func worstBackupState(states []string) string {
	worst := "ok"
	for _, s := range states {
		if backupStateRank[s] > backupStateRank[worst] {
			worst = s
		}
	}
	return worst
}

// humanAgeDE renders an age in seconds as "vor 3 Std." — the header has room for a
// glance, not for a timestamp.
func humanAgeDE(sec int64) string {
	switch {
	case sec >= 48*3600:
		return fmt.Sprintf("vor %d Tagen", sec/(24*3600))
	case sec >= 24*3600:
		return "vor 1 Tag"
	case sec >= 2*3600:
		return fmt.Sprintf("vor %d Std.", sec/3600)
	case sec >= 3600:
		return "vor 1 Std."
	case sec >= 120:
		return fmt.Sprintf("vor %d Min.", sec/60)
	default:
		return "gerade eben"
	}
}
