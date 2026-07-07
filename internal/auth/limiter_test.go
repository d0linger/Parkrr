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
