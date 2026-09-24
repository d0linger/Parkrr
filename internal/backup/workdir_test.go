package backup

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// isolateTempDir points os.TempDir() at a private directory, so a sweep in this
// test can never touch files of other processes on the machine.
func isolateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	return dir
}

// BAK-02: full-size temp files belong on the backup volume, not in /tmp.
func TestWorkFilesLiveUnderTheBackupDirectory(t *testing.T) {
	isolateTempDir(t)
	backupDir := t.TempDir()
	ConfigureWorkDir(backupDir)
	t.Cleanup(func() { ConfigureWorkDir("") })

	f, err := createWorkFile("parkrr-s3-", ".dump.enc")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if got, want := filepath.Dir(f.Name()), filepath.Join(backupDir, ".tmp"); got != want {
		t.Fatalf("work file in %s, want %s", got, want)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Dir(f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Fatalf("work dir mode = %v, want 0700", fi.Mode().Perm())
		}
	}

	ConfigureWorkDir("")
	if WorkDir() != os.TempDir() {
		t.Fatalf("without a backup directory WorkDir = %s, want os.TempDir()", WorkDir())
	}
}

// BAK-06: leftovers of an interrupted run are removed at startup — this host's own
// at once, another replica's only once stale, and never a file an active restore
// job still references.
func TestSweepWorkFilesRemovesLeftovers(t *testing.T) {
	tmp := isolateTempDir(t)
	backupDir := t.TempDir()
	work := filepath.Join(backupDir, ".tmp")
	staging := filepath.Join(backupDir, ".restore-staging")
	for _, d := range []string{work, staging} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * staleWorkFileAge)
	write := func(path string, mtime time.Time) string {
		t.Helper()
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if !mtime.IsZero() {
			if err := os.Chtimes(path, mtime, mtime); err != nil {
				t.Fatal(err)
			}
		}
		return path
	}
	own := "-" + hostTag + "-"
	gone := []string{
		write(filepath.Join(work, "parkrr-restore"+own+"1.dump"), time.Time{}),
		write(filepath.Join(work, "parkrr-s3-upload"+own+"2.dump.enc"), time.Time{}),
		write(filepath.Join(work, "parkrr-download-otherhost-3.dump.enc"), old),
		write(filepath.Join(tmp, "parkrr-restore-123456.dump"), old), // older version, /tmp
		write(filepath.Join(staging, "restore"+own+"4.dump.enc"), time.Time{}),
		write(filepath.Join(staging, "restore-otherhost-5.dump.enc"), old),
	}
	kept := []string{
		write(filepath.Join(work, "parkrr-restore-otherhost-6.dump"), time.Time{}), // live peer
		write(filepath.Join(staging, "restore-otherhost-7.dump.enc"), time.Time{}),
		write(filepath.Join(work, "unrelated.txt"), old),
		write(filepath.Join(tmp, "parkrr-notes-8.txt"), old),
	}
	active := write(filepath.Join(staging, "restore"+own+"9.dump.enc"), old)
	kept = append(kept, active)

	SweepWorkFiles(backupDir, active)

	for _, p := range gone {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should have been swept (err=%v)", filepath.Base(p), err)
		}
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must be kept: %v", filepath.Base(p), err)
		}
	}
}

func TestStagingPatternKeepsTheRecoverableShape(t *testing.T) {
	p := StagingFilePattern()
	if !strings.HasPrefix(p, "restore-") || !strings.HasSuffix(p, ".dump.enc") {
		t.Fatalf("staging pattern %q no longer matches restorectl.RemoveRecoveredArchive", p)
	}
}

// BAK-04: closing the generation's stop channel must cancel a running backup.
func TestContextUntilStopCancelsTheRun(t *testing.T) {
	stop := make(chan struct{})
	ctx, cancel := contextUntil(stop)
	defer cancel()
	if ctx.Err() != nil {
		t.Fatal("context cancelled before stop")
	}
	close(stop)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("closing stop did not cancel the run context")
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("ctx.Err() = %v", ctx.Err())
	}
}
