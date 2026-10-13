package handlers

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

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

func TestWriteTaxAllocationCSVSafe(t *testing.T) {
	// Verify that writeTaxAllocationCSV neutralizes formula injection triggers in relation and kind fields.
	var buf bytes.Buffer
	mockQ := &mockTaxAllocationQuerier{
		relation: "=SUM(1,2)",
		kind:     "@cmd",
		period:   "+2026-01",
		number:   "|calc",
	}
	if err := writeTaxAllocationCSV(context.Background(), mockQ, &buf, []int64{1}); err != nil {
		t.Fatalf("writeTaxAllocationCSV failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "'=SUM(1,2)") {
		t.Errorf("expected relation formula trigger to be guarded with single apostrophe, got: %s", out)
	}
	if !strings.Contains(out, "'@cmd") {
		t.Errorf("expected kind formula trigger to be guarded with single apostrophe, got: %s", out)
	}
}

type mockTaxAllocationQuerier struct {
	dbQuerier
	relation, kind, period, number string
}

func (m *mockTaxAllocationQuerier) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return &mockTaxAllocationRows{
		relation: m.relation,
		kind:     m.kind,
		period:   m.period,
		number:   m.number,
	}, nil
}

type mockTaxAllocationRows struct {
	pgx.Rows
	relation, kind, period, number string
	read                           bool
}

func (r *mockTaxAllocationRows) Next() bool {
	if !r.read {
		r.read = true
		return true
	}
	return false
}

func (r *mockTaxAllocationRows) Scan(dest ...any) error {
	*dest[0].(*string) = r.relation
	*dest[1].(*int64) = 1
	*dest[2].(*int64) = 10
	*dest[3].(*string) = r.kind
	*dest[4].(*int64) = 100
	*dest[5].(*string) = r.period
	*dest[6].(*int64) = 1000
	*dest[7].(*string) = r.number
	*dest[8].(*float64) = 50.0
	return nil
}

func (r *mockTaxAllocationRows) Close() {}
func (r *mockTaxAllocationRows) Err() error { return nil }
