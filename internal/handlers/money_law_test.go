package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Ausbau 32: Kautionskonto je Person. Erhalten ist kein Erlös; einbehalten wird zur
// Zahlung über pay-invoices; mehr als offen geht nicht; ein Einbehalt verschwindet
// nur mit seiner Zahlung.
func TestDeposits(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	pid, _, _ := seasonFixture(t, h, "")
	h.Now = func() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) }
	withAddress(t, h, pid)
	p := strconv.FormatInt(pid, 10)
	book := func(body string) *httptest.ResponseRecorder {
		return callJSONRaw(h, h.CreateDeposit, http.MethodPost, "/api/persons/"+p+"/deposits", p, body)
	}
	if w := book(`{"kind":"erhalten","amount":200,"method":"bar"}`); w.Code != http.StatusCreated {
		t.Fatalf("erhalten: %d %s", w.Code, w.Body.String())
	}
	var payments int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE person_id = $1`, pid).Scan(&payments); err != nil || payments != 0 {
		t.Fatalf("eine erhaltene Kaution ist keine Zahlung: %d %v", payments, err)
	}
	if w := book(`{"kind":"zurueck","amount":250}`); w.Code != http.StatusConflict {
		t.Fatalf("mehr zurück als offen: %d, want 409", w.Code)
	}
	if w := book(`{"kind":"einbehalten","amount":50}`); w.Code != http.StatusBadRequest {
		t.Fatalf("einbehalten ohne Grund: %d, want 400", w.Code)
	}
	iv := mustInvoice(t, h, pid)
	w := book(`{"kind":"einbehalten","amount":50,"note":"Delle am Heck"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("einbehalten: %d %s", w.Code, w.Body.String())
	}
	var res struct {
		ID        int64  `json:"id"`
		PaymentID *int64 `json:"payment_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.PaymentID == nil {
		t.Fatal("einbehalten muss eine Zahlung buchen")
	}
	var amount, paid float64
	var note string
	if err := h.Pool.QueryRow(ctx, `SELECT amount, note FROM payments WHERE id = $1`, *res.PaymentID).Scan(&amount, &note); err != nil {
		t.Fatal(err)
	}
	if err := h.Pool.QueryRow(ctx, `SELECT paid_amount FROM invoices WHERE id = $1`, iv.ID).Scan(&paid); err != nil {
		t.Fatal(err)
	}
	if amount != 50 || !strings.Contains(note, "Delle am Heck") || paid != 50 {
		t.Errorf("Einbehalt als Zahlung: %v %q, Rechnung bezahlt %v", amount, note, paid)
	}
	list := callJSON(h, h.ListDeposits, http.MethodGet, "/api/persons/x/deposits", p, nil)
	if !strings.Contains(list.Body.String(), `"balance":150`) {
		t.Fatalf("Saldo nach Einbehalt: %s", list.Body.String())
	}
	if c := callJSON(h, h.DeleteDeposit, http.MethodDelete, "/api/deposits/x", strconv.FormatInt(res.ID, 10), nil).Code; c != http.StatusConflict {
		t.Fatalf("Einbehalt löschen ohne Zahlungsstorno: %d, want 409", c)
	}
	var received int64
	if err := h.Pool.QueryRow(ctx, `SELECT id FROM deposits WHERE person_id = $1 AND kind = 'erhalten'`, pid).Scan(&received); err != nil {
		t.Fatal(err)
	}
	if c := callJSON(h, h.DeleteDeposit, http.MethodDelete, "/api/deposits/x", strconv.FormatInt(received, 10), nil).Code; c != http.StatusConflict {
		t.Fatalf("Eingang löschen, aus dem schon einbehalten wurde: %d, want 409", c)
	}
	if w := book(`{"kind":"zurueck","amount":150,"method":"ueberweisung"}`); w.Code != http.StatusCreated {
		t.Fatalf("Rest zurück: %d %s", w.Code, w.Body.String())
	}
	list = callJSON(h, h.ListDeposits, http.MethodGet, "/api/persons/x/deposits", p, nil)
	if !strings.Contains(list.Body.String(), `"balance":0`) {
		t.Fatalf("Saldo am Ende: %s", list.Body.String())
	}
}

// withAddress: Rechnungen über 400 € brauchen die Anschrift (§ 11 UStG).
func withAddress(t *testing.T, h *Handler, pid int64) {
	t.Helper()
	compliantSeller(t, h)
	if _, err := h.Pool.Exec(context.Background(), `UPDATE persons SET address = 'Hauptstraße 1, 1010 Wien' WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
}

const camtSample = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.08">
 <BkToCstmrStmt><Stmt><Acct><Id><IBAN>AT611904300234573201</IBAN></Id></Acct>
  <Ntry><Amt Ccy="EUR">{{A1}}</Amt><CdtDbtInd>CRDT</CdtDbtInd><Sts><Cd>BOOK</Cd></Sts><BookgDt><Dt>2026-10-10</Dt></BookgDt>
   <AcctSvcrRef>REF-1-{{TAG}}</AcctSvcrRef>
   <NtryDtls><TxDtls><RltdPties><Dbtr><Pty><Nm>Irgendwer</Nm></Pty></Dbtr></RltdPties><RmtInf><Ustrd>Rechnung {{NR}} danke</Ustrd></RmtInf></TxDtls></NtryDtls></Ntry>
  <Ntry><Amt Ccy="EUR">{{A2}}</Amt><CdtDbtInd>CRDT</CdtDbtInd><Sts>BOOK</Sts><BookgDt><Dt>2026-10-11</Dt></BookgDt>
   <AcctSvcrRef>REF-2-{{TAG}}</AcctSvcrRef>
   <NtryDtls><TxDtls><RltdPties><Dbtr><Nm>Bea Integration</Nm></Dbtr></RltdPties><RmtInf><Ustrd>Miete Oktober</Ustrd></RmtInf></TxDtls></NtryDtls></Ntry>
  <Ntry><Amt Ccy="EUR">99.00</Amt><CdtDbtInd>DBIT</CdtDbtInd><Sts>BOOK</Sts><BookgDt><Dt>2026-10-11</Dt></BookgDt></Ntry>
  <Ntry><Amt Ccy="EUR">12.00</Amt><CdtDbtInd>CRDT</CdtDbtInd><Sts>PDNG</Sts><BookgDt><Dt>2026-10-12</Dt></BookgDt></Ntry>
 </Stmt></BkToCstmrStmt>
</Document>`

func uploadCamt(t *testing.T, h *Handler, xmlText string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "auszug.xml")
	_, _ = fw.Write([]byte(xmlText))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/bank-import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ImportBankStatement(w, req)
	return w
}

// Ausbau 27: nur gebuchte Gutschriften, kein doppelter Import, Vorschlag über die
// Rechnungsnummer oder Betrag und Name, gebucht wird erst beim Zuordnen.
func TestBankStatementImport(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	h.Now = func() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) }
	pa, _, _ := seasonFixture(t, h, "")
	pb, vb, _ := seasonFixture(t, h, "")
	if _, err := h.Pool.Exec(ctx, `UPDATE vehicles SET rate = 85 WHERE id = $1`, vb); err != nil {
		t.Fatal(err)
	}
	withAddress(t, h, pa)
	withAddress(t, h, pb)
	ia, ib := mustInvoice(t, h, pa), mustInvoice(t, h, pb)
	tag := strconv.FormatInt(time.Now().UnixNano(), 10)
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM bank_transactions WHERE ref LIKE '%'||$1`, tag)
	})
	doc := strings.NewReplacer("{{A1}}", strconv.FormatFloat(ia.Total, 'f', 2, 64), "{{A2}}", strconv.FormatFloat(ib.Total, 'f', 2, 64),
		"{{NR}}", ia.Number, "{{TAG}}", tag).Replace(camtSample)

	w := uploadCamt(t, h, doc)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"imported":2`) || !strings.Contains(w.Body.String(), `"skipped":2`) {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	if w := uploadCamt(t, h, doc); !strings.Contains(w.Body.String(), `"duplicates":2`) {
		t.Fatalf("zweiter Import desselben Auszugs: %s", w.Body.String())
	}
	if w := uploadCamt(t, h, "<Document>kaputt"); w.Code != http.StatusBadRequest {
		t.Fatalf("kaputte Datei: %d, want 400", w.Code)
	}
	var payments int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE person_id IN ($1, $2)`, pa, pb).Scan(&payments); err != nil || payments != 0 {
		t.Fatalf("der Import selbst bucht nichts: %d %v", payments, err)
	}

	list := httptest.NewRecorder()
	h.ListBankTransactions(list, httptest.NewRequest(http.MethodGet, "/api/bank-transactions", nil))
	var out struct {
		Items []bankTxOut `json:"items"`
	}
	_ = json.Unmarshal(list.Body.Bytes(), &out)
	byRemit := map[string]bankTxOut{}
	for _, it := range out.Items {
		byRemit[it.Remittance] = it
	}
	t1, t2 := byRemit["Rechnung "+ia.Number+" danke"], byRemit["Miete Oktober"]
	if t1.Suggestion == nil || t1.Suggestion.ID != ia.ID || !t1.Suggestion.Strong {
		t.Fatalf("Vorschlag über die Rechnungsnummer: %+v", t1.Suggestion)
	}
	if t2.Suggestion == nil || t2.Suggestion.ID != ib.ID || t2.Suggestion.Strong {
		t.Fatalf("Vorschlag über Betrag und Name: %+v", t2.Suggestion)
	}

	assign := callJSONRaw(h, h.AssignBankTransaction, http.MethodPost, "/api/bank-transactions/x/assign", strconv.FormatInt(t1.ID, 10),
		`{"invoice_id":`+strconv.FormatInt(ia.ID, 10)+`}`)
	if assign.Code != http.StatusOK {
		t.Fatalf("zuordnen: %d %s", assign.Code, assign.Body.String())
	}
	var paid float64
	if err := h.Pool.QueryRow(ctx, `SELECT paid_amount FROM invoices WHERE id = $1`, ia.ID).Scan(&paid); err != nil || paid != ia.Total {
		t.Fatalf("Rechnung nach dem Zuordnen bezahlt: %v %v", paid, err)
	}
	if again := callJSONRaw(h, h.AssignBankTransaction, http.MethodPost, "/api/bank-transactions/x/assign", strconv.FormatInt(t1.ID, 10),
		`{"invoice_id":`+strconv.FormatInt(ia.ID, 10)+`}`); again.Code != http.StatusConflict {
		t.Fatalf("zweimal zuordnen: %d, want 409", again.Code)
	}
	id2 := strconv.FormatInt(t2.ID, 10)
	if c := callJSONRaw(h, h.SetBankTransactionIgnored, http.MethodPost, "/api/bank-transactions/x/ignore", id2, `{"ignored":true}`).Code; c != http.StatusOK {
		t.Fatalf("ignorieren: %d", c)
	}
	if c := callJSONRaw(h, h.SetBankTransactionIgnored, http.MethodPost, "/api/bank-transactions/x/ignore", id2, `{"ignored":false}`).Code; c != http.StatusOK {
		t.Fatalf("wieder öffnen: %d", c)
	}
}

func TestContainsNumberRespectsDigitBoundaries(t *testing.T) {
	cases := map[string]bool{"RECHNUNG2026-0001DANKE": true, "2026-00012": false, "X2026-0001": true, "12026-0001": false}
	for s, want := range cases {
		if got := containsNumber(s, "2026-0001"); got != want {
			t.Errorf("containsNumber(%q) = %v, want %v", s, got, want)
		}
	}
}

// Ausbau 30: versionierte Vorlage, Platzhalter je Gefährt, Annahme im Portal nur für
// die gelesene Fassung und die eigenen Gefährte; das PDF wird Anhang der Person.
func TestContractTemplateAndPortalAcceptance(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM contract_acceptances`)
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM contract_templates`)
	})
	pid, vid, _ := seasonFixture(t, h, "2027-03-31")
	_, otherVehicle, _ := seasonFixture(t, h, "")
	save := func(body string) contractTemplate {
		t.Helper()
		w := callJSONRaw(h, h.SaveContractTemplate, http.MethodPost, "/api/contract-template", "", body)
		if w.Code != http.StatusCreated {
			t.Fatalf("save: %d %s", w.Code, w.Body.String())
		}
		var c contractTemplate
		_ = json.Unmarshal(w.Body.Bytes(), &c)
		return c
	}
	v1 := save(`{"title":"Einstellvertrag","body":"Zwischen {{betreiber}} und {{name}} über {{kennzeichen}} ab {{beginn}} bis {{ende}}."}`)
	if f, _, err := h.contractFields(ctx, vid); err != nil || !strings.Contains(fillContract("{{ende}} {{ kennzeichen }}", f), "31.03.2027 S-") {
		t.Fatalf("Platzhalter: %v %v", f, err)
	}
	pdf := callJSON(h, h.VehicleContractPDF, http.MethodGet, "/api/vehicles/x/contract.pdf", strconv.FormatInt(vid, 10), nil)
	if pdf.Code != http.StatusOK || !bytes.HasPrefix(pdf.Body.Bytes(), []byte("%PDF")) {
		t.Fatalf("Vertrags-PDF: %d", pdf.Code)
	}

	token := createPortalLink(t, h, pid)
	accept := func(vehicle int64, version int) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]any{"vehicle_id": vehicle, "version": version})
		req := httptest.NewRequest(http.MethodPost, "/api/portal/contract/accept", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.PortalAcceptContract(w, req)
		return w
	}
	v2 := save(`{"title":"Einstellvertrag","body":"Neue Fassung für {{name}}."}`)
	if v2.Version != v1.Version+1 {
		t.Fatalf("neue Version: %d nach %d", v2.Version, v1.Version)
	}
	if c := accept(vid, v1.Version).Code; c != http.StatusConflict {
		t.Fatalf("alte Fassung annehmen: %d, want 409", c)
	}
	if c := accept(otherVehicle, v2.Version).Code; c != http.StatusNotFound {
		t.Fatalf("fremdes Gefährt: %d, want 404", c)
	}
	if w := accept(vid, v2.Version); w.Code != http.StatusCreated {
		t.Fatalf("annehmen: %d %s", w.Code, w.Body.String())
	}
	if c := accept(vid, v2.Version).Code; c != http.StatusConflict {
		t.Fatalf("zweimal annehmen: %d, want 409", c)
	}
	var att int
	if err := h.Pool.QueryRow(ctx, `SELECT count(*) FROM attachments WHERE person_id = $1 AND content_type = 'application/pdf' AND filename LIKE 'Einstellvertrag-v%'`, pid).Scan(&att); err != nil || att != 1 {
		t.Fatalf("PDF als Anhang der Person: %d %v", att, err)
	}
	sum := getPortalSummary(t, h, token)
	if !strings.Contains(sum.Body.String(), `"contract_accepted":`+strconv.Itoa(v2.Version)) || !strings.Contains(sum.Body.String(), `"contract":{"version":`+strconv.Itoa(v2.Version)) {
		t.Fatalf("Portal kennt Fassung und Annahme: %s", sum.Body.String())
	}
	list := callJSON(h, h.PersonContracts, http.MethodGet, "/api/persons/x/contracts", strconv.FormatInt(pid, 10), nil)
	if !strings.Contains(list.Body.String(), `"via":"portal"`) {
		t.Fatalf("Annahmen der Person: %s", list.Body.String())
	}
}

// Ausbau 29: Vorschau und Anpassen rechnen gleich; der neue Satz gilt ab der nächsten
// Periode, abgeschlossene Perioden und die schon gestellte Rechnung bleiben unberührt;
// die Rechnung danach rechnet den neuen Satz; zurücknehmen geht nur vor Beginn.
func TestIndexationAppliesFromNextPeriod(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	t.Cleanup(func() { _, _ = h.Pool.Exec(context.Background(), `DELETE FROM cpi_values`) })
	h.Now = func() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) }
	pid, vid, catID := seasonFixture(t, h, "")
	withAddress(t, h, pid)
	c := strconv.FormatInt(catID, 10)
	// Das Gefährt beginnt 11/2025: der Preis wurde zum damaligen Index vereinbart, also
	// ist 11/2025 seine Basis (nicht der ältere Basismonat des Tarifs).
	for _, cpi := range []string{`{"month":"2025-10","value":118.00}`, `{"month":"2025-11","value":120.00}`, `{"month":"2026-09","value":126.60}`} {
		if w := callJSONRaw(h, h.SaveCPI, http.MethodPut, "/api/cpi", "", cpi); w.Code != http.StatusOK {
			t.Fatalf("cpi: %d %s", w.Code, w.Body.String())
		}
	}
	if w := callJSONRaw(h, h.SetCategoryIndexation, http.MethodPut, "/api/categories/x/indexation", c, `{"base_month":"2025-10","threshold":5}`); w.Code != http.StatusOK {
		t.Fatalf("indexation: %d %s", w.Code, w.Body.String())
	}
	before := mustInvoice(t, h, pid) // bis September 2026
	preview := httptest.NewRecorder()
	h.IndexationPreview(preview, httptest.NewRequest(http.MethodGet, "/api/indexation/preview?month=2026-09", nil))
	var plan indexPlan
	_ = json.Unmarshal(preview.Body.Bytes(), &plan)
	var cat *indexCategory
	for i := range plan.Categories {
		if plan.Categories[i].CategoryID == catID {
			cat = &plan.Categories[i]
		}
	}
	if cat == nil || !cat.Due || len(cat.Vehicles) != 1 {
		t.Fatalf("Vorschau: %s", preview.Body.String())
	}
	pv := cat.Vehicles[0]
	// 60 € × 126,6/120 = 63,30 € ab 1. November.
	if pv.OldRate != 60 || pv.NewRate != 63.3 || pv.EffectiveFrom.Format(dateLayout) != "2026-11-01" || pv.BaseMonth.Format("2006-01") != "2025-11" {
		t.Fatalf("Gefährt in der Vorschau: %+v", pv)
	}
	apply := callJSONRaw(h, h.ApplyIndexation, http.MethodPost, "/api/indexation/apply", "", `{"month":"2026-09","category_ids":[`+c+`]}`)
	if apply.Code != http.StatusOK || !strings.Contains(apply.Body.String(), `"vehicles":1`) {
		t.Fatalf("anpassen: %d %s", apply.Code, apply.Body.String())
	}
	var monthly float64
	if err := h.Pool.QueryRow(ctx, `SELECT default_monthly_cost FROM categories WHERE id = $1`, catID).Scan(&monthly); err != nil || monthly != round2(70*126.6/118) {
		t.Errorf("Tarifpreis für neue Verträge: %v %v", monthly, err)
	}
	// Die alte Rechnung bleibt, wie sie ist (Rechnungen sind unveränderlich, und die
	// Änderung gilt erst ab November).
	var total float64
	if err := h.Pool.QueryRow(ctx, `SELECT total FROM invoices WHERE id = $1`, before.ID).Scan(&total); err != nil || total != before.Total {
		t.Fatalf("frühere Rechnung: %v %v", total, err)
	}
	// Dezember: Oktober (60) und November (neuer Satz) werden abgerechnet.
	h.Now = func() time.Time { return time.Date(2026, 12, 2, 12, 0, 0, 0, time.UTC) }
	after := mustInvoice(t, h, pid)
	if want := round2(60 + pv.NewRate); after.Total != want && after.Subtotal != want {
		t.Fatalf("Rechnung Okt+Nov: total %v subtotal %v, want %v", after.Total, after.Subtotal, want)
	}
	changes, _ := h.listRateChanges(ctx, vid)
	if len(changes) != 1 {
		t.Fatalf("Preisänderungen: %+v", changes)
	}
	if c := callJSON(h, h.DeleteRateChange, http.MethodDelete, "/api/vehicle-rate-changes/x", strconv.FormatInt(changes[0].ID, 10), nil).Code; c != http.StatusConflict {
		t.Fatalf("eine geltende Änderung zurücknehmen: %d, want 409", c)
	}
}
