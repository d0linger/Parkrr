// Package models defines the core domain types and cost calculations.
package models

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// BillingPeriod enumerates the supported billing intervals.
const (
	BillingMonthly = "monthly"
	BillingYearly  = "yearly"
)

// Role enumerates user roles, from most to least privileged.
const (
	RoleAdmin  = "admin"  // full access, including user management and the audit log
	RoleEditor = "editor" // everything except user management and the audit log
	RoleReader = "reader" // read-only access
)

// ValidRoles is the set of assignable roles.
var ValidRoles = map[string]bool{
	RoleAdmin: true, RoleEditor: true, RoleReader: true,
}

// VehicleStatus enumerates the lifecycle states of a vehicle.
const (
	StatusReserved  = "reserved"
	StatusStored    = "stored"
	StatusCollected = "collected"
	StatusCancelled = "cancelled"
)

// ValidStatuses is the set of valid vehicle statuses.
var ValidStatuses = map[string]bool{
	StatusReserved: true, StatusStored: true, StatusCollected: true, StatusCancelled: true,
}

// prorate returns amount prorated over [from, to) for the billing period,
// measured against the ACTUAL length of each calendar month/year the interval
// touches. As a result a full calendar month or year bills exactly the
// configured amount; only genuinely partial periods are charged proportionally.
//
// The arithmetic is carried out in integer cents (not float euros): each
// partial period is rounded to a whole cent and the periods are summed as
// integers, so aggregating many months/years never accumulates binary
// floating-point drift. money is stored as exact NUMERIC(12,2) in the database.
func prorate(amount float64, period string, from, to time.Time) float64 {
	cents := toCents(amount)
	var total int64
	walkSubPeriods(period, from, to, func(s, e, pStart, pEnd time.Time) {
		total += fractionCents(cents, days(s, e), days(pStart, pEnd))
	})
	return float64(total) / 100
}

// walkSubPeriods invokes fn for each calendar sub-period (month for monthly,
// year for yearly) that [from, to) intersects, passing the intersected slice
// [s, e) and the full sub-period bounds [pStart, pEnd). It is the single source
// of truth for the calendar walk shared by prorate and FlatRatePeriod.subPeriods,
// so per-period costs always sum to the prorated total.
func walkSubPeriods(period string, from, to time.Time, fn func(s, e, pStart, pEnd time.Time)) {
	if !to.After(from) {
		return
	}
	// emit clips one sub-period [pStart, pEnd) to [from, to) and dispatches the
	// overlap, so the two calendar loops share the intersection logic.
	emit := func(pStart, pEnd time.Time) {
		if s, e := maxTime(from, pStart), minTime(to, pEnd); e.After(s) {
			fn(s, e, pStart, pEnd)
		}
	}
	if period == BillingYearly {
		for y := from.Year(); ; y++ {
			pStart := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
			pEnd := pStart.AddDate(1, 0, 0)
			emit(pStart, pEnd)
			if !pEnd.Before(to) {
				break
			}
		}
		return
	}
	cur := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	for cur.Before(to) {
		pEnd := cur.AddDate(0, 1, 0)
		emit(cur, pEnd)
		cur = pEnd
	}
}

func days(a, b time.Time) float64 { return b.Sub(a).Hours() / 24.0 }

// toCents converts a euro amount to integer cents, rounded to the nearest cent.
func toCents(euros float64) int64 { return int64(math.Round(euros * 100)) }

// fractionCents returns cents * num/den rounded to the nearest whole cent. A
// full period (num == den) returns exactly cents, so a complete month or year
// always bills the configured amount to the cent.
func fractionCents(cents int64, num, den float64) int64 {
	if den <= 0 {
		return 0
	}
	return int64(math.Round(float64(cents) * num / den))
}

// User is an application login account. Admins manage other users.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	IsAdmin      bool      `json:"is_admin"`
	Role         string    `json:"role"`
	TOTPSecret   string    `json:"-"`
	TOTPEnabled  bool      `json:"totp_enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Person is a customer who stores one or more vehicles.
//
// FlatRate, when set, replaces per-vehicle billing: the person is charged one
// agreed amount (monthly or yearly) that covers all of their vehicles.
type Person struct {
	ID        int64     `json:"id"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	Email     string    `json:"email"`
	Phone     string    `json:"phone"`
	Address   string    `json:"address"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Flat rate (Pauschale). FlatRate == nil means per-vehicle billing.
	FlatRate       *float64   `json:"flat_rate"`
	FlatRatePeriod string     `json:"flat_rate_period"`
	FlatRateStart  *time.Time `json:"flat_rate_start"`
	FlatRateEnd    *time.Time `json:"flat_rate_end"`
	FlatRatePaid   bool       `json:"flat_rate_paid"`

	// Derived (not stored).
	HasFlatRate bool `json:"has_flat_rate"`
}

// FlatRatePeriod is a dated flat-rate agreement ("Pauschale-Eintrag"): one
// agreed amount (monthly or yearly) that covers some or all of a person's
// vehicles for a window, replacing per-vehicle billing for those vehicles while
// it is active. Multiple agreements can exist over time.
type FlatRatePeriod struct {
	ID         int64      `json:"id"`
	PersonID   int64      `json:"person_id"`
	Amount     float64    `json:"amount"`
	Period     string     `json:"period"` // monthly | yearly
	StartDate  time.Time  `json:"start_date"`
	EndDate    *time.Time `json:"end_date"` // nil = open-ended
	Paid       bool       `json:"paid"`     // master flag: whole agreement paid
	Note       string     `json:"note"`
	VehicleIDs []int64    `json:"vehicle_ids"` // empty = all of the person's vehicles
	// PaidPeriods lists the sub-period keys paid in full ("YYYY" for yearly,
	// "YYYY-MM" for monthly) — the whole period is settled (prepaid). A sub-period
	// is fully paid when Paid is set OR its key is in this list.
	PaidPeriods []string `json:"paid_periods"`
	// PaidFixed maps a sub-period key to a fixed partial amount ("Teilbetrag") in
	// euros: only that amount is credited, the rest of the period stays open.
	PaidFixed map[string]float64 `json:"paid_fixed,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Derived (not stored).
	Accrued float64 `json:"accrued"`
	// Settled is true when everything accrued so far is paid (whole periods, fixed
	// partials or the master flag). An ended + settled agreement may archive.
	Settled bool `json:"settled"`
	// PeriodCosts maps each elapsed sub-period key to its accrued cost (euros),
	// so the per-period payment list can show the amount beside each year/month.
	PeriodCosts map[string]float64 `json:"period_costs,omitempty"`
}

// paidKeySet returns the set of sub-period keys explicitly marked paid.
func (a *FlatRatePeriod) paidKeySet() map[string]bool {
	m := make(map[string]bool, len(a.PaidPeriods))
	for _, k := range a.PaidPeriods {
		m[k] = true
	}
	return m
}

// ActiveAt reports whether the agreement's coverage window includes t, i.e.
// StartDate <= t < EndDate (EndDate is exclusive; nil means open-ended). Once
// the end date has passed the agreement no longer covers its vehicles.
func (a *FlatRatePeriod) ActiveAt(t time.Time) bool {
	if t.Before(a.StartDate) {
		return false
	}
	if a.EndDate != nil && !t.Before(*a.EndDate) {
		return false
	}
	return true
}

// Ended reports whether the agreement's coverage window has closed by asOf
// (EndDate is exclusive; an open-ended agreement never ends).
func (a *FlatRatePeriod) Ended(asOf time.Time) bool {
	return a.EndDate != nil && !asOf.Before(*a.EndDate)
}

// SettledAsOf reports whether everything accrued through asOf has been paid — the
// paid amount (whole periods + fixed partials + master flag) covers the accrued
// cost. Used to decide when a finished Pauschale (and its vehicles) may archive.
func (a *FlatRatePeriod) SettledAsOf(asOf time.Time) bool {
	until := asOf.AddDate(0, 0, 1)
	return a.PaidCentsInRange(a.StartDate, until) >= toCents(a.CostInRange(a.StartDate, until))
}

// PeriodKey returns the sub-period key that time t falls in for this agreement's
// billing period: "YYYY" for yearly agreements, "YYYY-MM" for monthly ones.
func (a *FlatRatePeriod) PeriodKey(t time.Time) string {
	if a.Period == BillingYearly {
		return fmt.Sprintf("%04d", t.Year())
	}
	return fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month()))
}

// PeriodPaidAt reports whether the sub-period containing t is settled in full —
// either the whole-agreement Paid flag or a whole-period payment for that key. A
// fixed partial payment (PaidFixed) does not count as fully paid.
func (a *FlatRatePeriod) PeriodPaidAt(t time.Time) bool {
	return a.Paid || a.paidKeySet()[a.PeriodKey(t)]
}

// subPeriods invokes fn for each calendar sub-period (month or year, per Period)
// that the agreement's active window intersects within [from, to). It passes the
// intersected slice [s, e), the full sub-period bounds [pStart, pEnd) and the
// sub-period key. Iteration mirrors prorate, so a sub-period's cost is exactly
// fractionCents(toCents(Amount), days(s,e), days(pStart,pEnd)).
func (a *FlatRatePeriod) subPeriods(from, to time.Time, fn func(s, e, pStart, pEnd time.Time, key string)) {
	s, e := a.window(from, to)
	walkSubPeriods(a.Period, s, e, func(ss, ee, pStart, pEnd time.Time) {
		fn(ss, ee, pStart, pEnd, a.PeriodKey(pStart))
	})
}

// PaidCentsInRange returns, in integer cents, the portion of the agreement's
// cost within [from, to) that is marked paid — either by the whole-agreement
// Paid flag or by an explicit per-period payment. When Paid is set this equals
// toCents(CostInRange), so the legacy "fully paid" behavior is preserved.
func (a *FlatRatePeriod) PaidCentsInRange(from, to time.Time) int64 {
	paid := a.paidKeySet()
	cents := toCents(a.Amount)
	var total int64
	a.subPeriods(from, to, func(s, e, pStart, pEnd time.Time, key string) {
		if a.Paid || paid[key] {
			// Whole period paid (prepaid): credit the accrued portion, so a fully
			// paid period always nets to zero.
			total += fractionCents(cents, days(s, e), days(pStart, pEnd))
		} else if fx, ok := a.PaidFixed[key]; ok {
			// Fixed partial payment: credit the lump once (paid totals are computed
			// over the full accrual range, so each fixed amount is counted once).
			total += toCents(fx)
		}
	})
	return total
}

// ElapsedPeriodKeys returns the sub-period keys from the agreement's start
// through asOf (inclusive), oldest first — the periods that have begun and can
// therefore be marked paid.
func (a *FlatRatePeriod) ElapsedPeriodKeys(asOf time.Time) []string {
	var keys []string
	a.subPeriods(a.StartDate, asOf.AddDate(0, 0, 1), func(_, _, _, _ time.Time, key string) {
		keys = append(keys, key)
	})
	return keys
}

// ElapsedPeriodCosts returns the accrued cost (euros) of each elapsed sub-period
// from the agreement's start through asOf, keyed by sub-period key. The values
// sum to the agreement's accrued total.
func (a *FlatRatePeriod) ElapsedPeriodCosts(asOf time.Time) map[string]float64 {
	out := map[string]float64{}
	cents := toCents(a.Amount)
	a.subPeriods(a.StartDate, asOf.AddDate(0, 0, 1), func(s, e, pStart, pEnd time.Time, key string) {
		out[key] += float64(fractionCents(cents, days(s, e), days(pStart, pEnd))) / 100
	})
	return out
}

// Covers reports whether this agreement applies to the given vehicle. An empty
// VehicleIDs set means "all of the person's vehicles".
func (a *FlatRatePeriod) Covers(vehicleID int64) bool {
	if len(a.VehicleIDs) == 0 {
		return true
	}
	for _, id := range a.VehicleIDs {
		if id == vehicleID {
			return true
		}
	}
	return false
}

// window returns the agreement's active interval intersected with [from, to).
func (a *FlatRatePeriod) window(from, to time.Time) (time.Time, time.Time) {
	s := maxTime(a.StartDate, from)
	e := to
	if a.EndDate != nil && a.EndDate.Before(e) {
		e = *a.EndDate
	}
	return s, e
}

// CostInRange returns the agreement's prorated cost within [from, to),
// constrained to its own window.
func (a *FlatRatePeriod) CostInRange(from, to time.Time) float64 {
	s, e := a.window(from, to)
	if !e.After(s) {
		return 0
	}
	return prorate(a.Amount, a.Period, s, e)
}

// AccruedAsOf returns the agreement cost accrued through asOf (inclusive).
func (a *FlatRatePeriod) AccruedAsOf(asOf time.Time) float64 {
	return a.CostInRange(a.StartDate, asOf.AddDate(0, 0, 1))
}

// Category is a centrally-managed vehicle type with default pricing.
type Category struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	DefaultMonthlyCost float64 `json:"default_monthly_cost"`
	DefaultYearlyCost  float64 `json:"default_yearly_cost"`
	RatesSynced        bool    `json:"rates_synced"`
	// Archived hides a tariff from the pickers (new vehicle / agreement) while
	// keeping it valid for existing vehicles, whose rate is locked anyway.
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Vehicle is a stored conveyance belonging to a Person.
//
// Rate is the effective price per billing period, locked onto the vehicle when
// it is created/edited. Because pricing reads Rate (not the live category), a
// later Tarif change never re-prices existing vehicles. CostOverride is legacy.
type Vehicle struct {
	ID            int64      `json:"id"`
	PersonID      int64      `json:"person_id"`
	CategoryID    int64      `json:"category_id"`
	Label         string     `json:"label"`
	LicensePlate  string     `json:"license_plate"`
	Notes         string     `json:"notes"`
	BillingPeriod string     `json:"billing_period"`
	Rate          float64    `json:"rate"`
	CostOverride  *float64   `json:"cost_override"`
	StartDate     time.Time  `json:"start_date"`
	EndDate       *time.Time `json:"end_date"`
	Status        string     `json:"status"`
	ReservedFrom  *time.Time `json:"reserved_from"`
	ReservedUntil *time.Time `json:"reserved_until"`
	Paid          bool       `json:"paid"`
	// Archived marks a closed vehicle (cancelled, or collected + paid) that has
	// been tidied out of the active lists. Archived vehicles are read-only until
	// reactivated, but still count in all billing and statistics.
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Derived / joined fields (not stored directly).
	CategoryName  string  `json:"category_name,omitempty"`
	PersonName    string  `json:"person_name,omitempty"`
	AccruedCost   float64 `json:"accrued_cost"`
	EffectiveRate float64 `json:"effective_rate"`
	IsActive      bool    `json:"is_active"`
	PhotoCount    int     `json:"photo_count"`
	// FlatRateCovered is true when at least one flat-rate agreement covers this
	// vehicle (bound = billed entirely via the Pauschale, never per-vehicle).
	// FlatRateActive is true only when such an agreement's window includes today.
	// UncoveredCost is what the vehicle owes individually: 0 for bound vehicles
	// (ownership model — the Pauschale is the only charge), so the per-vehicle
	// paid marker is redundant for them.
	FlatRateCovered bool    `json:"flat_rate_covered"`
	FlatRateActive  bool    `json:"flat_rate_active"`
	UncoveredCost   float64 `json:"uncovered_cost"`
}

// ServiceType is a catalog entry for a chargeable extra service.
type ServiceType struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	DefaultAmount float64 `json:"default_amount"`
	// Archived hides the service from the "Aus Katalog" picker while keeping it
	// listed; existing charges are snapshots and are unaffected.
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Charge is an extra line item (service) billed to a person/vehicle.
type Charge struct {
	ID          int64     `json:"id"`
	PersonID    int64     `json:"person_id"`
	VehicleID   *int64    `json:"vehicle_id"`
	Description string    `json:"description"`
	Amount      float64   `json:"amount"`
	Quantity    float64   `json:"quantity"`
	ChargedOn   time.Time `json:"charged_on"`
	CreatedAt   time.Time `json:"created_at"`
	PersonName  string    `json:"person_name,omitempty"`
	Total       float64   `json:"total"`
	// Paid is the charge's own settle flag, used when it is not bound to a
	// vehicle. Bound charges instead follow VehiclePaid.
	Paid         bool   `json:"paid"`
	VehiclePaid  bool   `json:"vehicle_paid"`
	VehicleLabel string `json:"vehicle_label,omitempty"`
}

// StatusChange is one entry in a vehicle's status history.
type StatusChange struct {
	ID        int64     `json:"id"`
	VehicleID int64     `json:"vehicle_id"`
	OldStatus string    `json:"old_status"`
	NewStatus string    `json:"new_status"`
	Note      string    `json:"note"`
	ChangedBy string    `json:"changed_by"`
	CreatedAt time.Time `json:"created_at"`
}

// AuditEntry records a change made by a user. Changes, when present, holds the
// per-field before/after values as {"field": {"old": ..., "new": ...}}.
type AuditEntry struct {
	ID        int64           `json:"id"`
	UserID    *int64          `json:"user_id"`
	Username  string          `json:"username"`
	Action    string          `json:"action"`
	Entity    string          `json:"entity"`
	EntityID  *int64          `json:"entity_id"`
	Summary   string          `json:"summary"`
	Changes   json.RawMessage `json:"changes,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// SessionInfo describes an active login session (for session management).
type SessionInfo struct {
	Token     string    `json:"token"`
	UserAgent string    `json:"user_agent"`
	IP        string    `json:"ip"`
	Current   bool      `json:"current"`
	LastSeen  time.Time `json:"last_seen"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// EffectiveRateFor returns the rate that applies to this vehicle. The rate is
// locked onto the vehicle (Rate), so changing a Tarif never re-prices existing
// vehicles. cat is only a fallback for legacy rows created before Rate existed.
func (v *Vehicle) EffectiveRateFor(cat Category) float64 {
	if v.Rate != 0 {
		return v.Rate
	}
	if v.CostOverride != nil {
		return *v.CostOverride
	}
	if v.BillingPeriod == BillingYearly {
		return cat.DefaultYearlyCost
	}
	return cat.DefaultMonthlyCost
}

// CostInRange computes the accrued cost for the vehicle within [from, to).
// The vehicle's own start/end dates further constrain the interval.
func (v *Vehicle) CostInRange(cat Category, from, to time.Time) float64 {
	start := maxTime(v.StartDate, from)
	end := to
	if v.EndDate != nil && v.EndDate.Before(end) {
		end = *v.EndDate
	}
	if !end.After(start) {
		return 0
	}
	return prorate(v.EffectiveRateFor(cat), v.BillingPeriod, start, end)
}

// AccruedCostAsOf returns the total cost accrued from the start date until the
// earlier of the end date or asOf.
func (v *Vehicle) AccruedCostAsOf(cat Category, asOf time.Time) float64 {
	return v.CostInRange(cat, v.StartDate, asOf.AddDate(0, 0, 1))
}

// YearStat is the aggregated cost for a person in a single calendar year.
type YearStat struct {
	Year         int     `json:"year"`
	Cost         float64 `json:"cost"`
	VehicleCount int     `json:"vehicle_count"`
	Paid         bool    `json:"paid"` // flat-rate per-year paid status
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
