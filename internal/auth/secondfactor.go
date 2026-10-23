package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Second-factor failure accounting (audit AUTH-03).
//
// The in-memory login limiters are keyed on IPs and usernames, forget
// everything on restart and are reset by every successful login, so an attacker
// who already knows the password could keep guessing TOTP codes indefinitely at
// about 130 per hour. This counter is persistent, per account, and only a
// successful second factor (or an admin 2FA reset) clears it; a correct password
// alone does not.
//
// Policy: SecondFactorFreeFailures consecutive failures cost nothing, so a user
// who mistypes a code a few times never waits. Each further failure locks the
// second factor for 1, 2, 4, 8 ... minutes, capped at SecondFactorMaxLock. A
// legitimate user therefore waits at most a minute or two after a burst of
// typos, while a guesser gets about 16 codes in the first day and a half and one
// per day after that, instead of about 3000 per day.
//
// Only a caller who knew the password reaches this counter, so the lock cannot
// be used to lock a user out without that knowledge; passkey sign-in is not
// affected by it.
const (
	SecondFactorFreeFailures = 5
	SecondFactorMaxLock      = 24 * time.Hour
)

// ReserveSecondFactorAttempt atomically counts one second-factor attempt for
// userID BEFORE the code is checked, so parallel guesses cannot slip past the
// lock between check and count. It returns the account's consecutive failure
// count including this attempt. When the second factor is currently locked,
// nothing is counted and lockedFor is the remaining lock time (> 0).
//
// A successful check must call ResetSecondFactorFailures; a check that could not
// be completed for a server-side reason should call ReleaseSecondFactorAttempt.
// expectedTOTPSecret is the encrypted credential the login loaded. A reset or
// re-enrollment invalidates the attempt instead of charging the new credential.
func (m *Manager) ReserveSecondFactorAttempt(
	ctx context.Context, userID int64, expectedTOTPSecret string,
) (failures int, lockedFor time.Duration, err error) {
	err = m.pool.QueryRow(ctx,
		`UPDATE users
		    SET totp_failures = totp_failures + 1,
		        totp_locked_until = CASE
		            WHEN totp_failures + 1 >= $2 THEN now() + LEAST(
		                make_interval(mins => 1) * power(2, LEAST(totp_failures + 1 - $2, 20)),
		                make_interval(secs => $3))
		            ELSE NULL END
		  WHERE id = $1 AND totp_enabled AND totp_secret = $4
		    AND (totp_locked_until IS NULL OR totp_locked_until <= now())
		  RETURNING totp_failures`,
		userID, SecondFactorFreeFailures, SecondFactorMaxLock.Seconds(), expectedTOTPSecret).Scan(&failures)
	if err == nil {
		return failures, 0, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, err
	}
	// No row updated: distinguish the current credential's lock from a revoked
	// credential without reading or exposing the replacement's failure state.
	var until *time.Time
	if err := m.pool.QueryRow(ctx,
		`SELECT totp_failures, totp_locked_until FROM users
		  WHERE id = $1 AND totp_enabled AND totp_secret = $2`,
		userID, expectedTOTPSecret).Scan(&failures, &until); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, ErrCredentialChanged
		}
		return 0, 0, err
	}
	lockedFor = time.Second
	if until != nil {
		if d := time.Until(*until); d > lockedFor {
			lockedFor = d
		}
	}
	return failures, lockedFor, nil
}

// ReleaseSecondFactorAttempt takes back a reservation whose check failed for a
// server-side reason, so a transient database error does not count as a guess.
// A revoked encrypted credential returns ErrCredentialChanged without changing
// the failure count of a replacement factor.
func (m *Manager) ReleaseSecondFactorAttempt(ctx context.Context, userID int64, expectedTOTPSecret string) error {
	ct, err := m.pool.Exec(ctx,
		`UPDATE users SET totp_failures = GREATEST(totp_failures - 1, 0)
		  WHERE id = $1 AND totp_enabled AND totp_secret = $2`, userID, expectedTOTPSecret)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrCredentialChanged
	}
	return nil
}

// ResetSecondFactorFailures clears the counter and any lock after a successful
// second factor. It is deliberately NOT called on a password-only success.
// The success only clears the encrypted credential that was actually checked.
func (m *Manager) ResetSecondFactorFailures(ctx context.Context, userID int64, expectedTOTPSecret string) error {
	ct, err := m.pool.Exec(ctx,
		`UPDATE users SET totp_failures = 0, totp_locked_until = NULL
		  WHERE id = $1 AND totp_enabled AND totp_secret = $2`, userID, expectedTOTPSecret)
	if err != nil {
		return err
	}
	if ct.RowsAffected() != 1 {
		return ErrCredentialChanged
	}
	return nil
}
