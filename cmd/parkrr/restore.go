package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/preining/parkrr/internal/backup"
	"github.com/preining/parkrr/internal/config"
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
	enc, err := os.ReadFile(file) //nolint:gosec // operator-supplied path, run intentionally
	if err != nil {
		fmt.Fprintln(os.Stderr, "read backup:", err)
		return 1
	}
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
	if err := backup.Restore(ctx, cfg.DatabaseURL, enc, cfg.BackupKey); err != nil {
		fmt.Fprintln(os.Stderr, "restore failed:", err)
		return 1
	}
	fmt.Println("restore complete.")
	return 0
}
