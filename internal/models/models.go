// Package models defines the core domain types and cost calculations.
package models

import (
	"encoding/json"
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
	if !to.After(from) {
		return 0
	}
	cents := toCents(amount)
	var total int64
	if period == BillingYearly {
		total = prorateByYear(cents, from, to)
	} else {
		total = prorateByMonth(cents, from, to)
	}
	return float64(total) / 100
}

func prorateByYear(cents int64, from, to time.Time) int64 {
	var total int64
	for y := from.Year(); y <= to.Year(); y++ {
		yStart := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		yEnd := time.Date(y+1, 1, 1, 0, 0, 0, 0, time.UTC)
		s, e := maxTime(from, yStart), minTime(to, yEnd)
		if e.After(s) {
			total += fractionCents(cents, days(s, e), days(yStart, yEnd))
		}
	}
	return total
}

func prorateByMonth(cents int64, from, to time.Time) int64 {
	var total int64
	cur := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	for cur.Before(to) {
		mEnd := cur.AddDate(0, 1, 0)
		s, e := maxTime(from, cur), minTime(to, mEnd)
		if e.After(s) {
			total += fractionCents(cents, days(s, e), days(cur, mEnd))
		}
		cur = mEnd
	}
	return total
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

// FlatRateActive reports whether this person is billed a flat rate.
func (p *Person) FlatRateActive() bool {
	return p.FlatRate != nil && *p.FlatRate > 0 && p.FlatRateStart != nil
}

// FlatRateInRange returns the flat-rate cost accrued within [from, to),
// constrained to the flat-rate start/end window.
func (p *Person) FlatRateInRange(from, to time.Time) float64 {
	if !p.FlatRateActive() {
		return 0
	}
	start := maxTime(*p.FlatRateStart, from)
	end := to
	if p.FlatRateEnd != nil && p.FlatRateEnd.Before(end) {
		end = *p.FlatRateEnd
	}
	return ProrateAmount(*p.FlatRate, p.FlatRatePeriod, start, end)
}

// FlatRateAccruedAsOf returns the flat-rate cost accrued until asOf.
func (p *Person) FlatRateAccruedAsOf(asOf time.Time) float64 {
	if !p.FlatRateActive() {
		return 0
	}
	return p.FlatRateInRange(*p.FlatRateStart, asOf.AddDate(0, 0, 1))
}

// ProrateAmount prorates a flat amount over [from, to) for the billing period,
// using calendar-accurate proration (see prorate).
func ProrateAmount(amount float64, period string, from, to time.Time) float64 {
	return prorate(amount, period, from, to)
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
	Paid       bool       `json:"paid"`
	Note       string     `json:"note"`
	VehicleIDs []int64    `json:"vehicle_ids"` // empty = all of the person's vehicles

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Derived (not stored).
	Accrued float64 `json:"accrued"`
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

// VehicleUncoveredCost returns a vehicle's per-vehicle cost within [from, to)
// EXCLUDING the time covered by any of the given agreements (which must be the
// agreements that cover this vehicle and must not overlap each other). Because
// proration is additive over disjoint calendar intervals, each covered window's
// cost is simply subtracted from the full per-vehicle cost.
func VehicleUncoveredCost(v *Vehicle, cat Category, from, to time.Time, covering []FlatRatePeriod) float64 {
	total := v.CostInRange(cat, from, to)
	for i := range covering {
		s, e := covering[i].window(from, to)
		if e.After(s) {
			total -= v.CostInRange(cat, s, e)
		}
	}
	if total < 0 {
		total = 0
	}
	return total
}

// Category is a centrally-managed vehicle type with default pricing.
type Category struct {
	ID                 int64     `json:"id"`
	Name               string    `json:"name"`
	DefaultMonthlyCost float64   `json:"default_monthly_cost"`
	DefaultYearlyCost  float64   `json:"default_yearly_cost"`
	RatesSynced        bool      `json:"rates_synced"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Vehicle is a stored conveyance belonging to a Person.
// CostOverride, when non-nil, replaces the category default rate.
type Vehicle struct {
	ID            int64      `json:"id"`
	PersonID      int64      `json:"person_id"`
	CategoryID    int64      `json:"category_id"`
	Label         string     `json:"label"`
	LicensePlate  string     `json:"license_plate"`
	Notes         string     `json:"notes"`
	BillingPeriod string     `json:"billing_period"`
	CostOverride  *float64   `json:"cost_override"`
	StartDate     time.Time  `json:"start_date"`
	EndDate       *time.Time `json:"end_date"`
	Status        string     `json:"status"`
	ReservedFrom  *time.Time `json:"reserved_from"`
	ReservedUntil *time.Time `json:"reserved_until"`
	Paid          bool       `json:"paid"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`

	// Derived / joined fields (not stored directly).
	CategoryName  string  `json:"category_name,omitempty"`
	PersonName    string  `json:"person_name,omitempty"`
	AccruedCost   float64 `json:"accrued_cost"`
	EffectiveRate float64 `json:"effective_rate"`
	IsActive      bool    `json:"is_active"`
	PhotoCount    int     `json:"photo_count"`
}

// ServiceType is a catalog entry for a chargeable extra service.
type ServiceType struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	DefaultAmount float64   `json:"default_amount"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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

// EffectiveRate returns the rate that applies to this vehicle, using the
// override if present, otherwise the category default for the billing period.
func (v *Vehicle) EffectiveRateFor(cat Category) float64 {
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
