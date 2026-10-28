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

func duplicateCall(t *testing.T, hf http.HandlerFunc, path string, params url.Values) (int, []searchResult) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path+"?"+params.Encode(), nil)
	w := httptest.NewRecorder()
	hf(w, r)
	var out []searchResult
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func duplicateURLs(res []searchResult) []string {
	out := make([]string, 0, len(res))
	for _, r := range res {
		out = append(out, r.URL+" "+r.Sub)
	}
	return out
}

// Der Doppeleintrag-Hinweis meldet nur Gleiches: Name (auch vertauscht), E-Mail,
// Telefonnummer über die Ziffern. „Wolfgang Atzmüller“ ist kein Doppel von
// „Günter Kitzmüller“, auch wenn sich die Namen ähneln.
func TestPersonDuplicates(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	digits := tag[len(tag)-8:]
	insert := func(first, last, email, phone string, anonymized bool) int64 {
		t.Helper()
		var id int64
		if err := h.Pool.QueryRow(ctx, `INSERT INTO persons (first_name, last_name, email, phone, anonymized)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, first, last, email, phone, anonymized).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM persons WHERE id = $1`, id) })
		return id
	}
	last := "Kitzmüller" + tag
	edited := insert("Günter", last, "", "", false)
	insert("Wolfgang", "Atzmüller"+tag, "", "", false)
	ask := func(v url.Values) []string {
		t.Helper()
		code, res := duplicateCall(t, h.PersonDuplicates, "/api/persons/duplicates", v)
		if code != http.StatusOK {
			t.Fatalf("%v: %d", v, code)
		}
		return duplicateURLs(res)
	}
	byName := url.Values{"first_name": {"Günter"}, "last_name": {last}, "exclude": {strconv.FormatInt(edited, 10)}}
	if got := ask(byName); len(got) != 0 {
		t.Fatalf("ähnlicher, aber anderer Name oder die Person selbst gemeldet: %v", got)
	}

	same := insert("günter", "kitzmüller"+tag, "", "", false)
	swapped := insert("Kitzmüller"+tag, "Günter", "", "", false)
	got := ask(byName)
	want := []string{"#/person/" + strconv.FormatInt(same, 10) + " gleicher Name", "#/person/" + strconv.FormatInt(swapped, 10) + " gleicher Name"}
	if strings.Join(got, "|") != strings.Join(want, "|") && strings.Join(got, "|") != want[1]+"|"+want[0] {
		t.Fatalf("gleicher Name: %v, want %v", got, want)
	}
	byName.Set("exclude", strconv.FormatInt(same, 10))
	for _, g := range ask(byName) {
		if strings.HasPrefix(g, "#/person/"+strconv.FormatInt(same, 10)+" ") {
			t.Fatal("exclude schließt die bearbeitete Person nicht aus")
		}
	}

	mail := "gk." + tag + "@example.org"
	byMail := insert("Anna", "Andere"+tag, "GK."+tag+"@Example.org", "", false)
	insert("", "", mail, "", true) // anonymisiert: zählt nicht
	if got := ask(url.Values{"email": {mail}}); len(got) != 1 || !strings.HasPrefix(got[0], "#/person/"+strconv.FormatInt(byMail, 10)+" gleiche E-Mail") {
		t.Fatalf("gleiche E-Mail: %v", got)
	}

	byPhone := insert("Paul", "Phone"+tag, "", "+43 664 "+digits, false)
	if got := ask(url.Values{"phone": {"0664/" + digits}}); len(got) != 1 || !strings.HasPrefix(got[0], "#/person/"+strconv.FormatInt(byPhone, 10)+" gleiche Telefonnummer") {
		t.Fatalf("gleiche Telefonnummer in anderer Schreibweise: %v", got)
	}
	if got := ask(url.Values{"phone": {"12345"}}); len(got) != 0 {
		t.Fatalf("zu kurze Nummer vergleicht nicht: %v", got)
	}
	if got := ask(url.Values{}); len(got) != 0 {
		t.Fatalf("ohne Angaben: %v", got)
	}
	for _, v := range []url.Values{{"exclude": {"abc"}}, {"first_name": {strings.Repeat("x", 500)}}} {
		if code, _ := duplicateCall(t, h.PersonDuplicates, "/api/persons/duplicates", v); code != http.StatusBadRequest {
			t.Errorf("%v: %d, want 400", v, code)
		}
	}
}

// Gleiches Kennzeichen heißt gleiche Buchstaben und Ziffern; ein ähnliches ist keins.
func TestVehicleDuplicates(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	nr := tag[len(tag)-6:]
	_, edited, _ := seasonFixture(t, h, "")
	_, same, _ := seasonFixture(t, h, "")
	_, similar, _ := seasonFixture(t, h, "")
	for id, plate := range map[int64]string{edited: "WT-" + nr + "X", same: "wt " + nr + "x", similar: "WT-" + nr + "Y"} {
		if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET license_plate = $2 WHERE id = $1`, id, plate); err != nil {
			t.Fatal(err)
		}
	}
	code, res := duplicateCall(t, h.VehicleDuplicates, "/api/vehicles/duplicates",
		url.Values{"license_plate": {"WT-" + nr + "X"}, "exclude": {strconv.FormatInt(edited, 10)}})
	got := duplicateURLs(res)
	if code != http.StatusOK || len(got) != 1 || !strings.HasPrefix(got[0], "#/vehicle/"+strconv.FormatInt(same, 10)+" ") {
		t.Fatalf("gleiches Kennzeichen anders geschrieben, ohne das bearbeitete und ohne das ähnliche: %d %v", code, got)
	}
	if code, res := duplicateCall(t, h.VehicleDuplicates, "/api/vehicles/duplicates", url.Values{"license_plate": {"-"}}); code != http.StatusOK || len(res) != 0 {
		t.Fatalf("leeres Kennzeichen: %d %v", code, res)
	}
}
