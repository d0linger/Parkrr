package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Die Suche ist erst dann eine Verbesserung, wenn sie GENAU die Eingaben verzeiht,
// wegen derer sie gebaut wurde. Diese Tests führen jede der drei Toleranzen an ihrem
// eigenen Fall vor — nicht am Gutfall, den auch die alte Teilstring-Suche fand.

// --- ohne Datenbank: der Prädikatbau -----------------------------------------

// Ohne die Erweiterungen darf kein unaccent()/word_similarity() in der SQL stehen —
// sonst wirft die Datenbank 42883 und die Suche fällt komplett aus, statt nur
// unschärfer zu sein.
func TestPraedikatOhneErweiterungenNenntSieNicht(t *testing.T) {
	plain := searchCaps{}
	w := plain.where("name", true)
	for _, verboten := range []string{"unaccent", "word_similarity"} {
		if strings.Contains(w, verboten) {
			t.Errorf("ohne Erweiterung darf %q nicht in der Bedingung stehen:\n%s", verboten, w)
		}
	}
	if !strings.Contains(w, "ILIKE") {
		t.Errorf("der Teilstring-Zweig muss immer da sein, sonst findet die Suche gar nichts:\n%s", w)
	}
	if r := plain.rank("name"); strings.Contains(r, "word_similarity") {
		t.Errorf("ohne pg_trgm darf die Sortierung keine Ähnlichkeit berechnen:\n%s", r)
	}

	// Und mit: beide Erweiterungen müssen tatsächlich benutzt werden.
	full := searchCaps{unaccent: true, trigram: true}
	w = full.where("name", true)
	for _, noetig := range []string{"unaccent(name)", "word_similarity", "to_tsvector('german'"} {
		if !strings.Contains(w, noetig) {
			t.Errorf("%q fehlt in der vollen Bedingung:\n%s", noetig, w)
		}
	}
	// Kurze Eingabe: kein Trigramm-Zweig, sonst zieht "W1" lose Treffer heran.
	if strings.Contains(full.where("name", false), "word_similarity") {
		t.Error("unterhalb der Längenschwelle darf kein Trigramm-Vergleich in der Bedingung stehen")
	}
}

// --- mit Datenbank ------------------------------------------------------------

// seedSearchData legt einen kleinen, aber vollständigen Bestand an: eine Person mit
// Umlaut, ihr Gefährt, eine Rechnung, Garage, Halle, Tarif und Dienst. Der Suffix
// hält die Namen über Läufe hinweg eindeutig (invoices.number ist UNIQUE und
// Rechnungen werden nicht aufgeräumt).
type searchSeed struct {
	suffix                string
	personID, vehicleID   int64
	garageID, hallID      int64
	invoiceNo, categoryNm string
	serviceNm, garageNm   string
	hallNm                string
}

func seedSearchData(t *testing.T, h *Handler) searchSeed {
	t.Helper()
	ctx := context.Background()
	s := searchSeed{suffix: strconv.FormatInt(time.Now().UnixNano(), 10)}
	s.categoryNm = "Wohnwagen " + s.suffix
	s.serviceNm = "Stromanschluss " + s.suffix
	s.garageNm = "Nordgarage " + s.suffix
	s.hallNm = "Große Halle " + s.suffix
	s.invoiceNo = "SUCHE-" + s.suffix

	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO persons (first_name, last_name, email, address)
		 VALUES ('Jörg', $1, 'joerg@example.at', 'Hauptplatz 1') RETURNING id`,
		"Müller"+s.suffix).Scan(&s.personID); err != nil {
		t.Fatalf("Person anlegen: %v", err)
	}
	var catID int64
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO categories (name, default_monthly_cost) VALUES ($1, 50) RETURNING id`,
		s.categoryNm).Scan(&catID); err != nil {
		t.Fatalf("Tarif anlegen: %v", err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO service_types (name, default_amount) VALUES ($1, 10)`, s.serviceNm); err != nil {
		t.Fatalf("Dienst anlegen: %v", err)
	}
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO vehicles (person_id, category_id, label, license_plate)
		 VALUES ($1, $2, 'Hobby Prestige', $3) RETURNING id`,
		s.personID, catID, "W-"+s.suffix[len(s.suffix)-5:]).Scan(&s.vehicleID); err != nil {
		t.Fatalf("Gefährt anlegen: %v", err)
	}
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO garages (name) VALUES ($1) RETURNING id`, s.garageNm).Scan(&s.garageID); err != nil {
		t.Fatalf("Garage anlegen: %v", err)
	}
	if err := h.Pool.QueryRow(ctx,
		`INSERT INTO halls (garage_id, name) VALUES ($1, $2) RETURNING id`,
		s.garageID, s.hallNm).Scan(&s.hallID); err != nil {
		t.Fatalf("Halle anlegen: %v", err)
	}
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO invoices (number, person_id, issued_on, subtotal, ust_rate, tax_amount, total,
		        kleinunternehmer, seller_snapshot, buyer_snapshot)
		 VALUES ($1, $2, now(), 100, 0, 0, 100, true, '{}', '{}')`, s.invoiceNo, s.personID); err != nil {
		t.Fatalf("Rechnung anlegen: %v", err)
	}
	// Alles selbst wegräumen. cleanupPersons greift NUR bei last_name = 'Integration'
	// — die hier gesäten Namen tragen einen Zeitstempel, fielen also durch und blieben
	// je Testlauf als sieben Personen, Gefährte, Tarife und Rechnungen liegen. Und
	// Rechnungen sind ON DELETE RESTRICT plus unveränderlich: einmal liegengeblieben,
	// wären diese Personen ohne die Purge-Hintertür nie wieder zu entfernen.
	// Reihenfolge: Kinder vor Eltern.
	t.Cleanup(func() {
		c := context.Background()
		_, _ = h.Pool.Exec(c, `DELETE FROM halls WHERE id=$1`, s.hallID)
		_, _ = h.Pool.Exec(c, `DELETE FROM garages WHERE id=$1`, s.garageID)
		_, _ = h.Pool.Exec(c, `DELETE FROM service_types WHERE name=$1`, s.serviceNm)
		if err := purgeExec(c, h.Pool, `DELETE FROM invoices WHERE person_id=$1`, s.personID); err != nil {
			t.Logf("Aufräumen Rechnung: %v", err)
		}
		// Gefährte hängen per CASCADE an der Person; der Tarif erst danach.
		if err := purgeExec(c, h.Pool, `DELETE FROM persons WHERE id=$1`, s.personID); err != nil {
			t.Logf("Aufräumen Person: %v", err)
		}
		_, _ = h.Pool.Exec(c, `DELETE FROM categories WHERE id=$1`, catID)
	})
	return s
}

// find sucht in den Treffern nach einer Art mit einem Label, das teil enthält.
func find(res []searchResult, kind, teil string) *searchResult {
	for i := range res {
		if res[i].Kind == kind && strings.Contains(res[i].Label, teil) {
			return &res[i]
		}
	}
	return nil
}

func runSearch(t *testing.T, h *Handler, q string) []searchResult {
	t.Helper()
	res, err := h.searchWith(context.Background(), q, h.searchCapabilities(context.Background()))
	if err != nil {
		t.Fatalf("Suche %q: %v", q, err)
	}
	return res
}

// Der Grund für die ganze Übung: ein Name mit Umlaut, eingegeben ohne.
func TestSucheFindetUmlautNamenOhneUmlaut(t *testing.T) {
	h := testHandler(t)
	if !h.searchCapabilities(context.Background()).unaccent {
		t.Skip("unaccent nicht installiert — die Faltung ist hier nicht prüfbar")
	}
	s := seedSearchData(t, h)

	if find(runSearch(t, h, "Muller"+s.suffix), "person", "Müller") == nil {
		t.Error(`"Muller" muss das "Müller" finden — genau dafür ist die Faltung da`)
	}
	// Und die Gegenrichtung: mit Umlaut getippt, ohne gespeichert wäre derselbe Fall.
	if find(runSearch(t, h, "Müller"+s.suffix), "person", "Müller") == nil {
		t.Error(`die Eingabe MIT Umlaut muss weiterhin finden`)
	}
	// Gegenprobe: OHNE die Faltung darf derselbe Aufruf NICHT finden. Sonst könnte der
	// Test aus einem ganz anderen Grund grün sein und würde eine kaputte Faltung nicht
	// bemerken — ILIKE allein ist nicht diakritika-blind.
	ohne, err := h.searchWith(context.Background(), "Muller"+s.suffix, searchCaps{})
	if err != nil {
		t.Fatalf("Suche ohne Erweiterungen: %v", err)
	}
	if find(ohne, "person", "Müller") != nil {
		t.Error("ohne unaccent dürfte \"Muller\" das \"Müller\" nicht finden — der Test misst " +
			"dann nicht die Faltung, sondern irgendetwas anderes")
	}
}

// Tippfehlertoleranz, der zweite Grund. Ein ausgelassener Buchstabe im Namen.
func TestSucheVerzeihtEinenVertipper(t *testing.T) {
	h := testHandler(t)
	if !h.searchCapabilities(context.Background()).trigram {
		t.Skip("pg_trgm nicht installiert — die Tippfehlertoleranz ist hier nicht prüfbar")
	}
	s := seedSearchData(t, h)
	// "Nordgarge" statt "Nordgarage": ein fehlender Buchstabe, kein Teilstring mehr.
	res := runSearch(t, h, "Nordgarge")
	if find(res, "garage", s.garageNm) == nil {
		t.Errorf("ein ausgelassener Buchstabe muss noch finden, bekam %d Treffer: %v", len(res), kinds(res))
	}
}

// Die Abdeckung ist der Teil, den man täglich merkt: vorher kannte die Palette nur
// Personen und Gefährte.
func TestSucheDecktAlleSiebenObjektartenAb(t *testing.T) {
	h := testHandler(t)
	s := seedSearchData(t, h)

	for _, tc := range []struct{ q, kind, teil string }{
		{"Müller" + s.suffix, "person", "Müller"},
		{"Hobby Prestige", "vehicle", "Hobby"},
		{s.invoiceNo, "invoice", s.invoiceNo},
		{s.garageNm, "garage", s.garageNm},
		{s.hallNm, "hall", s.hallNm},
		{s.categoryNm, "category", s.categoryNm},
		{s.serviceNm, "service", s.serviceNm},
	} {
		res := runSearch(t, h, tc.q)
		if find(res, tc.kind, tc.teil) == nil {
			t.Errorf("%q findet kein %s (bekam: %v)", tc.q, tc.kind, kinds(res))
		}
	}
}

// Jeder Treffer muss auch irgendwo hinführen — ein Eintrag ohne Ziel ist eine
// Sackgasse, und genau das war die Palette vorher: sie sprang auf die LISTE statt auf
// den Datensatz, weil routes.persons die id gar nicht auswertet.
func TestJederTrefferFuehrtAufSeineEigeneSeite(t *testing.T) {
	h := testHandler(t)
	s := seedSearchData(t, h)
	res := runSearch(t, h, "Müller"+s.suffix)
	hit := find(res, "person", "Müller")
	if hit == nil {
		t.Fatal("Person nicht gefunden")
	}
	want := "#/person/" + strconv.FormatInt(s.personID, 10)
	if hit.URL != want {
		t.Errorf("URL = %q, want %q (der Plural führt auf die Liste, nicht auf die Person)", hit.URL, want)
	}
	for _, r := range res {
		if !strings.HasPrefix(r.URL, "#/") {
			t.Errorf("%s %q hat kein Sprungziel: %q", r.Kind, r.Label, r.URL)
		}
	}
}

// Der Rückfallpfad. Migration 049 darf scheitern, ohne den Start zu verhindern — dann
// muss die SQL OHNE die Erweiterungen trotzdem laufen. Ohne diesen Test wäre das eine
// Zusage im Kommentar, die nie jemand ausgeführt hat.
func TestSucheLaeuftAuchOhneErweiterungen(t *testing.T) {
	h := testHandler(t)
	s := seedSearchData(t, h)

	// Mehrere durch Leerzeichen getrennte Wörter: der Pfad durch websearch_to_tsquery,
	// bei dem eine falsch gebaute Rückfall-SQL am ehesten auffiele. Vorher wurde das
	// Ergebnis hier weggeworfen und der Aufruf prüfte nur "kein Fehler".
	res, err := h.searchWith(context.Background(), "Nordgarage "+s.suffix, searchCaps{})
	if err != nil {
		t.Fatalf("ohne Erweiterungen muss die Suche laufen, nicht scheitern: %v", err)
	}
	if find(res, "garage", s.garageNm) == nil {
		t.Errorf("mehrteilige Eingabe fand die Garage nicht: %v", kinds(res))
	}
	// Der Teilstring trägt weiterhin — nur eben ungefaltet.
	plain, err := h.searchWith(context.Background(), s.garageNm, searchCaps{})
	if err != nil {
		t.Fatalf("Teilstring-Suche ohne Erweiterungen: %v", err)
	}
	if find(plain, "garage", s.garageNm) == nil {
		t.Error("auch ohne Erweiterungen muss ein exakter Teilstring gefunden werden")
	}
}

// Der HTTP-Rand: zu kurze Eingaben kosten nichts und liefern eine leere Liste statt
// des halben Bestands.
func TestZuKurzeEingabeLiefertNichts(t *testing.T) {
	h := testHandler(t)
	seedSearchData(t, h)
	for _, q := range []string{"", " ", "a", "  x "} {
		w := httptest.NewRecorder()
		// Kodieren, sonst zerlegt schon httptest.NewRequest die Zeile am Leerzeichen.
		h.Search(w, httptest.NewRequest(http.MethodGet, "/api/search?q="+url.QueryEscape(q), nil))
		if w.Code != http.StatusOK {
			t.Fatalf("q=%q: Status %d, want 200", q, w.Code)
		}
		var res []searchResult
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("q=%q: Antwort nicht lesbar: %v", q, err)
		}
		if len(res) != 0 {
			t.Errorf("q=%q liefert %d Treffer, erwartet keine", q, len(res))
		}
	}
}

// Ein Platzhalter in der Eingabe darf kein Muster werden: "%" würde sonst ALLES
// finden und die Palette mit dem gesamten Bestand fluten.
func TestPlatzhalterInDerEingabeFindetNichtAlles(t *testing.T) {
	h := testHandler(t)
	seedSearchData(t, h)
	res := runSearch(t, h, "%%%%")
	if len(res) != 0 {
		t.Errorf("eine Eingabe aus lauter Platzhaltern lieferte %d Treffer: %v", len(res), kinds(res))
	}
	// Der Fall, den die alte Fassung durchließ: unaccent faltet nicht nur Diakritika,
	// sondern auch Vollbreiten-Zeichen — unaccent('％') ist '%'. Das Muster wurde in Go
	// entschärft und ERST DANACH in SQL gefaltet, aus dem harmlosen Zeichen wurde also
	// wieder ein echter Platzhalter und jede ILIKE-Bedingung traf jede Zeile. Der Test
	// oben prüfte nur ASCII und war deshalb grün.
	for _, q := range []string{"％％", "＿＿", "％a", "a＿"} {
		if res := runSearch(t, h, q); len(res) != 0 {
			t.Errorf("Vollbreiten-Platzhalter %q lieferte %d Treffer — das Muster wird gefaltet, "+
				"muss also auch NACH dem Falten entschärft werden: %v", q, len(res), kinds(res))
		}
	}
	// Und die Gegenprobe: ein echter Prozentsatz im Namen bleibt auffindbar.
	if _, err := h.Pool.Exec(context.Background(),
		`INSERT INTO categories (name, default_monthly_cost) VALUES ($1, 5)`, "Rabatt 50% Sonder"); err != nil {
		t.Fatalf("Tarif anlegen: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM categories WHERE name=$1`, "Rabatt 50% Sonder")
	})
	if find(runSearch(t, h, "50%"), "category", "Rabatt 50%") == nil {
		t.Error(`"50%" muss den Tarif "Rabatt 50% Sonder" wörtlich finden`)
	}
}

func kinds(res []searchResult) []string {
	out := make([]string, 0, len(res))
	for _, r := range res {
		out = append(out, r.Kind+":"+r.Label)
	}
	return out
}
