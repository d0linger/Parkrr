package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditFunc records a system-initiated backup action. It is injected rather than
// imported: internal/handlers already imports this package, so this package cannot
// import it back. main installs handlers.AuditSystem, which routes the entry through
// the same auditExec choke point (and therefore the same redaction) as every other
// audit write in the app.
type AuditFunc func(ctx context.Context, action, entity string, id int64, summary string, changes any)

// auditSink is set once at startup, before the scheduler goroutine starts, but is
// held atomically so the race detector stays quiet if a test installs one later.
var auditSink atomic.Pointer[AuditFunc]

// SetAuditor installs the sink used to record scheduled backups and pruned archives.
// Without it this package stays silent, which keeps the tests free of a DB dependency.
func SetAuditor(fn AuditFunc) { auditSink.Store(&fn) }

func audit(ctx context.Context, action, entity string, id int64, summary string, changes map[string]any) {
	p := auditSink.Load()
	if p == nil || *p == nil {
		return
	}
	// Detach from ctx and give the write its own short deadline. The most important
	// entry here is the one describing a run that FAILED, and the most common reason a
	// run fails is that this very ctx expired (the 30-minute scheduler budget, or
	// shutdown) — writing through it would reject the INSERT and lose exactly the
	// record worth keeping. Values on ctx (the actor, if any) are preserved.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	// These are outcome snapshots, not before/after diffs — wrap them here so the
	// entry has the same {old,new} shape as every other row.
	(*p)(wctx, action, entity, id, summary, snapshot(changes))
}

// actionBackupFailed is deliberately NOT in database.auditShortLivedActions, so a
// failed or unverified run follows the long retention window while routine successes
// age out with the rest of the ops noise. Kept in sync by a test in that package.
const actionBackupFailed = "backup_failed"

// snapshot renders outcome values as {field:{old:null,new:v}}, mirroring
// handlers.auditSnapshot. Duplicated rather than imported because this package
// cannot depend on internal/handlers.
func snapshot(v map[string]any) any {
	if len(v) == 0 {
		return nil
	}
	out := make(map[string]any, len(v))
	for k, val := range v {
		out[k] = map[string]any{"old": nil, "new": val}
	}
	return out
}

// runMu serializes backup execution. The scheduler and the "run now" endpoints
// share the same heavy dump→encrypt→write path, so at most one runs at a time.
var runMu sync.Mutex

func backupName(t time.Time) string {
	return "parkrr-" + t.Format("2006-01-02-150405") + ".dump.enc"
}

func dumpEncrypt(ctx context.Context, dbURL, key string) ([]byte, error) {
	dump, err := Dump(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	return Encrypt(dump, key)
}

// RunVolume makes an encrypted backup, writes it to dir, verifies the archive
// (decrypt + pg_restore --list), prunes to the newest `keep`, and records the
// outcome in backup_status. Returns the archive size and whether it VERIFIED.
//
// A written-but-unverified archive is deliberately not an error — the older, verified
// archives must not be rotated out behind it — but callers must be able to tell the
// two apart, or they report a success the status table simultaneously calls a failure.
func RunVolume(ctx context.Context, pool *pgxpool.Pool, dbURL, key, dir string, keep int) (int64, bool, error) {
	runMu.Lock()
	defer runMu.Unlock()

	enc, err := dumpEncrypt(ctx, dbURL, key)
	if err != nil {
		_ = recordVolume(ctx, pool, time.Now(), 0, false, false)
		return 0, false, err
	}
	_ = os.MkdirAll(dir, 0o700) // ensure the target exists; WriteFile surfaces real errors
	p := filepath.Join(dir, backupName(time.Now()))
	if err := os.WriteFile(p, enc, 0o600); err != nil {
		_ = recordVolume(ctx, pool, time.Now(), 0, false, false)
		return 0, false, err
	}
	// Verify the just-written archive is decryptable and structurally restorable.
	size := int64(len(enc))
	if _, verr := Validate(ctx, enc, key); verr != nil {
		// Do NOT rotate the older (verified) archives out behind an UNVERIFIED new
		// one, and record the run as not-OK — otherwise a persistent verify failure
		// would prune away the last good backup while last_volume_ok stayed green.
		slog.Warn("backup: archive verify failed – keeping older archives", "path", p, "err", verr)
		_ = recordVolume(ctx, pool, time.Now(), size, false, false)
		return size, false, nil
	}
	pruneDir(ctx, dir, keep)
	_ = recordVolume(ctx, pool, time.Now(), size, true, true)
	return size, true, nil
}

// RunS3 makes an encrypted backup, uploads it to the bucket (pruning to `keep`),
// and records the outcome. Returns the object name.
func RunS3(ctx context.Context, pool *pgxpool.Pool, dbURL, key string, s3 S3Config, keep int) (string, error) {
	runMu.Lock()
	defer runMu.Unlock()

	enc, err := dumpEncrypt(ctx, dbURL, key)
	if err != nil {
		_ = recordS3(ctx, pool, time.Now(), false)
		return "", err
	}
	name := backupName(time.Now())
	if err := UploadS3(ctx, s3, name, enc, keep); err != nil {
		_ = recordS3(ctx, pool, time.Now(), false)
		return "", err
	}
	_ = recordS3(ctx, pool, time.Now(), true)
	return name, nil
}

// StartScheduler runs scheduled backups driven by the DB-stored cron schedule
// (backup_settings). Each minute it reloads the schedule and fires any target
// whose cron is due since its last recorded run. Blocks until stop is closed —
// run it in a goroutine. A no-op unless a key and at least one target (a backup
// directory or S3) are configured.
func StartScheduler(stop <-chan struct{}, pool *pgxpool.Pool, dbURL, key, dir string, s3 S3Config) {
	if key == "" || (dir == "" && !s3.Enabled()) {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	// In-memory guards for the last fire of each target. They back-stop the DB
	// status row: if a backup runs but its status write fails, the guard still
	// advances, so fireDue can't relaunch the (multi-minute) backup every tick.
	var lastVol, lastS3 time.Time
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			schedulerTick(pool, dbURL, key, dir, s3, &lastVol, &lastS3)
		}
	}
}

// effectiveLast returns the later of the persisted last-run and the in-memory
// guard, or nil if neither is set (target never run).
func effectiveLast(dbLast *time.Time, mem time.Time) *time.Time {
	if dbLast == nil {
		if mem.IsZero() {
			return nil
		}
		return &mem
	}
	if mem.After(*dbLast) {
		return &mem
	}
	return dbLast
}

func schedulerTick(pool *pgxpool.Pool, dbURL, key, dir string, s3 S3Config, lastVol, lastS3 *time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	settings, err := LoadSettings(ctx, pool)
	if err != nil {
		slog.Error("backup scheduler: load settings failed", "err", err)
		return
	}
	status, err := LoadStatus(ctx, pool)
	if err != nil {
		slog.Error("backup scheduler: load status failed", "err", err)
		return
	}
	now := time.Now()

	// Both branches audit the OUTCOME, success and failure alike. A backup that
	// silently stopped running is the failure mode that matters here, and previously
	// the only trace was a log line that nobody keeps for seven years. The manual
	// endpoints audit themselves, so only the scheduled path is recorded here — no
	// double entry. Entity "system" matches what those endpoints already use, so one
	// filter shows scheduled and manual runs together instead of splitting one
	// operation across two buckets.
	//
	// Action: a SUCCESSFUL run is ops noise and uses "backup", which the retention
	// policy ages out on the short window. A FAILURE uses actionBackupFailed, which is
	// deliberately absent from auditShortLivedActions, so "when did the nightly backups
	// stop?" is still answerable once the 365-day short window has passed.
	if dir != "" && fireDue(settings.VolumeCron, effectiveLast(status.LastVolumeAt, *lastVol), now) {
		*lastVol = now // advance the guard before running so a status-write failure can't re-fire
		switch size, verified, err := RunVolume(ctx, pool, dbURL, key, dir, settings.VolumeKeep); {
		case err != nil:
			slog.Error("scheduled volume backup failed", "err", err)
			audit(ctx, actionBackupFailed, "system", 0, "Geplantes Volume-Backup FEHLGESCHLAGEN",
				map[string]any{"target": "volume", "ok": false, "cron": settings.VolumeCron, "error": err.Error()})
		case !verified:
			// Written, but it did not decrypt/restore-list cleanly. recordVolume already
			// flagged it not-OK; reporting ok:true here would leave the append-only trail
			// asserting a good backup that the status table simultaneously calls failed.
			slog.Warn("scheduled volume backup written but NOT verified", "dir", dir, "bytes", size)
			audit(ctx, actionBackupFailed, "system", 0, "Geplantes Volume-Backup geschrieben, aber NICHT verifiziert",
				map[string]any{"target": "volume", "ok": false, "verified": false, "bytes": size, "cron": settings.VolumeCron})
		default:
			slog.Info("scheduled volume backup written", "dir", dir, "bytes", size)
			audit(ctx, "backup", "system", 0, "Geplantes Volume-Backup erstellt und verifiziert",
				map[string]any{"target": "volume", "ok": true, "verified": true, "bytes": size, "cron": settings.VolumeCron, "keep": settings.VolumeKeep})
		}
	}
	if s3.Enabled() && fireDue(settings.S3Cron, effectiveLast(status.LastS3At, *lastS3), now) {
		*lastS3 = now
		if name, err := RunS3(ctx, pool, dbURL, key, s3, settings.S3Keep); err != nil {
			slog.Error("scheduled S3 backup failed", "err", err)
			audit(ctx, actionBackupFailed, "system", 0, "Geplantes S3-Backup FEHLGESCHLAGEN",
				map[string]any{"target": "s3", "ok": false, "bucket": s3.Bucket, "cron": settings.S3Cron, "error": err.Error()})
		} else {
			slog.Info("scheduled S3 backup uploaded", "bucket", s3.Bucket, "name", name)
			audit(ctx, "backup", "system", 0, "Geplantes S3-Backup hochgeladen",
				map[string]any{"target": "s3", "ok": true, "bucket": s3.Bucket, "object": name, "cron": settings.S3Cron, "keep": settings.S3Keep})
		}
	}
}

// fireDue reports whether a target should back up now. An empty cron is off. A
// never-run target (no recorded last run) takes an initial backup immediately,
// then follows the schedule from there.
func fireDue(cron string, last *time.Time, now time.Time) bool {
	if _, ok := parseCron(cron); !ok {
		return false // off (empty) or invalid: never fire
	}
	if last == nil {
		return true // never run: take an initial backup, then follow the schedule
	}
	return CronDue(cron, *last, now)
}

// pruneDir keeps only the newest `keep` timestamped backups in dir (0 = keep all).
func pruneDir(ctx context.Context, dir string, keep int) {
	if keep < 1 {
		return
	}
	files, err := filepath.Glob(filepath.Join(dir, "parkrr-*.dump.enc"))
	if err != nil || len(files) <= keep {
		return
	}
	sort.Strings(files) // timestamped names sort chronologically
	var removed []string
	for _, old := range files[:len(files)-keep] {
		if err := os.Remove(old); err != nil {
			slog.Warn("backup: prune failed", "path", old, "err", err)
			continue
		}
		removed = append(removed, filepath.Base(old))
	}
	// Deleting a backup is destructive and irreversible, and until now it happened
	// with nothing but a debug line. Record WHICH archives went and how many remain,
	// so a missing restore point can be explained rather than guessed at.
	if len(removed) > 0 {
		audit(ctx, "delete", "system", 0,
			fmt.Sprintf("%d alte Backup-Archive gelöscht (Aufbewahrung: %d)", len(removed), keep),
			map[string]any{"deleted_files": removed, "deleted_count": len(removed), "keep": keep})
	}
}
