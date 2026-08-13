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
	"net/smtp"
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
	from := addrOnly(s.cfg.From)
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
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("mail: starttls: %w", err)
			}
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

// buildMessage assembles an RFC 5322 plain-text UTF-8 message with CRLF lines.
func buildMessage(from, fromName string, to []string, subject, body string) []byte {
	fromHeader := addrOnly(from)
	if strings.TrimSpace(fromName) != "" {
		fromHeader = mime.QEncoding.Encode("utf-8", fromName) + " <" + addrOnly(from) + ">"
	}
	var b strings.Builder
	b.WriteString("From: " + fromHeader + "\r\n")
	b.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
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
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}
