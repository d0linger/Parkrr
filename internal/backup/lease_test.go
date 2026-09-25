package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func leaseTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return ctx, pool
}

// BAK-03/BAK-05: a second replica must not WAIT for a running backup (the wait was
// killed by the pool's 10 s statement_timeout and reported as a failure). It gets
// ErrBackupBusy at once, and RunVolume leaves the shared directory untouched.
func TestBackupLeaseReportsBusyInsteadOfWaiting(t *testing.T) {
	ctx, pool := leaseTestPool(t)

	dir := t.TempDir()
	_, release, err := tryAcquireLease(ctx, pool, volumeLeaseName(dir))
	if err != nil {
		t.Fatal(err)
	}
	// Released on any early t.Fatal too; cleared after the explicit release below.
	defer func() {
		if release != nil {
			release()
		}
	}()
	started := time.Now()
	if _, _, err := tryAcquireLease(ctx, pool, volumeLeaseName(dir)); !errors.Is(err, ErrBackupBusy) {
		t.Fatalf("second lease: err = %v, want ErrBackupBusy", err)
	}
	size, verified, err := RunVolume(ctx, pool, "postgres://unused/none", "key", dir, Retention{})
	if !errors.Is(err, ErrBackupBusy) || size != 0 || verified {
		t.Fatalf("RunVolume while a peer holds the lease: size=%d verified=%v err=%v", size, verified, err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("the busy lease blocked instead of returning at once")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != volumeIDFile {
			t.Fatalf("a skipped run touched the backup directory: %v", entries)
		}
	}
	// Another target (bucket) is independent.
	_, releaseS3, err := tryAcquireLease(ctx, pool, s3LeaseName("bucket-a"))
	if err != nil {
		t.Fatal(err)
	}
	releaseS3()
	release()
	release = nil
	_, again, err := tryAcquireLease(ctx, pool, volumeLeaseName(dir))
	if err != nil {
		t.Fatalf("lease not reusable after release: %v", err)
	}
	again()
}

// The lease name follows the volume, not the mount path: a second path to the
// same directory (as another replica would mount it) yields the same lease.
func TestVolumeLeaseNameFollowsTheVolume(t *testing.T) {
	dir := t.TempDir()
	first := volumeLeaseName(dir)
	if first == "parkrr.backup-volume:"+filepath.Clean(dir) {
		t.Fatal("lease name fell back to the path although the directory is writable")
	}
	if again := volumeLeaseName(dir + string(filepath.Separator) + "."); again != first {
		t.Fatalf("same volume, different lease names: %q vs %q", first, again)
	}
	if other := volumeLeaseName(t.TempDir()); other == first {
		t.Fatal("two different volumes share one lease name")
	}
}

// A lease whose session dies mid-run must end the run's context instead of
// letting it continue unprotected while another replica takes the lock.
func TestLostLeaseCancelsTheRun(t *testing.T) {
	ctx, pool := leaseTestPool(t)
	old := leaseCheckInterval
	leaseCheckInterval = 50 * time.Millisecond
	t.Cleanup(func() { leaseCheckInterval = old })

	runCtx, release, err := tryAcquireLease(ctx, pool, s3LeaseName("lease-loss-test"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := pool.Exec(ctx,
		`SELECT pg_terminate_backend(pid) FROM pg_locks
		  WHERE locktype = 'advisory' AND granted AND pid <> pg_backend_pid()
		    AND (classid::bigint << 32 | objid::bigint) = hashtextextended($1, 0)`,
		s3LeaseName("lease-loss-test")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runCtx.Done():
		if !errors.Is(context.Cause(runCtx), errLeaseLost) {
			t.Fatalf("run context ended with %v, want errLeaseLost", context.Cause(runCtx))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the run kept going after its lease session was terminated")
	}
}

// Concurrent first use yields exactly one identity (first creator wins), and no
// temp files are left behind.
func TestVolumeIdentityConcurrentCreation(t *testing.T) {
	dir := t.TempDir()
	const n = 16
	ids := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			id, err := volumeIdentity(dir)
			ids <- id
			errs <- err
		}()
	}
	first := ""
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		id := <-ids
		if first == "" {
			first = id
		} else if id != first {
			t.Fatalf("concurrent callers got different identities: %q vs %q", first, id)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != volumeIDFile {
		t.Fatalf("expected only %s, got %v", volumeIDFile, entries)
	}
}

// An incomplete identity file left by a crash (or an older build) must not block
// the volume forever once it is stale.
func TestVolumeIdentityReplacesStaleIncompleteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, volumeIDFile)
	if err := os.WriteFile(path, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	id, err := volumeIdentity(dir)
	if err != nil || len(id) != 32 {
		t.Fatalf("stale incomplete identity was not replaced: id=%q err=%v", id, err)
	}
	if again, _ := volumeIdentity(dir); again != id {
		t.Fatalf("identity changed on re-read: %q vs %q", id, again)
	}
}

// A volume without hard-link support falls back to an exclusive create; the
// identity is still stable, and a peer's file still wins.
func TestVolumeIdentityWithoutHardLinks(t *testing.T) {
	old := linkFile
	linkFile = func(string, string) error { return errors.New("operation not supported") }
	t.Cleanup(func() { linkFile = old })

	dir := t.TempDir()
	id, err := volumeIdentity(dir)
	if err != nil || len(id) != 32 {
		t.Fatalf("fallback create failed: id=%q err=%v", id, err)
	}
	if again, err := volumeIdentity(dir); err != nil || again != id {
		t.Fatalf("identity not stable after fallback: %q vs %q (err %v)", id, again, err)
	}
	if err := writeIdentityExclusive(filepath.Join(dir, volumeIDFile), "x"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("exclusive create over an existing identity: err = %v, want os.ErrExist", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}
