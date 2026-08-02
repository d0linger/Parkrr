package backup

import (
	"testing"
	"time"
)

func TestFireDue(t *testing.T) {
	now := time.Date(2026, 8, 2, 3, 1, 0, 0, time.UTC)
	yesterday := time.Date(2026, 8, 1, 3, 0, 0, 0, time.UTC)
	today0259 := time.Date(2026, 8, 2, 2, 59, 0, 0, time.UTC)

	// Off (empty cron) is never due, even with no prior run.
	if fireDue("", nil, now) {
		t.Error("empty cron must never fire")
	}
	// Never run + a real schedule: take an initial backup now.
	if !fireDue("0 3 * * *", nil, now) {
		t.Error("a never-run target should fire an initial backup")
	}
	// Ran yesterday at 03:00, now past 03:00 today -> due.
	last := yesterday
	if !fireDue("0 3 * * *", &last, now) {
		t.Error("daily backup should be due after the scheduled time")
	}
	// Ran yesterday, now still before today's 03:00 -> not due.
	if fireDue("0 3 * * *", &last, today0259) {
		t.Error("daily backup should not be due before the scheduled time")
	}
	// Invalid cron never fires.
	if fireDue("nonsense", nil, now) {
		t.Error("invalid cron must never fire")
	}
}
