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

func portalRequestReq(t *testing.T, h *Handler, token string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/portal/requests", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.PortalCreateRequest(rec, req)
	return rec
}

// Der Portal-Briefkasten (Hundert 85/87): der Kunde hinterlegt einen Wunsch,
// der Betreiber übernimmt ihn — und erst DIE Übernahme ändert Stammdaten.
func TestPortalContactUpdateRequestLifecycle(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid := createIntegrationPerson(t, h)
	token := createPortalLink(t, h, pid)

	// Kunde reicht neue Kontaktdaten ein.
	rec := portalRequestReq(t, h, token, map[string]any{
		"kind": "contact_update", "email": "neu@example.com", "phone": "+43 660 1234567",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// Die Stammdaten sind NOCH unverändert — der Briefkasten ist kein Stift.
	var email string
	if err := h.Pool.QueryRow(ctx, `SELECT email FROM persons WHERE id=$1`, pid).Scan(&email); err != nil {
		t.Fatalf("read person: %v", err)
	}
	if email == "neu@example.com" {
		t.Fatal("der Wunsch hat die Stammdaten direkt verändert")
	}

	// Betreiber sieht den Wunsch in der Liste, offene zuerst.
	lrec := httptest.NewRecorder()
	h.ListPortalRequests(lrec, httptest.NewRequest(http.MethodGet, "/api/portal-requests", nil))
	if lrec.Code != http.StatusOK {
		t.Fatalf("list: %d", lrec.Code)
	}
	if !bytes.Contains(lrec.Body.Bytes(), []byte("neu@example.com")) {
		t.Error("der Wunsch fehlt in der Betreiberliste")
	}

	// Übernahme: JETZT ändern sich die Stammdaten, mit Vorher/Nachher im Protokoll.
	resolve := func(id int64, action string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"action": action})
		req := httptest.NewRequest(http.MethodPost, "/api/portal-requests/"+strconv.FormatInt(id, 10)+"/resolve", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", strconv.FormatInt(id, 10))
		rec := httptest.NewRecorder()
		h.ResolvePortalRequest(rec, req)
		return rec
	}
	if rec := resolve(created.ID, "apply"); rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body.String())
	}
	var newEmail, phone string
	if err := h.Pool.QueryRow(ctx, `SELECT email, phone FROM persons WHERE id=$1`, pid).Scan(&newEmail, &phone); err != nil {
		t.Fatalf("read person: %v", err)
	}
	if newEmail != "neu@example.com" || phone != "+43 660 1234567" {
		t.Errorf("Übernahme kam nicht an: email=%q phone=%q", newEmail, phone)
	}
	// Ein zweites Erledigen desselben Wunschs ist ein 409, kein stilles Doppel.
	if rec := resolve(created.ID, "apply"); rec.Code != http.StatusConflict {
		t.Errorf("zweites apply: %d, erwartet 409", rec.Code)
	}
}

// Abholtermin: gültig nur mit Datum ab heute; und der Briefkasten hat einen
// Deckel gegen Spam über den Bearer-Link.
func TestPortalPickupRequestValidationAndCap(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	token := createPortalLink(t, h, pid)

	if rec := portalRequestReq(t, h, token, map[string]any{"kind": "pickup", "date": "gestern"}); rec.Code != http.StatusBadRequest {
		t.Errorf("kaputtes Datum: %d, erwartet 400", rec.Code)
	}
	past := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	if rec := portalRequestReq(t, h, token, map[string]any{"kind": "pickup", "date": past}); rec.Code != http.StatusBadRequest {
		t.Errorf("Vergangenheit: %d, erwartet 400", rec.Code)
	}
	future := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
	for i := 0; i < maxOpenPortalRequests; i++ {
		if rec := portalRequestReq(t, h, token, map[string]any{"kind": "pickup", "date": future, "note": "Wunsch " + strconv.Itoa(i)}); rec.Code != http.StatusCreated {
			t.Fatalf("Wunsch %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	// Der sechste offene Wunsch prallt ab.
	if rec := portalRequestReq(t, h, token, map[string]any{"kind": "pickup", "date": future}); rec.Code != http.StatusTooManyRequests {
		t.Errorf("über dem Deckel: %d, erwartet 429", rec.Code)
	}
	// Unbekannte Art ebenso.
	if rec := portalRequestReq(t, h, token, map[string]any{"kind": "sabotage"}); rec.Code != http.StatusBadRequest {
		t.Errorf("unbekannte Art: %d, erwartet 400", rec.Code)
	}
}
