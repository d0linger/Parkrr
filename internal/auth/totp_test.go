package auth

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

// TestValidateTOTPStep checks the replay primitive: a valid code returns the
// 30-second step it matched, garbage is rejected, and consecutive windows yield
// strictly increasing steps (so "accept only a newer step" makes each code
// single-use).
func TestValidateTOTPStep(t *testing.T) {
	key, err := GenerateTOTP("replay@parkrr")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	secret := key.Secret()

	now := time.Unix(1_700_000_000, 0) // fixed instant, mid-window
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("code: %v", err)
	}

	step, ok := ValidateTOTPStep(secret, code, now)
	if !ok {
		t.Fatal("valid code must validate")
	}
	if want := now.Unix() / 30; step != want {
		t.Errorf("step = %d, want %d", step, want)
	}

	// The same code one window later still matches (via -1 skew) but reports the
	// SAME step, so a replay guard keyed on the step rejects it.
	if step2, ok2 := ValidateTOTPStep(secret, code, now.Add(30*time.Second)); !ok2 || step2 != step {
		t.Errorf("replayed code within skew must keep the same step: ok=%v step=%d (first %d)", ok2, step2, step)
	}

	// A fresh code a full window later reports a strictly greater step.
	later := now.Add(30 * time.Second)
	nextCode, _ := totp.GenerateCode(secret, later)
	if nextStep, ok3 := ValidateTOTPStep(secret, nextCode, later); !ok3 || nextStep <= step {
		t.Errorf("next-window code must advance the step: ok=%v step=%d (prev %d)", ok3, nextStep, step)
	}

	// Garbage is rejected.
	if _, ok := ValidateTOTPStep(secret, "000000", now.Add(10*time.Minute)); ok {
		t.Error("a code far outside any window must not validate")
	}
}
