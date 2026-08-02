package backup

import (
	"testing"
	"time"
)

func TestValidCron(t *testing.T) {
	valid := []string{"", "0 3 * * *", "0 */6 * * *", "30 4 * * 0", "0 5 1 * *"}
	for _, e := range valid {
		if !ValidCron(e) {
			t.Errorf("ValidCron(%q) = false, want true", e)
		}
	}
	for _, e := range []string{"nonsense", "0 3 * *", "99 3 * * *"} {
		if ValidCron(e) {
			t.Errorf("ValidCron(%q) = true, want false", e)
		}
	}
}

func TestDescribeCron(t *testing.T) {
	cases := map[string]string{
		"":            "Aus — kein automatisches Backup.",
		"0 3 * * *":   "Täglich um 03:00 Uhr.",
		"30 4 * * *":  "Täglich um 04:30 Uhr.",
		"0 */6 * * *": "Alle 6 Stunden.",
		"0 5 * * 0":   "Jeden Sonntag um 05:00 Uhr.",
		"0 5 * * 1":   "Jeden Montag um 05:00 Uhr.",
		"0 2 1 * *":   "Monatlich am 1. um 02:00 Uhr.",
		"@daily":      "Täglich um 00:00 Uhr.",
		"@hourly":     "Stündlich.",
		"@every 6h":   "Alle 6h.",
		"bad":         "Ungültiger Cron-Ausdruck.",
	}
	for expr, want := range cases {
		if got := DescribeCron(expr); got != want {
			t.Errorf("DescribeCron(%q) = %q, want %q", expr, got, want)
		}
	}
}

func TestCronDueAndNext(t *testing.T) {
	// Daily at 03:00. Last run yesterday 03:00; now today 03:01 -> due.
	last := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 2, 3, 1, 0, 0, time.UTC)
	if !CronDue("0 3 * * *", last, now) {
		t.Error("expected daily backup to be due")
	}
	// Same day 02:59 -> not yet due.
	if CronDue("0 3 * * *", last, time.Date(2026, 8, 2, 2, 59, 0, 0, time.UTC)) {
		t.Error("expected not due before the scheduled time")
	}
	// Off/invalid never due.
	if CronDue("", last, now) || CronDue("bad", last, now) {
		t.Error("empty/invalid cron must never be due")
	}
	if runs := NextRuns("0 3 * * *", 3); len(runs) != 3 {
		t.Errorf("NextRuns returned %d, want 3", len(runs))
	}
	if !CronNext("bad", now).IsZero() {
		t.Error("CronNext of invalid must be zero")
	}
}
