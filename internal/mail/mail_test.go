package mail

import (
	"context"
	"strings"
	"testing"
)

func TestDisabledSender(t *testing.T) {
	s := New(Config{}) // no host
	if s.Enabled() {
		t.Error("sender with empty host must be disabled")
	}
	if err := s.Send(context.Background(), []string{"a@b.c"}, "hi", "body"); err != ErrDisabled {
		t.Errorf("Send on disabled sender = %v, want ErrDisabled", err)
	}
}

func TestNewDefaults(t *testing.T) {
	s := New(Config{Host: "smtp.example.com"})
	if !s.Enabled() {
		t.Fatal("configured sender must be enabled")
	}
	ss, ok := s.(*smtpSender)
	if !ok {
		t.Fatalf("want *smtpSender, got %T", s)
	}
	if ss.cfg.Port != 587 || ss.cfg.TLS != "starttls" {
		t.Errorf("defaults not applied: port=%d tls=%q", ss.cfg.Port, ss.cfg.TLS)
	}
}

func TestAddrOnly(t *testing.T) {
	cases := map[string]string{
		"a@b.c":                    "a@b.c",
		"  a@b.c  ":                "a@b.c",
		"Parkrr <billing@park.rr>": "billing@park.rr",
		"<x@y.z>":                  "x@y.z",
	}
	for in, want := range cases {
		if got := addrOnly(in); got != want {
			t.Errorf("addrOnly(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildMessageHeaders(t *testing.T) {
	msg := string(buildMessage("billing@park.rr", "Parkrr Büro", []string{"kunde@example.com"},
		"Zahlungserinnerung – Rechnung 2026-0001", "Zeile 1\nZeile 2"))

	for _, want := range []string{
		"From: ", "billing@park.rr",
		"To: kunde@example.com",
		"Subject: ", // RFC2047-encoded, so don't match the raw umlaut text
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q\n---\n%s", want, msg)
		}
	}
	// Body must be CRLF-normalised.
	if !strings.Contains(msg, "Zeile 1\r\nZeile 2") {
		t.Error("body not CRLF-normalised")
	}
	// Header/body separator present.
	if !strings.Contains(msg, "\r\n\r\n") {
		t.Error("missing header/body separator")
	}
}
