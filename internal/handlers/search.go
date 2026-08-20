package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Globale Suche für die Befehlspalette (⌘K).
//
// Vorher lief sie im Browser: die Palette lud /persons und /vehicles VOLLSTÄNDIG und
// filterte mit .includes() über eine reine Kleinschreibung. Das fand kein "Müller"
// bei der Eingabe "Muller", nichts außerhalb dieser zwei Objektarten — keine Garage,
// keine Halle, keinen Tarif, keine Rechnungsnummer — und nichts bei einem Tippfehler.
//
// Verglichen wird jetzt in der Datenbank, dreifach je Textfeld (ODER-verknüpft):
//
//   - Deutsches Stemming über to_tsvector('german', …). Trägt bei den Freitextfeldern,
//     also Tarif- und Dienstnamen: "Wohnwägen" findet den "Wohnwagen".
//   - Diakritika-gefalteter Teilstring über unaccent + ILIKE. Das ist der Arbeitspferd-
//     Zweig: er trägt Kennzeichen, Rechnungsnummern und jede Teileingabe, und er ist
//     der Grund, warum "Muller" das "Müller" findet.
//   - Trigramm-Ähnlichkeit für Tippfehler ("Müler" findet "Müller"), erst ab vier
//     Zeichen, damit eine kurze Eingabe wie "W1" keine losen Treffer heranzieht.
//
// Sortiert wird exakt-vor-unscharf (rank): ohne das könnte ein loser Trigramm-Treffer
// den echten unter dem Limit je Objektart verdrängen — die Zeilen kämen sonst nach
// Namen, nicht nach Relevanz.
//
// Vorbild ist Treckrrs internal/store/search.go; der Zuschnitt ist derselbe.

const (
	// searchPerKind begrenzt JE Objektart, nicht insgesamt: sonst füllen 40 Personen
	// die Liste und die eine gesuchte Halle steht nicht mehr drin.
	searchPerKind = 6
	// Unter zwei Zeichen ist jede Eingabe ein Präfix von allem — eine leere Antwort
	// ist dann ehrlicher als 200 Zeilen Rauschen.
	searchMinRunes = 2
	// Obergrenze gegen eine absurd lange Eingabe; abgeschnitten statt abgelehnt, damit
	// ein versehentlich eingefügter Absatz trotzdem etwas findet.
	searchMaxRunes = 120
	// Ab dieser Länge ist ein Trigramm-Vergleich aussagekräftig.
	searchFuzzyMinRunes = 4
)

// searchResult ist ein Treffer für die Palette. URL ist eine fertige Hash-Adresse,
// damit die Zeile ein echtes <a href> sein kann (Tastatur, mittlere Maustaste,
// Screenreader) und nicht ein div mit onclick.
type searchResult struct {
	Kind  string `json:"kind"`  // person | vehicle | invoice | garage | hall | category | service
	Label string `json:"label"` // Haupttext
	Sub   string `json:"sub"`   // Beitext
	URL   string `json:"url"`   // z. B. "#/person/12"
}

// searchCaps hält fest, welche der beiden Erweiterungen wirklich installiert sind.
// Migration 049 legt sie an, darf dabei aber scheitern (siehe dort), also wird hier
// nachgesehen statt vorausgesetzt.
type searchCaps struct{ unaccent, trigram bool }

// searchCapabilities fragt den Katalog bei JEDER Suche ab, statt das Ergebnis zu
// merken. Die Abfrage ist ein Blick in pg_extension und verschwindet neben den sieben
// Objektabfragen, die gleich folgen; dafür wirkt ein nachträglich installiertes
// unaccent sofort, ohne Neustart. Bei einem Fehler gilt "nicht vorhanden": lieber
// unschärfer suchen als gar nicht.
func (h *Handler) searchCapabilities(ctx context.Context) searchCaps {
	var c searchCaps
	if err := h.Pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'unaccent'),
		       EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')`).
		Scan(&c.unaccent, &c.trigram); err != nil {
		slog.Warn("search: could not read extension catalog", "err", err)
		return searchCaps{}
	}
	return c
}

// fold legt unaccent() um einen SQL-Ausdruck, wenn die Erweiterung da ist.
// $1::text / $2::text müssen dabei gecastet sein: unaccent ist überladen (text vs.
// regdictionary), ein ungecasteter Platzhalter ist ein mehrdeutiger Parametertyp (42P18).
func (c searchCaps) fold(expr string) string {
	if !c.unaccent {
		return expr
	}
	return "unaccent(" + expr + ")"
}

// where baut das Prädikat über den SQL-Textausdruck expr. expr ist vertrauenswürdig
// (eine Konstante aus dieser Datei, nie Benutzereingabe), es zusammenzusetzen ist also
// unbedenklich; die Eingabe selbst hängt an $1/$2.
//
//	$1 = rohe Eingabe   → tsquery (Stemming) und Trigramm-Ähnlichkeit
//	$2 = '%eingabe%'    → gefalteter Teilstring
func (c searchCaps) where(expr string, fuzzy bool) string {
	w := "to_tsvector('german', " + c.fold(expr) + ") @@ websearch_to_tsquery('german', " + c.fold("$1::text") + ")" +
		" OR " + c.fold(expr) + " ILIKE " + c.fold("$2::text") + ` ESCAPE '\'`
	if fuzzy && c.trigram {
		w += " OR word_similarity(" + c.fold("$1::text") + ", " + c.fold(expr) + ") >= 0.4"
	}
	return "(" + w + ")"
}

// rank ist der ORDER-BY-Vorspann: erst der Teilstring-Treffer, dann nach
// Trigramm-Nähe. Steht vor dem jeweils eigenen Tiebreak der Objektart.
func (c searchCaps) rank(expr string) string {
	r := "(" + c.fold(expr) + " ILIKE " + c.fold("$2::text") + ` ESCAPE '\') DESC`
	if c.trigram {
		r += ", word_similarity(" + c.fold("$1::text") + ", " + c.fold(expr) + ") DESC"
	}
	return r
}

// likeEscape entschärft ILIKE-Platzhalter in der Eingabe, damit "50%" wörtlich
// gesucht wird und nicht als Muster.
func likeEscape(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// searchSub führt eine Objektabfrage aus ($1 = Eingabe, $2 = ILIKE-Muster,
// $3 = Limit) und hängt je Zeile ein Ergebnis an. Bündelt Abfrage, Scan und Schließen,
// damit jede Objektart ein paar Zeilen bleibt und rows auch bei einem Scan-Fehler
// geschlossen wird.
func (h *Handler) searchSub(ctx context.Context, out *[]searchResult, query, q, pat string,
	mk func(pgx.Rows) (searchResult, error)) error {
	rows, err := h.Pool.Query(ctx, query, q, pat, searchPerKind)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		res, err := mk(rows)
		if err != nil {
			return err
		}
		*out = append(*out, res)
	}
	return rows.Err()
}

// dotJoin fügt die nicht-leeren Teile mit " · " zusammen — der Beitext soll keine
// führenden oder doppelten Trennzeichen zeigen, wenn ein Feld leer ist.
func dotJoin(parts ...string) string {
	keep := parts[:0]
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			keep = append(keep, s)
		}
	}
	return strings.Join(keep, " · ")
}

// Search beantwortet GET /api/search?q= für die Befehlspalette.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(q) < searchMinRunes {
		writeJSON(w, http.StatusOK, []searchResult{})
		return
	}
	if utf8.RuneCountInString(q) > searchMaxRunes {
		q = string([]rune(q)[:searchMaxRunes])
	}
	res, err := h.searchWith(r.Context(), q, h.searchCapabilities(r.Context()))
	if err != nil {
		slog.Error("search failed", "err", err)
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// searchWith durchsucht die gepflegten Stammdaten plus die Rechnungsnummern. Buchungen
// (Zusatzkosten, Zahlungen) bleiben absichtlich draußen: die Palette ist zum Springen
// da, nicht zum Auswerten — dafür haben die Listenseiten ihre eigene Suche.
//
// caps kommt von außen, damit ein Test BEIDE Wege wirklich ausführen kann. Sonst wäre
// der Rückfallpfad ohne Erweiterungen zwar dokumentiert, aber nie gelaufen — und
// könnte schlicht kaputte SQL erzeugen, ohne dass es jemandem auffällt.
func (h *Handler) searchWith(ctx context.Context, q string, caps searchCaps) ([]searchResult, error) {
	pat := likeEscape(q)
	fuzzy := utf8.RuneCountInString(q) >= searchFuzzyMinRunes
	out := make([]searchResult, 0, searchPerKind*7)

	// Personen. Gesucht wird auch in Adresse und Notiz ("wer wohnt am Hauptplatz?"),
	// angezeigt werden nur E-Mail und Telefon: ein Notizschnipsel in der Palette
	// könnte eine private Bemerkung auf den Schirm holen, die dort nichts zu suchen hat.
	// Anonymisierte Personen (DSGVO Art. 17) stehen hinten — die Zeile existiert nur
	// noch, damit die Buchhaltung vollständig bleibt.
	pExpr := "concat_ws(' ', p.first_name, p.last_name, p.email, p.phone, p.address, p.notes)"
	if err := h.searchSub(ctx, &out, `
		SELECT p.id, p.first_name, p.last_name, p.email, p.phone, p.anonymized
		  FROM persons p
		 WHERE `+caps.where(pExpr, fuzzy)+`
		 ORDER BY p.anonymized, `+caps.rank(pExpr)+`, p.last_name, p.first_name
		 LIMIT $3`, q, pat, func(rows pgx.Rows) (searchResult, error) {
		var id int64
		var first, last, email, phone string
		var anon bool
		if err := rows.Scan(&id, &first, &last, &email, &phone, &anon); err != nil {
			return searchResult{}, err
		}
		name := strings.TrimSpace(first + " " + last)
		if name == "" {
			name = "(ohne Namen)"
		}
		sub := dotJoin(email, phone)
		if anon {
			sub = "anonymisiert"
		}
		return searchResult{Kind: "person", Label: name, Sub: sub,
			URL: "#/person/" + strconv.FormatInt(id, 10)}, nil
	}); err != nil {
		return nil, err
	}

	// Gefährte. Der Halter und der Tarifname zählen mit, damit "Müller Wohnwagen"
	// dessen Gefährt findet, ohne dass jemand das Kennzeichen im Kopf hat.
	// Archivierte stehen hinten, wie ausgetragene Traktoren bei Treckrr.
	vExpr := "concat_ws(' ', v.label, v.license_plate, c.name, p.first_name, p.last_name)"
	if err := h.searchSub(ctx, &out, `
		SELECT v.id, v.label, v.license_plate, c.name,
		       concat_ws(' ', p.first_name, p.last_name), v.archived
		  FROM vehicles v
		  JOIN persons p ON p.id = v.person_id
		  JOIN categories c ON c.id = v.category_id
		 WHERE `+caps.where(vExpr, fuzzy)+`
		 ORDER BY v.archived, `+caps.rank(vExpr)+`, v.label, v.license_plate
		 LIMIT $3`, q, pat, func(rows pgx.Rows) (searchResult, error) {
		var id int64
		var label, plate, cat, owner string
		var archived bool
		if err := rows.Scan(&id, &label, &plate, &cat, &owner, &archived); err != nil {
			return searchResult{}, err
		}
		title := label
		if title == "" {
			title = plate
		}
		if title == "" {
			title = cat
		}
		sub := dotJoin(plate, owner, cat)
		if archived {
			sub = dotJoin("archiviert", sub)
		}
		return searchResult{Kind: "vehicle", Label: title, Sub: sub,
			URL: "#/vehicle/" + strconv.FormatInt(id, 10)}, nil
	}); err != nil {
		return nil, err
	}

	// Rechnungen über die Nummer. Dasselbe Prädikat der Gleichförmigkeit halber (und
	// damit $1 auch hier gebunden ist); die eigentliche Arbeit macht der Teilstring.
	if err := h.searchSub(ctx, &out, `
		SELECT iv.id, iv.number, concat_ws(' ', p.first_name, p.last_name), iv.issued_on
		  FROM invoices iv
		  JOIN persons p ON p.id = iv.person_id
		 WHERE `+caps.where("iv.number", fuzzy)+`
		 ORDER BY `+caps.rank("iv.number")+`, iv.issued_on DESC
		 LIMIT $3`, q, pat, func(rows pgx.Rows) (searchResult, error) {
		var id int64
		var number, owner string
		var issued time.Time
		if err := rows.Scan(&id, &number, &owner, &issued); err != nil {
			return searchResult{}, err
		}
		return searchResult{Kind: "invoice", Label: "Rechnung " + number,
			Sub: dotJoin(owner, issued.Format("02.01.2006")),
			URL: "#/invoices/" + strconv.FormatInt(id, 10)}, nil
	}); err != nil {
		return nil, err
	}

	// Garagen.
	if err := h.searchSub(ctx, &out, `
		SELECT id, name FROM garages
		 WHERE `+caps.where("name", fuzzy)+`
		 ORDER BY `+caps.rank("name")+`, sort_order, name
		 LIMIT $3`, q, pat, func(rows pgx.Rows) (searchResult, error) {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return searchResult{}, err
		}
		return searchResult{Kind: "garage", Label: name, Sub: "Garage",
			URL: "#/garage/" + strconv.FormatInt(id, 10)}, nil
	}); err != nil {
		return nil, err
	}

	// Hallen. Der Garagenname zählt mit, weil Hallennamen sich wiederholen ("Halle 1"
	// gibt es in jeder Garage) und erst die Garage sie unterscheidbar macht.
	hExpr := "concat_ws(' ', h.name, g.name)"
	if err := h.searchSub(ctx, &out, `
		SELECT h.id, h.name, g.name FROM halls h
		  JOIN garages g ON g.id = h.garage_id
		 WHERE `+caps.where(hExpr, fuzzy)+`
		 ORDER BY `+caps.rank(hExpr)+`, g.name, h.sort_order, h.name
		 LIMIT $3`, q, pat, func(rows pgx.Rows) (searchResult, error) {
		var id int64
		var name, garage string
		if err := rows.Scan(&id, &name, &garage); err != nil {
			return searchResult{}, err
		}
		return searchResult{Kind: "hall", Label: name, Sub: dotJoin("Halle", garage),
			URL: "#/hall/" + strconv.FormatInt(id, 10)}, nil
	}); err != nil {
		return nil, err
	}

	// Tarife und Dienste führen beide auf dieselbe Seite (dort zwei Reiter), tragen
	// aber verschiedene Kennungen, damit die Palette sie auseinanderhält.
	if err := h.searchSub(ctx, &out, `
		SELECT name FROM categories
		 WHERE `+caps.where("name", fuzzy)+`
		 ORDER BY `+caps.rank("name")+`, name
		 LIMIT $3`, q, pat, func(rows pgx.Rows) (searchResult, error) {
		var name string
		if err := rows.Scan(&name); err != nil {
			return searchResult{}, err
		}
		return searchResult{Kind: "category", Label: name, Sub: "Tarif", URL: "#/tariffs"}, nil
	}); err != nil {
		return nil, err
	}

	if err := h.searchSub(ctx, &out, `
		SELECT name FROM service_types
		 WHERE `+caps.where("name", fuzzy)+`
		 ORDER BY `+caps.rank("name")+`, name
		 LIMIT $3`, q, pat, func(rows pgx.Rows) (searchResult, error) {
		var name string
		if err := rows.Scan(&name); err != nil {
			return searchResult{}, err
		}
		return searchResult{Kind: "service", Label: name, Sub: "Dienst", URL: "#/tariffs"}, nil
	}); err != nil {
		return nil, err
	}

	return out, nil
}
