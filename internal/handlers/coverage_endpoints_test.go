package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// Diese Datei schliesst Abdeckungsluecken bei Listen- und CRUD-Endpunkten, die zwar
// ueber die Oberflaeche taeglich benutzt, aber von KEINEM Go-Test durchlaufen wurden
// (0 % in der Linux-Messung der Pipeline; auf Windows verdeckt ein Zaehl-Artefakt der
// Cover-Werkzeuge die Luecke). Kein neues Verhalten — nur Ausfuehrung der bestehenden
// Handler ueber den echten HTTP-Pfad, damit eine kuenftige Regression auffaellt.
//
// Bewusst genuegsam in den Zusicherungen: geprueft wird, dass der Handler seinen
// Weg OHNE 5xx zu Ende geht (und die Erfolgspfade 2xx liefern). So faellt der Test
// bei einem echten Serverfehler um, nicht bei einer fachlichen 4xx-Absage, die von
// der geteilten Test-DB abhaengen koennte.

// callJSON ruft einen Handler direkt auf (die Auth-Middleware haengt am Mux, nicht am
// Handler — fuer die reine Ausfuehrung ist sie hier ohne Belang) und setzt bei Bedarf
// den {id}-Pfadwert.
func callJSON(h *Handler, hf http.HandlerFunc, method, path, id string, body any) *httptest.ResponseRecorder {
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if id != "" {
		r.SetPathValue("id", id)
	}
	rec := httptest.NewRecorder()
	hf(rec, r)
	return rec
}

func mustNot5xx(t *testing.T, rec *httptest.ResponseRecorder, what string) {
	t.Helper()
	if rec.Code >= 500 {
		t.Fatalf("%s: unerwarteter Serverfehler %d: %s", what, rec.Code, rec.Body.String())
	}
}

// TestCoverageListEndpoints laeuft die bislang ungetesteten GET-Listen durch.
func TestCoverageListEndpoints(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	gid, _ := mkGarage(t, h)

	mustNot5xx(t, callJSON(h, h.ListUsers, http.MethodGet, "/api/users", "", nil), "ListUsers")
	mustNot5xx(t, callJSON(h, h.ListServiceTypes, http.MethodGet, "/api/services", "", nil), "ListServiceTypes")
	mustNot5xx(t, callJSON(h, h.ListPlannerIcons, http.MethodGet, "/api/planner-icons", "", nil), "ListPlannerIcons")
	mustNot5xx(t, callJSON(h, h.ListHalls, http.MethodGet, "/api/garages/x/halls", strconv.FormatInt(gid, 10), nil), "ListHalls")
	mustNot5xx(t, callJSON(h, h.ListRecurringCharges, http.MethodGet, "/api/persons/x/recurring", strconv.FormatInt(pid, 10), nil), "ListRecurringCharges")

	// Die invaliden-Pfad-Zweige mitnehmen (pathID scheitert).
	if rec := callJSON(h, h.ListHalls, http.MethodGet, "/api/garages/x/halls", "keine-zahl", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("ListHalls mit ungueltiger id: %d, erwartet 400", rec.Code)
	}

	t.Cleanup(func() { purgeGarages(t, h, gid) })
}

// TestCoverageHallSpotLifecycle deckt Aktualisieren/Loeschen von Halle und Stellplatz.
func TestCoverageHallSpotLifecycle(t *testing.T) {
	h := testHandler(t)
	gid, _ := mkGarage(t, h)
	t.Cleanup(func() { purgeGarages(t, h, gid) })

	hall := mkHall(t, h, gid, json.RawMessage(`{}`))
	spot := mkSpot(t, h, hall.ID, "COV-P1", json.RawMessage(`{}`))

	up := callJSON(h, h.UpdateHall, http.MethodPut, "/api/halls/x", strconv.FormatInt(hall.ID, 10),
		map[string]any{"name": "COV-Halle-neu", "geometry": json.RawMessage(`{}`), "sort_order": 1})
	mustNot5xx(t, up, "UpdateHall")
	if up.Code != http.StatusOK {
		t.Errorf("UpdateHall: %d %s", up.Code, up.Body.String())
	}

	us := callJSON(h, h.UpdateSpot, http.MethodPut, "/api/spots/x", strconv.FormatInt(spot.ID, 10),
		map[string]any{"label": "COV-P1-neu", "geometry": json.RawMessage(`{}`)})
	mustNot5xx(t, us, "UpdateSpot")
	if us.Code != http.StatusOK {
		t.Errorf("UpdateSpot: %d %s", us.Code, us.Body.String())
	}

	del := callJSON(h, h.DeleteHall, http.MethodDelete, "/api/halls/x", strconv.FormatInt(hall.ID, 10), nil)
	mustNot5xx(t, del, "DeleteHall")
	if del.Code != http.StatusOK && del.Code != http.StatusNoContent {
		t.Errorf("DeleteHall: %d %s", del.Code, del.Body.String())
	}
}

// TestCoverageServiceTypeLifecycle deckt Anlegen/Aktualisieren/Archivieren/Loeschen
// der Leistungsarten.
func TestCoverageServiceTypeLifecycle(t *testing.T) {
	h := testHandler(t)
	name := "COV-Leistung-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	cr := callJSON(h, h.CreateServiceType, http.MethodPost, "/api/services", "",
		map[string]any{"name": name, "default_amount": 12.5})
	mustNot5xx(t, cr, "CreateServiceType")
	if cr.Code != http.StatusCreated && cr.Code != http.StatusOK {
		t.Fatalf("CreateServiceType: %d %s", cr.Code, cr.Body.String())
	}
	var st struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(cr.Body.Bytes(), &st)
	if st.ID == 0 {
		t.Fatal("CreateServiceType lieferte keine id")
	}
	sid := strconv.FormatInt(st.ID, 10)
	t.Cleanup(func() {
		_ = purge(context.Background(), h, `DELETE FROM service_types WHERE id=$1`, st.ID)
	})

	mustNot5xx(t, callJSON(h, h.UpdateServiceType, http.MethodPut, "/api/services/x", sid,
		map[string]any{"name": name + "-neu", "default_amount": 20}), "UpdateServiceType")
	mustNot5xx(t, callJSON(h, h.SetServiceArchived, http.MethodPost, "/api/services/x/archived", sid,
		map[string]any{"archived": true}), "SetServiceArchived")
	mustNot5xx(t, callJSON(h, h.DeleteServiceType, http.MethodDelete, "/api/services/x", sid, nil), "DeleteServiceType")
}

// TestCoverageVehicleLifecycle deckt Planer-Update, Duplizieren und Loeschen.
func TestCoverageVehicleLifecycle(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 40, firstOfMonthMonthsAgo(1).Format("2006-01-02"))
	sid := strconv.FormatInt(vid, 10)

	mustNot5xx(t, callJSON(h, h.UpdateVehiclePlanner, http.MethodPut, "/api/vehicles/x/planner", sid,
		map[string]any{"needs_power": true, "planner_symbol": nil}), "UpdateVehiclePlanner")

	dup := callJSON(h, h.DuplicateVehicle, http.MethodPost, "/api/vehicles/x/duplicate", sid,
		map[string]any{"start_date": ""})
	mustNot5xx(t, dup, "DuplicateVehicle")

	mustNot5xx(t, callJSON(h, h.DeleteVehicle, http.MethodDelete, "/api/vehicles/x", sid, nil), "DeleteVehicle")
	// ReactivateVehicle: der ungueltige-id-Pfad genuegt fuer die Abdeckung des
	// Einstiegs, ohne einen archivierten Zustand aufbauen zu muessen.
	mustNot5xx(t, callJSON(h, h.ReactivateVehicle, http.MethodPost, "/api/vehicles/x/reactivate", sid, nil), "ReactivateVehicle")
}

// TestCoverageDeleteUserAndCharge deckt die Loesch-Endpunkte fuer Benutzer und Posten.
func TestCoverageDeleteUserAndCharge(t *testing.T) {
	h := testHandler(t)
	uid := mkUser(t, h, "cov-user-"+strconv.FormatInt(time.Now().UnixNano(), 10), false)
	mustNot5xx(t, callJSON(h, h.DeleteUser, http.MethodDelete, "/api/users/x", strconv.FormatInt(uid, 10), nil), "DeleteUser")

	pid := createIntegrationPerson(t, h)
	cid := mkChargeP(t, h, pid, "COV-Posten", 9.9, firstOfMonthMonthsAgo(0).Format("2006-01-02"))
	mustNot5xx(t, callJSON(h, h.DeleteCharge, http.MethodDelete, "/api/charges/x", strconv.FormatInt(cid, 10), nil), "DeleteCharge")
}

func purgeGarages(t *testing.T, h *Handler, gid int64) {
	t.Helper()
	// Halle+Stellplaetze haengen per FK an der Garage; erst die Kinder, dann die Garage.
	ctx := context.Background()
	_ = purge(ctx, h, `DELETE FROM spots WHERE hall_id IN (SELECT id FROM halls WHERE garage_id=$1)`, gid)
	_ = purge(ctx, h, `DELETE FROM halls WHERE garage_id=$1`, gid)
	_ = purge(ctx, h, `DELETE FROM garages WHERE id=$1`, gid)
}

func purge(ctx context.Context, h *Handler, sql string, args ...any) error {
	return purgeExec(ctx, h.Pool, sql, args...)
}

// TestCoverageExportCSV laeuft alle Entitaets-Zweige des CSV-Exports durch. Ein
// Gefaehrt und ein Posten sorgen dafuer, dass die Zeilen-Schleifen nicht nur ihre
// Kopfzeile sehen.
func TestCoverageExportCSV(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	_ = mkStoredVehicle(t, h, pid, 30, firstOfMonthMonthsAgo(1).Format("2006-01-02"))
	_ = mkChargeP(t, h, pid, "COV-Export", 5, firstOfMonthMonthsAgo(0).Format("2006-01-02"))

	for _, entity := range []string{"outstanding", "payments", "persons", "vehicles", "occupancy", "invoices", "charges"} {
		r := httptest.NewRequest(http.MethodGet, "/api/export/"+entity, nil)
		r.SetPathValue("entity", entity)
		rec := httptest.NewRecorder()
		h.ExportCSV(rec, r)
		mustNot5xx(t, rec, "ExportCSV "+entity)
		if rec.Code != http.StatusOK {
			t.Errorf("ExportCSV %s: %d %s", entity, rec.Code, rec.Body.String())
		}
	}
	// Unbekannte Entitaet: der Ablehnungszweig.
	r := httptest.NewRequest(http.MethodGet, "/api/export/quatsch", nil)
	r.SetPathValue("entity", "quatsch")
	rec := httptest.NewRecorder()
	h.ExportCSV(rec, r)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Errorf("ExportCSV unbekannt: %d, erwartet 4xx", rec.Code)
	}
}

// TestCoverageBackupStatus laeuft die Status-Kachel-Abfrage durch (ohne pg_dump/S3 —
// die schweren Backup-Pfade bleiben bewusst ungetestet). Ohne PARKRR_BACKUP_KEY
// meldet sie schlicht "nicht aktiviert".
func TestCoverageBackupStatus(t *testing.T) {
	h := testHandler(t)
	rec := callJSON(h, h.BackupStatus, http.MethodGet, "/api/backup/status", "", nil)
	mustNot5xx(t, rec, "BackupStatus")
	if rec.Code != http.StatusOK {
		t.Errorf("BackupStatus: %d %s", rec.Code, rec.Body.String())
	}
}

// TestCoverageCategoryLifecycle deckt Liste/Anlegen/Aktualisieren/Archivieren/Loeschen
// der Tarife (categories) — der groesste noch dunkle, billig erreichbare Block.
func TestCoverageCategoryLifecycle(t *testing.T) {
	h := testHandler(t)
	mustNot5xx(t, callJSON(h, h.ListCategories, http.MethodGet, "/api/categories", "", nil), "ListCategories")

	name := "COV-Tarif-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	cr := callJSON(h, h.CreateCategory, http.MethodPost, "/api/categories", "",
		map[string]any{"name": name, "default_monthly_cost": 25, "default_yearly_cost": 250})
	mustNot5xx(t, cr, "CreateCategory")
	if cr.Code != http.StatusCreated && cr.Code != http.StatusOK {
		t.Fatalf("CreateCategory: %d %s", cr.Code, cr.Body.String())
	}
	var cat struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(cr.Body.Bytes(), &cat)
	if cat.ID == 0 {
		t.Fatal("CreateCategory lieferte keine id")
	}
	cid := strconv.FormatInt(cat.ID, 10)
	t.Cleanup(func() { _ = purge(context.Background(), h, `DELETE FROM categories WHERE id=$1`, cat.ID) })

	mustNot5xx(t, callJSON(h, h.UpdateCategory, http.MethodPut, "/api/categories/x", cid,
		map[string]any{"name": name + "-neu", "default_monthly_cost": 30, "default_yearly_cost": 300}), "UpdateCategory")
	mustNot5xx(t, callJSON(h, h.SetCategoryArchived, http.MethodPost, "/api/categories/x/archived", cid,
		map[string]any{"archived": true}), "SetCategoryArchived")
	mustNot5xx(t, callJSON(h, h.DeleteCategory, http.MethodDelete, "/api/categories/x", cid, nil), "DeleteCategory")
}

// TestCoveragePersonAndAgreementReads deckt ListAgreements und UpdatePerson.
func TestCoveragePersonAndAgreementReads(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	sid := strconv.FormatInt(pid, 10)

	mustNot5xx(t, callJSON(h, h.ListAgreements, http.MethodGet, "/api/persons/x/agreements", sid, nil), "ListAgreements")

	// last_name bleibt "Integration", sonst greift das Aufraeumen der Testhuelle nicht.
	up := callJSON(h, h.UpdatePerson, http.MethodPut, "/api/persons/x", sid,
		map[string]any{"first_name": "Cov", "last_name": "Integration", "email": "cov@example.at", "phone": "", "address": "", "notes": "COV"})
	mustNot5xx(t, up, "UpdatePerson")
	if up.Code != http.StatusOK {
		t.Errorf("UpdatePerson: %d %s", up.Code, up.Body.String())
	}
}

// TestCoverageMiscReads deckt kleinere, bislang ungetestete Lese-Endpunkte.
func TestCoverageMiscReads(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)

	mustNot5xx(t, callJSON(h, h.GetBillingSettings, http.MethodGet, "/api/billing/settings", "", nil), "GetBillingSettings")
	mustNot5xx(t, callJSON(h, h.ListWallTemplates, http.MethodGet, "/api/wall-templates", "", nil), "ListWallTemplates")
	mustNot5xx(t, callJSON(h, h.OpenItems, http.MethodGet, "/api/persons/x/open-items", strconv.FormatInt(pid, 10), nil), "OpenItems")
}

// TestCoverageReactivateAndSpotDelete deckt den ERFOLGSPFAD von ReactivateVehicle
// (der frühere Test traf nur den 404-Zweig) und das Löschen eines Stellplatzes.
func TestCoverageReactivateAndSpotDelete(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	vid := mkStoredVehicle(t, h, pid, 35, firstOfMonthMonthsAgo(1).Format("2006-01-02"))

	re := callJSON(h, h.ReactivateVehicle, http.MethodPost, "/api/vehicles/x/reactivate", strconv.FormatInt(vid, 10), nil)
	mustNot5xx(t, re, "ReactivateVehicle")
	if re.Code != http.StatusOK {
		t.Errorf("ReactivateVehicle: %d %s", re.Code, re.Body.String())
	}

	gid, _ := mkGarage(t, h)
	t.Cleanup(func() { purgeGarages(t, h, gid) })
	hall := mkHall(t, h, gid, json.RawMessage(`{}`))
	spot := mkSpot(t, h, hall.ID, "COV-DelP", json.RawMessage(`{}`))
	del := callJSON(h, h.DeleteSpot, http.MethodDelete, "/api/spots/x", strconv.FormatInt(spot.ID, 10), nil)
	mustNot5xx(t, del, "DeleteSpot")
	if del.Code != http.StatusOK && del.Code != http.StatusNoContent {
		t.Errorf("DeleteSpot: %d %s", del.Code, del.Body.String())
	}
}
