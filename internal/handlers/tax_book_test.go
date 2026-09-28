package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestAnnualTaxDepreciation covers the half-year rule, disposal, and basis cap.
func TestAnnualTaxDepreciation(t *testing.T) {
	date := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	tests := []struct {
		name      string
		basis     float64
		life      float64
		inService string
		disposed  string
		year      int
		halfYear  bool
		want      float64
	}{
		{name: "before commissioning", basis: 1000, life: 10, inService: "2030-01-01", year: 2029, halfYear: true, want: 0},
		{name: "first half gets full year", basis: 1000, life: 10, inService: "2030-06-30", year: 2030, halfYear: true, want: 100},
		{name: "second half gets half year", basis: 1000, life: 10, inService: "2030-07-01", year: 2030, halfYear: true, want: 50},
		{name: "middle year is full", basis: 1000, life: 10, inService: "2030-07-01", year: 2035, halfYear: true, want: 100},
		{name: "final year is capped remainder", basis: 1000, life: 10, inService: "2030-07-01", year: 2040, halfYear: true, want: 50},
		{name: "nothing after basis consumed", basis: 1000, life: 10, inService: "2030-07-01", year: 2041, halfYear: true, want: 0},
		{name: "disposal in first half gets half year", basis: 1000, life: 10, inService: "2030-01-01", disposed: "2035-06-30", year: 2035, halfYear: true, want: 50},
		{name: "disposal after more than six months gets full year", basis: 1000, life: 10, inService: "2030-01-01", disposed: "2035-07-02", year: 2035, halfYear: true, want: 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var disposed *time.Time
			if tc.disposed != "" {
				d := date(tc.disposed)
				disposed = &d
			}
			got := annualTaxDepreciation(tc.basis, tc.life, date(tc.inService), disposed, tc.year, tc.halfYear)
			if got != tc.want {
				t.Fatalf("annualTaxDepreciation() = %.2f, want %.2f", got, tc.want)
			}
		})
	}
}

// TestTaxE1BAmountUsesSelectedRule changes only income when the 15-day rule is selected.
func TestTaxE1BAmountUsesSelectedRule(t *testing.T) {
	rep := taxYearReport{PropertySummaries: []taxPropertySummary{{ID: 7, IncomeByDate: 100, IncomeWithRule: 80}}}
	income := taxE1BLine{PropertyID: 7, Code: "9460", Amount: 100}
	expense := taxE1BLine{PropertyID: 7, Code: "9530", Amount: 20}
	if got := taxE1BAmount(rep, income, true); got != 80 {
		t.Fatalf("rule income = %.2f, want 80", got)
	}
	if got := taxE1BAmount(rep, income, false); got != 100 {
		t.Fatalf("cash-basis income = %.2f, want 100", got)
	}
	if got := taxE1BAmount(rep, expense, true); got != 20 {
		t.Fatalf("expense with rule = %.2f, want 20", got)
	}
}

// TestCreateTaxAssetRejectsAnyLockedAffectedYear covers bounded and open asset ranges.
func TestCreateTaxAssetRejectsAnyLockedAffectedYear(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	var propertyID int64
	if err := h.Pool.QueryRow(ctx, `SELECT id FROM tax_properties ORDER BY is_default DESC,id LIMIT 1`).Scan(&propertyID); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		lockedYear int
		disposedOn string
	}{
		{name: "disposed range is inclusive", lockedYear: 2096, disposedOn: "2097-12-31"},
		{name: "open asset has no upper disposal bound", lockedYear: 2097},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := h.Pool.Exec(ctx, `DELETE FROM tax_year_locks WHERE year=$1`, tc.lockedYear); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = h.Pool.Exec(context.Background(), `DELETE FROM tax_year_locks WHERE year=$1`, tc.lockedYear)
			})
			if _, err := h.Pool.Exec(ctx, `INSERT INTO tax_year_locks(year) VALUES($1)`, tc.lockedYear); err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			h.CreateTaxAsset(rec, taxJSONRequest(t, http.MethodPost, "/api/tax/assets", map[string]any{
				"property_id": propertyID, "name": "Locked range test", "in_service_on": "2095-01-01",
				"depreciable_basis": 1000.0, "useful_life_years": 10.0, "half_year_rule": true,
				"disposed_on": tc.disposedOn, "notes": "",
			}))
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), strconv.Itoa(tc.lockedYear)) {
				t.Fatalf("create asset with locked year %d: %d %s", tc.lockedYear, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestCreateTaxExpenseWaitsForConcurrentYearLock proves lock checks serialize with lock writes.
func TestCreateTaxExpenseWaitsForConcurrentYearLock(t *testing.T) {
	h := testHandler(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const year = 2094
	if _, err := h.Pool.Exec(ctx, `DELETE FROM tax_year_locks WHERE year=$1`, year); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM tax_year_locks WHERE year=$1`, year) })

	var propertyID, categoryID int64
	if err := h.Pool.QueryRow(ctx, `SELECT id FROM tax_properties ORDER BY is_default DESC,id LIMIT 1`).Scan(&propertyID); err != nil {
		t.Fatal(err)
	}
	if err := h.Pool.QueryRow(ctx, `SELECT id FROM tax_expense_categories ORDER BY id LIMIT 1`).Scan(&categoryID); err != nil {
		t.Fatal(err)
	}
	blocker, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if err := acquireTaxYearAdvisoryLock(ctx, blocker, year); err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, `INSERT INTO tax_year_locks(year) VALUES($1)`, year); err != nil {
		t.Fatal(err)
	}

	req := taxJSONRequest(t, http.MethodPost, "/api/tax/expenses", map[string]any{
		"property_id": propertyID, "category_id": categoryID, "paid_on": "2094-04-01",
		"amount": 1.0, "vat_amount": 0.0, "description": "concurrent lock", "payment_method": "bar",
	}).WithContext(ctx)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.CreateTaxExpense(rec, req)
		done <- rec
	}()
	waitForLockWait(ctx, t, h, "%pg_advisory_xact_lock%")
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case rec := <-done:
		if rec.Code != http.StatusConflict {
			t.Fatalf("create after concurrent lock: %d %s", rec.Code, rec.Body.String())
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// taxJSONRequest creates a JSON handler request for tax-book tests.
func taxJSONRequest(t *testing.T, method, target string, body any) *http.Request {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestTaxBookFlow exercises the expense, receipt, asset, reversal, lock, and export lifecycle.
func TestTaxBookFlow(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	const year = 2098
	if err := purgeExec(ctx, h.Pool, `DELETE FROM tax_expense_receipts a USING tax_expenses e WHERE a.expense_id=e.id AND EXTRACT(YEAR FROM e.paid_on)=$1`, year); err != nil {
		t.Fatal(err)
	}
	if err := purgeExec(ctx, h.Pool, `DELETE FROM tax_expenses WHERE EXTRACT(YEAR FROM paid_on)=$1`, year); err != nil {
		t.Fatal(err)
	}
	if err := purgeExec(ctx, h.Pool, `DELETE FROM tax_assets WHERE name='Steuerjahr Integration'`); err != nil {
		t.Fatal(err)
	}
	if err := purgeExec(ctx, h.Pool, `DELETE FROM tax_properties WHERE name LIKE 'Steuerobjekt Integration%'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = purgeExec(ctx, h.Pool, `DELETE FROM tax_year_locks WHERE year=$1`, year)
		_ = purgeExec(ctx, h.Pool, `DELETE FROM tax_expense_receipts a USING tax_expenses e WHERE a.expense_id=e.id AND EXTRACT(YEAR FROM e.paid_on)=$1`, year)
		_ = purgeExec(ctx, h.Pool, `DELETE FROM tax_expenses WHERE EXTRACT(YEAR FROM paid_on)=$1`, year)
		_ = purgeExec(ctx, h.Pool, `DELETE FROM tax_assets WHERE name='Steuerjahr Integration'`)
		_ = purgeExec(ctx, h.Pool, `DELETE FROM tax_properties WHERE name LIKE 'Steuerobjekt Integration%'`)
	})

	var propertyID, categoryID int64
	rec := httptest.NewRecorder()
	h.CreateTaxProperty(rec, taxJSONRequest(t, http.MethodPost, "/api/tax/properties", map[string]any{
		"name": "Steuerobjekt Integration", "address": "Testweg 1", "postal_code": "2098", "eawz": "EW-1", "garage_ids": []int64{},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create property: %d %s", rec.Code, rec.Body.String())
	}
	var propertyCreated struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &propertyCreated); err != nil || propertyCreated.ID == 0 {
		t.Fatalf("decode property: %v / %s", err, rec.Body.String())
	}
	propertyID = propertyCreated.ID
	updateProperty := taxJSONRequest(t, http.MethodPut, "/api/tax/properties/1", map[string]any{
		"name": "Steuerobjekt Integration geändert", "address": "Testweg 2", "postal_code": "2098", "eawz": "EW-2", "garage_ids": []int64{},
	})
	updateProperty.SetPathValue("id", strconv.FormatInt(propertyID, 10))
	rec = httptest.NewRecorder()
	h.UpdateTaxProperty(rec, updateProperty)
	if rec.Code != http.StatusOK {
		t.Fatalf("update property: %d %s", rec.Code, rec.Body.String())
	}
	if err := h.Pool.QueryRow(ctx, `SELECT id FROM tax_expense_categories WHERE key='maintenance'`).Scan(&categoryID); err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	h.CreateTaxExpense(rec, taxJSONRequest(t, http.MethodPost, "/api/tax/expenses", map[string]any{
		"property_id": propertyID, "category_id": categoryID, "paid_on": "2098-03-10",
		"amount": 120.0, "vat_amount": 20.0, "payee": "Werkstatt", "description": "Torwartung", "payment_method": "ueberweisung",
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create expense: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.ID == 0 {
		t.Fatalf("decode expense: %v / %s", err, rec.Body.String())
	}

	var upload bytes.Buffer
	mw := multipart.NewWriter(&upload)
	part, err := mw.CreateFormFile("file", "rechnung.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("%PDF-1.4\n%%EOF\n")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	uploadReq := httptest.NewRequest(http.MethodPost, "/api/tax/expenses/1/receipts", &upload)
	uploadReq.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	uploadReq.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	h.UploadTaxReceipt(rec, uploadReq)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload receipt: %d %s", rec.Code, rec.Body.String())
	}
	var receipt struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil || receipt.ID == 0 {
		t.Fatalf("decode receipt: %v / %s", err, rec.Body.String())
	}
	downloadReq := httptest.NewRequest(http.MethodGet, "/api/tax/receipts/1", nil)
	downloadReq.SetPathValue("id", strconv.FormatInt(receipt.ID, 10))
	rec = httptest.NewRecorder()
	h.GetTaxReceipt(rec, downloadReq)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download receipt: %d, headers %v", rec.Code, rec.Header())
	}

	rec = httptest.NewRecorder()
	h.CreateTaxAsset(rec, taxJSONRequest(t, http.MethodPost, "/api/tax/assets", map[string]any{
		"property_id": propertyID, "name": "Steuerjahr Integration", "in_service_on": "2098-07-01",
		"depreciable_basis": 1000.0, "useful_life_years": 10.0, "half_year_rule": true, "disposed_on": "", "notes": "",
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create asset: %d %s", rec.Code, rec.Body.String())
	}

	rep, err := h.buildTaxYearReport(ctx, year)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ExpenseTotal != 120 || rep.DepreciationTotal != 50 || rep.Surplus != -170 {
		t.Fatalf("totals expense %.2f depreciation %.2f surplus %.2f", rep.ExpenseTotal, rep.DepreciationTotal, rep.Surplus)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tax/expenses/1/reverse", nil)
	req.SetPathValue("id", strconv.FormatInt(created.ID, 10))
	rec = httptest.NewRecorder()
	h.ReverseTaxExpense(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reverse expense: %d %s", rec.Code, rec.Body.String())
	}
	rep, err = h.buildTaxYearReport(ctx, year)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ExpenseTotal != 0 || rep.DepreciationTotal != 50 {
		t.Fatalf("after reversal expense %.2f depreciation %.2f", rep.ExpenseTotal, rep.DepreciationTotal)
	}

	lockReq := httptest.NewRequest(http.MethodPost, "/api/tax/years/2098/lock", nil)
	lockReq.SetPathValue("year", "2098")
	rec = httptest.NewRecorder()
	h.LockTaxYear(rec, lockReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("lock: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.CreateTaxExpense(rec, taxJSONRequest(t, http.MethodPost, "/api/tax/expenses", map[string]any{
		"property_id": propertyID, "category_id": categoryID, "paid_on": "2098-04-01",
		"amount": 1.0, "vat_amount": 0.0, "description": "gesperrt", "payment_method": "bar",
	}))
	if rec.Code != http.StatusConflict {
		t.Fatalf("locked create: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.TaxYearPackage(rec, httptest.NewRequest(http.MethodGet, "/api/reports/tax-year.zip?year=2098", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("zip: %d %s", rec.Code, rec.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"steuerjahr-2098.pdf": false, "einnahmen.csv": false, "werbungskosten.csv": false, "anlageverzeichnis.csv": false, "e1b-vorschau.csv": false, "LESE_MICH.txt": false}
	foundReceipt := false
	for _, f := range zr.File {
		if _, ok := want[f.Name]; ok {
			want[f.Name] = true
		}
		if strings.HasPrefix(f.Name, "belege/ausgabe-") && strings.HasSuffix(f.Name, "rechnung.pdf") {
			foundReceipt = true
		}
		rc, err := f.Open()
		if err == nil {
			_, _ = io.Copy(io.Discard, rc)
			_ = rc.Close()
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("ZIP missing %s", name)
		}
	}
	if !foundReceipt {
		t.Error("ZIP missing uploaded receipt")
	}
}
