package backup

import (
	"context"
	"testing"
	"time"
)

// TestRestoreSerializesWithBackup proves Restore takes the same runMu the dump path
// (RunVolume/RunS3) holds, so a scheduled backup can't run mid-restore. With the lock
// held, Restore must block before doing any work; once released it proceeds (and here
// fails fast on the bad archive — that it returns at all is the point).
func TestRestoreSerializesWithBackup(t *testing.T) {
	runMu.Lock()

	done := make(chan struct{})
	go func() {
		_ = Restore(context.Background(), "postgres://u:p@localhost/none", []byte("not-a-real-archive"), "key")
		close(done)
	}()

	select {
	case <-done:
		runMu.Unlock()
		t.Fatal("Restore proceeded while the backup lock was held — restore and backup are not mutually exclusive")
	case <-time.After(150 * time.Millisecond):
		// Still blocked on runMu.Lock — correct.
	}

	runMu.Unlock()
	select {
	case <-done:
		// Proceeded once the lock was free (then failed on the bad input) — correct.
	case <-time.After(3 * time.Second):
		t.Fatal("Restore did not proceed after the backup lock was released")
	}
}
