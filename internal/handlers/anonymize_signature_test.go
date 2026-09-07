package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// mkHandover legt ein Gefährt mit einem unterschriebenen Übergabeprotokoll an und
// gibt Personen- und Protokoll-ID zurück.
func mkSignedHandover(t *testing.T, h *Handler) (personID, protoID int64) {
	t.Helper()
	ctx := context.Background()
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name) VALUES ('Unterschrift','Integration') RETURNING id`).Scan(&personID); err != nil {
		t.Fatalf("insert person: %v", err)
	}
	var catID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO categories (name) VALUES ('SigTarif-'||clock_timestamp()::text) RETURNING id`).Scan(&catID); err != nil {
		t.Fatalf("insert category: %v", err)
	}
	var vehID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO vehicles (person_id, category_id, status, label) VALUES ($1,$2,'stored','SigAuto') RETURNING id`,
		personID, catID).Scan(&vehID); err != nil {
		t.Fatalf("insert vehicle: %v", err)
	}
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO handover_protocols (vehicle_id, direction, notes, signer_name, signature)
		 VALUES ($1,'einlagerung','Kratzer links vorne','Erika Mustermann',$2) RETURNING id`,
		vehID, []byte("PNG-Unterschrift")).Scan(&protoID); err != nil {
		t.Fatalf("insert handover: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_ = purgeExec(c, h.Pool, `DELETE FROM handover_protocols WHERE id=$1`, protoID)
		_ = purgeExec(c, h.Pool, `DELETE FROM vehicles WHERE id=$1`, vehID)
		_, _ = h.Pool.Exec(c, `DELETE FROM categories WHERE id=$1`, catID)
		_ = purgeExec(c, h.Pool, `DELETE FROM persons WHERE id=$1`, personID)
	})
	return personID, protoID
}

// Die DSGVO-Löschung muss bis zur Unterschrift reichen — sie ist der
// personenbezogenste Datenpunkt der Anwendung und blieb bisher stehen (Hundert 39).
// Der Beleg selbst muss dabei ein Beleg BLEIBEN: Richtung, Datum und Zustandsnotizen
// sind der Nachweis über die Sache und aufbewahrungspflichtig.
func TestAnonymizeClearsHandoverSignatureButKeepsTheRecord(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, proto := mkSignedHandover(t, h)

	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/anonymize", nil)
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	rec := httptest.NewRecorder()
	h.AnonymizePerson(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymize: %d %s", rec.Code, rec.Body.String())
	}

	var signer, notes, direction string
	var sig []byte
	if err := h.Pool.QueryRow(ctx,
		`SELECT signer_name, signature, notes, direction FROM handover_protocols WHERE id=$1`, proto).
		Scan(&signer, &sig, &notes, &direction); err != nil {
		t.Fatalf("read handover: %v", err)
	}
	if signer != "Anonymisiert" {
		t.Errorf("der Name des Unterzeichners steht noch da: %q", signer)
	}
	if sig != nil {
		t.Errorf("die gezeichnete Unterschrift ist noch gespeichert (%d Bytes)", len(sig))
	}
	if notes != "Kratzer links vorne" {
		t.Errorf("die Zustandsnotiz gehört zur Sache und muss bleiben, war %q", notes)
	}
	if direction != "einlagerung" {
		t.Errorf("die Richtung des Belegs wurde verändert: %q", direction)
	}
}

// Der Schalter aus Migration 056 ist ABSICHTLICH enger als parkrr.purge: er gibt
// nur signer_name und signature frei. Wer ihn setzt und dann etwas anderes ändert,
// muss weiterhin am Unveränderlichkeits-Trigger scheitern — sonst wäre er ein
// Generalschlüssel mit anderem Namen.
func TestAnonymizeSwitchDoesNotUnlockTheWholeHandover(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	_, proto := mkSignedHandover(t, h)

	tx, err := h.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL parkrr.anonymize = 'on'`); err != nil {
		t.Fatalf("set switch: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE handover_protocols SET notes='nachträglich verbogen' WHERE id=$1`, proto); err == nil {
		t.Error("der Anonymisierungs-Schalter hat auch die Zustandsnotiz freigegeben — er ist zu breit")
	}
}

// Ohne den Schalter bleibt das Protokoll unveränderlich, auch für die Unterschrift.
// Das ist die Regel aus Migration 051, die 056 nicht aufweichen darf.
func TestHandoverStaysImmutableWithoutTheSwitch(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	_, proto := mkSignedHandover(t, h)
	if _, err := h.Pool.Exec(ctx,
		`UPDATE handover_protocols SET signer_name='Jemand anderer' WHERE id=$1`, proto); err == nil {
		t.Error("ohne Schalter ließ sich die Unterschrift ändern")
	}
}
