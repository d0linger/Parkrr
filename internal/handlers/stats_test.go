package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseYearParam(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  int
	}{
		{name: "empty", want: 2026},
		{name: "valid", query: "?year=2025", want: 2025},
		{name: "out of range", query: "?year=1999", want: 2026},
		{
			name:  "overlong",
			query: "?year=" + strings.Repeat("9", maxNumericParamLen+1),
			want:  2026,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/stats"+tt.query, nil)
			if got := parseYearParam(r, 2026); got != tt.want {
				t.Fatalf("parseYearParam() = %d, want %d", got, tt.want)
			}
		})
	}
}
