package backup

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// StartScheduled writes an encrypted backup into dir every intervalHours and
// keeps the newest `keep` files. It blocks until stop is closed, so run it in a
// goroutine. A no-op if key or dir is empty.
func StartScheduled(stop <-chan struct{}, dbURL, key, dir string, intervalHours, keep int) {
	if key == "" || dir == "" {
		return
	}
	if intervalHours < 1 {
		intervalHours = 24
	}
	interval := time.Duration(intervalHours) * time.Hour
	// First run shortly after startup, then on the interval.
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-stop:
			return
		case <-timer.C:
			runScheduled(dbURL, key, dir, keep)
			timer.Reset(interval)
		}
	}
}

func runScheduled(dbURL, key, dir string, keep int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	dump, err := Dump(ctx, dbURL)
	if err != nil {
		slog.Error("scheduled backup: pg_dump failed", "err", err)
		return
	}
	enc, err := Encrypt(dump, key)
	if err != nil {
		slog.Error("scheduled backup: encrypt failed", "err", err)
		return
	}
	name := filepath.Join(dir, "parkrr-"+time.Now().Format("2006-01-02-150405")+".dump.enc")
	if err := os.WriteFile(name, enc, 0o600); err != nil {
		slog.Error("scheduled backup: write failed", "err", err, "path", name)
		return
	}
	slog.Info("scheduled backup written", "path", name, "bytes", len(enc))
	prune(dir, keep)
}

// prune keeps only the newest `keep` timestamped backups in dir.
func prune(dir string, keep int) {
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
			slog.Warn("scheduled backup: prune failed", "path", old, "err", err)
		}
	}
}
