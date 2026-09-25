package mail

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRedactAddrs(t *testing.T) {
	cases := []struct {
		in    string
		addrs []string
		want  string
	}{
		{"mail: RCPT a@x.at: 550 <A@X.AT> rejected", []string{"a@x.at"},
			"mail: RCPT " + AddrPlaceholder + ": 550 <" + AddrPlaceholder + "> rejected"},
		// Nur an Adressgrenzen: "an@x.at" steckt in "susan@x.at", ist aber nicht sie.
		{"RCPT susan@x.at: 550", []string{"an@x.at"}, "RCPT susan@x.at: 550"},
		{"RCPT an@x.at.evil: 550", []string{"an@x.at"}, "RCPT an@x.at.evil: 550"},
		{"no address here", []string{"a@x.at"}, "no address here"},
		// Satzzeichen hinter bzw. um die Adresse gehören nicht dazu.
		{"rejected a@x.at.", []string{"a@x.at"}, "rejected " + AddrPlaceholder + "."},
		{"user 'a@x.at' unknown", []string{"a@x.at"}, "user '" + AddrPlaceholder + "' unknown"},
		{"to a@x.at- bounced", []string{"a@x.at"}, "to " + AddrPlaceholder + "- bounced"},
		// Ein Apostroph INNERHALB einer längeren Adresse bleibt eine fremde Adresse.
		{"RCPT o'a@x.at: 550", []string{"a@x.at"}, "RCPT o'a@x.at: 550"},
		{"x", []string{"", "  ", "nope"}, "x"},
	}
	for _, c := range cases {
		if got := RedactAddrs(c.in, c.addrs); got != c.want {
			t.Errorf("RedactAddrs(%q, %v) = %q, want %q", c.in, c.addrs, got, c.want)
		}
	}
}

type failingSender struct{ err error }

func (failingSender) Enabled() bool { return true }
func (f failingSender) Send(context.Context, []string, string, string) error {
	return f.err
}

// PRT-03: das Versandprotokoll bekommt den Fehlertext OHNE Adressen — weder die
// des Empfängers noch eine, die das Relay umgeschrieben zurückmeldet. Der Aufrufer
// bekommt den Originalfehler.
func TestWithLogRedactsAddressesFromLoggedError(t *testing.T) {
	orig := errors.New("mail: RCPT kunde@example.at: 550 5.1.1 <Kunde@Example.at>: unknown, alias of real.kunde@internal.example")
	var logged error
	s := WithLog(failingSender{err: orig}, func(_ []string, _ string, _ bool, sendErr error) {
		logged = sendErr
	})
	err := s.Send(context.Background(), []string{"kunde@example.at"}, "Betreff", "Text")
	if err != orig {
		t.Errorf("Send must return the original error, got %v", err)
	}
	if logged == nil {
		t.Fatal("nothing logged")
	}
	msg := logged.Error()
	if strings.Contains(strings.ToLower(msg), "kunde@") {
		t.Errorf("logged error still names an address: %q", msg)
	}
	if !strings.Contains(msg, "550 5.1.1") {
		t.Errorf("the SMTP status is the diagnosis and must stay: %q", msg)
	}
	if !errors.Is(logged, orig) {
		t.Error("the redacted error must still unwrap to the original")
	}
}
