package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Eine bestehende Sitzung muss SOFORT wertlos werden, wenn das Konto gesperrt
// wird — nicht erst mit ihrem Ablauf. Die Sitzung wird pro Request aus der
// Datenbank aufgelöst, also gehört die Prüfung genau dorthin (API-31).
func TestDisabledAccountLosesItsLiveSession(t *testing.T) {
	m, pool := testAuthManager(t)
	id := mkAuthUser(t, pool, "editor", false)
	cookies := sessionCookies(t, m, id)

	reached := false
	h := m.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, withCookies(httptest.NewRequest(http.MethodGet, "/api/x", nil), cookies))
	if !reached {
		t.Fatalf("die Sitzung sollte vor dem Sperren tragen, Status %d", rec.Code)
	}

	if _, err := pool.Exec(context.Background(), `UPDATE users SET disabled=true WHERE id=$1`, id); err != nil {
		t.Fatalf("disable: %v", err)
	}
	reached = false
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, withCookies(httptest.NewRequest(http.MethodGet, "/api/x", nil), cookies))
	if reached {
		t.Error("ein gesperrtes Konto kam mit seiner alten Sitzung durch")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Status %d, erwartet 401", rec.Code)
	}
}

// Ein gesperrtes Konto darf sich auch mit RICHTIGEM Passwort nicht anmelden —
// und die Ablehnung muss ununterscheidbar von falschen Zugangsdaten sein, sonst
// verrät sie, welche Konten existieren.
func TestDisabledAccountCannotAuthenticate(t *testing.T) {
	m, pool := testAuthManager(t)
	id := mkAuthUser(t, pool, "editor", false)
	const pw = "korrekt-pferd-batterie"
	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var uname string
	if err := pool.QueryRow(context.Background(),
		`UPDATE users SET password_hash=$1 WHERE id=$2 RETURNING username`, hash, id).Scan(&uname); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if _, err := m.Authenticate(context.Background(), uname, pw); err != nil {
		t.Fatalf("das aktive Konto sollte sich anmelden können: %v", err)
	}

	if _, err := pool.Exec(context.Background(), `UPDATE users SET disabled=true WHERE id=$1`, id); err != nil {
		t.Fatalf("disable: %v", err)
	}
	_, derr := m.Authenticate(context.Background(), uname, pw)
	if derr == nil {
		t.Fatal("ein gesperrtes Konto hat sich mit richtigem Passwort angemeldet")
	}
	// Dieselbe Meldung wie bei falschen Zugangsdaten: die Antwort darf nicht
	// verraten, dass das Konto existiert und nur gesperrt ist.
	_, werr := m.Authenticate(context.Background(), uname, "falsches-passwort-hier")
	if werr == nil {
		t.Fatal("ein falsches Passwort wurde akzeptiert")
	}
	if derr.Error() != werr.Error() {
		t.Errorf("die Ablehnung verrät den Kontostatus: gesperrt %q vs. falsches Passwort %q", derr, werr)
	}
	if strings.Contains(strings.ToLower(derr.Error()), "disabled") {
		t.Errorf("die Fehlermeldung nennt den Sperrgrund: %q", derr)
	}
}
