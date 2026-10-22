package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"strings"
	"testing"
	"time"
)

// Die Mail mit Anhang muss sich wie jede normale Mail lesen lassen: Kopf mit
// bereinigten Feldern, Textteil unverändert, Anhang byte-genau zurück.
func TestBuildMultipartRoundTrip(t *testing.T) {
	pdf := bytes.Repeat([]byte("%PDF-1.4 ÄÖÜ\x00\xff"), 40) // länger als eine base64-Zeile
	raw := buildMultipart("rechnung@example.com", "Hof Huber", []string{"kunde@example.com"},
		"Rechnung 2026-0013", "Guten Tag,\nanbei die Rechnung.\n",
		[]Attachment{{Name: "Rechnung 2026-0013.pdf", ContentType: "application/pdf", Data: pdf}},
		time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC))

	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("Content-Type = %q (%v)", msg.Header.Get("Content-Type"), err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	text, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(text)
	if !strings.Contains(string(body), "anbei die Rechnung.\r\n") {
		t.Errorf("text part = %q", body)
	}
	att, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if att.FileName() != "Rechnung 2026-0013.pdf" {
		t.Errorf("filename = %q", att.FileName())
	}
	got, _ := io.ReadAll(att) // multipart dekodiert base64 nicht selbst
	dec, err := decodeB64Lines(got)
	if err != nil || !bytes.Equal(dec, pdf) {
		t.Fatalf("attachment does not round-trip (err=%v, %d vs %d bytes)", err, len(dec), len(pdf))
	}
	for _, ln := range strings.Split(string(got), "\r\n") {
		if len(ln) > 76 {
			t.Fatalf("base64 line of %d characters", len(ln))
		}
	}
	if _, err := mr.NextPart(); err != io.EOF {
		t.Errorf("expected exactly two parts, got more (%v)", err)
	}
}

// Ein Dateiname mit Zeilenumbruch darf keine Kopfzeile einschleusen.
func TestBuildMultipartStripsHeaderInjection(t *testing.T) {
	raw := string(buildMultipart("a@example.com", "", []string{"b@example.com"}, "S", "x",
		[]Attachment{{Name: "a.pdf\r\nBcc: evil@example.com", Data: []byte("x")}}, time.Now()))
	if strings.Contains(raw, "\r\nBcc:") {
		t.Fatal("header injection through the attachment name")
	}
}

type plainOnly struct{ sent int }

func (p *plainOnly) Enabled() bool { return true }
func (p *plainOnly) Send(context.Context, []string, string, string) error {
	p.sent++
	return nil
}

func TestSendAll(t *testing.T) {
	p := &plainOnly{}
	if err := SendAll(context.Background(), p, []string{"a@example.com"}, "s", "b", nil); err != nil || p.sent != 1 {
		t.Fatalf("without attachments SendAll must use Send: err=%v sent=%d", err, p.sent)
	}
	err := SendAll(context.Background(), p, []string{"a@example.com"}, "s", "b", []Attachment{{Name: "x.pdf", Data: []byte("x")}})
	if !errors.Is(err, ErrNoAttachments) {
		t.Fatalf("err = %v, want ErrNoAttachments", err)
	}
	if err := SendAll(context.Background(), disabledSender{}, []string{"a@example.com"}, "s", "b", []Attachment{{Data: []byte("x")}}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	var logged []bool
	ls := WithLog(disabledSender{}, func(_ []string, _ string, ok bool, _ error) { logged = append(logged, ok) })
	_ = SendAll(context.Background(), ls, []string{"a@example.com"}, "s", "b", []Attachment{{Data: []byte("x")}})
	if len(logged) != 1 || logged[0] {
		t.Fatalf("logging sender must record the failed attachment send once: %v", logged)
	}
}

func decodeB64Lines(b []byte) ([]byte, error) {
	return base64Std.DecodeString(strings.NewReplacer("\r", "", "\n", "").Replace(string(b)))
}

var base64Std = base64.StdEncoding
