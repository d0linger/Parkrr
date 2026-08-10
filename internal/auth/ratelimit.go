package auth

import (
	"sync"
	"time"
)

// LoginLimiter is an in-memory login throttle. After too many failures for a
// given key (username+IP) it locks that key for a cooldown window. Suitable for
// a single-instance deployment.
type LoginLimiter struct {
	mu         sync.Mutex
	attempts   map[string]*attemptState
	maxFails   int
	lockFor    time.Duration
	failWindow time.Duration
	sticky     bool
}

type attemptState struct {
	fails      int
	firstFail  time.Time
	lockedTill time.Time
}

// NewLoginLimiter creates a limiter allowing maxFails within failWindow before
// locking for lockDuration.
func NewLoginLimiter(maxFails int, failWindow, lockDuration time.Duration) *LoginLimiter {
	return &LoginLimiter{
		attempts:   make(map[string]*attemptState),
		maxFails:   maxFails,
		lockFor:    lockDuration,
		failWindow: failWindow,
	}
}

// NewStickyLoginLimiter is like NewLoginLimiter but a cooldown lock does NOT
// reset the failure window: the maxFails budget is enforced per failWindow no
// matter how many short cooldowns elapse. Suited to a per-username throttle with
// a short lock, where a fixed-window reset would let an attacker refill the
// budget every cooldown (20 guesses per lock, forever) instead of bounding it to
// maxFails per window.
func NewStickyLoginLimiter(maxFails int, failWindow, lockDuration time.Duration) *LoginLimiter {
	l := NewLoginLimiter(maxFails, failWindow, lockDuration)
	l.sticky = true
	return l
}

// Allowed reports whether an attempt for key may proceed, and if not, how long
// until it may retry.
func (l *LoginLimiter) Allowed(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.attempts[key]
	if st == nil {
		return true, 0
	}
	if time.Now().Before(st.lockedTill) {
		return false, time.Until(st.lockedTill)
	}
	return true, 0
}

// RecordFailure registers a failed attempt for key and locks it if over the
// threshold within the failure window.
func (l *LoginLimiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	st := l.attempts[key]
	if st == nil || now.Sub(st.firstFail) > l.failWindow {
		st = &attemptState{firstFail: now}
		l.attempts[key] = st
	}
	st.fails++
	if st.fails >= l.maxFails {
		st.lockedTill = now.Add(l.lockFor)
		if !l.sticky {
			// Fixed-window: each cooldown starts a fresh budget.
			st.fails = 0
			st.firstFail = now
		}
		// Sticky: keep fails and firstFail so the cooldown does NOT refill the
		// budget — after the lock expires one more failure re-locks, until the
		// failWindow elapses from the first failure and the state is rebuilt
		// above. Bounds distributed brute force to maxFails per window.
	}
}

// Cleanup removes expired and non-locked attempt entries to prevent memory leaks.
func (l *LoginLimiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for key, st := range l.attempts {
		// Remove if not locked and failure window has passed.
		if now.After(st.lockedTill) && now.Sub(st.firstFail) > l.failWindow {
			delete(l.attempts, key)
		}
	}
}

// Reset clears the failure state for key after a successful login.
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
