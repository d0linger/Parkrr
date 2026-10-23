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
		`SELECT b.id, b.booked_on, b.amount, b.currency, b.name, b.iban, b.remittance,
		        CASE WHEN b.status = 'zugeordnet' AND COALESCE(p.reversed, false) THEN 'offen' ELSE b.status END,
		        b.payment_id, b.person_id
		   FROM bank_transactions b LEFT JOIN payments p ON p.id = b.payment_id
		  WHERE CASE WHEN b.status = 'zugeordnet' AND COALESCE(p.reversed, false) THEN 'offen' ELSE b.status END = $1
		  ORDER BY b.booked_on DESC, b.id DESC LIMIT 500`, status)
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
	ctx := r.Context()
	var personID, paymentID int64
	var by *int64
	if u, ok := auth.UserFrom(ctx); ok {
		by = &u.ID
	}
	err := pgx.BeginFunc(ctx, h.Pool, func(tx pgx.Tx) error {
		t, err := loadBankTransactionTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if t.effectiveStatus() != "offen" || (t.PaymentID != nil && !t.paymentReversed) {
			return &paymentRequestError{status: http.StatusConflict, message: "Buchung ist nicht mehr offen"}
		}
		if t.Currency != "EUR" {
			return &paymentRequestError{status: http.StatusConflict, message: "Nur Gutschriften in EUR können zugeordnet werden"}
		}
		if in.InvoiceID != nil {
			if err := tx.QueryRow(ctx, `SELECT person_id FROM invoices WHERE id = $1`, *in.InvoiceID).Scan(&personID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return &paymentRequestError{status: http.StatusBadRequest, message: "Rechnung gibt es nicht"}
				}
				return err
			}
		} else {
			personID = *in.PersonID
		}
		// Match reversal's bank-before-person lock order, then read invoice state
		// again under the person's settlement lock.
		if err := lockInvoicePersonTx(ctx, tx, personID); err != nil {
			return err
		}
		req := payInvoicesRequest{Amount: t.Amount, PaidOn: t.BookedOn.Format(dateLayout), Method: "ueberweisung",
			Note: truncRunes(strings.TrimSpace("Kontoauszug: "+t.Remittance), 200)}
		if in.InvoiceID != nil {
			var owner int64
			var open float64
			var canceled bool
			if err := tx.QueryRow(ctx,
				`SELECT person_id, round(total - paid_amount, 2), canceled OR cancels_id IS NOT NULL FROM invoices WHERE id = $1`, *in.InvoiceID).
				Scan(&owner, &open, &canceled); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return &paymentRequestError{status: http.StatusBadRequest, message: "Rechnung gibt es nicht"}
				}
				return err
			}
			if owner != personID || canceled || open <= 0.005 {
				return &paymentRequestError{status: http.StatusConflict, message: "Rechnung ist nicht mehr offen"}
			}
			req.Allocations = []invoiceAlloc{{InvoiceID: *in.InvoiceID, Amount: round2(min(t.Amount, open))}}
		} else {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM persons WHERE id = $1 AND NOT anonymized)`, personID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return &paymentRequestError{status: http.StatusBadRequest, message: "Person gibt es nicht"}
			}
		}
		key := "bank-tx-" + strconv.FormatInt(id, 10)
		if t.PaymentID != nil {
			// Retain the prior receipt as the generation marker. Its original key
			// would replay reversed money instead of recording a new receipt.
			key += "-after-" + strconv.FormatInt(*t.PaymentID, 10)
		}
		prepared, err := h.prepareInvoicePayment(r, personID, req, key)
		if err != nil {
			return err
		}
		out, _, err := h.recordInvoicePaymentTx(r, tx, prepared)
		if err != nil {
			return err
		}
		var reversed bool
		if err := tx.QueryRow(ctx, `SELECT reversed FROM payments WHERE id = $1 FOR UPDATE`, out.PaymentID).Scan(&reversed); err != nil {
			return err
		}
		if reversed {
			return &paymentRequestError{status: http.StatusConflict, message: "Die frühere Zahlung ist bereits storniert – Kontobuchung bitte neu laden"}
		}
		paymentID = out.PaymentID
		if _, err := tx.Exec(ctx,
			`UPDATE bank_transactions SET status = 'zugeordnet', payment_id = $2, person_id = $3, resolved_at = now(), resolved_by = $4 WHERE id = $1`,
			id, paymentID, personID, by); err != nil {
			return err
		}
		return h.auditChangeTx(ctx, tx, r, "assign", "bank_transaction", id, "Kontobuchung zugeordnet",
			diffFields(t.auditValues(), map[string]any{
				"status": "zugeordnet", "payment_id": paymentID, "person_id": personID, "resolved_at": "jetzt", "resolved_by": by,
			}))
	})
	if errors.Is(err, ErrTaskBusy) {
		writeError(w, http.StatusConflict, "Diese Kontobuchung wird gerade bearbeitet")
		return
	}
	if writeInvoicePaymentError(w, r, err) {
		return
	}
	h.archiveInvoiceSettledCollected(ctx, personID)
	writeJSON(w, http.StatusOK, map[string]any{"payment_id": paymentID, "person_id": personID})
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
	from, to := "offen", "ignoriert"
	if !in.Ignored {
		from, to = "ignoriert", "offen"
	}
	var by *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		by = &u.ID
	}
	err := pgx.BeginFunc(r.Context(), h.Pool, func(tx pgx.Tx) error {
		t, err := loadBankTransactionTx(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if t.effectiveStatus() != from || (t.PaymentID != nil && !t.paymentReversed) {
			return &paymentRequestError{status: http.StatusConflict, message: "Buchung ist nicht " + from}
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE bank_transactions SET status = $2, person_id = NULL,
			        resolved_at = CASE WHEN $2 = 'ignoriert' THEN now() END,
			        resolved_by = CASE WHEN $2 = 'ignoriert' THEN $3::bigint END
			  WHERE id = $1`, id, to, by); err != nil {
			return err
		}
		var resolvedAt, resolvedBy any
		if in.Ignored {
			resolvedAt, resolvedBy = "jetzt", by
		}
		return h.auditChangeTx(r.Context(), tx, r, "update", "bank_transaction", id, "Kontobuchung "+to,
			diffFields(t.auditValues(), map[string]any{
				"status": to, "payment_id": t.PaymentID, "person_id": nil, "resolved_at": resolvedAt, "resolved_by": resolvedBy,
			}))
	})
	if errors.Is(err, ErrTaskBusy) {
		writeError(w, http.StatusConflict, "Diese Kontobuchung wird gerade bearbeitet")
		return
	}
	if writeInvoicePaymentError(w, r, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": to})
}

type bankTransactionState struct {
	bankTxOut
	resolvedAt      *time.Time
	resolvedBy      *int64
	paymentReversed bool
}

func (t bankTransactionState) effectiveStatus() string {
	if t.Status == "zugeordnet" && t.paymentReversed {
		return "offen"
	}
	return t.Status
}

func (t bankTransactionState) auditValues() map[string]any {
	return map[string]any{
		"status": t.Status, "payment_id": t.PaymentID, "person_id": t.PersonID,
		"resolved_at": t.resolvedAt, "resolved_by": t.resolvedBy,
	}
}

func loadBankTransactionTx(ctx context.Context, tx pgx.Tx, id int64) (bankTransactionState, error) {
	var t bankTransactionState
	if err := lockBankTransactionTx(ctx, tx, id); err != nil {
		return t, err
	}
	err := tx.QueryRow(ctx,
		`SELECT b.id, b.booked_on, b.amount, b.currency, b.name, b.remittance, b.status,
		        b.payment_id, b.person_id, b.resolved_at, b.resolved_by, COALESCE(p.reversed, false)
		   FROM bank_transactions b LEFT JOIN payments p ON p.id = b.payment_id
		  WHERE b.id = $1 FOR UPDATE OF b`, id).
		Scan(&t.ID, &t.BookedOn, &t.Amount, &t.Currency, &t.Name, &t.Remittance, &t.Status,
			&t.PaymentID, &t.PersonID, &t.resolvedAt, &t.resolvedBy, &t.paymentReversed)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, &paymentRequestError{status: http.StatusNotFound, message: "not found"}
	}
	return t, err
}

// Assignment, ignore and reversal share a transaction lock across operators and
// replicas. Take it before person or payment locks so competing mutations return
// a retryable conflict instead of booking the same bank receipt twice.
func lockBankTransactionTx(ctx context.Context, tx pgx.Tx, id int64) error {
	key := advisoryKey("parkrr.bank.transaction." + strconv.FormatInt(id, 10))
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, key).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return ErrTaskBusy
	}
	return nil
}
