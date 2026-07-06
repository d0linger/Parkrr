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
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM persons WHERE last_name = 'Integration'`); err != nil {
		t.Logf("cleanup: %v", err)
	}
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
