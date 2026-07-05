package auth

import (
	"testing"
	"time"
)

func TestLoginLimiter_Cleanup(t *testing.T) {
	l := NewLoginLimiter(3, 100*time.Millisecond, 1*time.Second)

	// 1. Entry that should be cleaned up (expired and not locked)
	l.RecordFailure("cleanup-me")

	// 2. Entry that is locked and should NOT be cleaned up yet
	l.RecordFailure("lock-me")
	l.RecordFailure("lock-me")
	l.RecordFailure("lock-me") // Now locked for 1s

	// Wait for failWindow to pass for "cleanup-me"
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

	// Wait for lockFor to pass for "lock-me"
	time.Sleep(1 * time.Second)

	// Now both should be cleaned up
	l.Cleanup()

	l.mu.Lock()
	if _, ok := l.attempts["lock-me"]; ok {
		t.Error("expected 'lock-me' to be cleaned up after lock expired")
	}
	l.mu.Unlock()
}
