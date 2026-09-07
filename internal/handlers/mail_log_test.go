package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Das Versandprotokoll (Hundert 86) hält Erfolg UND Fehlschlag fest und liefert
// sie neueste zuerst über den Admin-Endpunkt.
func TestMailLogListsAttempts(t *testing.T) {
	h := testHandler(t)
	ctx := context.Background()
	if _, err := h.Pool.Exec(ctx,
		`INSERT INTO mail_log (recipients, subject, ok, error) VALUES
		 ('kunde@example.com', 'MailLog-Test: Zahlungserinnerung', true, ''),
		 ('kunde@example.com', 'MailLog-Test: 1. Mahnung', false, 'connection refused')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.Pool.Exec(context.Background(), `DELETE FROM mail_log WHERE subject LIKE 'MailLog-Test:%'`)
	})
	rec := httptest.NewRecorder()
	h.ListMailLog(rec, httptest.NewRequest(http.MethodGet, "/api/mail-log", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Zahlungserinnerung", "1. Mahnung", "connection refused", `"ok":true`, `"ok":false`} {
		if !strings.Contains(body, want) {
			t.Errorf("Antwort ohne %q", want)
		}
	}
}
