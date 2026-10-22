package mail

import (
	"context"
	"encoding/base64"
	"errors"
	"mime"
	"strings"
	"time"
)

// Attachment ist eine Datei, die einer Mail beiliegt (Ausbau 19: die Rechnung als PDF).
type Attachment struct {
	Name        string
	ContentType string
	Data        []byte
}

// AttachmentSender verschickt eine Mail mit Anhängen. Ein eigenes Interface, damit
// Sender, die keine Anhänge brauchen (und alle Test-Attrappen), unverändert bleiben.
type AttachmentSender interface {
	SendWithAttachments(ctx context.Context, to []string, subject, body string, atts []Attachment) error
}

// ErrNoAttachments meldet einen Sender, der keine Anhänge kann.
var ErrNoAttachments = errors.New("mail: sender does not support attachments")

// SendAll schickt ohne Anhänge über Send, mit Anhängen über SendWithAttachments.
func SendAll(ctx context.Context, s Sender, to []string, subject, body string, atts []Attachment) error {
	if len(atts) == 0 {
		return s.Send(ctx, to, subject, body)
	}
	as, ok := s.(AttachmentSender)
	if !ok {
		return ErrNoAttachments
	}
	return as.SendWithAttachments(ctx, to, subject, body, atts)
}

func (disabledSender) SendWithAttachments(context.Context, []string, string, string, []Attachment) error {
	return ErrDisabled
}

func (s *smtpSender) SendWithAttachments(ctx context.Context, to []string, subject, body string, atts []Attachment) error {
	return s.deliver(ctx, to, func(rcpts []string) []byte {
		return buildMultipart(s.cfg.From, s.cfg.FromName, rcpts, subject, body, atts, time.Now())
	})
}

// SendWithAttachments protokolliert wie Send; der Anhang selbst kommt nicht ins Protokoll.
func (l *loggingSender) SendWithAttachments(ctx context.Context, to []string, subject, body string, atts []Attachment) error {
	err := SendAll(ctx, l.inner, to, subject, body, atts)
	rcpts := cleanAddrs(to)
	l.log(rcpts, subject, err == nil, redactErr(err, append(rcpts, to...)))
	return err
}

// buildMultipart baut eine multipart/mixed-Nachricht: Textteil wie buildMessage, jeder
// Anhang base64 in 76-Zeichen-Zeilen. Dateinamen werden wie Kopfzeilen bereinigt.
func buildMultipart(from, fromName string, to []string, subject, body string, atts []Attachment, now time.Time) []byte {
	boundary := "parkrr-" + strings.ReplaceAll(now.UTC().Format("20060102150405.000000000"), ".", "-")
	head := string(buildMessage(from, fromName, to, subject, ""))
	// buildMessage endet mit Content-Type text/plain + Leerzeile; den Kopf bis vor
	// MIME-Version übernehmen und den Rest neu setzen.
	cut := strings.Index(head, "MIME-Version:")
	var b strings.Builder
	b.WriteString(head[:cut])
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString(`Content-Type: multipart/mixed; boundary="` + boundary + "\"\r\n\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n"))
	b.WriteString("\r\n")
	for _, a := range atts {
		ct := stripHdr(a.ContentType)
		if ct == "" {
			ct = "application/octet-stream"
		}
		name := mime.QEncoding.Encode("utf-8", stripHdr(strings.ReplaceAll(a.Name, `"`, "")))
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + ct + `; name="` + name + "\"\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		b.WriteString(`Content-Disposition: attachment; filename="` + name + "\"\r\n\r\n")
		enc := base64.StdEncoding.EncodeToString(a.Data)
		for len(enc) > 76 {
			b.WriteString(enc[:76] + "\r\n")
			enc = enc[76:]
		}
		b.WriteString(enc + "\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String())
}
