package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const maxTaxTextRunes = 500

type taxProperty struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Address    string  `json:"address"`
	PostalCode string  `json:"postal_code"`
	EAWZ       string  `json:"eawz"`
	Default    bool    `json:"is_default"`
	GarageIDs  []int64 `json:"garage_ids"`
	Garages    string  `json:"garages"`
}

type taxCategory struct {
	ID        int64  `json:"id"`
	Key       string `json:"key"`
	Label     string `json:"label"`
	E1BCode   string `json:"e1b_code"`
	SortOrder int    `json:"sort_order"`
}

type taxExpense struct {
	ID            int64   `json:"id"`
	PropertyID    int64   `json:"property_id"`
	Property      string  `json:"property"`
	CategoryID    int64   `json:"category_id"`
	Category      string  `json:"category"`
	E1BCode       string  `json:"e1b_code"`
	PaidOn        string  `json:"paid_on"`
	Amount        float64 `json:"amount"`
	VATAmount     float64 `json:"vat_amount"`
	Payee         string  `json:"payee"`
	Description   string  `json:"description"`
	PaymentMethod string  `json:"payment_method"`
	ReversesID    *int64  `json:"reverses_id,omitempty"`
	Reversed      bool    `json:"reversed"`
	ReceiptCount  int     `json:"receipt_count"`
}

type taxReceiptMeta struct {
	ID          int64  `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	ByteSize    int    `json:"byte_size"`
	CreatedAt   string `json:"created_at"`
}

type taxRecurringExpense struct {
	ID            int64   `json:"id"`
	PropertyID    int64   `json:"property_id"`
	Property      string  `json:"property"`
	CategoryID    int64   `json:"category_id"`
	Category      string  `json:"category"`
	Description   string  `json:"description"`
	Payee         string  `json:"payee"`
	Amount        float64 `json:"amount"`
	PaymentMethod string  `json:"payment_method"`
	Frequency     string  `json:"frequency"`
	DueDay        int     `json:"due_day"`
	DueMonth      int     `json:"due_month"`
	StartOn       string  `json:"start_on"`
	EndOn         string  `json:"end_on,omitempty"`
	Active        bool    `json:"active"`
}

type taxAsset struct {
	ID               int64   `json:"id"`
	PropertyID       int64   `json:"property_id"`
	Property         string  `json:"property"`
	Name             string  `json:"name"`
	InServiceOn      string  `json:"in_service_on"`
	DepreciableBasis float64 `json:"depreciable_basis"`
	UsefulLifeYears  float64 `json:"useful_life_years"`
	HalfYearRule     bool    `json:"half_year_rule"`
	DisposedOn       string  `json:"disposed_on,omitempty"`
	Notes            string  `json:"notes"`
	YearDepreciation float64 `json:"year_depreciation"`
}

type taxPropertySummary struct {
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	Address         string  `json:"address"`
	IncomeByDate    float64 `json:"income_by_date"`
	IncomeWithRule  float64 `json:"income_with_rule"`
	Expenses        float64 `json:"expenses"`
	Depreciation    float64 `json:"depreciation"`
	Surplus         float64 `json:"surplus"`
	SurplusWithRule float64 `json:"surplus_with_rule"`
}

type taxE1BLine struct {
	PropertyID int64   `json:"property_id"`
	Code       string  `json:"code"`
	Label      string  `json:"label"`
	Amount     float64 `json:"amount"`
}

type taxSettings struct {
	SmallBusinessLimit float64 `json:"small_business_limit"`
	WarningPercent     float64 `json:"warning_percent"`
	VATOptedIn         bool    `json:"vat_opted_in"`
}

func parseTaxDate(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local)
}

func validTaxText(s string, required bool) bool {
	s = strings.TrimSpace(s)
	return (!required || s != "") && utf8.RuneCountInString(s) <= maxTaxTextRunes
}

func (h *Handler) taxYearLocked(ctx context.Context, year int) (bool, error) {
	var locked bool
	err := h.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tax_year_locks WHERE year=$1)`, year).Scan(&locked)
	return locked, err
}

func (h *Handler) rejectLockedYear(w http.ResponseWriter, r *http.Request, year int) bool {
	locked, err := h.taxYearLocked(r.Context(), year)
	if err != nil {
		serverError(w, r, "Steuerjahrsperre konnte nicht geprüft werden", err)
		return true
	}
	if locked {
		writeError(w, http.StatusConflict, "Steuerjahr "+strconv.Itoa(year)+" ist gesperrt")
		return true
	}
	return false
}

// annualTaxDepreciation calculates straight-line depreciation and caps the final
// year so rounding or the half-year rule can never exceed the original basis.
func annualTaxDepreciation(basis, usefulYears float64, inService time.Time, disposed *time.Time, year int, halfYear bool) float64 {
	if basis <= 0 || usefulYears <= 0 || year < inService.Year() || (disposed != nil && year > disposed.Year()) {
		return 0
	}
	annual := basis / usefulYears
	used := 0.0
	for y := inService.Year(); y <= year; y++ {
		if disposed != nil && y > disposed.Year() {
			break
		}
		factor := 1.0
		if halfYear {
			activeFrom := time.Date(y, time.January, 1, 0, 0, 0, 0, time.UTC)
			if y == inService.Year() {
				activeFrom = inService
			}
			activeUntil := time.Date(y, time.December, 31, 0, 0, 0, 0, time.UTC)
			if disposed != nil && y == disposed.Year() {
				activeUntil = *disposed
			}
			// Austrian half-year depreciation applies unless the asset was used
			// for more than six months in this calendar year.
			if !activeUntil.After(activeFrom.AddDate(0, 6, 0)) {
				factor = 0.5
			}
		}
		amount := annual * factor
		if amount > basis-used {
			amount = basis - used
		}
		if amount < 0 {
			amount = 0
		}
		if y == year {
			return round2(amount)
		}
		used += amount
		if used >= basis-0.005 {
			return 0
		}
	}
	return 0
}

func (h *Handler) loadTaxProperties(ctx context.Context) ([]taxProperty, error) {
	rows, err := h.Pool.Query(ctx, `
		SELECT p.id, p.name, p.address, p.postal_code, p.eawz, p.is_default,
		       COALESCE(array_agg(g.id ORDER BY g.id) FILTER (WHERE g.id IS NOT NULL), '{}'),
		       COALESCE(string_agg(g.name, ', ' ORDER BY g.sort_order, g.name), '')
		  FROM tax_properties p LEFT JOIN garages g ON g.tax_property_id=p.id
		 GROUP BY p.id ORDER BY p.is_default DESC, p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []taxProperty{}
	for rows.Next() {
		var p taxProperty
		if err := rows.Scan(&p.ID, &p.Name, &p.Address, &p.PostalCode, &p.EAWZ, &p.Default, &p.GarageIDs, &p.Garages); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (h *Handler) loadTaxCategories(ctx context.Context) ([]taxCategory, error) {
	rows, err := h.Pool.Query(ctx, `SELECT id, key, label, e1b_code, sort_order FROM tax_expense_categories WHERE active ORDER BY sort_order, label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []taxCategory{}
	for rows.Next() {
		var c taxCategory
		if err := rows.Scan(&c.ID, &c.Key, &c.Label, &c.E1BCode, &c.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (h *Handler) loadTaxExpenses(ctx context.Context, year int) ([]taxExpense, error) {
	rows, err := h.Pool.Query(ctx, `
		SELECT e.id, e.tax_property_id, p.name, e.category_id, c.label, c.e1b_code,
		       e.paid_on, e.amount, e.vat_amount, e.payee, e.description, e.payment_method,
		       e.reverses_id, EXISTS(SELECT 1 FROM tax_expenses r WHERE r.reverses_id=e.id),
		       (SELECT count(*) FROM tax_expense_receipts a WHERE a.expense_id=e.id)
		  FROM tax_expenses e
		  JOIN tax_properties p ON p.id=e.tax_property_id
		  JOIN tax_expense_categories c ON c.id=e.category_id
		 WHERE e.paid_on >= make_date($1,1,1) AND e.paid_on < make_date($1+1,1,1)
		 ORDER BY e.paid_on DESC, e.id DESC`, year)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []taxExpense{}
	for rows.Next() {
		var e taxExpense
		var paidOn time.Time
		if err := rows.Scan(&e.ID, &e.PropertyID, &e.Property, &e.CategoryID, &e.Category, &e.E1BCode,
			&paidOn, &e.Amount, &e.VATAmount, &e.Payee, &e.Description, &e.PaymentMethod,
			&e.ReversesID, &e.Reversed, &e.ReceiptCount); err != nil {
			return nil, err
		}
		e.PaidOn = paidOn.Format("2006-01-02")
		out = append(out, e)
	}
	return out, rows.Err()
}

func (h *Handler) loadTaxRecurring(ctx context.Context) ([]taxRecurringExpense, error) {
	rows, err := h.Pool.Query(ctx, `
		SELECT r.id, r.tax_property_id, p.name, r.category_id, c.label, r.description, r.payee,
		       r.amount, r.payment_method, r.frequency, r.due_day, r.due_month,
		       r.start_on, r.end_on, r.active
		  FROM tax_recurring_expenses r JOIN tax_properties p ON p.id=r.tax_property_id
		  JOIN tax_expense_categories c ON c.id=r.category_id
		 ORDER BY r.active DESC, r.description, r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []taxRecurringExpense{}
	for rows.Next() {
		var v taxRecurringExpense
		var start time.Time
		var end *time.Time
		if err := rows.Scan(&v.ID, &v.PropertyID, &v.Property, &v.CategoryID, &v.Category, &v.Description,
			&v.Payee, &v.Amount, &v.PaymentMethod, &v.Frequency, &v.DueDay, &v.DueMonth, &start, &end, &v.Active); err != nil {
			return nil, err
		}
		v.StartOn = start.Format("2006-01-02")
		if end != nil {
			v.EndOn = end.Format("2006-01-02")
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (h *Handler) loadTaxAssets(ctx context.Context, year int) ([]taxAsset, error) {
	rows, err := h.Pool.Query(ctx, `
		SELECT a.id, a.tax_property_id, p.name, a.name, a.in_service_on, a.depreciable_basis,
		       a.useful_life_years, a.half_year_rule, a.disposed_on, a.notes
		  FROM tax_assets a JOIN tax_properties p ON p.id=a.tax_property_id
		 ORDER BY a.in_service_on, a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []taxAsset{}
	for rows.Next() {
		var a taxAsset
		var inService time.Time
		var disposed *time.Time
		if err := rows.Scan(&a.ID, &a.PropertyID, &a.Property, &a.Name, &inService, &a.DepreciableBasis,
			&a.UsefulLifeYears, &a.HalfYearRule, &disposed, &a.Notes); err != nil {
			return nil, err
		}
		a.InServiceOn = inService.Format("2006-01-02")
		if disposed != nil {
			a.DisposedOn = disposed.Format("2006-01-02")
		}
		a.YearDepreciation = annualTaxDepreciation(a.DepreciableBasis, a.UsefulLifeYears, inService, disposed, year, a.HalfYearRule)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (h *Handler) loadTaxSettings(ctx context.Context) (taxSettings, error) {
	var s taxSettings
	err := h.Pool.QueryRow(ctx, `SELECT small_business_limit, warning_percent, vat_opted_in FROM tax_settings WHERE singleton`).
		Scan(&s.SmallBusinessLimit, &s.WarningPercent, &s.VATOptedIn)
	return s, err
}

func taxReceiptData(raw []byte) ([]byte, string, error) {
	if bytes.HasPrefix(raw, []byte("%PDF-")) {
		return raw, "application/pdf", nil
	}
	return sanitizeImage(raw)
}

func safeTaxFilename(name string) string {
	name = strings.TrimSpace(name)
	if rs := []rune(name); len(rs) > 200 {
		name = string(rs[:200])
	}
	return name
}

func taxDateString(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format("2006-01-02")
}

func scanTaxID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func taxActorID(r *http.Request) any {
	id, _ := actorFrom(r)
	if id == 0 {
		return nil
	}
	return id
}

func taxConflict(w http.ResponseWriter, err error) bool {
	if isForeignKeyViolation(err) {
		writeError(w, http.StatusBadRequest, "Steuerobjekt oder Kategorie existiert nicht")
		return true
	}
	return false
}

func nullableDate(s string) (*time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	d, err := parseTaxDate(s)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func expenseAudit(e taxExpense) map[string]any {
	return map[string]any{"property_id": e.PropertyID, "category_id": e.CategoryID, "paid_on": e.PaidOn,
		"amount": e.Amount, "vat_amount": e.VATAmount, "payee": e.Payee, "description": e.Description,
		"payment_method": e.PaymentMethod, "reverses_id": e.ReversesID}
}

func (h *Handler) ListTaxProperties(w http.ResponseWriter, r *http.Request) {
	items, err := h.loadTaxProperties(r.Context())
	if err != nil {
		serverError(w, r, "Steuerobjekte konnten nicht geladen werden", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type taxPropertyRequest struct {
	Name       string  `json:"name"`
	Address    string  `json:"address"`
	PostalCode string  `json:"postal_code"`
	EAWZ       string  `json:"eawz"`
	GarageIDs  []int64 `json:"garage_ids"`
}

func validateTaxPropertyRequest(req *taxPropertyRequest) bool {
	req.Name, req.Address = strings.TrimSpace(req.Name), strings.TrimSpace(req.Address)
	req.PostalCode, req.EAWZ = strings.TrimSpace(req.PostalCode), strings.TrimSpace(req.EAWZ)
	return validTaxText(req.Name, true) && validTaxText(req.Address, false) &&
		validTaxText(req.PostalCode, false) && validTaxText(req.EAWZ, false)
}

func (h *Handler) CreateTaxProperty(w http.ResponseWriter, r *http.Request) {
	var req taxPropertyRequest
	if err := decodeJSON(r, &req); err != nil || !validateTaxPropertyRequest(&req) {
		writeError(w, http.StatusBadRequest, "Ungültiges Steuerobjekt")
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Steuerobjekt konnte nicht angelegt werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var id int64
	if err := tx.QueryRow(r.Context(), `INSERT INTO tax_properties(name,address,postal_code,eawz) VALUES($1,$2,$3,$4) RETURNING id`,
		req.Name, req.Address, req.PostalCode, req.EAWZ).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "Ein Steuerobjekt mit diesem Namen existiert bereits")
			return
		}
		serverError(w, r, "Steuerobjekt konnte nicht angelegt werden", err)
		return
	}
	if len(req.GarageIDs) > 0 {
		if _, err := tx.Exec(r.Context(), `UPDATE garages SET tax_property_id=$1, updated_at=now() WHERE id=ANY($2)`, id, req.GarageIDs); err != nil {
			serverError(w, r, "Hallen konnten nicht zugeordnet werden", err)
			return
		}
	}
	if err := h.auditCreatedTx(r.Context(), tx, r, "tax_property", id, "Steuerobjekt angelegt: "+req.Name,
		map[string]any{"name": req.Name, "address": req.Address, "postal_code": req.PostalCode, "eawz": req.EAWZ, "garage_ids": req.GarageIDs}); err != nil {
		serverError(w, r, "Steuerobjekt konnte nicht angelegt werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Steuerobjekt konnte nicht angelegt werden", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (h *Handler) UpdateTaxProperty(w http.ResponseWriter, r *http.Request) {
	id, ok := scanTaxID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req taxPropertyRequest
	if err := decodeJSON(r, &req); err != nil || !validateTaxPropertyRequest(&req) {
		writeError(w, http.StatusBadRequest, "Ungültiges Steuerobjekt")
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Steuerobjekt konnte nicht gespeichert werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var oldName, oldAddress, oldPostal, oldEAWZ string
	var oldGarageIDs []int64
	if err := tx.QueryRow(r.Context(), `SELECT name,address,postal_code,eawz FROM tax_properties WHERE id=$1 FOR UPDATE`, id).
		Scan(&oldName, &oldAddress, &oldPostal, &oldEAWZ); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Steuerobjekt nicht gefunden")
			return
		}
		serverError(w, r, "Steuerobjekt konnte nicht gespeichert werden", err)
		return
	}
	if err := tx.QueryRow(r.Context(), `SELECT COALESCE(array_agg(id ORDER BY id), '{}') FROM garages WHERE tax_property_id=$1`, id).
		Scan(&oldGarageIDs); err != nil {
		serverError(w, r, "Steuerobjekt konnte nicht gespeichert werden", err)
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE tax_properties SET name=$2,address=$3,postal_code=$4,eawz=$5,updated_at=now() WHERE id=$1`,
		id, req.Name, req.Address, req.PostalCode, req.EAWZ); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "Ein Steuerobjekt mit diesem Namen existiert bereits")
			return
		}
		serverError(w, r, "Steuerobjekt konnte nicht gespeichert werden", err)
		return
	}
	var defaultID int64
	if err := tx.QueryRow(r.Context(), `SELECT id FROM tax_properties ORDER BY is_default DESC,id LIMIT 1`).Scan(&defaultID); err != nil {
		serverError(w, r, "Hallen konnten nicht zugeordnet werden", err)
		return
	}
	if id != defaultID {
		if _, err := tx.Exec(r.Context(), `UPDATE garages SET tax_property_id=$2, updated_at=now() WHERE tax_property_id=$1`, id, defaultID); err != nil {
			serverError(w, r, "Hallen konnten nicht zugeordnet werden", err)
			return
		}
	}
	if len(req.GarageIDs) > 0 {
		if _, err := tx.Exec(r.Context(), `UPDATE garages SET tax_property_id=$1, updated_at=now() WHERE id=ANY($2)`, id, req.GarageIDs); err != nil {
			serverError(w, r, "Hallen konnten nicht zugeordnet werden", err)
			return
		}
	}
	if err := h.auditChangeTx(r.Context(), tx, r, "update", "tax_property", id, "Steuerobjekt gespeichert: "+req.Name,
		mergeChanges(diffFields(
			map[string]any{"name": oldName, "address": oldAddress, "postal_code": oldPostal, "eawz": oldEAWZ, "garage_ids": oldGarageIDs},
			map[string]any{"name": req.Name, "address": req.Address, "postal_code": req.PostalCode, "eawz": req.EAWZ, "garage_ids": req.GarageIDs}),
			auditSnapshot(map[string]any{"tax_property_id": id}))); err != nil {
		serverError(w, r, "Steuerobjekt konnte nicht gespeichert werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Steuerobjekt konnte nicht gespeichert werden", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (h *Handler) ListTaxCategories(w http.ResponseWriter, r *http.Request) {
	items, err := h.loadTaxCategories(r.Context())
	if err != nil {
		serverError(w, r, "Kategorien konnten nicht geladen werden", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) GetTaxSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.loadTaxSettings(r.Context())
	if err != nil {
		serverError(w, r, "Steuereinstellungen konnten nicht geladen werden", err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *Handler) SaveTaxSettings(w http.ResponseWriter, r *http.Request) {
	var req taxSettings
	if err := decodeJSON(r, &req); err != nil || req.SmallBusinessLimit <= 0 || req.SmallBusinessLimit > 1e9 ||
		req.WarningPercent <= 0 || req.WarningPercent > 100 {
		writeError(w, http.StatusBadRequest, "Ungültige Steuereinstellungen")
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Steuereinstellungen konnten nicht gespeichert werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var old taxSettings
	if err := tx.QueryRow(r.Context(), `SELECT small_business_limit,warning_percent,vat_opted_in FROM tax_settings WHERE singleton FOR UPDATE`).
		Scan(&old.SmallBusinessLimit, &old.WarningPercent, &old.VATOptedIn); err != nil {
		serverError(w, r, "Steuereinstellungen konnten nicht gespeichert werden", err)
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE tax_settings SET small_business_limit=$1,warning_percent=$2,vat_opted_in=$3,updated_at=now() WHERE singleton`,
		round2(req.SmallBusinessLimit), round2(req.WarningPercent), req.VATOptedIn); err != nil {
		serverError(w, r, "Steuereinstellungen konnten nicht gespeichert werden", err)
		return
	}
	if err := h.auditChangeTx(r.Context(), tx, r, "update", "tax_settings", 1, "Steuereinstellungen gespeichert", diffFields(
		map[string]any{"small_business_limit": old.SmallBusinessLimit, "warning_percent": old.WarningPercent, "vat_opted_in": old.VATOptedIn},
		map[string]any{"small_business_limit": round2(req.SmallBusinessLimit), "warning_percent": round2(req.WarningPercent), "vat_opted_in": req.VATOptedIn})); err != nil {
		serverError(w, r, "Steuereinstellungen konnten nicht gespeichert werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Steuereinstellungen konnten nicht gespeichert werden", err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

type taxExpenseRequest struct {
	PropertyID    int64   `json:"property_id"`
	CategoryID    int64   `json:"category_id"`
	PaidOn        string  `json:"paid_on"`
	Amount        float64 `json:"amount"`
	VATAmount     float64 `json:"vat_amount"`
	Payee         string  `json:"payee"`
	Description   string  `json:"description"`
	PaymentMethod string  `json:"payment_method"`
}

func validateTaxExpenseRequest(req *taxExpenseRequest) (time.Time, bool) {
	d, err := parseTaxDate(req.PaidOn)
	req.Payee = strings.TrimSpace(req.Payee)
	req.Description = strings.TrimSpace(req.Description)
	req.PaymentMethod = strings.TrimSpace(req.PaymentMethod)
	validMethod := req.PaymentMethod == "bar" || req.PaymentMethod == "ueberweisung" || req.PaymentMethod == "paypal" || req.PaymentMethod == "sonstiges"
	return d, err == nil && req.PropertyID > 0 && req.CategoryID > 0 && req.Amount > 0 && req.Amount <= 1e9 &&
		req.VATAmount >= 0 && req.VATAmount <= req.Amount && validTaxText(req.Payee, false) &&
		validTaxText(req.Description, true) && validMethod
}

func (h *Handler) ListTaxExpenses(w http.ResponseWriter, r *http.Request) {
	items, err := h.loadTaxExpenses(r.Context(), parseYearParam(r, h.now().Year()))
	if err != nil {
		serverError(w, r, "Ausgaben konnten nicht geladen werden", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) CreateTaxExpense(w http.ResponseWriter, r *http.Request) {
	var req taxExpenseRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Ungültige Ausgabe")
		return
	}
	paidOn, ok := validateTaxExpenseRequest(&req)
	if !ok {
		writeError(w, http.StatusBadRequest, "Ungültige Ausgabe")
		return
	}
	if h.rejectLockedYear(w, r, paidOn.Year()) {
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Ausgabe konnte nicht gebucht werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var id int64
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO tax_expenses(tax_property_id,category_id,paid_on,amount,vat_amount,payee,description,payment_method,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, req.PropertyID, req.CategoryID, paidOn,
		round2(req.Amount), round2(req.VATAmount), req.Payee, req.Description, req.PaymentMethod, taxActorID(r)).Scan(&id); err != nil {
		if taxConflict(w, err) {
			return
		}
		serverError(w, r, "Ausgabe konnte nicht gebucht werden", err)
		return
	}
	e := taxExpense{ID: id, PropertyID: req.PropertyID, CategoryID: req.CategoryID, PaidOn: paidOn.Format("2006-01-02"),
		Amount: round2(req.Amount), VATAmount: round2(req.VATAmount), Payee: req.Payee, Description: req.Description, PaymentMethod: req.PaymentMethod}
	if err := h.auditCreatedTx(r.Context(), tx, r, "tax_expense", id, "Werbungskosten gebucht: "+req.Description, expenseAudit(e)); err != nil {
		serverError(w, r, "Ausgabe konnte nicht gebucht werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Ausgabe konnte nicht gebucht werden", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (h *Handler) ReverseTaxExpense(w http.ResponseWriter, r *http.Request) {
	id, ok := scanTaxID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Storno konnte nicht gebucht werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var e taxExpense
	var paidOn time.Time
	if err := tx.QueryRow(r.Context(), `
		SELECT tax_property_id,category_id,paid_on,amount,vat_amount,payee,description,payment_method,reverses_id
		  FROM tax_expenses WHERE id=$1 FOR UPDATE`, id).Scan(&e.PropertyID, &e.CategoryID, &paidOn, &e.Amount,
		&e.VATAmount, &e.Payee, &e.Description, &e.PaymentMethod, &e.ReversesID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Ausgabe nicht gefunden")
			return
		}
		serverError(w, r, "Storno konnte nicht gebucht werden", err)
		return
	}
	if e.ReversesID != nil {
		writeError(w, http.StatusConflict, "Eine Gegenbuchung kann nicht erneut storniert werden")
		return
	}
	var exists bool
	if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM tax_expenses WHERE reverses_id=$1)`, id).Scan(&exists); err != nil {
		serverError(w, r, "Storno konnte nicht geprüft werden", err)
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "Diese Ausgabe wurde bereits storniert")
		return
	}
	locked, err := h.taxYearLocked(r.Context(), paidOn.Year())
	if err != nil {
		serverError(w, r, "Steuerjahrsperre konnte nicht geprüft werden", err)
		return
	}
	if locked {
		writeError(w, http.StatusConflict, "Steuerjahr "+strconv.Itoa(paidOn.Year())+" ist gesperrt")
		return
	}
	var reversalID int64
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO tax_expenses(tax_property_id,category_id,paid_on,amount,vat_amount,payee,description,payment_method,reverses_id,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, e.PropertyID, e.CategoryID, paidOn,
		-e.Amount, -e.VATAmount, e.Payee, "Storno: "+e.Description, e.PaymentMethod, id, taxActorID(r)).Scan(&reversalID); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "Diese Ausgabe wurde bereits storniert")
			return
		}
		serverError(w, r, "Storno konnte nicht gebucht werden", err)
		return
	}
	e.ID, e.PaidOn, e.Amount, e.VATAmount, e.ReversesID = reversalID, paidOn.Format("2006-01-02"), -e.Amount, -e.VATAmount, &id
	if err := h.auditCreatedTx(r.Context(), tx, r, "tax_expense", reversalID, "Werbungskosten storniert: "+e.Description, expenseAudit(e)); err != nil {
		serverError(w, r, "Storno konnte nicht gebucht werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Storno konnte nicht gebucht werden", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": reversalID, "reverses_id": id})
}

func (h *Handler) UploadTaxReceipt(w http.ResponseWriter, r *http.Request) {
	id, ok := scanTaxID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentBytes+1024)
	if err := r.ParseMultipartForm(maxAttachmentBytes + 1024); err != nil {
		writeMultipartError(w, err, "Beleg ist zu groß (max. 8 MB)")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Beleg fehlt")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxAttachmentBytes+1))
	if err != nil || len(raw) > maxAttachmentBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Beleg ist zu groß (max. 8 MB)")
		return
	}
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "Beleg ist leer")
		return
	}
	data, contentType, err := taxReceiptData(raw)
	if err != nil {
		if errors.Is(err, errDecodeBusy) {
			writeError(w, http.StatusServiceUnavailable, "Server ausgelastet, bitte erneut versuchen")
			return
		}
		writeError(w, http.StatusUnsupportedMediaType, "Erlaubt sind PDF, JPEG und PNG")
		return
	}
	filename := safeTaxFilename(header.Filename)
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Beleg konnte nicht gespeichert werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var paidOn time.Time
	var count int
	if err := tx.QueryRow(r.Context(), `SELECT e.paid_on,(SELECT count(*) FROM tax_expense_receipts a WHERE a.expense_id=e.id) FROM tax_expenses e WHERE e.id=$1 FOR UPDATE`, id).
		Scan(&paidOn, &count); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Ausgabe nicht gefunden")
			return
		}
		serverError(w, r, "Beleg konnte nicht gespeichert werden", err)
		return
	}
	if count >= 20 {
		writeError(w, http.StatusConflict, "Beleglimit erreicht")
		return
	}
	locked, err := h.taxYearLocked(r.Context(), paidOn.Year())
	if err != nil {
		serverError(w, r, "Steuerjahrsperre konnte nicht geprüft werden", err)
		return
	}
	if locked {
		writeError(w, http.StatusConflict, "Steuerjahr "+strconv.Itoa(paidOn.Year())+" ist gesperrt")
		return
	}
	sum := sha256.Sum256(data)
	var receiptID int64
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO tax_expense_receipts(expense_id,filename,content_type,byte_size,sha256,data,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, id, filename, contentType, len(data),
		hex.EncodeToString(sum[:]), data, taxActorID(r)).Scan(&receiptID); err != nil {
		serverError(w, r, "Beleg konnte nicht gespeichert werden", err)
		return
	}
	if err := h.auditCreatedTx(r.Context(), tx, r, "tax_receipt", receiptID, "Beleg hochgeladen: "+filename,
		map[string]any{"expense_id": id, "filename": filename, "content_type": contentType, "byte_size": len(data), "sha256": hex.EncodeToString(sum[:])}); err != nil {
		serverError(w, r, "Beleg konnte nicht gespeichert werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Beleg konnte nicht gespeichert werden", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": receiptID, "filename": filename, "byte_size": len(data)})
}

func (h *Handler) ListTaxReceipts(w http.ResponseWriter, r *http.Request) {
	id, ok := scanTaxID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT id,filename,content_type,byte_size,created_at FROM tax_expense_receipts WHERE expense_id=$1 ORDER BY created_at,id`, id)
	if err != nil {
		serverError(w, r, "Belege konnten nicht geladen werden", err)
		return
	}
	defer rows.Close()
	out := []taxReceiptMeta{}
	for rows.Next() {
		var v taxReceiptMeta
		var created time.Time
		if err := rows.Scan(&v.ID, &v.Filename, &v.ContentType, &v.ByteSize, &created); err != nil {
			serverError(w, r, "Belege konnten nicht geladen werden", err)
			return
		}
		v.CreatedAt = created.Format(time.RFC3339)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, r, "Belege konnten nicht geladen werden", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) GetTaxReceipt(w http.ResponseWriter, r *http.Request) {
	id, ok := scanTaxID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var contentType, filename string
	var data []byte
	if err := h.Pool.QueryRow(r.Context(), `SELECT content_type,filename,data FROM tax_expense_receipts WHERE id=$1`, id).
		Scan(&contentType, &filename, &data); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Beleg nicht gefunden")
			return
		}
		serverError(w, r, "Beleg konnte nicht geladen werden", err)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeDownloadFilename(filename)+`"`)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}

func safeDownloadFilename(filename string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == '"' || r == '\\' || r > 0x7e {
			return '_'
		}
		return r
	}, filename)
}

type taxRecurringRequest struct {
	PropertyID    int64   `json:"property_id"`
	CategoryID    int64   `json:"category_id"`
	Description   string  `json:"description"`
	Payee         string  `json:"payee"`
	Amount        float64 `json:"amount"`
	PaymentMethod string  `json:"payment_method"`
	Frequency     string  `json:"frequency"`
	DueDay        int     `json:"due_day"`
	DueMonth      int     `json:"due_month"`
	StartOn       string  `json:"start_on"`
	EndOn         string  `json:"end_on"`
	Active        *bool   `json:"active,omitempty"`
}

func validateTaxRecurringRequest(req *taxRecurringRequest) (time.Time, *time.Time, bool) {
	start, err := parseTaxDate(req.StartOn)
	end, endErr := nullableDate(req.EndOn)
	req.Description, req.Payee = strings.TrimSpace(req.Description), strings.TrimSpace(req.Payee)
	validMethod := req.PaymentMethod == "bar" || req.PaymentMethod == "ueberweisung" || req.PaymentMethod == "paypal" || req.PaymentMethod == "sonstiges"
	validFrequency := req.Frequency == "monthly" || req.Frequency == "yearly"
	ok := err == nil && endErr == nil && (end == nil || !end.Before(start)) && req.PropertyID > 0 && req.CategoryID > 0 &&
		req.Amount > 0 && req.Amount <= 1e9 && validTaxText(req.Description, true) && validTaxText(req.Payee, false) &&
		validMethod && validFrequency && req.DueDay >= 1 && req.DueDay <= 28 && req.DueMonth >= 1 && req.DueMonth <= 12
	return start, end, ok
}

func (h *Handler) ListTaxRecurring(w http.ResponseWriter, r *http.Request) {
	items, err := h.loadTaxRecurring(r.Context())
	if err != nil {
		serverError(w, r, "Wiederkehrende Ausgaben konnten nicht geladen werden", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) CreateTaxRecurring(w http.ResponseWriter, r *http.Request) {
	var req taxRecurringRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Ungültige wiederkehrende Ausgabe")
		return
	}
	start, end, ok := validateTaxRecurringRequest(&req)
	if !ok {
		writeError(w, http.StatusBadRequest, "Ungültige wiederkehrende Ausgabe")
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Wiederkehrende Ausgabe konnte nicht angelegt werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var id int64
	err = tx.QueryRow(r.Context(), `
		INSERT INTO tax_recurring_expenses(tax_property_id,category_id,description,payee,amount,payment_method,frequency,due_day,due_month,start_on,end_on,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, req.PropertyID, req.CategoryID,
		req.Description, req.Payee, round2(req.Amount), req.PaymentMethod, req.Frequency, req.DueDay, req.DueMonth,
		start, end, taxActorID(r)).Scan(&id)
	if err != nil {
		if taxConflict(w, err) {
			return
		}
		serverError(w, r, "Wiederkehrende Ausgabe konnte nicht angelegt werden", err)
		return
	}
	if err := h.auditCreatedTx(r.Context(), tx, r, "tax_recurring_expense", id, "Wiederkehrende Ausgabe angelegt: "+req.Description,
		map[string]any{"property_id": req.PropertyID, "category_id": req.CategoryID, "amount": round2(req.Amount),
			"frequency": req.Frequency, "start_on": start.Format("2006-01-02"), "end_on": taxDateString(end)}); err != nil {
		serverError(w, r, "Wiederkehrende Ausgabe konnte nicht angelegt werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Wiederkehrende Ausgabe konnte nicht angelegt werden", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (h *Handler) UpdateTaxRecurring(w http.ResponseWriter, r *http.Request) {
	id, ok := scanTaxID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		Active bool `json:"active"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Ungültige Änderung")
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Wiederkehrende Ausgabe konnte nicht gespeichert werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var old bool
	if err := tx.QueryRow(r.Context(), `SELECT active FROM tax_recurring_expenses WHERE id=$1 FOR UPDATE`, id).Scan(&old); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Wiederkehrende Ausgabe nicht gefunden")
			return
		}
		serverError(w, r, "Wiederkehrende Ausgabe konnte nicht gespeichert werden", err)
		return
	}
	if _, err := tx.Exec(r.Context(), `UPDATE tax_recurring_expenses SET active=$2,updated_at=now() WHERE id=$1`, id, req.Active); err != nil {
		serverError(w, r, "Wiederkehrende Ausgabe konnte nicht gespeichert werden", err)
		return
	}
	if err := h.auditChangeTx(r.Context(), tx, r, "update", "tax_recurring_expense", id, "Wiederkehrende Ausgabe geändert",
		diffFields(map[string]any{"active": old}, map[string]any{"active": req.Active})); err != nil {
		serverError(w, r, "Wiederkehrende Ausgabe konnte nicht gespeichert werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Wiederkehrende Ausgabe konnte nicht gespeichert werden", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "active": req.Active})
}

func (h *Handler) BookTaxRecurring(w http.ResponseWriter, r *http.Request) {
	id, ok := scanTaxID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		PaidOn string `json:"paid_on"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Ungültiges Zahlungsdatum")
		return
	}
	paidOn, err := parseTaxDate(req.PaidOn)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Ungültiges Zahlungsdatum")
		return
	}
	if h.rejectLockedYear(w, r, paidOn.Year()) {
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Ausgabe konnte nicht gebucht werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var v taxRecurringExpense
	var start time.Time
	var end *time.Time
	if err := tx.QueryRow(r.Context(), `
		SELECT tax_property_id,category_id,description,payee,amount,payment_method,frequency,due_day,due_month,start_on,end_on,active
		  FROM tax_recurring_expenses WHERE id=$1 FOR UPDATE`, id).Scan(&v.PropertyID, &v.CategoryID, &v.Description,
		&v.Payee, &v.Amount, &v.PaymentMethod, &v.Frequency, &v.DueDay, &v.DueMonth, &start, &end, &v.Active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Wiederkehrende Ausgabe nicht gefunden")
			return
		}
		serverError(w, r, "Ausgabe konnte nicht gebucht werden", err)
		return
	}
	if !v.Active || paidOn.Before(start) || (end != nil && paidOn.After(*end)) {
		writeError(w, http.StatusConflict, "Vorlage ist für dieses Datum nicht aktiv")
		return
	}
	var expenseID int64
	if err := tx.QueryRow(r.Context(), `
		INSERT INTO tax_expenses(tax_property_id,category_id,recurring_expense_id,paid_on,amount,payee,description,payment_method,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, v.PropertyID, v.CategoryID, id, paidOn,
		v.Amount, v.Payee, v.Description, v.PaymentMethod, taxActorID(r)).Scan(&expenseID); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "Diese Vorlage wurde an diesem Tag bereits gebucht")
			return
		}
		serverError(w, r, "Ausgabe konnte nicht gebucht werden", err)
		return
	}
	e := taxExpense{ID: expenseID, PropertyID: v.PropertyID, CategoryID: v.CategoryID, PaidOn: paidOn.Format("2006-01-02"),
		Amount: v.Amount, Payee: v.Payee, Description: v.Description, PaymentMethod: v.PaymentMethod}
	if err := h.auditCreatedTx(r.Context(), tx, r, "tax_expense", expenseID, "Wiederkehrende Werbungskosten gebucht: "+v.Description, expenseAudit(e)); err != nil {
		serverError(w, r, "Ausgabe konnte nicht gebucht werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Ausgabe konnte nicht gebucht werden", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": expenseID})
}

type taxAssetRequest struct {
	PropertyID       int64   `json:"property_id"`
	Name             string  `json:"name"`
	InServiceOn      string  `json:"in_service_on"`
	DepreciableBasis float64 `json:"depreciable_basis"`
	UsefulLifeYears  float64 `json:"useful_life_years"`
	HalfYearRule     bool    `json:"half_year_rule"`
	DisposedOn       string  `json:"disposed_on"`
	Notes            string  `json:"notes"`
}

func validateTaxAssetRequest(req *taxAssetRequest) (time.Time, *time.Time, bool) {
	inService, err := parseTaxDate(req.InServiceOn)
	disposed, disposeErr := nullableDate(req.DisposedOn)
	req.Name, req.Notes = strings.TrimSpace(req.Name), strings.TrimSpace(req.Notes)
	ok := err == nil && disposeErr == nil && (disposed == nil || !disposed.Before(inService)) && req.PropertyID > 0 &&
		req.DepreciableBasis > 0 && req.DepreciableBasis <= 1e9 && req.UsefulLifeYears > 0 && req.UsefulLifeYears <= 200 &&
		validTaxText(req.Name, true) && validTaxText(req.Notes, false)
	return inService, disposed, ok
}

func (h *Handler) ListTaxAssets(w http.ResponseWriter, r *http.Request) {
	items, err := h.loadTaxAssets(r.Context(), parseYearParam(r, h.now().Year()))
	if err != nil {
		serverError(w, r, "Anlageverzeichnis konnte nicht geladen werden", err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) CreateTaxAsset(w http.ResponseWriter, r *http.Request) {
	var req taxAssetRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Ungültiges Wirtschaftsgut")
		return
	}
	inService, disposed, ok := validateTaxAssetRequest(&req)
	if !ok {
		writeError(w, http.StatusBadRequest, "Ungültiges Wirtschaftsgut")
		return
	}
	if h.rejectLockedYear(w, r, inService.Year()) {
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Wirtschaftsgut konnte nicht angelegt werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var id int64
	err = tx.QueryRow(r.Context(), `
		INSERT INTO tax_assets(tax_property_id,name,in_service_on,depreciable_basis,useful_life_years,half_year_rule,disposed_on,notes,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`, req.PropertyID, req.Name, inService, round2(req.DepreciableBasis),
		req.UsefulLifeYears, req.HalfYearRule, disposed, req.Notes, taxActorID(r)).Scan(&id)
	if err != nil {
		if taxConflict(w, err) {
			return
		}
		serverError(w, r, "Wirtschaftsgut konnte nicht angelegt werden", err)
		return
	}
	if err := h.auditCreatedTx(r.Context(), tx, r, "tax_asset", id, "Wirtschaftsgut angelegt: "+req.Name,
		map[string]any{"property_id": req.PropertyID, "name": req.Name, "in_service_on": inService.Format("2006-01-02"),
			"depreciable_basis": round2(req.DepreciableBasis), "useful_life_years": req.UsefulLifeYears,
			"half_year_rule": req.HalfYearRule, "disposed_on": taxDateString(disposed)}); err != nil {
		serverError(w, r, "Wirtschaftsgut konnte nicht angelegt werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Wirtschaftsgut konnte nicht angelegt werden", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (h *Handler) UpdatePaymentTaxMetadata(w http.ResponseWriter, r *http.Request) {
	id, ok := scanTaxID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req struct {
		ReceivedOn string `json:"received_on"`
		PropertyID int64  `json:"property_id"`
	}
	if err := decodeJSON(r, &req); err != nil || req.PropertyID <= 0 {
		writeError(w, http.StatusBadRequest, "Ungültige Steuerzuordnung")
		return
	}
	receivedOn, err := nullableDate(req.ReceivedOn)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Ungültiges Zuflussdatum")
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Steuerzuordnung konnte nicht gespeichert werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var paidOn time.Time
	var auto bool
	var oldReceived *time.Time
	var oldProperty int64
	if err := tx.QueryRow(r.Context(), `
		SELECT p.paid_on,p.auto,m.received_on,
		       COALESCE(m.tax_property_id,(SELECT id FROM tax_properties ORDER BY is_default DESC,id LIMIT 1))
		  FROM payments p LEFT JOIN payment_tax_metadata m ON m.payment_id=p.id
		 WHERE p.id=$1 FOR UPDATE OF p`, id).Scan(&paidOn, &auto, &oldReceived, &oldProperty); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Zahlung nicht gefunden")
			return
		}
		serverError(w, r, "Steuerzuordnung konnte nicht gespeichert werden", err)
		return
	}
	if !auto && receivedOn != nil && !sameTaxDay(*receivedOn, paidOn) {
		writeError(w, http.StatusBadRequest, "Das Zuflussdatum kann nur bei Schalter-Zahlungen abweichend gesetzt werden")
		return
	}
	oldEffective := paidOn
	if oldReceived != nil {
		oldEffective = *oldReceived
	}
	newEffective := paidOn
	if receivedOn != nil {
		newEffective = *receivedOn
	}
	for _, year := range []int{oldEffective.Year(), newEffective.Year()} {
		var locked bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM tax_year_locks WHERE year=$1)`, year).Scan(&locked); err != nil {
			serverError(w, r, "Steuerjahrsperre konnte nicht geprüft werden", err)
			return
		}
		if locked {
			writeError(w, http.StatusConflict, "Steuerjahr "+strconv.Itoa(year)+" ist gesperrt")
			return
		}
	}
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO payment_tax_metadata(payment_id,received_on,tax_property_id,updated_by,updated_at)
		VALUES($1,$2,$3,$4,now())
		ON CONFLICT(payment_id) DO UPDATE SET received_on=EXCLUDED.received_on,tax_property_id=EXCLUDED.tax_property_id,
		                                      updated_by=EXCLUDED.updated_by,updated_at=now()`,
		id, receivedOn, req.PropertyID, taxActorID(r)); err != nil {
		if taxConflict(w, err) {
			return
		}
		serverError(w, r, "Steuerzuordnung konnte nicht gespeichert werden", err)
		return
	}
	if err := h.auditChangeTx(r.Context(), tx, r, "update", "payment_tax_metadata", id, "Steuerzuordnung einer Zahlung geändert",
		diffFields(map[string]any{"received_on": taxDateString(oldReceived), "property_id": oldProperty},
			map[string]any{"received_on": taxDateString(receivedOn), "property_id": req.PropertyID})); err != nil {
		serverError(w, r, "Steuerzuordnung konnte nicht gespeichert werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Steuerzuordnung konnte nicht gespeichert werden", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payment_id": id, "received_on": taxDateString(receivedOn), "property_id": req.PropertyID})
}

func sameTaxDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func taxYearPath(r *http.Request) (int, bool) {
	year, err := strconv.Atoi(r.PathValue("year"))
	return year, err == nil && year >= 2000 && year <= 2100
}

func (h *Handler) LockTaxYear(w http.ResponseWriter, r *http.Request) {
	year, ok := taxYearPath(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Ungültiges Steuerjahr")
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Steuerjahr konnte nicht gesperrt werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var lockedAt time.Time
	if err := tx.QueryRow(r.Context(), `INSERT INTO tax_year_locks(year,locked_by) VALUES($1,$2)
		ON CONFLICT(year) DO UPDATE SET year=EXCLUDED.year RETURNING locked_at`, year, taxActorID(r)).Scan(&lockedAt); err != nil {
		serverError(w, r, "Steuerjahr konnte nicht gesperrt werden", err)
		return
	}
	if err := h.auditCreatedTx(r.Context(), tx, r, "tax_year_lock", int64(year), "Steuerjahr gesperrt: "+strconv.Itoa(year),
		map[string]any{"year": year, "locked_at": lockedAt}); err != nil {
		serverError(w, r, "Steuerjahr konnte nicht gesperrt werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Steuerjahr konnte nicht gesperrt werden", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"year": year, "locked": true, "locked_at": lockedAt})
}

func (h *Handler) UnlockTaxYear(w http.ResponseWriter, r *http.Request) {
	year, ok := taxYearPath(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "Ungültiges Steuerjahr")
		return
	}
	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		serverError(w, r, "Steuerjahr konnte nicht entsperrt werden", err)
		return
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	var lockedAt time.Time
	if err := tx.QueryRow(r.Context(), `DELETE FROM tax_year_locks WHERE year=$1 RETURNING locked_at`, year).Scan(&lockedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Steuerjahr ist nicht gesperrt")
			return
		}
		serverError(w, r, "Steuerjahr konnte nicht entsperrt werden", err)
		return
	}
	if err := h.auditDeletedTx(r.Context(), tx, r, "tax_year_lock", int64(year), "Steuerjahr entsperrt: "+strconv.Itoa(year),
		map[string]any{"year": year, "locked_at": lockedAt}); err != nil {
		serverError(w, r, "Steuerjahr konnte nicht entsperrt werden", err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, r, "Steuerjahr konnte nicht entsperrt werden", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"year": year, "locked": false})
}

func (h *Handler) addTaxBook(ctx context.Context, rep *taxYearReport) error {
	var err error
	if rep.Properties, err = h.loadTaxProperties(ctx); err != nil {
		return err
	}
	if rep.Categories, err = h.loadTaxCategories(ctx); err != nil {
		return err
	}
	if rep.Expenses, err = h.loadTaxExpenses(ctx, rep.Year); err != nil {
		return err
	}
	if rep.RecurringExpenses, err = h.loadTaxRecurring(ctx); err != nil {
		return err
	}
	if rep.Assets, err = h.loadTaxAssets(ctx, rep.Year); err != nil {
		return err
	}
	if rep.Settings, err = h.loadTaxSettings(ctx); err != nil {
		return err
	}
	if rep.Locked, err = h.taxYearLocked(ctx, rep.Year); err != nil {
		return err
	}

	summaries := make(map[int64]*taxPropertySummary, len(rep.Properties))
	for _, p := range rep.Properties {
		summaries[p.ID] = &taxPropertySummary{ID: p.ID, Name: p.Name, Address: p.Address}
	}
	for _, p := range rep.payments {
		s := summaries[p.PropertyID]
		if s == nil {
			continue
		}
		if p.PaidOn.Year() == rep.Year {
			s.IncomeByDate += p.Amount
		}
		if p.RuleYear == rep.Year {
			s.IncomeWithRule += p.Amount
		}
	}
	e1b := map[int64]map[string]float64{}
	for _, e := range rep.Expenses {
		s := summaries[e.PropertyID]
		if s == nil {
			continue
		}
		s.Expenses += e.Amount
		if e1b[e.PropertyID] == nil {
			e1b[e.PropertyID] = map[string]float64{}
		}
		e1b[e.PropertyID][e.E1BCode] += e.Amount
		rep.ExpenseTotal += e.Amount
	}
	for _, a := range rep.Assets {
		s := summaries[a.PropertyID]
		if s == nil {
			continue
		}
		s.Depreciation += a.YearDepreciation
		if e1b[a.PropertyID] == nil {
			e1b[a.PropertyID] = map[string]float64{}
		}
		e1b[a.PropertyID]["9500"] += a.YearDepreciation
		rep.DepreciationTotal += a.YearDepreciation
	}
	rep.PropertySummaries = []taxPropertySummary{}
	for _, p := range rep.Properties {
		s := summaries[p.ID]
		s.IncomeByDate, s.IncomeWithRule = round2(s.IncomeByDate), round2(s.IncomeWithRule)
		s.Expenses, s.Depreciation = round2(s.Expenses), round2(s.Depreciation)
		s.Surplus = round2(s.IncomeByDate - s.Expenses - s.Depreciation)
		s.SurplusWithRule = round2(s.IncomeWithRule - s.Expenses - s.Depreciation)
		rep.PropertySummaries = append(rep.PropertySummaries, *s)
		rep.E1B = append(rep.E1B, taxE1BLine{PropertyID: p.ID, Code: "9460", Label: "Einnahmen", Amount: s.IncomeByDate})
		for _, code := range []string{"9500", "9510", "9520", "9530"} {
			label := map[string]string{"9500": "AfA", "9510": "Fremdfinanzierungskosten", "9520": "Erhaltung / Instandsetzung", "9530": "Übrige Werbungskosten"}[code]
			rep.E1B = append(rep.E1B, taxE1BLine{PropertyID: p.ID, Code: code, Label: label, Amount: round2(e1b[p.ID][code])})
		}
	}
	rep.ExpenseTotal, rep.DepreciationTotal = round2(rep.ExpenseTotal), round2(rep.DepreciationTotal)
	rep.Surplus = round2(rep.TotalByDate - rep.ExpenseTotal - rep.DepreciationTotal)
	rep.SurplusWithRule = round2(rep.TotalWithRule - rep.ExpenseTotal - rep.DepreciationTotal)
	if rep.Settings.SmallBusinessLimit > 0 {
		rep.SmallBusinessPercent = round2(rep.TotalByDate / rep.Settings.SmallBusinessLimit * 100)
	}
	rep.SmallBusinessExceeded = rep.TotalByDate > rep.Settings.SmallBusinessLimit
	return nil
}
