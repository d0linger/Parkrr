package backup

import (
	"testing"
	"time"
)

// The tile's whole value is that it turns red when — and only when — a backup has
// actually stopped happening. A threshold that fires on a healthy weekly schedule
// gets ignored within a week; one that stays green on a dead hourly schedule is
// worse than no tile at all. So the verdict is derived from each target's own cron.

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestOverdueFollowsTheTargetsOwnSchedule(t *testing.T) {
	now := at("2026-08-19T12:00:00Z")
	for _, tc := range []struct {
		name    string
		cron    string
		last    string
		overdue bool
	}{
		// Nightly at 03:00: this morning's run is fine, the day before is not.
		{"nightly, ran today", "0 3 * * *", "2026-08-19T03:00:00Z", false},
		{"nightly, missed one night", "0 3 * * *", "2026-08-18T03:00:00Z", true},
		// Weekly on Sunday, evaluated on a Wednesday: the run from LAST Sunday
		// (2026-08-16) is three days old and entirely normal — a fixed 48-hour
		// threshold would cry wolf here every single week.
		{"weekly, ran last Sunday", "0 3 * * 0", "2026-08-16T03:00:00Z", false},
		// 2026-08-15 is a SATURDAY, so a Sunday schedule has genuinely missed a run.
		{"weekly, last run predates the Sunday", "0 3 * * 0", "2026-08-15T03:00:00Z", true},
		{"weekly, three weeks old", "0 3 * * 0", "2026-07-26T03:00:00Z", true},
		// Hourly: two hours is late, ten minutes is not — the same fixed threshold
		// would have stayed green here for two full days.
		{"hourly, ten minutes old", "0 * * * *", "2026-08-19T11:50:00Z", false},
		{"hourly, four hours old", "0 * * * *", "2026-08-19T08:00:00Z", true},
	} {
		last := at(tc.last)
		h := targetHealth(true, tc.cron, &last, true, now)
		if h.Overdue != tc.overdue {
			t.Errorf("%s: overdue = %v, want %v (cron %q, last %s)",
				tc.name, h.Overdue, tc.overdue, tc.cron, tc.last)
		}
	}
}

func TestGracePeriodAbsorbsARunInProgress(t *testing.T) {
	// Scheduled 03:00, it is now 03:20 and last night's run is the newest one. The
	// backup may still be running; flipping to red every night at 03:00 would train
	// the operator to ignore the tile.
	last := at("2026-08-18T03:00:00Z")
	if h := targetHealth(true, "0 3 * * *", &last, true, at("2026-08-19T03:20:00Z")); h.Overdue {
		t.Error("a target within the grace window must not be reported overdue")
	}
	// Well past the grace window, it is genuinely late.
	if h := targetHealth(true, "0 3 * * *", &last, true, at("2026-08-19T08:00:00Z")); !h.Overdue {
		t.Error("past the grace window the missed run must be reported")
	}
}

func TestScheduledButNeverRunIsTheLoudestCase(t *testing.T) {
	h := targetHealth(true, "0 3 * * *", nil, false, at("2026-08-19T12:00:00Z"))
	if !h.Overdue || !h.Never {
		t.Errorf("a scheduled target that never produced a backup must be overdue and flagged "+
			"as never-run, got overdue=%v never=%v", h.Overdue, h.Never)
	}
	if h.AgeSeconds != nil {
		t.Error("there is no age without a run — age must stay nil rather than read as 0")
	}
}

func TestUnscheduledTargetIsNeverOverdue(t *testing.T) {
	// An empty cron means the operator switched the schedule off. An old backup is
	// then a deliberate state, not a fault.
	old := at("2020-01-01T00:00:00Z")
	for _, cron := range []string{"", "not a cron", "* * *"} {
		if h := targetHealth(true, cron, &old, true, at("2026-08-19T12:00:00Z")); h.Overdue {
			t.Errorf("cron %q is off/invalid, so the target must not be reported overdue", cron)
		}
	}
}

func TestUnconfiguredTargetReportsNothing(t *testing.T) {
	h := targetHealth(false, "0 3 * * *", nil, false, at("2026-08-19T12:00:00Z"))
	if h.Overdue || h.Never || h.AgeSeconds != nil {
		t.Error("a target that is not configured at all must stay silent, not raise an alarm")
	}
}

func TestAgeIsReportedEvenWhenNotOverdue(t *testing.T) {
	last := at("2026-08-19T09:00:00Z")
	h := targetHealth(true, "0 3 * * *", &last, true, at("2026-08-19T12:00:00Z"))
	if h.AgeSeconds == nil || *h.AgeSeconds != 3*3600 {
		t.Errorf("age should be 3h in seconds, got %v", h.AgeSeconds)
	}
}

func TestBackupHealthSplitsTheTwoTargets(t *testing.T) {
	now := at("2026-08-19T12:00:00Z")
	volLast, s3Last := at("2026-08-19T03:00:00Z"), at("2026-08-10T04:00:00Z")
	st := Status{LastVolumeAt: &volLast, LastVolumeOK: true, LastS3At: &s3Last, LastS3OK: true}
	s := Settings{VolumeCron: "0 3 * * *", S3Cron: "0 4 * * *"}

	h := BackupHealth(s, st, true, true, now)
	if h.Volume.Overdue {
		t.Error("the volume target ran this morning and must be green")
	}
	if !h.S3.Overdue {
		t.Error("the S3 target is nine days behind a daily schedule and must be red")
	}
	// One healthy target must not mask the other — that is the entire point of
	// reporting them separately.
	if h2 := BackupHealth(s, st, true, false, now); h2.S3.Overdue {
		t.Error("an S3 target that is not configured must not report overdue")
	}
}
