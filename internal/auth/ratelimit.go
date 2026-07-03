package auth

import (
	"sync"
	"time"
)

// LoginLimiter is an in-memory login throttle. After too many failures for a
// given key (username+IP) it locks that key for a cooldown window. Suitable for
// a single-instance deployment.
type LoginLimiter struct {
	mu        sync.Mutex
	attempts  map[string]*attemptState
	maxFails  int
	lockFor   time.Duration
	failWindw time.Duration
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
		attempts:  make(map[string]*attemptState),
		maxFails:  maxFails,
		lockFor:   lockDuration,
		failWindw: failWindow,
	}
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
	if st == nil || now.Sub(st.firstFail) > l.failWindw {
		st = &attemptState{firstFail: now}
		l.attempts[key] = st
	}
	st.fails++
	if st.fails >= l.maxFails {
		st.lockedTill = now.Add(l.lockFor)
		st.fails = 0
		st.firstFail = now
	}
}

// Reset clears the failure state for key after a successful login.
func (l *LoginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
