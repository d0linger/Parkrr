package handlers

import (
	"net/http"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

// Rollen Buchhaltung und Hallendienst (Ausbau 35). Die Rechte setzen die Routen-
// Wrapper in internal/server; hier steht nur, was Antworten betrifft, die beide Seiten
// lesen dürfen: der Hallendienst sieht keine Beträge, auch nicht in Gefährte-, Tarif-
// und Dienstlisten, die er für Planer und Übergaben braucht.

// hallStaff meldet, ob die Anfrage vom Hallendienst kommt.
func hallStaff(r *http.Request) bool {
	u, ok := auth.UserFrom(r.Context())
	return ok && u != nil && !u.IsAdmin && u.Role == models.RoleHall
}

// redactVehicleMoney nimmt einem Gefährt alle Beträge.
func redactVehicleMoney(v *models.Vehicle) {
	v.Rate, v.CostOverride, v.AccruedCost, v.EffectiveRate, v.UncoveredCost = 0, nil, 0, 0, 0
	v.RateChanges, v.Paid, v.PaidThrough, v.Invoiced, v.InvoiceOpen = nil, false, nil, false, false
}
