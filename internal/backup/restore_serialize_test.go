package backup

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRestoreSerializesWithBackup proves Restore takes the same runGate the dump path
// (RunVolume/RunS3) holds, so a scheduled backup can't run mid-restore. With the lock
// held, Restore must block before doing any work; once released it proceeds (and here
// fails fast on the bad archive — that it returns at all is the point).
func TestRestoreSerializesWithBackup(t *testing.T) {
	runGate <- struct{}{}

	done := make(chan struct{})
	go func() {
		_ = Restore(context.Background(), "postgres://u:p@localhost/none", []byte("not-a-real-archive"), "key")
		close(done)
	}()

	select {
	case <-done:
		releaseRun()
		t.Fatal("Restore proceeded while the backup lock was held — restore and backup are not mutually exclusive")
	case <-time.After(150 * time.Millisecond):
		// Still blocked on runGate — correct.
	}

	releaseRun()
	select {
	case <-done:
		// Proceeded once the lock was free (then failed on the bad input) — correct.
	case <-time.After(3 * time.Second):
		t.Fatal("Restore did not proceed after the backup lock was released")
	}
}

func TestRestoreWaitHonorsCancellation(t *testing.T) {
	runGate <- struct{}{}
	defer releaseRun()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := Restore(ctx, "postgres://u:p@localhost/none", []byte("not-a-real-archive"), "key")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Restore error = %v, want deadline exceeded", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("Restore did not leave the queued backup gate promptly after cancellation")
	}
}
