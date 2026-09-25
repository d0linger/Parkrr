// Package mail sends transactional e-mail over SMTP. It is optional: when no
// host is configured, New returns a disabled Sender whose Send reports
// ErrDisabled, so callers can degrade gracefully instead of crashing.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	netmail "net/mail"
	"net/smtp"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrDisabled is returned by Send when SMTP is not configured.
var ErrDisabled = errors.New("mail: SMTP not configured")

// Config describes an SMTP relay. Host empty means "disabled".
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string // envelope + header From address
	FromName string // optional display name
	TLS      string // "starttls" (default), "tls" (implicit), or "none"
	Timeout  time.Duration
}

// Sender delivers a plain-text UTF-8 message to one or more recipients.
type Sender interface {
	Enabled() bool
	Send(ctx context.Context, to []string, subject, body string) error
}

type disabledSender struct{}

func (disabledSender) Enabled() bool { return false }
func (disabledSender) Send(context.Context, []string, string, string) error {
	return ErrDisabled
}

type smtpSender struct{ cfg Config }

func (s *smtpSender) Enabled() bool { return true }

// New returns an SMTP Sender, or a disabled Sender when Host is empty.
func New(cfg Config) Sender {
	if strings.TrimSpace(cfg.Host) == "" {
		return disabledSender{}
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if strings.TrimSpace(cfg.TLS) == "" {
		cfg.TLS = "starttls"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.From == "" {
		cfg.From = cfg.Username
	}
	return &smtpSender{cfg: cfg}
}

func (s *smtpSender) Send(ctx context.Context, to []string, subject, body string) error {
	rcpts := cleanAddrs(to)
	if len(rcpts) == 0 {
		return errors.New("mail: no recipients")
	}
	from := stripHdr(addrOnly(s.cfg.From))
	if from == "" {
		return errors.New("mail: no From address configured")
	}

	msg := buildMessage(s.cfg.From, s.cfg.FromName, rcpts, subject, body)
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := net.Dialer{Timeout: s.cfg.Timeout}

	var conn net.Conn
	var err error
	if strings.EqualFold(s.cfg.TLS, "tls") {
		conn, err = tls.DialWithDialer(&dialer, "tcp", addr,
			&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("mail: dial: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(s.cfg.Timeout))
	}

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("mail: client: %w", err)
	}
	defer c.Close()

	if strings.EqualFold(s.cfg.TLS, "starttls") {
		// Fail closed: if the operator chose starttls we never fall back to
		// cleartext (that is what TLS=none is for). This blocks a STARTTLS-stripping
		// downgrade that would otherwise leak the message body over plaintext.
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("mail: server does not advertise STARTTLS (set PARKRR_SMTP_TLS=none to allow cleartext)")
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("mail: auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("mail: MAIL FROM: %w", err)
	}
	for _, rc := range rcpts {
		if err := c.Rcpt(rc); err != nil {
			return fmt.Errorf("mail: RCPT %s: %w", rc, err)
		}
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		_ = wc.Close()
		return fmt.Errorf("mail: write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("mail: body close: %w", err)
	}
	return c.Quit()
}

// stripHdr removes CR/LF/NUL so a value placed in a message header cannot inject
// additional header lines (SMTP header injection, CWE-93). Applied to every
// header field (recipients, subject, sender) as an explicit sanitizer barrier —
// net/smtp's Rcpt/Mail and Q-encoding also guard this, this makes it defensive
// and unmistakable.
func stripHdr(s string) string {
	return strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(s)
}

// buildMessage assembles an RFC 5322 plain-text UTF-8 message with CRLF lines.
func buildMessage(from, fromName string, to []string, subject, body string) []byte {
	fromAddr := stripHdr(addrOnly(from))
	fromHeader := fromAddr
	if strings.TrimSpace(fromName) != "" {
		fromHeader = mime.QEncoding.Encode("utf-8", stripHdr(fromName)) + " <" + fromAddr + ">"
	}
	toClean := make([]string, 0, len(to))
	for _, a := range to {
		toClean = append(toClean, stripHdr(a))
	}
	var b strings.Builder
	b.WriteString("From: " + fromHeader + "\r\n")
	b.WriteString("To: " + strings.Join(toClean, ", ") + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", stripHdr(subject)) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	// Normalise to CRLF so bare-LF bodies don't confuse the SMTP data phase.
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	return []byte(b.String())
}

// addrOnly extracts the bare address from a "Name <a@b>" header value.
func addrOnly(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '<'); i >= 0 {
		if j := strings.IndexByte(s[i:], '>'); j > 0 {
			return strings.TrimSpace(s[i+1 : i+j])
		}
	}
	return s
}

func cleanAddrs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, a := range in {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		// Validate via RFC 5322 parsing: any header-injection attempt (CR/LF) or
		// malformed value is rejected outright, and the canonical address is used.
		// This blocks SMTP header injection and sanitizes the value for callers and
		// static analysis alike.
		if parsed, err := netmail.ParseAddress(a); err == nil {
			out = append(out, parsed.Address)
		}
	}
	return out
}

// Log ist die Schnittstelle, über die ein Sender seine Versuche festhält —
// abstrahiert, damit dieses Paket keine Datenbank kennt.
type Log func(recipients []string, subject string, ok bool, sendErr error)

type loggingSender struct {
	inner Sender
	log   Log
}

// WithLog umwickelt einen Sender so, dass JEDER Versuch (Erfolg wie Fehlschlag)
// protokolliert wird (Hundert 86). Der Fehler des inneren Senders wird
// unverändert durchgereicht — das Protokoll beobachtet, es verändert nichts.
func WithLog(s Sender, log Log) Sender {
	if log == nil {
		return s
	}
	return &loggingSender{inner: s, log: log}
}

func (l *loggingSender) Enabled() bool { return l.inner.Enabled() }
func (l *loggingSender) Send(ctx context.Context, to []string, subject, body string) error {
	err := l.inner.Send(ctx, to, subject, body)
	// Protokolliert werden die Adressen, die der Versender TATSÄCHLICH auf den
	// Umschlag schreibt — cleanAddrs verwirft unparsbare stillschweigend. Mit der
	// Rohliste behauptete das Protokoll eine Zustellung an eine Adresse, die nie
	// angesprochen wurde, und der Betreiber las in der Versandübersicht ein "ja"
	// auf die Frage, ob der Kunde die Mahnung bekommen hat.
	rcpts := cleanAddrs(to)
	l.log(rcpts, subject, err == nil, redactErr(err, append(rcpts, to...)))
	return err
}

// AddrPlaceholder ersetzt eine E-Mail-Adresse in protokollierten Fehlertexten.
const AddrPlaceholder = "[Adresse entfernt]"

// redactedError trägt den bereinigten Text für das Protokoll; Unwrap liefert den
// Originalfehler, damit errors.Is/As weiter greifen.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// redactErr entfernt Empfängeradressen aus einem Versandfehler, bevor er ins
// Versandprotokoll geht (PRT-03). Der SMTP-Fehlertext nennt die Adresse gleich
// doppelt — in "mail: RCPT <addr>" und meist noch einmal in der Antwort des Relays
// ("550 5.1.1 <addr>: Recipient address rejected"). mail_log unterliegt keiner
// Aufräumfrist; eine Adresse darin überlebt sonst jede Löschung der Person, weil
// die Anonymisierung nur die Empfängerspalte kennt. Der Statuscode bleibt stehen —
// er ist die eigentliche Diagnose. Über die bekannten Empfänger hinaus wird jedes
// adressförmige Wort ersetzt: ein Relay kann die Adresse umgeschrieben zurückmelden.
func redactErr(err error, addrs []string) error {
	if err == nil {
		return nil
	}
	msg := RedactAddrs(err.Error(), addrs)
	msg = addrLike.ReplaceAllString(msg, AddrPlaceholder)
	if msg == err.Error() {
		return err
	}
	return &redactedError{msg: msg, err: err}
}

// addrLike trifft ein adressförmiges Wort: Nicht-Trennzeichen, @, Nicht-Trennzeichen.
var addrLike = regexp.MustCompile(`[^\s<>"'(),;:\[\]]+@[^\s<>"'(),;:\[\]]+`)

// RedactAddrs ersetzt jedes Vorkommen der angegebenen Adressen in s durch
// AddrPlaceholder — ohne Rücksicht auf Groß-/Kleinschreibung (Domains sind
// es nicht, und Relays schreiben gern um) und NUR an Wortgrenzen einer Adresse:
// "an@x.at" darf in "susan@x.at" nicht treffen, sonst stünde dort eine fremde,
// halb verstümmelte Adresse.
func RedactAddrs(s string, addrs []string) string {
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" || !strings.Contains(a, "@") {
			continue
		}
		s = redactOne(s, a)
	}
	return s
}

func redactOne(s, addr string) string {
	lower, needle := strings.ToLower(s), strings.ToLower(addr)
	// ToLower kann in exotischen Fällen die Bytelänge ändern; dann sind die Indizes
	// nicht mehr übertragbar. Konservativ: den ganzen Text ersetzen, statt eine
	// Adresse stehen zu lassen.
	if len(lower) != len(s) {
		if strings.Contains(lower, needle) {
			return AddrPlaceholder
		}
		return s
	}
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(lower[i:], needle)
		if j < 0 {
			break
		}
		start, end := i+j, i+j+len(needle)
		if !addrBoundaryBefore(s, start) || !addrBoundaryAfter(s, end) {
			b.WriteString(s[i : start+1])
			i = start + 1
			continue
		}
		b.WriteString(s[i:start])
		b.WriteString(AddrPlaceholder)
		i = end
	}
	b.WriteString(s[i:])
	return b.String()
}

// addrBoundaryBefore/After melden, ob der Treffer an dieser Stelle endet. Ein
// Apostroph davor bzw. ein Punkt, Bindestrich oder Apostroph danach gehört nur dann
// zur Adresse, wenn dahinter (davor) ein weiteres Adresszeichen folgt — sonst ist
// es Satzzeichen: "… an a@x.at." oder 'a@x.at'.
func addrBoundaryBefore(s string, start int) bool {
	if start == 0 || !isAddrByte(s[start-1]) {
		return true
	}
	return s[start-1] == '\'' && (start == 1 || !isAddrByte(s[start-2]))
}

func addrBoundaryAfter(s string, end int) bool {
	if end >= len(s) || !isAddrByte(s[end]) {
		return true
	}
	switch s[end] {
	case '.', '-', '\'':
		return end+1 >= len(s) || !isAddrByte(s[end+1])
	}
	return false
}

// isAddrByte meldet Zeichen, die innerhalb einer Adresse stehen dürfen (RFC 5322
// atext plus Punkt und @) — grenzt eines davon an den Treffer, ist er nur ein Teil
// einer längeren, fremden Adresse. Bytes >= 0x80 zählen mit (internationalisierte
// Adressen).
func isAddrByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c >= 0x80:
		return true
	}
	return strings.IndexByte(".!#$%&'*+/=?^_`{|}~-@", c) >= 0
}
