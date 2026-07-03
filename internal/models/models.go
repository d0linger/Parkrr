// Package models defines the core domain types and cost calculations.
package models

import "time"

// BillingPeriod enumerates the supported billing intervals.
const (
	BillingMonthly = "monthly"
	BillingYearly  = "yearly"
)

// Role enumerates user roles, from most to least privileged.
const (
	RoleAdmin      = "admin"      // full access incl. users, tariffs, roles
	RoleManager    = "manager"    // manage persons, vehicles, reservations, photos
	RoleAccounting = "accounting" // payments, charges, read everything
	RoleReadonly   = "readonly"   // read-only access
)

// ValidRoles is the set of assignable roles.
var ValidRoles = map[string]bool{
	RoleAdmin: true, RoleManager: true, RoleAccounting: true, RoleReadonly: true,
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

// Average length of a year/month in days, used for proration.
const (
	daysPerYear  = 365.25
	daysPerMonth = daysPerYear / 12.0
)

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
}

// Category is a centrally-managed vehicle type with default pricing.
type Category struct {
	ID                 int64     `json:"id"`
	Name               string    `json:"name"`
	DefaultMonthlyCost float64   `json:"default_monthly_cost"`
	DefaultYearlyCost  float64   `json:"default_yearly_cost"`
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

// AuditEntry records a change made by a user.
type AuditEntry struct {
	ID        int64     `json:"id"`
	UserID    *int64    `json:"user_id"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	Entity    string    `json:"entity"`
	EntityID  *int64    `json:"entity_id"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
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
	days := end.Sub(start).Hours() / 24.0
	rate := v.EffectiveRateFor(cat)
	if v.BillingPeriod == BillingYearly {
		return rate * (days / daysPerYear)
	}
	return rate * (days / daysPerMonth)
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
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
