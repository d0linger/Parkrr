package backup

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// BAK-03/BAK-05: a second replica must not WAIT for a running backup (the wait was
// killed by the pool's 10 s statement_timeout and reported as a failure). It gets
// ErrBackupBusy at once, and RunVolume leaves the shared directory untouched.
func TestBackupLeaseReportsBusyInsteadOfWaiting(t *testing.T) {
	url := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	dir := t.TempDir()
	release, err := tryAcquireLease(ctx, pool, volumeLeaseName(dir))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := tryAcquireLease(ctx, pool, volumeLeaseName(dir)); !errors.Is(err, ErrBackupBusy) {
		t.Fatalf("second lease: err = %v, want ErrBackupBusy", err)
	}
	size, verified, err := RunVolume(ctx, pool, "postgres://unused/none", "key", dir, Retention{})
	if !errors.Is(err, ErrBackupBusy) || size != 0 || verified {
		t.Fatalf("RunVolume while a peer holds the lease: size=%d verified=%v err=%v", size, verified, err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("the busy lease blocked instead of returning at once")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a skipped run touched the backup directory: %v", entries)
	}
	// Another target (bucket) is independent.
	releaseS3, err := tryAcquireLease(ctx, pool, s3LeaseName("bucket-a"))
	if err != nil {
		t.Fatal(err)
	}
	releaseS3()
	release()
	again, err := tryAcquireLease(ctx, pool, volumeLeaseName(dir))
	if err != nil {
		t.Fatalf("lease not reusable after release: %v", err)
	}
	again()
}
