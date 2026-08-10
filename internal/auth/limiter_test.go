package auth

import (
	"testing"
	"time"
)

func TestLoginLimiterLocksAfterMaxFails(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute, 15*time.Minute)
	const key = "user|1.2.3.4"

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allowed(key); !ok {
			t.Fatalf("attempt %d should be allowed before threshold", i)
		}
		l.RecordFailure(key)
	}
	ok, wait := l.Allowed(key)
	if ok {
		t.Fatal("expected lockout after reaching max failures")
	}
	if wait <= 0 {
		t.Fatal("expected a positive retry-after duration while locked")
	}
}

func TestLoginLimiterResetClears(t *testing.T) {
	l := NewLoginLimiter(2, time.Minute, time.Minute)
	const key = "user|5.6.7.8"
	l.RecordFailure(key)
	l.RecordFailure(key)
	if ok, _ := l.Allowed(key); ok {
		t.Fatal("should be locked")
	}
	l.Reset(key)
	if ok, _ := l.Allowed(key); !ok {
		t.Fatal("Reset should clear the lockout")
	}
}

func TestLoginLimiterUnknownKeyAllowed(t *testing.T) {
	l := NewLoginLimiter(5, time.Minute, time.Minute)
	if ok, wait := l.Allowed("never-seen"); !ok || wait != 0 {
		t.Fatalf("unknown key should be allowed with no wait, got ok=%v wait=%v", ok, wait)
	}
}

// TestStickyLimiterDoesNotRefillOnCooldown: the sticky (per-username) limiter must
// NOT hand out a fresh budget after each short cooldown. A fixed-window limiter
// would; the sticky one re-locks on the first failure after the lock expires,
// bounding failures to maxFails per window regardless of how many cooldowns pass.
func TestStickyLimiterDoesNotRefillOnCooldown(t *testing.T) {
	const key = "victim"
	// Long window (budget doesn't age out during the test), very short lock so the
	// cooldown expires in-test.
	l := NewStickyLoginLimiter(2, time.Minute, 20*time.Millisecond)
	l.RecordFailure(key)
	l.RecordFailure(key) // budget exhausted → locked
	if ok, _ := l.Allowed(key); ok {
		t.Fatal("should be locked after reaching the budget")
	}
	time.Sleep(30 * time.Millisecond) // let the cooldown expire
	if ok, _ := l.Allowed(key); !ok {
		t.Fatal("cooldown should have expired")
	}
	l.RecordFailure(key) // one more failure within the same window
	if ok, _ := l.Allowed(key); ok {
		t.Fatal("sticky limiter must re-lock on the first post-cooldown failure, not refill the budget")
	}

	// Contrast: a plain fixed-window limiter DOES refill after the cooldown.
	nl := NewLoginLimiter(2, time.Minute, 20*time.Millisecond)
	nl.RecordFailure(key)
	nl.RecordFailure(key)
	time.Sleep(30 * time.Millisecond)
	nl.RecordFailure(key) // fresh budget: 1 of 2, not yet locked
	if ok, _ := nl.Allowed(key); !ok {
		t.Fatal("fixed-window limiter should grant a fresh budget after the cooldown")
	}
}
