package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/database"
	"github.com/preining/parkrr/internal/models"
)

func testHandler(t *testing.T) *Handler {
	t.Helper()
	url := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set; skipping handler integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	t.Cleanup(func() { cleanupPersons(t, pool) })
	return New(pool)
}

func cleanupPersons(t *testing.T, pool *pgxpool.Pool) {
	ctx := context.Background()
	// Protected handovers now require explicit deletion before their parents.
	if err := purgeExec(ctx, pool, `DELETE FROM handover_protocols WHERE vehicle_id IN
		(SELECT id FROM vehicles WHERE person_id IN (SELECT id FROM persons WHERE last_name='Integration'))`); err != nil {
		t.Logf("cleanup handovers: %v", err)
	}
	// Records of account are immutable (migration 034); teardown uses the purge
	// escape hatch. Invoices are ON DELETE RESTRICT (immutable), so drop them
	// before their persons; payments cascade with the person.
	if err := purgeExec(ctx, pool,
		`DELETE FROM invoices WHERE person_id IN (SELECT id FROM persons WHERE last_name = 'Integration')`); err != nil {
		t.Logf("cleanup invoices: %v", err)
	}
	if err := purgeExec(ctx, pool,
		`DELETE FROM persons WHERE last_name = 'Integration'`); err != nil {
		t.Logf("cleanup: %v", err)
	}
	// Test-Tarife abräumen (Hundert-Nacharbeit): etliche Tests taufen Kategorien
	// "<Name>-<UnixNano>" und ließen sie liegen — über hunderte Läufe wuchs die
	// geteilte Tarif-Ansicht auf 400+ Karten, bis die a11y-Prüfung in ihr
	// Zeitlimit lief (schon einmal passiert, siehe VehTarif-Fix). EIN Aufräumer
	// hier statt zwanzig einzelner t.Cleanup: gelöscht wird nur, was den
	// 15+-stelligen Zeitstempel-Suffix trägt UND von keinem Gefährt mehr
	// referenziert wird — die laufenden Tests anderer Pakete bleiben unberührt.
	if _, err := pool.Exec(ctx,
		`DELETE FROM categories c
		  WHERE c.name ~ '-[0-9]{15,}$'
		    AND NOT EXISTS (SELECT 1 FROM vehicles v WHERE v.category_id = c.id)`); err != nil {
		t.Logf("cleanup categories: %v", err)
	}
}

// purgeExec runs a teardown delete that the immutability triggers (migration 034)
// would otherwise block, inside a transaction that sets the sanctioned escape
// hatch (parkrr.purge). SET LOCAL scopes it to this tx, so cascaded deletes of
// payments/invoice_items are permitted too. Test-only — the app never sets it.
func purgeExec(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL parkrr.purge = 'on'`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func TestCreateAndListPerson(t *testing.T) {
	h := testHandler(t)

	body, _ := json.Marshal(map[string]string{
		"first_name": "Test", "last_name": "Integration",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/persons", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.CreatePerson(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}
	var created models.Person
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected a non-zero id")
	}

	// List and confirm the new person is present.
	listReq := httptest.NewRequest(http.MethodGet, "/api/persons?limit=1000", nil)
	listRec := httptest.NewRecorder()
	h.ListPersons(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: status %d", listRec.Code)
	}
	var persons []models.Person
	if err := json.Unmarshal(listRec.Body.Bytes(), &persons); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, p := range persons {
		if p.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("created person not returned by ListPersons")
	}
}

func TestCreatePersonValidation(t *testing.T) {
	h := testHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/persons", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	h.CreatePerson(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty person should be rejected, got %d", rec.Code)
	}
}
