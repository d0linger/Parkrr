package handlers

import (
	"math"
	"testing"
)

// TestRound2HalfAwayFromZero locks the half-away-from-zero behavior — the old
// int64(f*100+0.5) truncated negative (overpaid) balances toward zero.
func TestRound2HalfAwayFromZero(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0.125, 0.13}, {-0.125, -0.13}, // the sign-sensitive case
		{0.375, 0.38}, {-0.375, -0.38},
		{2.5, 2.5}, {-1.0, -1.0},
	}
	for _, c := range cases {
		if got := round2(c.in); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("round2(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
