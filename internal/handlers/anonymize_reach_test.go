package handlers

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// Die Löschung muss JEDE Tabelle erreichen, in der Personenbezug liegt. Mit dem
// Verbesserungsprogramm sind vier dazugekommen, und sie blieben zunächst außen
// vor — drei davon hängen an keinem Fremdschlüssel der Person, überleben also
// auch ein DELETE.
//
// Der Test sät in alle vier und prüft nach dem Anonymisieren, dass nichts
// Personenbezogenes stehen bleibt. Ohne die Erweiterung fällt er an der ersten
// Tabelle.
func TestAnonymisierenErreichtDieNeuenNebentabellen(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	id := seedPerson(t, h)

	// Ein Gefährt, damit die am Gefährt hängenden Anhänge und die Platzgeschichte
	// einen Bezug haben.
	// Zeitstempel-Suffix: so greift der gemeinsame Aufräumer in cleanupPersons, der
	// verwaiste Testkategorien einsammelt. Ein fester Name bliebe hängen, weil das
	// Gefährt beim Aufräumen der Kategorie noch am Fremdschlüssel steht.
	var catID, vehID int64
	catName := "AnonReach-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO categories (name, default_monthly_cost, default_yearly_cost)
		 VALUES ($1, 10, 100) RETURNING id`, catName).
		Scan(&catID); err != nil {
		t.Fatalf("Kategorie: %v", err)
	}
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO vehicles (person_id, category_id, license_plate, status)
		 VALUES ($1, $2, 'W-99999X', 'stored') RETURNING id`, id, catID).Scan(&vehID); err != nil {
		t.Fatalf("Gefährt: %v", err)
	}

	// 1) Portal-Wunsch mit der gewünschten neuen Adresse im Klartext.
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO portal_requests (person_id, kind, payload)
		 VALUES ($1, 'contact_update', '{"address":"Geheimgasse 7","email":"neu@example.at"}'::jsonb)`,
		id); err != nil {
		t.Fatalf("Portal-Wunsch: %v", err)
	}
	// 2) Anhang am Gefährt (der Typenschein-Scan).
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO attachments (vehicle_id, filename, content_type, byte_size, data)
		 VALUES ($1, 'typenschein.pdf', 'application/pdf', 4, '\x25504446'::bytea)`, vehID); err != nil {
		t.Fatalf("Anhang: %v", err)
	}
	// 3) Platzgeschichte mit Kennzeichen und Personenbezug.
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO spot_occupancy_history (spot_id, spot_label, vehicle_id, vehicle_label, person_id)
		 VALUES (0, 'A1', $1, 'W-99999X', $2)`, vehID, id); err != nil {
		t.Fatalf("Platzgeschichte: %v", err)
	}
	// 4) Versandprotokoll mit der E-Mail-Adresse.
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO mail_log (recipients, subject, ok) VALUES ('hermann@example.at', 'Zahlungserinnerung', true)`); err != nil {
		t.Fatalf("Versandprotokoll: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(ctx, `DELETE FROM mail_log WHERE subject='Zahlungserinnerung'`)
		_, _ = h.Pool.Exec(ctx, `DELETE FROM spot_occupancy_history WHERE vehicle_id=$1`, vehID)
	})

	if rec := anonymize(t, h, id); rec.Code != 200 {
		t.Fatalf("anonymisieren: %d %s", rec.Code, rec.Body.String())
	}

	var payloadLeft int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM portal_requests WHERE person_id=$1 AND payload <> '{}'::jsonb`, id).
		Scan(&payloadLeft); err != nil {
		t.Fatalf("Abfrage Portal-Wunsch: %v", err)
	}
	if payloadLeft != 0 {
		t.Error("der Portal-Wunsch trägt die gewünschte Adresse weiterhin im Klartext")
	}

	var attLeft int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM attachments
		  WHERE person_id=$1 OR vehicle_id IN (SELECT id FROM vehicles WHERE person_id=$1)`, id).
		Scan(&attLeft); err != nil {
		t.Fatalf("Abfrage Anhänge: %v", err)
	}
	if attLeft != 0 {
		t.Error("die hochgeladenen Unterlagen der Person liegen nach der Löschung noch da")
	}

	var plate string
	var stillLinked *int64
	if err := h.Pool.QueryRow(ctx,
		`SELECT vehicle_label, person_id FROM spot_occupancy_history WHERE vehicle_id=$1`, vehID).
		Scan(&plate, &stillLinked); err != nil {
		t.Fatalf("Abfrage Platzgeschichte: %v", err)
	}
	if plate == "W-99999X" || stillLinked != nil {
		t.Errorf("die Platzgeschichte zeigt weiter auf die Person: label=%q person_id=%v", plate, stillLinked)
	}

	var mailLeft int
	if err := h.Pool.QueryRow(ctx,
		`SELECT count(*) FROM mail_log WHERE recipients ILIKE '%hermann@example.at%'`).Scan(&mailLeft); err != nil {
		t.Fatalf("Abfrage Versandprotokoll: %v", err)
	}
	if mailLeft != 0 {
		t.Error("das Versandprotokoll nennt die gelöschte E-Mail-Adresse weiterhin")
	}
}
