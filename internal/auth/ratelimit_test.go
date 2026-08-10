package auth

import (
	"testing"
	"testing/synctest"
	"time"
)

// TestLoginLimiter_Cleanup runs inside a synctest bubble: the limiter reads
// time.Now()/timers off the fake clock, so the failure-window and lock-duration
// sleeps advance virtual time instantly and deterministically — no real waiting.
func TestLoginLimiter_Cleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLoginLimiter(3, 100*time.Millisecond, 1*time.Second)

		// 1. Entry that should be cleaned up (expired and not locked).
		l.RecordFailure("cleanup-me")

		// 2. Entry that is locked and should NOT be cleaned up yet.
		l.RecordFailure("lock-me")
		l.RecordFailure("lock-me")
		l.RecordFailure("lock-me") // now locked for 1s

		// Past the failure window for "cleanup-me" (virtual time).
		time.Sleep(150 * time.Millisecond)
		l.Cleanup()

		l.mu.Lock()
		if _, ok := l.attempts["cleanup-me"]; ok {
			t.Error("expected 'cleanup-me' to be cleaned up")
		}
		if _, ok := l.attempts["lock-me"]; !ok {
			t.Error("expected 'lock-me' to be preserved because it is locked")
		}
		l.mu.Unlock()

		// Past the lock duration for "lock-me".
		time.Sleep(1 * time.Second)
		l.Cleanup()

		l.mu.Lock()
		if _, ok := l.attempts["lock-me"]; ok {
			t.Error("expected 'lock-me' to be cleaned up after lock expired")
		}
		l.mu.Unlock()
	})
}
