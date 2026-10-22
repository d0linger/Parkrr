package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

// Kontoauszug importieren (Ausbau 27): camt.053 aus dem Online-Banking. Eingelesen
// werden nur gebuchte Gutschriften; derselbe Auszug zweimal legt nichts doppelt an
// (Buchungsreferenz). Parkrr schlägt eine Zuordnung vor — Rechnungsnummer im
// Verwendungszweck, sonst Betrag und Name —, gebucht wird nur per Klick und dann über
// den normalen Zahlungsweg (pay-invoices).

const maxBankFileBytes = 5 << 20

// camt.053 in den Versionen .02 bis .08: Go vergleicht ohne Namensraum nur den lokalen
// Namen, daher passt eine Struktur für alle Fassungen.
type camtDoc struct {
	Stmts []camtStmt `xml:"BkToCstmrStmt>Stmt"`
}

type camtStmt struct {
	IBAN    string      `xml:"Acct>Id>IBAN"`
	Entries []camtEntry `xml:"Ntry"`
}

type camtAmt struct {
	Value string `xml:",chardata"`
	Ccy   string `xml:"Ccy,attr"`
}

type camtStatus struct {
	Text string `xml:",chardata"`
	Cd   string `xml:"Cd"`
}

type camtEntry struct {
	NtryRef     string      `xml:"NtryRef"`
	Amt         camtAmt     `xml:"Amt"`
	CdtDbtInd   string      `xml:"CdtDbtInd"`
	Sts         camtStatus  `xml:"Sts"`
	BookgDt     string      `xml:"BookgDt>Dt"`
	BookgDtTm   string      `xml:"BookgDt>DtTm"`
	AcctSvcrRef string      `xml:"AcctSvcrRef"`
	Details     []camtTxDtl `xml:"NtryDtls>TxDtls"`
}

type camtTxDtl struct {
	AcctSvcrRef string   `xml:"Refs>AcctSvcrRef"`
	EndToEndID  string   `xml:"Refs>EndToEndId"`
	Amt         camtAmt  `xml:"AmtDtls>TxAmt>Amt"`
	Amt2        camtAmt  `xml:"Amt"`
	DbtrNm      string   `xml:"RltdPties>Dbtr>Nm"`
	DbtrPtyNm   string   `xml:"RltdPties>Dbtr>Pty>Nm"`
	DbtrIBAN    string   `xml:"RltdPties>DbtrAcct>Id>IBAN"`
	Ustrd       []string `xml:"RmtInf>Ustrd"`
	StrdRef     []string `xml:"RmtInf>Strd>CdtrRefInf>Ref"`
	AddtlInfo   string   `xml:"AddtlTxInf"`
}

type bankTx struct {
	Ref        string
	BookedOn   time.Time
	Amount     float64
	Currency   string
	Name       string
	IBAN       string
	Remittance string
}

// parseCamt053 liefert die gebuchten Gutschriften. Lastschriften und vorgemerkte
// Buchungen fallen weg (skipped zählt sie).
func parseCamt053(data []byte) ([]bankTx, int, error) {
	var doc camtDoc
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	if err := dec.Decode(&doc); err != nil {
		return nil, 0, camtError("Keine lesbare camt.053-Datei")
	}
	if len(doc.Stmts) == 0 {
		return nil, 0, camtError("Keine Kontoauszüge in der Datei (camt.053 erwartet)")
	}
	var out []bankTx
	skipped := 0
	for si, st := range doc.Stmts {
		for ei, e := range st.Entries {
			status := strings.TrimSpace(e.Sts.Cd)
			if status == "" {
				status = strings.TrimSpace(e.Sts.Text)
			}
			if strings.TrimSpace(e.CdtDbtInd) != "CRDT" || (status != "" && status != "BOOK") {
				skipped++
				continue
			}
			day := strings.TrimSpace(e.BookgDt)
			if day == "" && len(strings.TrimSpace(e.BookgDtTm)) >= 10 {
				day = strings.TrimSpace(e.BookgDtTm)[:10]
			}
			booked, err := time.Parse(dateLayout, day)
			if err != nil {
				return nil, 0, camtError(fmt.Sprintf("Buchung %d ohne Buchungsdatum", ei+1))
			}
			details := e.Details
			if len(details) == 0 {
				details = []camtTxDtl{{}}
			}
			for di, d := range details {
				amt := e.Amt
				if len(details) > 1 {
					amt = d.Amt
					if strings.TrimSpace(amt.Value) == "" {
						amt = d.Amt2
					}
				}
				amount, err := strconv.ParseFloat(strings.TrimSpace(amt.Value), 64)
				if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 || amount > maxMoneyAmount || round2(amount) <= 0 {
					return nil, 0, camtError(fmt.Sprintf("Buchung %d ohne gültigen Betrag", ei+1))
				}
				ccy := strings.TrimSpace(amt.Ccy)
				if ccy == "" {
					ccy = "EUR"
				}
				if ccy != "EUR" {
					return nil, 0, camtError(fmt.Sprintf("Buchung %d: Nur Gutschriften in EUR können zugeordnet werden", ei+1))
				}
				name := strings.TrimSpace(d.DbtrNm)
				if name == "" {
					name = strings.TrimSpace(d.DbtrPtyNm)
				}
				parts := append(append([]string{}, d.Ustrd...), d.StrdRef...)
				remit := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
				if remit == "" {
					remit = strings.Join(strings.Fields(d.AddtlInfo), " ")
				}
				ref := firstNonEmpty(d.AcctSvcrRef, e.AcctSvcrRef, e.NtryRef)
				if ref != "" && len(details) > 1 && d.AcctSvcrRef == "" {
					ref += "/" + strconv.Itoa(di+1)
				}
				if ref == "" {
					// Ohne Bankreferenz: Hash der Buchung samt Position in der Datei, so
					// bleibt derselbe Auszug beim zweiten Import erkennbar.
					sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%.2f|%s|%s|%d|%d|%d", st.IBAN, day, amount, name, remit, si, ei, di)))
					ref = "hash:" + hex.EncodeToString(sum[:16])
				} else {
					ref = strings.TrimSpace(st.IBAN) + ":" + strings.TrimSpace(ref)
				}
				out = append(out, bankTx{Ref: truncRunes(ref, 200), BookedOn: booked, Amount: round2(amount), Currency: ccy,
					Name: truncRunes(name, 140), IBAN: truncRunes(strings.TrimSpace(d.DbtrIBAN), 40), Remittance: truncRunes(remit, 500)})
			}
		}
	}
	return out, skipped, nil
}

// camtError trägt die Meldung für den Betreiber (deutsch, Satzanfang groß).
type camtError string

func (e camtError) Error() string { return string(e) }

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" && s != "NOTPROVIDED" {
			return s
		}
	}
	return ""
}

// ImportBankStatement: POST /api/bank-import (multipart: file).
func (h *Handler) ImportBankStatement(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBankFileBytes+1024)
	// #nosec G120 -- the request body is capped by MaxBytesReader above.
	if err := r.ParseMultipartForm(maxBankFileBytes + 1024); err != nil {
		writeMultipartError(w, err, "Datei zu groß (höchstens 5 MB)")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBankFileBytes+1))
	if err != nil || len(data) > maxBankFileBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "Datei zu groß (höchstens 5 MB)")
		return
	}
	txs, skipped, err := parseCamt053(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	imported, dupes := 0, 0
	err = pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		for _, t := range txs {
			ct, err := tx.Exec(r.Context(),
				`INSERT INTO bank_transactions (ref, booked_on, amount, currency, name, iban, remittance)
				 VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (ref) DO NOTHING`,
				t.Ref, t.BookedOn, t.Amount, t.Currency, t.Name, t.IBAN, t.Remittance)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 1 {
				imported++
			} else {
				dupes++
			}
		}
		return nil
	})
	if err != nil {
		serverError(w, r, "could not import", err)
		return
	}
	h.audit(r, "import", "bank_transaction", 0, fmt.Sprintf("Kontoauszug importiert: %d neu, %d schon vorhanden, %d übersprungen", imported, dupes, skipped))
	writeJSON(w, http.StatusOK, map[string]int{"imported": imported, "duplicates": dupes, "skipped": skipped})
}

type openInvoiceRef struct {
	ID         int64   `json:"invoice_id"`
	Number     string  `json:"number"`
	PersonID   int64   `json:"person_id"`
	PersonName string  `json:"person_name"`
	LastName   string  `json:"-"`
	OpenAmount float64 `json:"open_amount"`
}

type bankSuggestion struct {
	openInvoiceRef
	Reason string `json:"reason"`
	Strong bool   `json:"strong"`
}

type bankTxOut struct {
	ID         int64           `json:"id"`
	BookedOn   time.Time       `json:"booked_on"`
	Amount     float64         `json:"amount"`
	Currency   string          `json:"currency"`
	Name       string          `json:"name"`
	IBAN       string          `json:"iban"`
	Remittance string          `json:"remittance"`
	Status     string          `json:"status"`
	PaymentID  *int64          `json:"payment_id"`
	PersonID   *int64          `json:"person_id"`
	Suggestion *bankSuggestion `json:"suggestion"`
}

func compactUpper(s string) string {
	return strings.Map(func(c rune) rune {
		if unicode.IsSpace(c) {
			return -1
		}
		return unicode.ToUpper(c)
	}, s)
}

func (h *Handler) openInvoiceRefs(ctx context.Context) ([]openInvoiceRef, error) {
	rows, err := h.Pool.Query(ctx,
		`SELECT i.id, i.number, i.person_id, trim(p.first_name || ' ' || p.last_name), p.last_name, round(i.total - i.paid_amount, 2)
		   FROM invoices i JOIN persons p ON p.id = i.person_id
		  WHERE NOT i.canceled AND i.cancels_id IS NULL AND (i.total - i.paid_amount) > 0.005
		  ORDER BY i.issued_on, i.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []openInvoiceRef
	for rows.Next() {
		var o openInvoiceRef
		if err := rows.Scan(&o.ID, &o.Number, &o.PersonID, &o.PersonName, &o.LastName, &o.OpenAmount); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// containsNumber: n steht in s, ohne dass davor oder dahinter eine Ziffer klebt —
// "2026-0001" darf nicht in "2026-00012" gefunden werden.
func containsNumber(s, n string) bool {
	for from := 0; ; {
		i := strings.Index(s[from:], n)
		if i < 0 {
			return false
		}
		i += from
		before := i == 0 || !unicode.IsDigit(rune(s[i-1]))
		after := i+len(n) >= len(s) || !unicode.IsDigit(rune(s[i+len(n)]))
		if before && after {
			return true
		}
		from = i + 1
	}
}

// suggestBankMatch: Rechnungsnummer im Verwendungszweck (sicher), sonst genau eine
// offene Rechnung mit diesem Betrag, deren Nachname im Auftraggeber steht (Vorschlag).
func suggestBankMatch(t bankTxOut, open []openInvoiceRef) *bankSuggestion {
	if t.Currency != "" && t.Currency != "EUR" {
		return nil
	}
	remit := compactUpper(t.Remittance)
	var numbered *bankSuggestion
	for _, o := range open {
		if n := compactUpper(o.Number); len(n) >= 4 && containsNumber(remit, n) {
			if numbered != nil {
				return nil
			}
			numbered = &bankSuggestion{openInvoiceRef: o, Reason: "Rechnungsnummer im Verwendungszweck", Strong: true}
		}
	}
	if numbered != nil {
		return numbered
	}
	name := strings.ToLower(t.Name)
	var hit *openInvoiceRef
	for i := range open {
		o := &open[i]
		ln := strings.ToLower(strings.TrimSpace(o.LastName))
		if toCentsH(o.OpenAmount) == toCentsH(t.Amount) && ln != "" && strings.Contains(name, ln) {
			if hit != nil {
				return nil // mehrdeutig: lieber kein Vorschlag als ein falscher
			}
			hit = o
		}
	}
	if hit != nil {
		return &bankSuggestion{openInvoiceRef: *hit, Reason: "Betrag und Name"}
	}
	return nil
}

// ListBankTransactions: GET /api/bank-transactions?status=offen|zugeordnet|ignoriert.
func (h *Handler) ListBankTransactions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "offen"
	}
	if status != "offen" && status != "zugeordnet" && status != "ignoriert" {
		writeError(w, http.StatusBadRequest, "unknown status")
		return
	}
	rows, err := h.Pool.Query(r.Context(),
		`SELECT id, booked_on, amount, currency, name, iban, remittance, status, payment_id, person_id
		   FROM bank_transactions WHERE status = $1 ORDER BY booked_on DESC, id DESC LIMIT 500`, status)
	if err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	out := []bankTxOut{}
	for rows.Next() {
		var t bankTxOut
		if err := rows.Scan(&t.ID, &t.BookedOn, &t.Amount, &t.Currency, &t.Name, &t.IBAN, &t.Remittance, &t.Status, &t.PaymentID, &t.PersonID); err != nil {
			rows.Close()
			serverError(w, r, "scan failed", err)
			return
		}
		out = append(out, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, r, "query failed", err)
		return
	}
	var open []openInvoiceRef
	if status == "offen" {
		if open, err = h.openInvoiceRefs(r.Context()); err != nil {
			serverError(w, r, "query failed", err)
			return
		}
		for i := range out {
			out[i].Suggestion = suggestBankMatch(out[i], open)
		}
	}
	if open == nil {
		open = []openInvoiceRef{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "open_invoices": open})
}

// AssignBankTransaction: POST /api/bank-transactions/{id}/assign {invoice_id} oder
// {person_id}. Die Zahlung entsteht über pay-invoices: mit Rechnung auf diese (ein
// Überschuss wird Guthaben), nur mit Person ganz als Guthaben.
func (h *Handler) AssignBankTransaction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		InvoiceID *int64 `json:"invoice_id"`
		PersonID  *int64 `json:"person_id"`
	}
	if err := decodeJSON(r, &in); err != nil || (in.InvoiceID == nil) == (in.PersonID == nil) {
		writeError(w, http.StatusBadRequest, "Rechnung oder Person angeben (genau eins)")
		return
	}
	unlock, err := h.lockBankTransaction(r.Context(), id)
	if errors.Is(err, ErrTaskBusy) {
		writeError(w, http.StatusConflict, "Diese Kontobuchung wird gerade bearbeitet")
		return
	}
	if err != nil {
		serverError(w, r, "could not lock bank transaction", err)
		return
	}
	defer unlock()
	var t bankTxOut
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT id, booked_on, amount, currency, name, remittance, status FROM bank_transactions WHERE id = $1`, id).
		Scan(&t.ID, &t.BookedOn, &t.Amount, &t.Currency, &t.Name, &t.Remittance, &t.Status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		serverError(w, r, "query failed", err)
		return
	}
	if t.Status != "offen" {
		writeError(w, http.StatusConflict, "Buchung ist nicht mehr offen")
		return
	}
	if t.Currency != "EUR" {
		writeError(w, http.StatusConflict, "Nur Gutschriften in EUR können zugeordnet werden")
		return
	}
	req := payInvoicesRequest{Amount: t.Amount, PaidOn: t.BookedOn.Format(dateLayout), Method: "ueberweisung",
		Note: truncRunes(strings.TrimSpace("Kontoauszug: "+t.Remittance), 200)}
	var personID int64
	if in.InvoiceID != nil {
		var open float64
		var canceled bool
		if err := h.Pool.QueryRow(r.Context(),
			`SELECT person_id, round(total - paid_amount, 2), canceled OR cancels_id IS NOT NULL FROM invoices WHERE id = $1`, *in.InvoiceID).
			Scan(&personID, &open, &canceled); err != nil {
			writeError(w, http.StatusBadRequest, "Rechnung gibt es nicht")
			return
		}
		if canceled || open <= 0.005 {
			writeError(w, http.StatusConflict, "Rechnung ist nicht mehr offen")
			return
		}
		alloc := t.Amount
		if open < alloc {
			alloc = open
		}
		req.Allocations = []invoiceAlloc{{InvoiceID: *in.InvoiceID, Amount: round2(alloc)}}
	} else {
		personID = *in.PersonID
		var exists bool
		if err := h.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM persons WHERE id = $1 AND NOT anonymized)`, personID).Scan(&exists); err != nil || !exists {
			writeError(w, http.StatusBadRequest, "Person gibt es nicht")
			return
		}
	}
	payID, status, msg := h.payViaHandler(r, personID, req, "bank-tx-"+strconv.FormatInt(id, 10))
	if payID == 0 {
		writeError(w, status, "Zahlung nicht gebucht: "+msg)
		return
	}
	var by *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		by = &u.ID
	}
	if _, err := h.Pool.Exec(r.Context(),
		`UPDATE bank_transactions SET status = 'zugeordnet', payment_id = $2, person_id = $3, resolved_at = now(), resolved_by = $4 WHERE id = $1`,
		id, payID, personID, by); err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	h.auditChange(r, "assign", "bank_transaction", id, "Kontobuchung zugeordnet",
		diffFields(map[string]any{"status": "offen", "payment_id": nil, "person_id": nil, "resolved_at": nil, "resolved_by": nil},
			map[string]any{"status": "zugeordnet", "payment_id": payID, "person_id": personID, "resolved_at": "jetzt", "resolved_by": by}))
	writeJSON(w, http.StatusOK, map[string]any{"payment_id": payID, "person_id": personID})
}

// SetBankTransactionIgnored: POST /api/bank-transactions/{id}/ignore {ignored}. Für
// Gutschriften, die keine Mietzahlung sind (Rückerstattung, Umbuchung).
func (h *Handler) SetBankTransactionIgnored(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Ignored bool `json:"ignored"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	unlock, err := h.lockBankTransaction(r.Context(), id)
	if errors.Is(err, ErrTaskBusy) {
		writeError(w, http.StatusConflict, "Diese Kontobuchung wird gerade bearbeitet")
		return
	}
	if err != nil {
		serverError(w, r, "could not lock bank transaction", err)
		return
	}
	defer unlock()
	from, to := "offen", "ignoriert"
	if !in.Ignored {
		from, to = "ignoriert", "offen"
	}
	var by *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		by = &u.ID
	}
	ct, err := h.Pool.Exec(r.Context(),
		`UPDATE bank_transactions SET status = $3, resolved_at = CASE WHEN $3 = 'ignoriert' THEN now() END,
		        resolved_by = CASE WHEN $3 = 'ignoriert' THEN $4::bigint END
		  WHERE id = $1 AND status = $2`, id, from, to, by)
	if err != nil {
		serverError(w, r, "could not save", err)
		return
	}
	if ct.RowsAffected() == 0 {
		writeError(w, http.StatusConflict, "Buchung ist nicht "+from)
		return
	}
	h.auditChange(r, "update", "bank_transaction", id, "Kontobuchung "+to,
		diffFields(map[string]any{"status": from, "resolved_at": nil, "resolved_by": nil}, map[string]any{"status": to, "resolved_at": to, "resolved_by": by}))
	writeJSON(w, http.StatusOK, map[string]string{"status": to})
}

// The payment handler uses its own transaction. Keep a session lock until both
// the payment and its bank reference are saved, across operators and replicas.
func (h *Handler) lockBankTransaction(ctx context.Context, id int64) (func(), error) {
	conn, err := h.Pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	key := advisoryKey("parkrr.bank.transaction." + strconv.FormatInt(id, 10))
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
		discardLockedConn(conn)
		conn.Release()
		return nil, err
	}
	if !locked {
		conn.Release()
		return nil, ErrTaskBusy
	}
	return func() {
		defer conn.Release()
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock($1)`, key).Scan(&unlocked); err != nil || !unlocked {
			discardLockedConn(conn)
		}
	}, nil
}
