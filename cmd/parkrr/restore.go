package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/preining/parkrr/internal/backup"
	"github.com/preining/parkrr/internal/config"
	"github.com/preining/parkrr/internal/database"
)

// runRestore implements "parkrr restore <file.dump.enc> [--force]": decrypt and
// pg_restore a backup into the configured database. DESTRUCTIVE — requires a
// typed confirmation unless --force (for non-interactive ops).
func runRestore(args []string) int {
	var file string
	force := false
	for _, a := range args {
		switch a {
		case "--force":
			force = true
		default:
			file = a
		}
	}
	if file == "" {
		fmt.Fprintln(os.Stderr, "usage: parkrr restore <file.dump.enc> [--force]")
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 1
	}
	if cfg.BackupKey == "" {
		fmt.Fprintln(os.Stderr, "PARKRR_BACKUP_KEY is not set — cannot decrypt the backup")
		return 1
	}
	// Open once before confirmation so a missing/unreadable operator path fails
	// early. RestoreFile re-opens it after the exclusive safety lease is held.
	// #nosec G304 G703 -- this is an operator-supplied CLI restore path.
	f, err := os.Open(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read backup:", err)
		return 1
	}
	_ = f.Close()
	if !force {
		fmt.Printf("This will OVERWRITE the database with %q.\nType RESTORE to confirm: ", file)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(line) != "RESTORE" {
			fmt.Println("aborted.")
			return 1
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	lease, err := database.TryAcquireRestoreLease(ctx, cfg.DatabaseURL)
	if errors.Is(err, database.ErrApplicationActive) {
		fmt.Fprintln(os.Stderr, "restore refused: stop every running Parkrr application instance first")
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "restore safety check failed:", err)
		return 1
	}
	defer func() { _ = lease.Release() }()
	if err := backup.RestoreFile(ctx, cfg.DatabaseURL, file, cfg.BackupKey); err != nil {
		fmt.Fprintln(os.Stderr, "restore failed:", err)
		return 1
	}
	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "restored, but could not reconnect to purge sessions:", err)
		return 1
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, "restored, but schema migration failed:", err)
		return 1
	}
	if _, err := pool.Exec(ctx, `DELETE FROM sessions`); err != nil {
		fmt.Fprintln(os.Stderr, "restored, but could not purge sessions:", err)
		return 1
	}
	fmt.Println("restore complete.")
	return 0
}
