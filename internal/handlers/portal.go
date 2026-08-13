package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/preining/parkrr/internal/auth"
)

const portalDefaultTTLDays = 30

func hashPortalToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func newPortalToken() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashPortalToken(raw), nil
}

// resolvePortalPerson validates a raw token and, on success, touches last_used_at
// and returns the owning person id. A missing/expired/revoked token yields false.
func (h *Handler) resolvePortalPerson(ctx context.Context, rawToken string) (int64, bool) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" || len(rawToken) > 200 {
		return 0, false
	}
	var pid int64
	err := h.Pool.QueryRow(ctx,
		`UPDATE self_service_tokens SET last_used_at = now()
		   WHERE token_hash = $1 AND NOT revoked AND expires_at > now()
		 RETURNING person_id`, hashPortalToken(rawToken)).Scan(&pid)
	if err != nil {
		return 0, false
	}
	return pid, true
}

// CreatePortalLink issues a self-service magic link for a person (editor+),
// optionally e-mailing it. Body (optional JSON): {"days":1..365, "send":bool}.
func (h *Handler) CreatePortalLink(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var email, first, last string
	if err := h.Pool.QueryRow(r.Context(),
		`SELECT email, first_name, last_name FROM persons WHERE id=$1`, id,
	).Scan(&email, &first, &last); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "person not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	var req struct {
		Days int  `json:"days"`
		Send bool `json:"send"`
	}
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ttlDays := portalDefaultTTLDays
	if req.Days >= 1 && req.Days <= 365 {
		ttlDays = req.Days
	}
	expires := time.Now().Add(time.Duration(ttlDays) * 24 * time.Hour)

	raw, hash, err := newPortalToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create token")
		return
	}
	var createdBy *int64
	if u, ok := auth.UserFrom(r.Context()); ok {
		createdBy = &u.ID
	}
	if _, err := h.Pool.Exec(r.Context(),
		`INSERT INTO self_service_tokens (token_hash, person_id, expires_at, created_by)
		 VALUES ($1,$2,$3,$4)`, hash, id, expires, createdBy); err != nil {
		writeError(w, http.StatusInternalServerError, "could not store token")
		return
	}

	link := strings.TrimRight(h.PublicBaseURL, "/") + "/#/portal/" + raw

	emailed := false
	email = trim(email)
	if req.Send && h.Mail != nil && h.Mail.Enabled() && email != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := h.Mail.Send(ctx, []string{email}, "Ihr Parkrr-Zugang",
			portalMailBody(trim(first+" "+last), link, expires)); err == nil {
			emailed = true
		}
	}
	h.audit(r, "create", "portal-link", id, "Self-Service-Link erstellt (gültig bis "+expires.Format("2006-01-02")+")")
	writeJSON(w, http.StatusCreated, map[string]any{
		"link":       link,
		"expires_at": expires,
		"emailed":    emailed,
		"has_email":  email != "",
	})
}

func portalMailBody(name, link string, expires time.Time) string {
	var b strings.Builder
	if name != "" {
		fmt.Fprintf(&b, "Guten Tag %s,\n\n", name)
	} else {
		b.WriteString("Guten Tag,\n\n")
	}
	b.WriteString("über den folgenden Link können Sie jederzeit Ihre Gefährte, offenen Beträge und Rechnungen einsehen:\n\n")
	b.WriteString(link + "\n\n")
	fmt.Fprintf(&b, "Der Link ist bis %s gültig. Bitte geben Sie ihn nicht weiter.\n\n", expires.Format("02.01.2006"))
	b.WriteString("Mit freundlichen Grüßen\n")
	return b.String()
}

// RevokePortalLinks revokes all active self-service tokens of a person (editor+).
func (h *Handler) RevokePortalLinks(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	tag, err := h.Pool.Exec(r.Context(),
		`UPDATE self_service_tokens SET revoked = TRUE WHERE person_id=$1 AND NOT revoked`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not revoke")
		return
	}
	n := tag.RowsAffected()
	if n > 0 {
		h.audit(r, "revoke", "portal-link", id, fmt.Sprintf("%d Self-Service-Link(s) widerrufen", n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": n})
}

type portalVehicle struct {
	Label  string `json:"label"`
	Status string `json:"status"`
}

type portalInvoice struct {
	ID       int64      `json:"id"`
	Number   string     `json:"number"`
	IssuedOn time.Time  `json:"issued_on"`
	DueOn    *time.Time `json:"due_on"`
	Total    float64    `json:"total"`
	Open     float64    `json:"open"`
	Status   string     `json:"status"`
}

type portalSummary struct {
	PersonName string          `json:"person_name"`
	OpenTotal  float64         `json:"open_total"`
	Vehicles   []portalVehicle `json:"vehicles"`
	Invoices   []portalInvoice `json:"invoices"`
}

// PortalSummary is the PUBLIC read-only view behind a valid magic-link token.
func (h *Handler) PortalSummary(w http.ResponseWriter, r *http.Request) {
	pid, ok := h.resolvePortalPerson(r.Context(), r.PathValue("token"))
	if !ok {
		writeError(w, http.StatusNotFound, "Link ungültig oder abgelaufen")
		return
	}
	var out portalSummary
	out.Vehicles = []portalVehicle{}
	out.Invoices = []portalInvoice{}

	if err := h.Pool.QueryRow(r.Context(),
		`SELECT trim(first_name || ' ' || last_name) FROM persons WHERE id=$1`, pid).Scan(&out.PersonName); err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	vrows, err := h.Pool.Query(r.Context(),
		`SELECT COALESCE(NULLIF(v.label,''), NULLIF(v.license_plate,''), cat.name, 'Gefährt'), v.status
		   FROM vehicles v JOIN categories cat ON cat.id = v.category_id
		  WHERE v.person_id=$1 AND NOT v.archived
		  ORDER BY v.id`, pid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer vrows.Close()
	for vrows.Next() {
		var pv portalVehicle
		if err := vrows.Scan(&pv.Label, &pv.Status); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		out.Vehicles = append(out.Vehicles, pv)
	}
	if vrows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}

	irows, err := h.Pool.Query(r.Context(),
		`SELECT id, number, issued_on, due_on, total, paid_amount, canceled, cancels_id
		   FROM invoices WHERE person_id=$1
		  ORDER BY issued_on DESC, id DESC`, pid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	defer irows.Close()
	for irows.Next() {
		var (
			pi        portalInvoice
			paid      float64
			canceled  bool
			cancelsID *int64
		)
		if err := irows.Scan(&pi.ID, &pi.Number, &pi.IssuedOn, &pi.DueOn, &pi.Total, &paid, &canceled, &cancelsID); err != nil {
			writeError(w, http.StatusInternalServerError, "scan failed")
			return
		}
		pi.Open = round2(pi.Total - paid)
		pi.Status = invoiceStatus(pi.Total, paid, canceled, cancelsID)
		// A canceled/storno document is not payable — keep it visible but with a
		// zero open amount so it neither inflates the total nor renders a pay-QR.
		if canceled || cancelsID != nil {
			pi.Open = 0
		}
		if pi.Open > 0.005 {
			out.OpenTotal += pi.Open
		}
		out.Invoices = append(out.Invoices, pi)
	}
	if irows.Err() != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	out.OpenTotal = round2(out.OpenTotal)
	writeJSON(w, http.StatusOK, out)
}

// PortalInvoicePDF serves a person's own invoice PDF behind a valid token.
func (h *Handler) PortalInvoicePDF(w http.ResponseWriter, r *http.Request) {
	pid, ok := h.resolvePortalPerson(r.Context(), r.PathValue("token"))
	if !ok {
		writeError(w, http.StatusNotFound, "Link ungültig oder abgelaufen")
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	iv, found, err := h.fetchInvoice(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed")
		return
	}
	// Not found and not-owned are answered identically so the token can't be used
	// to probe which invoice ids exist.
	if !found || iv.PersonID != pid {
		writeError(w, http.StatusNotFound, "invoice not found")
		return
	}
	writeInvoicePDF(w, iv)
}
