package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestPageParams(t *testing.T) {
	cases := []struct {
		query            string
		defLimit         int
		maxLimit         int
		wantLim, wantOff int
	}{
		{"", 100, 500, 100, 0},                    // defaults
		{"?limit=50&offset=20", 100, 500, 50, 20}, // explicit
		{"?limit=9999", 100, 500, 500, 0},         // capped at max
		{"?limit=-5", 100, 500, 100, 0},           // invalid ignored
		{"?offset=-3", 100, 500, 100, 0},          // negative offset ignored
		{"?limit=abc", 100, 500, 100, 0},          // non-numeric ignored
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/x"+c.query, nil)
		lim, off := pageParams(r, c.defLimit, c.maxLimit)
		if lim != c.wantLim || off != c.wantOff {
			t.Errorf("%q: got (limit=%d, offset=%d) want (%d, %d)",
				c.query, lim, off, c.wantLim, c.wantOff)
		}
	}
}

func TestRound2(t *testing.T) {
	cases := map[float64]float64{
		2.346:   2.35,
		2.344:   2.34,
		100.001: 100.0,
		0:       0,
	}
	for in, want := range cases {
		if got := round2(in); got != want {
			t.Errorf("round2(%v): got %v want %v", in, got, want)
		}
	}
}
