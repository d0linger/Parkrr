package server

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/preining/parkrr/internal/database"
)

// WICHTIG: die Testpersonen heissen NICHT 'Integration'. Das Paket internal/handlers
// raeumt in seinem Teardown `DELETE FROM persons WHERE last_name = 'Integration'` ab,
// und `go test ./...` laesst beide Pakete GLEICHZEITIG auf dieselbe Datenbank los —
// die Person verschwand dann zwischen INSERT und Token-Anlage (FK-Verletzung).
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("PARKRR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PARKRR_TEST_DATABASE_URL not set; skipping expiry-cleanup integration test")
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
	return pool
}

// Die Nebentabellen wurden bisher nur BEILÄUFIG aufgeräumt: beim Anlegen des
// nächsten Portal-Links, beim Start der nächsten Passkey-Zeremonie. Wer das nicht
// mehr tut, räumt auch nicht mehr auf — die abgelaufenen Zeilen bleiben für immer
// liegen (Hundert 32). Der Test belegt, dass der Sweep genau das Abgelaufene
// entfernt und das Gültige stehen lässt.
func TestExpirySweepsRemoveOnlyExpiredRows(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var personID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name) VALUES ('Sweep','ServerSweep') RETURNING id`).Scan(&personID); err != nil {
		t.Fatalf("insert person: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM self_service_tokens WHERE person_id=$1`, personID)
		_, _ = pool.Exec(c, `DELETE FROM persons WHERE id=$1`, personID)
	})

	// Ein frischer und ein lange abgelaufener Portal-Link.
	var liveTok, deadTok int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO self_service_tokens (token_hash, person_id, expires_at)
		 VALUES ('sweep-live', $1, now() + interval '10 days') RETURNING id`, personID).Scan(&liveTok); err != nil {
		t.Fatalf("insert live token: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO self_service_tokens (token_hash, person_id, expires_at)
		 VALUES ('sweep-dead', $1, now() - interval '90 days') RETURNING id`, personID).Scan(&deadTok); err != nil {
		t.Fatalf("insert expired token: %v", err)
	}
	// Eine abgelaufene Passkey-Zeremonie.
	if _, err := pool.Exec(ctx,
		`INSERT INTO webauthn_ceremonies (id, data, expires_at)
		 VALUES ('sweep-ceremony', '{}', now() - interval '1 hour')
		 ON CONFLICT (id) DO UPDATE SET expires_at = EXCLUDED.expires_at`); err != nil {
		t.Fatalf("insert ceremony: %v", err)
	}

	for _, s := range expirySweeps {
		if _, err := pool.Exec(ctx, s.sql); err != nil {
			t.Fatalf("sweep %s: %v", s.table, err)
		}
	}

	var liveLeft, deadLeft, ceremonyLeft int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM self_service_tokens WHERE id=$1`, liveTok).Scan(&liveLeft); err != nil {
		t.Fatalf("count live: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM self_service_tokens WHERE id=$1`, deadTok).Scan(&deadLeft); err != nil {
		t.Fatalf("count dead: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM webauthn_ceremonies WHERE id='sweep-ceremony'`).Scan(&ceremonyLeft); err != nil {
		t.Fatalf("count ceremony: %v", err)
	}
	if liveLeft != 1 {
		t.Error("der GÜLTIGE Portal-Link wurde mitentfernt")
	}
	if deadLeft != 0 {
		t.Error("der lange abgelaufene Portal-Link blieb liegen")
	}
	if ceremonyLeft != 0 {
		t.Error("die abgelaufene Passkey-Zeremonie blieb liegen")
	}
}

// Ein gerade erst abgelaufener Link soll in der Verwaltung noch als "abgelaufen"
// sichtbar bleiben, statt spurlos zu verschwinden — deshalb die 30 Tage Nachlauf.
func TestExpirySweepKeepsRecentlyExpiredPortalLinks(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var personID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name) VALUES ('Nachlauf','ServerSweep') RETURNING id`).Scan(&personID); err != nil {
		t.Fatalf("insert person: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM self_service_tokens WHERE person_id=$1`, personID)
		_, _ = pool.Exec(c, `DELETE FROM persons WHERE id=$1`, personID)
	})
	var tok int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO self_service_tokens (token_hash, person_id, expires_at)
		 VALUES ('sweep-recent', $1, now() - interval '2 days') RETURNING id`, personID).Scan(&tok); err != nil {
		t.Fatalf("insert token: %v", err)
	}
	for _, s := range expirySweeps {
		if _, err := pool.Exec(ctx, s.sql); err != nil {
			t.Fatalf("sweep %s: %v", s.table, err)
		}
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM self_service_tokens WHERE id=$1`, tok).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 1 {
		t.Error("ein erst vor zwei Tagen abgelaufener Link wurde bereits entfernt — der Nachlauf fehlt")
	}
}
