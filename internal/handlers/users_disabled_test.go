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

// mkUser legt ein Konto an und räumt es nach dem Test wieder ab.
func mkUser(t *testing.T, h *Handler, name string, admin bool) int64 {
	t.Helper()
	ctx := context.Background()
	role := "editor"
	if admin {
		role = "admin"
	}
	var id int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, role, is_admin)
		 VALUES ($1, $2, 'hash', $3, $4) RETURNING id`,
		name, name+"@example.com", role, admin).Scan(&id); err != nil {
		t.Fatalf("insert user %s: %v", name, err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id) })
	return id
}

func putUser(t *testing.T, h *Handler, id int64, req userRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPut, "/api/users/"+strconv.FormatInt(id, 10), bytes.NewReader(body))
	r.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	h.UpdateUser(rec, r)
	return rec
}

// Ein weggelassenes disabled-Feld darf ein gesperrtes Konto NICHT wieder
// freischalten. Genau das täte ein `bool` statt eines Zeigers: die
// Schnellzurücksetzung des Passworts nebenan sendet das Feld nicht mit, und ein
// Passwortwechsel würde die Sperre stillschweigend aufheben (API-31).
func TestUpdateUserOmittedDisabledKeepsTheLock(t *testing.T) {
	h := testHandler(t)
	id := mkUser(t, h, "gesperrt_bleibt", false)
	on := true
	if rec := putUser(t, h, id, userRequest{Username: "gesperrt_bleibt", Email: "gesperrt_bleibt@example.com", Role: "editor", Disabled: &on}); rec.Code != http.StatusOK {
		t.Fatalf("sperren: %d %s", rec.Code, rec.Body.String())
	}
	// Feld weggelassen — wie beim Schnell-Passwortsetzen im Frontend.
	if rec := putUser(t, h, id, userRequest{Username: "gesperrt_bleibt", Email: "gesperrt_bleibt@example.com", Role: "editor"}); rec.Code != http.StatusOK {
		t.Fatalf("update ohne disabled: %d %s", rec.Code, rec.Body.String())
	}
	var disabled bool
	if err := h.Pool.QueryRow(context.Background(), `SELECT disabled FROM users WHERE id=$1`, id).Scan(&disabled); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !disabled {
		t.Error("ein Update ohne disabled-Feld hat die Sperre aufgehoben")
	}
}

// Der Schutz vor "null Admins" muss beim Sperren genauso greifen wie beim
// Degradieren und Löschen — sonst sperrt man sich per Schalter aus, was die
// beiden anderen Wege ausdrücklich verhindern.
func TestDisablingTheLastAdminIsRefused(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	// Vorhandene Admins vorübergehend degradieren, damit genau EINER übrig bleibt.
	if _, err := h.Pool.Exec(ctx, `UPDATE users SET is_admin=false, role='editor' WHERE is_admin`); err != nil {
		t.Fatalf("clear admins: %v", err)
	}
	t.Cleanup(func() { _, _ = h.Pool.Exec(ctx, `UPDATE users SET is_admin=true, role='admin' WHERE username='admin'`) })
	id := mkUser(t, h, "letzter_admin", true)

	on := true
	rec := putUser(t, h, id, userRequest{Username: "letzter_admin", Email: "letzter_admin@example.com", Role: "admin", Disabled: &on})
	if rec.Code != http.StatusConflict {
		t.Fatalf("das Sperren des letzten Admins muss 409 liefern, war %d %s", rec.Code, rec.Body.String())
	}
	var disabled bool
	if err := h.Pool.QueryRow(ctx, `SELECT disabled FROM users WHERE id=$1`, id).Scan(&disabled); err != nil {
		t.Fatalf("query: %v", err)
	}
	if disabled {
		t.Error("der letzte Admin wurde trotz 409 gesperrt")
	}
}

// Der eigentliche Grund für die Spalte: payments.created_by (und ebenso
// invoices.created_by, payments.reversed_by, handover_protocols.created_by,
// self_service_tokens.created_by) hängen mit ON DELETE SET NULL am Konto. Ein
// gelöschter Benutzer nimmt die Urheberschaft auf Zahlungen und Rechnungen mit —
// bei Aufzeichnungen, die nach BAO §131/§132 unveränderlich sein sollen. Sperren
// hält sie fest (API-31).
//
// Der Test belegt BEIDE Hälften: das Sperren lässt den Verweis stehen, das Löschen
// nullt ihn. Nur so ist bewiesen, dass die neue Spalte einen echten Unterschied
// macht und nicht bloß eine zweite Schreibweise für dasselbe ist.
func TestDisablingKeepsRecordAuthorshipThatDeletingDestroys(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()

	var personID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name) VALUES ('API31', 'Integration') RETURNING id`).Scan(&personID); err != nil {
		t.Fatalf("insert person: %v", err)
	}
	mkPayment := func(userID int64) int64 {
		t.Helper()
		var pid int64
		if err := h.Pool.QueryRow(ctx,
			`INSERT INTO payments (person_id, amount, created_by) VALUES ($1, 10.00, $2) RETURNING id`,
			personID, userID).Scan(&pid); err != nil {
			t.Fatalf("insert payment: %v", err)
		}
		return pid
	}
	creatorOf := func(paymentID int64) *int64 {
		t.Helper()
		var c *int64
		if err := h.Pool.QueryRow(ctx, `SELECT created_by FROM payments WHERE id=$1`, paymentID).Scan(&c); err != nil {
			t.Fatalf("query payment: %v", err)
		}
		return c
	}

	// (a) Sperren — der Verweis muss bleiben.
	kept := mkUser(t, h, "spur_bleibt", false)
	keptPay := mkPayment(kept)
	on := true
	if rec := putUser(t, h, kept, userRequest{Username: "spur_bleibt", Email: "spur_bleibt@example.com", Role: "editor", Disabled: &on}); rec.Code != http.StatusOK {
		t.Fatalf("sperren: %d %s", rec.Code, rec.Body.String())
	}
	if c := creatorOf(keptPay); c == nil || *c != kept {
		t.Error("die Urheberschaft der Zahlung ging beim SPERREN verloren")
	}

	// (b) Löschen — der Verweis geht verloren. Das ist der Zustand, den die Spalte
	// vermeidbar macht; schlägt diese Hälfte fehl, ist die Begründung hinfällig.
	gone := mkUser(t, h, "spur_weg", false)
	gonePay := mkPayment(gone)
	if _, err := h.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, gone); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if c := creatorOf(gonePay); c != nil {
		t.Errorf("erwartet: das Löschen nullt payments.created_by — es steht aber noch %d", *c)
	}
}

// Das Sperren beendet laufende Sitzungen. Die Sitzungsauflösung prüft disabled
// pro Request; die Zeilen zusätzlich zu löschen ist die zweite Verteidigungslinie.
func TestDisablingDropsExistingSessions(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	id := mkUser(t, h, "sitzung_endet", false)
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO sessions (token, user_id, expires_at) VALUES ('api31-token-hash', $1, now() + interval '1 day')`, id); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	on := true
	if rec := putUser(t, h, id, userRequest{Username: "sitzung_endet", Email: "sitzung_endet@example.com", Role: "editor", Disabled: &on}); rec.Code != http.StatusOK {
		t.Fatalf("sperren: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, id).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if n != 0 {
		t.Errorf("nach dem Sperren blieben %d Sitzungen bestehen", n)
	}
}
