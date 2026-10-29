package handlers

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
	"github.com/preining/parkrr/internal/models"
)

// Wertsicherung nach Verbraucherpreisindex (Ausbau 29). Der Betreiber trägt die
// VPI-Werte ein (kein Abruf von außen) und setzt je Tarif Basismonat und Schwellwert.
// Die Vorschau zeigt je Tarif die Änderung und je Gefährt alten und neuen Preis; erst
// „Anpassen“ legt Preisänderungen an — immer ab dem Beginn der NÄCHSTEN Periode, nie
// rückwirkend. Die Abrechnung teilt die Perioden an diesen Daten (sliceCents).

type cpiValue struct {
	Month time.Time `json:"month"`
	Value float64   `json:"value"`
}

func parseMonth(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if !validDateLength(s) {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01", s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// ListCPI: GET /api/cpi → VPI-Werte, neueste zuerst.
func (h *Handler) ListCPI(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(), `SELECT month, value FROM cpi_values ORDER BY month DESC LIMIT 240`)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	defer rows.Close()
	out := []cpiValue{}
	for rows.Next() {
		var c cpiValue
		if err := rows.Scan(&c.Month, &c.Value); err != nil {
			serverError(w, r, "scan failed", err)
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// SaveCPI: PUT /api/cpi {month: "YYYY-MM", value}.
func (h *Handler) SaveCPI(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Month string  `json:"month"`
		Value float64 `json:"value"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	m, ok := parseMonth(in.Month)
	if !ok || in.Value <= 0 || in.Value > 100000 {
		writeError(w, http.StatusBadRequest, "Monat (JJJJ-MM) und Indexwert über 0 angeben")
		return
	}
	var old *float64
	_ = h.Pool.QueryRow(r.Context(), `SELECT value FROM cpi_values WHERE month = $1`, m).Scan(&old)
	if _, err := h.Pool.Exec(r.Context(),
		`INSERT INTO cpi_values (month, value) VALUES ($1, $2)
		 ON CONFLICT (month) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, m, round2(in.Value)); err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	h.auditChange(r, "update", "cpi", 0, "VPI "+m.Format("01/2006")+" gespeichert",
		diffFields(map[string]any{"value": old, "updated_at": nil}, map[string]any{"value": round2(in.Value), "updated_at": "jetzt"}))
	writeJSON(w, http.StatusOK, cpiValue{Month: m, Value: round2(in.Value)})
}

// DeleteCPI: DELETE /api/cpi/{month}.
func (h *Handler) DeleteCPI(w http.ResponseWriter, r *http.Request) {
	m, ok := parseMonth(r.PathValue("month"))
	if !ok {
		writeError(w, http.StatusBadRequest, "Monat als JJJJ-MM")
		return
	}
	var v float64
	err := h.Pool.QueryRow(r.Context(), `DELETE FROM cpi_values WHERE month = $1 RETURNING value`, m).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		serverError(w, r, "could not delete", err)
		return
	}
	h.auditDeleted(r, "cpi", 0, "VPI "+m.Format("01/2006")+" gelöscht", map[string]any{"month": m.Format("2006-01"), "value": v})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// IndexationSettings: GET /api/indexation → Tarife mit Basismonat und Schwellwert,
// dazu die eingetragenen VPI-Monate (für die Auswahl in der Oberfläche).
func (h *Handler) IndexationSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, name, to_char(index_base_month, 'YYYY-MM'), index_threshold FROM categories WHERE NOT archived ORDER BY lower(name)`)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	type catIdx struct {
		ID        int64   `json:"id"`
		Name      string  `json:"name"`
		BaseMonth *string `json:"base_month"`
		Threshold float64 `json:"threshold"`
	}
	out := []catIdx{}
	for rows.Next() {
		var c catIdx
		if err := rows.Scan(&c.ID, &c.Name, &c.BaseMonth, &c.Threshold); err != nil {
			rows.Close()
			serverError(w, r, "scan failed", err)
			return
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": out})
}

// SetCategoryIndexation: PUT /api/categories/{id}/indexation {base_month|null, threshold}.
func (h *Handler) SetCategoryIndexation(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		BaseMonth *string `json:"base_month"`
		Threshold float64 `json:"threshold"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if in.Threshold < 0 || in.Threshold > 50 {
		writeError(w, http.StatusBadRequest, "Schwellwert zwischen 0 und 50 %")
		return
	}
	var base *time.Time
	if in.BaseMonth != nil && strings.TrimSpace(*in.BaseMonth) != "" {
		m, ok := parseMonth(*in.BaseMonth)
		if !ok {
			writeError(w, http.StatusBadRequest, "Basismonat als JJJJ-MM")
			return
		}
		base = &m
	}
	var oldBase *time.Time
	var oldThreshold float64
	if err := h.Pool.QueryRow(r.Context(), `SELECT index_base_month, index_threshold FROM categories WHERE id = $1`, id).Scan(&oldBase, &oldThreshold); err != nil {
		writeError(w, http.StatusNotFound, "Tarif nicht gefunden")
		return
	}
	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE categories SET index_base_month = $2, index_threshold = $3, updated_at = now() WHERE id = $1`, id, base, round2(in.Threshold)); err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	monthStr := func(t *time.Time) any {
		if t == nil {
			return nil
		}
		return t.Format("2006-01")
	}
	h.auditChange(r, "update", "category", id, "Wertsicherung des Tarifs geändert", diffFields(
		map[string]any{"index_base_month": monthStr(oldBase), "index_threshold": oldThreshold},
		map[string]any{"index_base_month": monthStr(base), "index_threshold": round2(in.Threshold)}))
	writeJSON(w, http.StatusOK, map[string]any{"base_month": monthStr(base), "threshold": round2(in.Threshold)})
}

type indexVehicle struct {
	VehicleID     int64     `json:"vehicle_id"`
	PersonID      int64     `json:"person_id"`
	Label         string    `json:"label"`
	PersonName    string    `json:"person_name"`
	Period        string    `json:"period"`
	OldRate       float64   `json:"old_rate"`
	NewRate       float64   `json:"new_rate"`
	EffectiveFrom time.Time `json:"effective_from"`
	BaseMonth     time.Time `json:"base_month"`
	ChangePct     float64   `json:"change_pct"`
}

type indexSkip struct {
	Label  string `json:"label"`
	Reason string `json:"reason"`
}

type indexCategory struct {
	CategoryID int64          `json:"category_id"`
	Name       string         `json:"name"`
	BaseMonth  *time.Time     `json:"base_month"`
	BaseValue  *float64       `json:"base_value"`
	ChangePct  *float64       `json:"change_pct"`
	Threshold  float64        `json:"threshold"`
	Due        bool           `json:"due"`
	NewMonthly *float64       `json:"new_monthly,omitempty"`
	NewYearly  *float64       `json:"new_yearly,omitempty"`
	Vehicles   []indexVehicle `json:"vehicles"`
	Skipped    []indexSkip    `json:"skipped"`
	Note       string         `json:"note,omitempty"`
}

type indexPlan struct {
	Month      time.Time       `json:"month"`
	Value      float64         `json:"value"`
	Categories []indexCategory `json:"categories"`
}

// nextPeriodStart: Beginn der nächsten Periode nach today (Monatserster bzw. 1. Jänner).
func nextPeriodStart(period string, today time.Time) time.Time {
	if period == models.BillingYearly {
		return time.Date(today.Year()+1, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
}

// indexationPlan rechnet die Vorschau für den VPI-Monat month. Anpassen nutzt dieselbe
// Rechnung, damit angelegt wird, was die Vorschau gezeigt hat.
func (h *Handler) indexationPlan(r *http.Request, month time.Time) (*indexPlan, error) {
	ctx := r.Context()
	cpi := map[time.Time]float64{}
	rows, err := h.Pool.Query(ctx, `SELECT month, value FROM cpi_values`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var m time.Time
		var v float64
		if err := rows.Scan(&m, &v); err != nil {
			rows.Close()
			return nil, err
		}
		cpi[m] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	current, ok := cpi[month]
	if !ok {
		return nil, errNoCPI
	}
	plan := &indexPlan{Month: month, Value: current, Categories: []indexCategory{}}

	// Basismonat je Gefährt: die letzte Anpassung, sonst der des Tarifs.
	lastIndex := map[int64]time.Time{}
	rows, err = h.Pool.Query(ctx,
		`SELECT DISTINCT ON (vehicle_id) vehicle_id, index_month FROM vehicle_rate_changes
		  WHERE index_month IS NOT NULL ORDER BY vehicle_id, effective_from DESC`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var vid int64
		var m time.Time
		if err := rows.Scan(&vid, &m); err != nil {
			rows.Close()
			return nil, err
		}
		lastIndex[vid] = m
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	vehicles, cats, err := h.loadVehiclesWithCategories(r, 0)
	if err != nil {
		return nil, err
	}
	ags, err := h.loadAllAgreements(ctx, 0)
	if err != nil {
		return nil, err
	}
	now := h.now()
	setFlatRateCoverage(vehicles, ags, now)
	today := startOfDayUTC(now)

	crow, err := h.Pool.Query(ctx,
		`SELECT id, name, index_base_month, index_threshold, default_monthly_cost, default_yearly_cost
		   FROM categories WHERE NOT archived ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	type catRow struct {
		indexCategory
		monthly, yearly float64
	}
	var catList []catRow
	for crow.Next() {
		var c catRow
		if err := crow.Scan(&c.CategoryID, &c.Name, &c.BaseMonth, &c.Threshold, &c.monthly, &c.yearly); err != nil {
			crow.Close()
			return nil, err
		}
		catList = append(catList, c)
	}
	crow.Close()
	if err := crow.Err(); err != nil {
		return nil, err
	}

	pct := func(base float64) float64 { return math.Round((current/base-1)*10000) / 100 }
	for _, c := range catList {
		ic := c.indexCategory
		ic.Vehicles, ic.Skipped = []indexVehicle{}, []indexSkip{}
		if ic.BaseMonth == nil {
			ic.Note = "Keine Wertsicherung vereinbart (kein Basismonat)"
			plan.Categories = append(plan.Categories, ic)
			continue
		}
		bv, ok := cpi[*ic.BaseMonth]
		if !ok {
			ic.Note = "Für den Basismonat " + ic.BaseMonth.Format("01/2006") + " fehlt der Indexwert"
			plan.Categories = append(plan.Categories, ic)
			continue
		}
		ic.BaseValue = &bv
		cp := pct(bv)
		ic.ChangePct = &cp
		ic.Due = math.Abs(cp) >= ic.Threshold && !month.Equal(*ic.BaseMonth)
		if ic.Due {
			nm, ny := round2(c.monthly*current/bv), round2(c.yearly*current/bv)
			ic.NewMonthly, ic.NewYearly = &nm, &ny
		}
		for i := range vehicles {
			v := &vehicles[i]
			if v.CategoryID != ic.CategoryID || v.Archived || (v.Status != models.StatusStored && v.Status != models.StatusReserved) {
				continue
			}
			label := vehicleTitleGo(v)
			if v.FlatRateCovered {
				ic.Skipped = append(ic.Skipped, indexSkip{Label: label, Reason: "über eine Pauschale abgerechnet"})
				continue
			}
			from := nextPeriodStart(v.BillingPeriod, today)
			if !from.After(v.StartDate) {
				ic.Skipped = append(ic.Skipped, indexSkip{Label: label, Reason: "Vertrag beginnt erst, der vereinbarte Preis gilt"})
				continue
			}
			if v.EndDate != nil && !v.EndDate.After(from) {
				ic.Skipped = append(ic.Skipped, indexSkip{Label: label, Reason: "endet vor der nächsten Periode"})
				continue
			}
			// Basis: die letzte Anpassung; sonst der Basismonat des Tarifs, bei einem
			// später geschlossenen Vertrag aber dessen Beginn — der Preis wurde dann
			// zum damaligen Index vereinbart.
			base := *ic.BaseMonth
			if m, ok := lastIndex[v.ID]; ok {
				base = m
			} else if sm := time.Date(v.StartDate.Year(), v.StartDate.Month(), 1, 0, 0, 0, 0, time.UTC); sm.After(base) {
				base = sm
			}
			vb, ok := cpi[base]
			if !ok {
				ic.Skipped = append(ic.Skipped, indexSkip{Label: label, Reason: "Indexwert für " + base.Format("01/2006") + " fehlt"})
				continue
			}
			vp := pct(vb)
			if math.Abs(vp) < ic.Threshold || !month.After(base) {
				continue
			}
			cat := cats[v.CategoryID]
			old := v.RateAt(cat, from.AddDate(0, 0, -1))
			nr := round2(old * current / vb)
			if toCentsH(nr) == toCentsH(old) {
				continue
			}
			ic.Vehicles = append(ic.Vehicles, indexVehicle{VehicleID: v.ID, PersonID: v.PersonID, Label: label, PersonName: v.PersonName,
				Period: v.BillingPeriod, OldRate: old, NewRate: nr, EffectiveFrom: from, BaseMonth: base, ChangePct: vp})
		}
		sort.Slice(ic.Vehicles, func(i, j int) bool { return ic.Vehicles[i].Label < ic.Vehicles[j].Label })
		plan.Categories = append(plan.Categories, ic)
	}
	return plan, nil
}

var errNoCPI = errors.New("no cpi value for month")

func vehicleTitleGo(v *models.Vehicle) string {
	if s := strings.TrimSpace(v.Label); s != "" {
		return s
	}
	if s := strings.TrimSpace(v.LicensePlate); s != "" {
		return s
	}
	return v.CategoryName
}

// IndexationPreview: GET /api/indexation/preview?month=YYYY-MM.
func (h *Handler) IndexationPreview(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("month")
	if !validDateLength(raw) {
		writeError(w, http.StatusBadRequest, "month is too long")
		return
	}
	month, ok := parseMonth(raw)
	if !ok {
		writeError(w, http.StatusBadRequest, "month als JJJJ-MM")
		return
	}
	plan, err := h.indexationPlan(r, month)
	if errors.Is(err, errNoCPI) {
		writeError(w, http.StatusBadRequest, "Für "+month.Format("01/2006")+" ist kein Indexwert eingetragen")
		return
	}
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// ApplyIndexation: POST /api/indexation/apply {month, category_ids, notify}.
// Legt die Preisänderungen der Vorschau an (ab der nächsten Periode), setzt den
// Tarifpreis für neue Verträge und den Basismonat; auf Wunsch eine Mitteilung per Mail.
func (h *Handler) ApplyIndexation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Month       string  `json:"month"`
		CategoryIDs []int64 `json:"category_ids"`
		Notify      bool    `json:"notify"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	month, ok := parseMonth(in.Month)
	if !ok || len(in.CategoryIDs) == 0 {
		writeError(w, http.StatusBadRequest, "Monat und mindestens ein Tarif")
		return
	}
	if len(in.CategoryIDs) > 100 {
		writeError(w, http.StatusBadRequest, "too many category_ids")
		return
	}
	plan, err := h.indexationPlan(r, month)
	if errors.Is(err, errNoCPI) {
		writeError(w, http.StatusBadRequest, "Für "+month.Format("01/2006")+" ist kein Indexwert eingetragen")
		return
	}
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	want := map[int64]bool{}
	for _, id := range in.CategoryIDs {
		want[id] = true
	}
	var createdBy *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		createdBy = &u.ID
	}
	notify := in.Notify && h.Mail != nil && h.Mail.Enabled()
	seller := ""
	if bs, err := h.loadBillingSettings(r.Context()); err == nil {
		seller = trim(bs.SellerName)
	}
	changed, cats, mails := 0, 0, 0
	err = pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		for _, c := range plan.Categories {
			if !want[c.CategoryID] || !c.Due {
				continue
			}
			cats++
			for _, v := range c.Vehicles {
				ct, err := tx.Exec(r.Context(),
					`INSERT INTO vehicle_rate_changes (vehicle_id, effective_from, rate, previous_rate, index_month, reason, created_by)
					 VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (vehicle_id, effective_from) DO NOTHING`,
					v.VehicleID, v.EffectiveFrom, v.NewRate, v.OldRate, month,
					fmt.Sprintf("Wertsicherung VPI %s → %s (%+.2f %%)", v.BaseMonth.Format("01/2006"), month.Format("01/2006"), v.ChangePct), createdBy)
				if err != nil {
					return err
				}
				if ct.RowsAffected() == 0 {
					continue
				}
				changed++
				if notify {
					var email, name string
					if err := tx.QueryRow(r.Context(), `SELECT btrim(email), trim(first_name || ' ' || last_name) FROM persons WHERE id = $1 AND NOT anonymized`, v.PersonID).Scan(&email, &name); err == nil && email != "" {
						ref, pid := v.VehicleID, v.PersonID
						if _, err := enqueueMail(r.Context(), tx, outboxRow{Kind: "price_change", RefID: &ref, PersonID: &pid, Recipient: email,
							Subject: "Preisanpassung ab " + v.EffectiveFrom.Format("02.01.2006"),
							Body:    priceChangeBody(name, v, plan.Month, seller)}); err != nil {
							return err
						}
						mails++
					}
				}
			}
			// Gekoppelte Tarife (Jahr = 12 × Monat) bleiben gekoppelt.
			if _, err := tx.Exec(r.Context(),
				`UPDATE categories SET default_monthly_cost = $2,
				        default_yearly_cost = CASE WHEN rates_synced THEN round($2::numeric * 12, 2) ELSE $3::numeric END,
				        index_base_month = $4, updated_at = now() WHERE id = $1`,
				c.CategoryID, *c.NewMonthly, *c.NewYearly, month); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		serverError(w, r, "could not apply", err)
		return
	}
	h.audit(r, "update", "category", 0, fmt.Sprintf("Wertsicherung VPI %s angewendet: %d Tarife, %d Gefährte, %d Mitteilungen",
		month.Format("01/2006"), cats, changed, mails))
	if mails > 0 {
		h.KickOutbox()
	}
	writeJSON(w, http.StatusOK, map[string]int{"categories": cats, "vehicles": changed, "mails": mails})
}

func priceChangeBody(name string, v indexVehicle, month time.Time, seller string) string {
	var b strings.Builder
	if name != "" {
		fmt.Fprintf(&b, "Guten Tag %s,\n\n", name)
	} else {
		b.WriteString("Guten Tag,\n\n")
	}
	per := "Monat"
	if v.Period == models.BillingYearly {
		per = "Jahr"
	}
	fmt.Fprintf(&b, "der Einstellpreis für %s ist an den Verbraucherpreisindex gebunden. Der Index hat sich seit %s um %.2f %% verändert (Stand %s).\n\n",
		v.Label, v.BaseMonth.Format("01/2006"), v.ChangePct, month.Format("01/2006"))
	fmt.Fprintf(&b, "Ab %s beträgt der Preis %s € pro %s (bisher %s €).\n\n", v.EffectiveFrom.Format("02.01.2006"), pdfMoney(v.NewRate), per, pdfMoney(v.OldRate))
	b.WriteString("Frühere Zeiträume bleiben unverändert.\n\nMit freundlichen Grüßen\n")
	if seller != "" {
		b.WriteString(seller + "\n")
	}
	return b.String()
}

type rateChangeOut struct {
	ID            int64      `json:"id"`
	EffectiveFrom time.Time  `json:"effective_from"`
	Rate          float64    `json:"rate"`
	PreviousRate  float64    `json:"previous_rate"`
	IndexMonth    *time.Time `json:"index_month"`
	Reason        string     `json:"reason"`
}

// VehicleRateChanges: GET /api/vehicles/{id}/rate-changes.
func (h *Handler) VehicleRateChanges(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	out, err := h.listRateChanges(r.Context(), id)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) listRateChanges(ctx context.Context, vehicleID int64) ([]rateChangeOut, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT id, effective_from, rate, previous_rate, index_month, reason FROM vehicle_rate_changes
		  WHERE vehicle_id = $1 ORDER BY effective_from`, vehicleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []rateChangeOut{}
	for rows.Next() {
		var c rateChangeOut
		if err := rows.Scan(&c.ID, &c.EffectiveFrom, &c.Rate, &c.PreviousRate, &c.IndexMonth, &c.Reason); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteRateChange: DELETE /api/vehicle-rate-changes/{id}. Nur solange sie noch nicht
// gilt: eine begonnene Periode rechnet schon mit ihr.
func (h *Handler) DeleteRateChange(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	today := startOfDayUTC(h.now())
	var vid int64
	var from time.Time
	var rate, prev float64
	err := h.Pool.QueryRow(r.Context(),
		`DELETE FROM vehicle_rate_changes WHERE id = $1 AND effective_from > $2
		 RETURNING vehicle_id, effective_from, rate, previous_rate`, id, today).Scan(&vid, &from, &rate, &prev)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if qerr := h.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM vehicle_rate_changes WHERE id = $1)`, id).Scan(&exists); qerr == nil && exists {
			writeError(w, http.StatusConflict, "Die Preisänderung gilt schon und lässt sich nicht mehr zurücknehmen.")
			return
		}
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		serverError(w, r, "could not delete", err)
		return
	}
	h.auditDeleted(r, "vehicle", vid, "Preisänderung ab "+from.Format("02.01.2006")+" zurückgenommen",
		map[string]any{"effective_from": from.Format(dateLayout), "rate": rate, "previous_rate": prev, "rate_change_id": strconv.FormatInt(id, 10)})
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
