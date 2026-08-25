package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// createPortalLink issues a link for pid and returns the raw token.
func createPortalLink(t *testing.T, h *Handler, pid int64) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/portal-link",
		bytes.NewReader([]byte(`{}`)))
	req.SetPathValue("id", strconv.FormatInt(pid, 10))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.CreatePortalLink(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create portal link: %d %s", w.Code, w.Body.String())
	}
	var res struct {
		Link string `json:"link"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	i := strings.Index(res.Link, "/#/portal/")
	if i < 0 {
		t.Fatalf("link missing portal path: %q", res.Link)
	}
	return res.Link[i+len("/#/portal/"):]
}

func getPortalSummary(t *testing.T, h *Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	// Token travels in the Authorization header, not the URL path (SEC-01).
	req := httptest.NewRequest(http.MethodGet, "/api/portal/summary", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.PortalSummary(w, req)
	return w
}

func TestPortalLinkManagement(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	pids := strconv.FormatInt(pid, 10)

	raw := createPortalLink(t, h, pid)
	_ = createPortalLink(t, h, pid) // a second token

	list := func() []portalLinkInfo {
		req := httptest.NewRequest(http.MethodGet, "/api/persons/"+pids+"/portal-links", nil)
		req.SetPathValue("id", pids)
		w := httptest.NewRecorder()
		h.ListPortalLinks(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list: %d %s", w.Code, w.Body.String())
		}
		var out []portalLinkInfo
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		return out
	}

	if got := list(); len(got) != 2 {
		t.Fatalf("expected 2 links, got %d", len(got))
	}

	// Use the first token so last_used_at is recorded, then find its id.
	if _, ok := h.resolvePortalPerson(context.Background(), raw); !ok {
		t.Fatal("token should resolve before revoke")
	}
	var tid int64
	if err := h.Pool.QueryRow(context.Background(),
		`SELECT id FROM self_service_tokens WHERE token_hash=$1`, hashPortalToken(raw)).Scan(&tid); err != nil {
		t.Fatal(err)
	}

	// Revoke that single token.
	rreq := httptest.NewRequest(http.MethodPost, "/api/portal-links/"+strconv.FormatInt(tid, 10)+"/revoke", nil)
	rreq.SetPathValue("id", strconv.FormatInt(tid, 10))
	rw := httptest.NewRecorder()
	h.RevokePortalLink(rw, rreq)
	if rw.Code != http.StatusOK {
		t.Fatalf("revoke single: %d %s", rw.Code, rw.Body.String())
	}

	// The revoked one shows status "widerrufen" and carries last_used_at; the
	// other stays "aktiv".
	var revoked, active int
	for _, l := range list() {
		if l.ID == tid {
			if l.Status != "widerrufen" {
				t.Errorf("token %d status = %q, want widerrufen", tid, l.Status)
			}
			if l.LastUsedAt == nil {
				t.Error("used token should have last_used_at")
			}
			revoked++
		} else if l.Status == "aktiv" {
			active++
		}
	}
	if revoked != 1 || active != 1 {
		t.Errorf("revoked=%d active=%d, want 1/1", revoked, active)
	}
}

func TestSelfServicePortal(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)

	pid := createIntegrationPerson(t, h)
	seedHandoverVehicle(t, h) // unrelated vehicle for another person (noise)
	chargeFor(t, h, pid, 55.0)
	iv := createInvoice(t, h, pid)

	token := createPortalLink(t, h, pid)

	// Valid token → summary with the person's data.
	w := getPortalSummary(t, h, token)
	if w.Code != http.StatusOK {
		t.Fatalf("summary: %d %s", w.Code, w.Body.String())
	}
	var sum portalSummary
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if len(sum.Invoices) != 1 || sum.Invoices[0].Number != iv.Number {
		t.Errorf("invoices = %+v, want the one issued", sum.Invoices)
	}
	if sum.OpenTotal <= 0 {
		t.Errorf("open_total = %v, want > 0", sum.OpenTotal)
	}

	// Bogus token → 404.
	if bw := getPortalSummary(t, h, "not-a-real-token"); bw.Code != http.StatusNotFound {
		t.Errorf("bogus token: want 404, got %d", bw.Code)
	}

	// No Authorization header at all → 404 (portalBearer rejects it; SEC-01).
	noAuth := httptest.NewRequest(http.MethodGet, "/api/portal/summary", nil)
	naw := httptest.NewRecorder()
	h.PortalSummary(naw, noAuth)
	if naw.Code != http.StatusNotFound {
		t.Errorf("missing Authorization header: want 404, got %d", naw.Code)
	}

	// Own invoice PDF via the token.
	preq := httptest.NewRequest(http.MethodGet, "/api/portal/invoices/"+strconv.FormatInt(iv.ID, 10)+"/pdf", nil)
	preq.Header.Set("Authorization", "Bearer "+token)
	preq.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
	pw := httptest.NewRecorder()
	h.PortalInvoicePDF(pw, preq)
	if pw.Code != http.StatusOK || !bytes.HasPrefix(pw.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("portal pdf: %d", pw.Code)
	}

	// Another person's invoice must not be reachable with this token → 404.
	otherPid := createIntegrationPerson(t, h)
	chargeFor(t, h, otherPid, 10.0)
	otherIv := createInvoice(t, h, otherPid)
	oreq := httptest.NewRequest(http.MethodGet, "/api/portal/invoices/"+strconv.FormatInt(otherIv.ID, 10)+"/pdf", nil)
	oreq.Header.Set("Authorization", "Bearer "+token)
	oreq.SetPathValue("id", strconv.FormatInt(otherIv.ID, 10))
	ow := httptest.NewRecorder()
	h.PortalInvoicePDF(ow, oreq)
	if ow.Code != http.StatusNotFound {
		t.Errorf("cross-person PDF: want 404, got %d", ow.Code)
	}

	// Expiry → token stops working. Mint a fresh token, expire it, assert 404.
	expToken := createPortalLink(t, h, pid)
	if _, err := h.Pool.Exec(context.Background(),
		`UPDATE self_service_tokens SET expires_at = now() - interval '1 hour' WHERE token_hash = $1`,
		hashPortalToken(expToken)); err != nil {
		t.Fatalf("expire token: %v", err)
	}
	if ew := getPortalSummary(t, h, expToken); ew.Code != http.StatusNotFound {
		t.Errorf("expired token: want 404, got %d", ew.Code)
	}

	// Revoke → token stops working.
	rreq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/portal-link/revoke", nil)
	rreq.SetPathValue("id", strconv.FormatInt(pid, 10))
	rw := httptest.NewRecorder()
	h.RevokePortalLinks(rw, rreq)
	if rw.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", rw.Code, rw.Body.String())
	}
	if aw := getPortalSummary(t, h, token); aw.Code != http.StatusNotFound {
		t.Errorf("after revoke: want 404, got %d", aw.Code)
	}
}

// TestAnonymizeRevokesPortalTokens: anonymizing a person must also revoke their
// self-service portal links — otherwise a live magic link keeps exposing the
// (now-scrubbed) record after a GDPR anonymization (finding H-05).
func TestAnonymizeRevokesPortalTokens(t *testing.T) {
	h := testHandler(t)
	pid := createIntegrationPerson(t, h)
	token := createPortalLink(t, h, pid)

	if w := getPortalSummary(t, h, token); w.Code != http.StatusOK {
		t.Fatalf("summary before anonymize: %d %s", w.Code, w.Body.String())
	}

	areq := httptest.NewRequest(http.MethodPost, "/api/persons/"+strconv.FormatInt(pid, 10)+"/anonymize", nil)
	areq.SetPathValue("id", strconv.FormatInt(pid, 10))
	aw := httptest.NewRecorder()
	h.AnonymizePerson(aw, areq)
	if aw.Code != http.StatusOK {
		t.Fatalf("anonymize: %d %s", aw.Code, aw.Body.String())
	}

	if w := getPortalSummary(t, h, token); w.Code != http.StatusNotFound {
		t.Errorf("portal token still works after anonymize: got %d, want 404", w.Code)
	}
}
