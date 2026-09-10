package auth

import (
	"sync"
	"sync/atomic"
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

// Consume counts EVERY call, not just failures — that is the whole point of it
// next to Allowed/RecordFailure, which only ever react to a failed credential
// guess. Without it a caller that never fails is never bounded.
func TestLoginLimiter_ConsumeCountsEveryAttempt(t *testing.T) {
	l := NewStickyLoginLimiter(3, time.Minute, time.Minute)

	for i := 0; i < 3; i++ {
		if ok, _ := l.Consume("k"); !ok {
			t.Fatalf("attempt %d should be allowed", i)
		}
	}
	ok, wait := l.Consume("k")
	if ok {
		t.Fatal("the 4th attempt must be refused without any recorded failure")
	}
	if wait <= 0 {
		t.Errorf("a refusal should report a positive retry delay, got %v", wait)
	}

	// A refused key must not be counted again: hammering a locked key may not
	// keep pushing its own cooldown further out. Asserted on the limiter's own
	// state, not on the returned wait: two time.Until values taken microseconds
	// apart compare equal on a coarse clock, so a wall-clock delta cannot detect
	// a re-lock at all (measured: 0 catches in 100 runs against an implementation
	// that deliberately re-locks on every refusal). This test is in-package, and
	// TestLoginLimiter_Cleanup already reaches into l.mu/l.attempts the same way.
	l.mu.Lock()
	lockedBefore, failsBefore := l.attempts["k"].lockedTill, l.attempts["k"].fails
	l.mu.Unlock()
	for i := 0; i < 5; i++ {
		if ok, _ := l.Consume("k"); ok {
			t.Fatal("an already locked key must stay refused")
		}
	}
	l.mu.Lock()
	lockedAfter, failsAfter := l.attempts["k"].lockedTill, l.attempts["k"].fails
	l.mu.Unlock()
	if !lockedAfter.Equal(lockedBefore) {
		t.Errorf("hammering a locked key extended its cooldown: %v -> %v", lockedBefore, lockedAfter)
	}
	if failsAfter != failsBefore {
		t.Errorf("a refused attempt must not be counted: fails %d -> %d", failsBefore, failsAfter)
	}

	// Keys are independent.
	if ok, _ := l.Consume("other"); !ok {
		t.Error("a different key must have its own budget")
	}
}

// Consume must not be raceable: the check and the increment happen under one
// lock, so a burst of concurrent callers cannot slip past the threshold.
//
// Two deliberate choices make this an actual detector rather than decoration.
// The goroutines are released from a BARRIER instead of racing the spawn loop —
// without it most goroutines finish before the last is even started, so nothing
// contends at the moment the threshold is crossed. And the burst is REPEATED,
// because even with a barrier one round is probabilistic.
//
// Measured against the naive alternative this is meant to prevent (Allowed, then
// RecordFailure, with the lock released between them): the spawn-loop version
// caught it in under 10% of runs; barrier + repetition raised that to 19 of 20
// runs at 50 rounds, and this uses 200. A correctly locked implementation grants exactly
// `limit` every round, so this stays deterministic for correct code — it can
// never fail wrongly, only fail late.
func TestLoginLimiter_ConsumeIsAtomicUnderConcurrency(t *testing.T) {
	const (
		limit  = 10
		burst  = 200
		rounds = 200
	)
	for round := 0; round < rounds; round++ {
		l := NewStickyLoginLimiter(limit, time.Minute, time.Minute)
		var wg sync.WaitGroup
		var granted atomic.Int64
		start := make(chan struct{})
		for i := 0; i < burst; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if ok, _ := l.Consume("burst"); ok {
					granted.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()
		if got := granted.Load(); got != limit {
			t.Fatalf("round %d: a %d-goroutine barrier burst granted %d attempts, want exactly %d",
				round, burst, got, limit)
		}
	}
}
