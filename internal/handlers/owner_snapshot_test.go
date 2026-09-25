package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// PRT-01: Übergabeprotokolle und Gefährt-Anhänge tragen den Halter, der beim
// Anlegen galt. Vorher wurde er über den HEUTIGEN Halter des Gefährts aufgelöst:
// nach einem Halterwechsel sah der neue Kunde im Portal die unterschriebenen
// Protokolle des alten, und die Anonymisierung traf die falsche Person.
func TestOwnerSnapshotSurvivesVehicleReassignment(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	vid := seedHandoverVehicle(t, h)
	vids := strconv.FormatInt(vid, 10)
	var pidA int64
	if err := h.Pool.QueryRow(ctx, `SELECT person_id FROM vehicles WHERE id=$1`, vid).Scan(&pidA); err != nil {
		t.Fatalf("Halter lesen: %v", err)
	}
	pidB := createIntegrationPerson(t, h)
	// Nach dem Anonymisieren heißen beide nicht mehr 'Integration' — cleanupPersons
	// greift dann nicht. Selbst wegräumen (LIFO: läuft vor cleanupPersons).
	t.Cleanup(func() {
		c := context.Background()
		_ = purgeExec(c, h.Pool, `DELETE FROM handover_protocols WHERE vehicle_id=$1`, vid)
		_ = purgeExec(c, h.Pool, `DELETE FROM vehicles WHERE id=$1`, vid)
		_ = purgeExec(c, h.Pool, `DELETE FROM persons WHERE id = ANY($1)`, []int64{pidA, pidB})
	})

	body, _ := json.Marshal(map[string]any{
		"direction": "einlagerung", "notes": "Delle hinten",
		"signer_name": "Alt Halter", "signature": tinyPNGDataURL(t),
	})
	creq := httptest.NewRequest(http.MethodPost, "/api/vehicles/"+vids+"/handovers", bytes.NewReader(body))
	creq.SetPathValue("id", vids)
	cw := httptest.NewRecorder()
	h.CreateHandover(cw, creq)
	if cw.Code != http.StatusCreated {
		t.Fatalf("Protokoll anlegen: %d %s", cw.Code, cw.Body.String())
	}
	var proto handoverMeta
	_ = json.Unmarshal(cw.Body.Bytes(), &proto)

	if rec := uploadAttachment(t, h, "/api/vehicles/"+vids+"/attachments", vids, "vertrag.pdf",
		[]byte("%PDF-1.4\n%%EOF")); rec.Code != http.StatusCreated {
		t.Fatalf("Anhang: %d %s", rec.Code, rec.Body.String())
	}

	var snapHandover, snapAttachment int64
	if err := h.Pool.QueryRow(ctx, `SELECT person_id FROM handover_protocols WHERE id=$1`, proto.ID).
		Scan(&snapHandover); err != nil {
		t.Fatalf("Snapshot lesen: %v", err)
	}
	if err := h.Pool.QueryRow(ctx, `SELECT owner_person_id FROM attachments WHERE vehicle_id=$1`, vid).
		Scan(&snapAttachment); err != nil {
		t.Fatalf("Anhang-Snapshot lesen: %v", err)
	}
	if snapHandover != pidA || snapAttachment != pidA {
		t.Fatalf("Snapshot falsch: Protokoll=%d Anhang=%d, erwartet %d", snapHandover, snapAttachment, pidA)
	}

	// 1) Der Halterwechsel über die API wird abgelehnt, sobald ein Protokoll da ist.
	var v struct {
		CategoryID    int64  `json:"category_id"`
		BillingPeriod string `json:"billing_period"`
		Status        string `json:"status"`
	}
	if err := h.Pool.QueryRow(ctx, `SELECT category_id, billing_period, status FROM vehicles WHERE id=$1`, vid).
		Scan(&v.CategoryID, &v.BillingPeriod, &v.Status); err != nil {
		t.Fatalf("Gefährt lesen: %v", err)
	}
	ubody, _ := json.Marshal(map[string]any{
		"person_id": pidB, "category_id": v.CategoryID, "billing_period": v.BillingPeriod,
		"status": v.Status, "label": "Ho-Test", "start_date": "2024-01-01",
	})
	ureq := httptest.NewRequest(http.MethodPut, "/api/vehicles/"+vids, bytes.NewReader(ubody))
	ureq.SetPathValue("id", vids)
	urec := httptest.NewRecorder()
	h.UpdateVehicle(urec, ureq)
	if urec.Code != http.StatusConflict {
		t.Fatalf("Halterwechsel trotz Übergabeprotokoll: %d %s", urec.Code, urec.Body.String())
	}

	// 2) Altbestand: ein Gefährt, das VOR dieser Sperre umgehängt wurde. Die Abfragen
	//    müssen dem Snapshot folgen, nicht dem heutigen Halter.
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET person_id=$1 WHERE id=$2`, pidB, vid); err != nil {
		t.Fatalf("Umhängen: %v", err)
	}

	portalHandovers := func(pid int64) int {
		t.Helper()
		w := getPortalSummary(t, h, createPortalLink(t, h, pid))
		if w.Code != http.StatusOK {
			t.Fatalf("Portal: %d %s", w.Code, w.Body.String())
		}
		var sum struct {
			Handovers []json.RawMessage `json:"handovers"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &sum)
		return len(sum.Handovers)
	}
	if n := portalHandovers(pidB); n != 0 {
		t.Errorf("der neue Halter sieht %d fremde Protokolle im Portal", n)
	}
	if n := portalHandovers(pidA); n != 1 {
		t.Errorf("der alte Halter sieht sein eigenes Protokoll nicht (%d)", n)
	}

	timelineHandovers := func(pid int64) int {
		t.Helper()
		ps := strconv.FormatInt(pid, 10)
		req := httptest.NewRequest(http.MethodGet, "/api/persons/"+ps+"/timeline", nil)
		req.SetPathValue("id", ps)
		w := httptest.NewRecorder()
		h.PersonTimeline(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("Zeitleiste: %d %s", w.Code, w.Body.String())
		}
		var evs []timelineEvent
		_ = json.Unmarshal(w.Body.Bytes(), &evs)
		n := 0
		for _, e := range evs {
			if e.Kind == "handover" && e.RefID == proto.ID {
				n++
			}
		}
		return n
	}
	if timelineHandovers(pidB) != 0 || timelineHandovers(pidA) != 1 {
		t.Error("die Zeitleiste ordnet das Protokoll dem heutigen statt dem damaligen Halter zu")
	}

	// 3) Anonymisieren des NEUEN Halters lässt den Beleg des alten unberührt …
	if rec := anonymize(t, h, pidB); rec.Code != http.StatusOK {
		t.Fatalf("anonymisieren B: %d %s", rec.Code, rec.Body.String())
	}
	var signer string
	var sig []byte
	var atts int
	readState := func() {
		t.Helper()
		if err := h.Pool.QueryRow(ctx, `SELECT signer_name, signature FROM handover_protocols WHERE id=$1`, proto.ID).
			Scan(&signer, &sig); err != nil {
			t.Fatalf("Protokoll lesen: %v", err)
		}
		if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE vehicle_id=$1`, vid).Scan(&atts); err != nil {
			t.Fatalf("Anhänge zählen: %v", err)
		}
	}
	readState()
	if signer != "Alt Halter" || sig == nil || atts != 1 {
		t.Errorf("Anonymisieren von B traf A's Daten: signer=%q sig=%v Anhänge=%d", signer, sig != nil, atts)
	}

	// … und das Anonymisieren des ALTEN entfernt seine Unterschrift und Unterlagen.
	if rec := anonymize(t, h, pidA); rec.Code != http.StatusOK {
		t.Fatalf("anonymisieren A: %d %s", rec.Code, rec.Body.String())
	}
	readState()
	if signer != "Anonymisiert" || sig != nil || atts != 0 {
		t.Errorf("A's Unterschrift/Unterlagen überleben die Anonymisierung: signer=%q sig=%v Anhänge=%d",
			signer, sig != nil, atts)
	}
}
