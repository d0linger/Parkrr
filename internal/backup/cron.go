package backup

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

func parseCron(expr string) (cron.Schedule, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, false
	}
	s, err := cron.ParseStandard(expr)
	if err != nil {
		return nil, false
	}
	return s, true
}

// ValidCron reports whether expr is a valid 5-field cron. Empty is valid — it
// means "off" (no automatic backup for that target).
func ValidCron(expr string) bool {
	if strings.TrimSpace(expr) == "" {
		return true
	}
	_, ok := parseCron(expr)
	return ok
}

// CronDue reports whether the schedule should fire now, given its last run: due
// when now has reached the first scheduled time after `last`.
func CronDue(expr string, last, now time.Time) bool {
	s, ok := parseCron(expr)
	if !ok {
		return false
	}
	return !now.Before(s.Next(last))
}

// CronNext returns the next fire time after `from` (zero for off/invalid).
func CronNext(expr string, from time.Time) time.Time {
	s, ok := parseCron(expr)
	if !ok {
		return time.Time{}
	}
	return s.Next(from)
}

// NextRuns returns up to k upcoming fire times after now.
func NextRuns(expr string, k int) []time.Time {
	s, ok := parseCron(expr)
	if !ok {
		return nil
	}
	out := make([]time.Time, 0, k)
	t := time.Now()
	for i := 0; i < k; i++ {
		t = s.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t)
	}
	return out
}

// describeDescriptor renders a German description of a cron @-descriptor
// (@daily, @hourly, @every 1h30m, …), all accepted by cron.ParseStandard.
func describeDescriptor(expr string) string {
	switch strings.ToLower(strings.Fields(expr)[0]) {
	case "@yearly", "@annually":
		return "Jährlich am 1. Januar um 00:00 Uhr."
	case "@monthly":
		return "Monatlich am 1. um 00:00 Uhr."
	case "@weekly":
		return "Wöchentlich (Sonntag um 00:00 Uhr)."
	case "@daily", "@midnight":
		return "Täglich um 00:00 Uhr."
	case "@hourly":
		return "Stündlich."
	case "@every":
		return "Alle " + strings.TrimSpace(expr[len("@every"):]) + "."
	}
	return "Zeitplan: " + expr
}

var cronDOW = []string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}

// DescribeCron renders a short German description of a 5-field cron, matching the
// mode tabs in the UI (daily / every-N-hours / weekly / monthly / raw).
func DescribeCron(expr string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "Aus — kein automatisches Backup."
	}
	if !ValidCron(expr) {
		return "Ungültiger Cron-Ausdruck."
	}
	// ParseStandard also accepts @-descriptors (@daily, @every 1h, …); describe
	// those rather than mislabeling a valid expression as invalid below.
	if strings.HasPrefix(expr, "@") {
		return describeDescriptor(expr)
	}
	p := strings.Fields(expr)
	if len(p) != 5 {
		return "Ungültiger Cron-Ausdruck."
	}
	minF, hrF, dom, mon, dow := p[0], p[1], p[2], p[3], p[4]
	num := func(s string) (int, bool) { v, err := strconv.Atoi(s); return v, err == nil }
	atTime := func() string {
		h, _ := strconv.Atoi(hrF)
		m, _ := strconv.Atoi(minF)
		return fmt.Sprintf("%02d:%02d", h, m)
	}
	// every N hours: "0 */n * * *"
	if minF == "0" && strings.HasPrefix(hrF, "*/") && dom == "*" && mon == "*" && dow == "*" {
		if n, ok := num(strings.TrimPrefix(hrF, "*/")); ok {
			return "Alle " + strconv.Itoa(n) + " Stunden."
		}
	}
	if _, ok := num(minF); ok {
		if _, ok := num(hrF); ok {
			switch {
			case dom == "*" && mon == "*" && dow != "*":
				if d, ok := num(dow); ok {
					return "Jeden " + cronDOW[((d%7)+7)%7] + " um " + atTime() + " Uhr."
				}
			case dom != "*" && mon == "*" && dow == "*":
				if d, ok := num(dom); ok {
					return "Monatlich am " + strconv.Itoa(d) + ". um " + atTime() + " Uhr."
				}
			case dom == "*" && mon == "*" && dow == "*":
				return "Täglich um " + atTime() + " Uhr."
			}
		}
	}
	return "Cron: " + expr
}
