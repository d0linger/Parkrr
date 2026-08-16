package handlers

import "testing"

// csvSafe neutralizes formula triggers and csvUnguard reverses it exactly, so an
// exported file re-imports losslessly (protects the export-as-template workflow).
func TestCSVSafeRoundTrip(t *testing.T) {
	cases := map[string]string{
		`=HYPERLINK("x")`: `'=HYPERLINK("x")`,
		"+43 660 12345":   "'+43 660 12345",
		"-1+2":            "'-1+2",
		"@cmd":            "'@cmd",
		"|calc":           "'|calc",
		"%10":             "'%10",
		"'|calc":          "''|calc", // literal leading apostrophe before a trigger — doubled, round-trips
		"'%10":            "''%10",
		"Normaler Name":   "Normaler Name",
		"":                "",
		"O'Brien":         "O'Brien",
	}
	for in, wantGuarded := range cases {
		if got := csvSafe(in); got != wantGuarded {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, wantGuarded)
		}
		if got := csvUnguard(csvSafe(in)); got != in {
			t.Errorf("round-trip csvUnguard(csvSafe(%q)) = %q", in, got)
		}
	}
	// A legitimate leading apostrophe that does not guard a trigger is preserved.
	if got := csvUnguard("'99"); got != "'99" {
		t.Errorf("csvUnguard(%q) = %q, want unchanged", "'99", got)
	}
}
