package backup

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

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
// outcome in backup_status. Returns the archive size in bytes.
func RunVolume(ctx context.Context, pool *pgxpool.Pool, dbURL, key, dir string, keep int) (int64, error) {
	runMu.Lock()
	defer runMu.Unlock()

	enc, err := dumpEncrypt(ctx, dbURL, key)
	if err != nil {
		_ = recordVolume(ctx, pool, time.Now(), 0, false, false)
		return 0, err
	}
	_ = os.MkdirAll(dir, 0o700) // ensure the target exists; WriteFile surfaces real errors
	p := filepath.Join(dir, backupName(time.Now()))
	if err := os.WriteFile(p, enc, 0o600); err != nil {
		_ = recordVolume(ctx, pool, time.Now(), 0, false, false)
		return 0, err
	}
	// Verify the just-written archive is decryptable and structurally restorable.
	tested := false
	if _, verr := Validate(enc, key); verr != nil {
		slog.Warn("backup: archive verify failed", "path", p, "err", verr)
	} else {
		tested = true
	}
	pruneDir(dir, keep)
	size := int64(len(enc))
	_ = recordVolume(ctx, pool, time.Now(), size, true, tested)
	return size, nil
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

	if dir != "" && fireDue(settings.VolumeCron, effectiveLast(status.LastVolumeAt, *lastVol), now) {
		*lastVol = now // advance the guard before running so a status-write failure can't re-fire
		if size, err := RunVolume(ctx, pool, dbURL, key, dir, settings.VolumeKeep); err != nil {
			slog.Error("scheduled volume backup failed", "err", err)
		} else {
			slog.Info("scheduled volume backup written", "dir", dir, "bytes", size)
		}
	}
	if s3.Enabled() && fireDue(settings.S3Cron, effectiveLast(status.LastS3At, *lastS3), now) {
		*lastS3 = now
		if name, err := RunS3(ctx, pool, dbURL, key, s3, settings.S3Keep); err != nil {
			slog.Error("scheduled S3 backup failed", "err", err)
		} else {
			slog.Info("scheduled S3 backup uploaded", "bucket", s3.Bucket, "name", name)
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
func pruneDir(dir string, keep int) {
	if keep < 1 {
		return
	}
	files, err := filepath.Glob(filepath.Join(dir, "parkrr-*.dump.enc"))
	if err != nil || len(files) <= keep {
		return
	}
	sort.Strings(files) // timestamped names sort chronologically
	for _, old := range files[:len(files)-keep] {
		if err := os.Remove(old); err != nil {
			slog.Warn("backup: prune failed", "path", old, "err", err)
		}
	}
}
