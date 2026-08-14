package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// captureSender is a mail.Sender that records the last message instead of
// sending it, so reminder composition can be asserted without a real relay.
type captureSender struct {
	enabled bool
	calls   int
	to      []string
	subject string
	body    string
}

func (c *captureSender) Enabled() bool { return c.enabled }
func (c *captureSender) Send(_ context.Context, to []string, subject, body string) error {
	c.calls++
	c.to = to
	c.subject = subject
	c.body = body
	return nil
}

func TestRemindInvoice(t *testing.T) {
	h := testHandler(t)
	compliantSeller(t, h)
	pid := createIntegrationPerson(t, h)
	if _, err := h.Pool.Exec(context.Background(),
		`UPDATE persons SET email='reminder@example.com' WHERE id=$1`, pid); err != nil {
		t.Fatal(err)
	}
	chargeFor(t, h, pid, 42.0)
	iv := createInvoice(t, h, pid)

	remind := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/invoices/"+strconv.FormatInt(iv.ID, 10)+"/remind", nil)
		req.SetPathValue("id", strconv.FormatInt(iv.ID, 10))
		w := httptest.NewRecorder()
		h.RemindInvoice(w, req)
		return w
	}

	// Default handler has a disabled mailer → 503, nothing sent.
	if w := remind(); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled mail: want 503, got %d %s", w.Code, w.Body.String())
	}

	fake := &captureSender{enabled: true}
	h.Mail = fake
	w := remind()
	if w.Code != http.StatusOK {
		t.Fatalf("remind: %d %s", w.Code, w.Body.String())
	}
	if fake.calls != 1 {
		t.Fatalf("Send calls = %d, want 1", fake.calls)
	}
	if len(fake.to) != 1 || fake.to[0] != "reminder@example.com" {
		t.Errorf("recipients = %v, want [reminder@example.com]", fake.to)
	}
	if !strings.Contains(fake.subject, iv.Number) {
		t.Errorf("subject %q missing invoice number %s", fake.subject, iv.Number)
	}
	if !strings.Contains(fake.body, iv.Number) {
		t.Errorf("body missing invoice number %s", iv.Number)
	}

	// A person without an e-mail → 400, still nothing further sent.
	if _, err := h.Pool.Exec(context.Background(),
		`UPDATE persons SET email='' WHERE id=$1`, pid); err != nil {
		t.Fatal(err)
	}
	if w := remind(); w.Code != http.StatusBadRequest {
		t.Errorf("no-email: want 400, got %d", w.Code)
	}
	if fake.calls != 1 {
		t.Errorf("must not send when person has no e-mail; calls=%d", fake.calls)
	}
}
