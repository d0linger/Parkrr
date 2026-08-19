package handlers

import (
	"encoding/json"
	"strings"
	"testing"
)

// The audit trail must never carry a secret in clear text. These tests pin the
// policy at the choke point (redactChanges), which auditExec applies to every
// write, so a new handler cannot leak by forgetting a skip list.

func redacted(t *testing.T, changes any) map[string]any {
	t.Helper()
	b, err := json.Marshal(redactChanges(normalizeChanges(changes)))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func diffOf(field string, oldV, newV any) map[string]any {
	return map[string]any{field: map[string]any{"old": oldV, "new": newV}}
}

func TestRedactSecretsWholesale(t *testing.T) {
	secrets := []string{
		"password", "current_password", "new_password", "password_hash",
		"token", "csrf_token", "api_key", "apikey", "s3_secret_key",
		"totp_secret", "totp_code", "backup_code", "recovery_codes",
		"code", "salt", "access_key", "private_key", "credential",
	}
	for _, f := range secrets {
		got := redacted(t, diffOf(f, "hunter2", "correct horse battery staple"))
		inner := got[f].(map[string]any)
		if inner["old"] != redactedMark || inner["new"] != redactedMark {
			t.Errorf("%s not redacted: old=%v new=%v", f, inner["old"], inner["new"])
		}
	}
}

func TestRedactNeverLeaksTheSecretAnywhereInTheJSON(t *testing.T) {
	// Strongest form: the plaintext must not appear in the serialized row at all.
	const plain = "SuperSecret!42"
	b, err := json.Marshal(redactChanges(normalizeChanges(map[string]any{
		"password":    map[string]any{"old": "", "new": plain},
		"nested":      map[string]any{"s3_secret_key": plain},
		"list":        []any{map[string]any{"token": plain}},
		"seller_name": map[string]any{"old": "A", "new": "B"},
	})))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), plain) {
		t.Fatalf("secret leaked into audit changes: %s", b)
	}
}

func TestRedactBankIdentifiersArePartiallyMasked(t *testing.T) {
	const iban = "AT611904300234573201"
	got := redacted(t, diffOf("iban", iban, "AT611904300234571111"))
	inner := got["iban"].(map[string]any)
	oldS, newS := inner["old"].(string), inner["new"].(string)
	if strings.Contains(oldS, "1904300234") || strings.Contains(newS, "1904300234") {
		t.Fatalf("IBAN body not masked: old=%q new=%q", oldS, newS)
	}
	// The tail must survive so the change stays provable/attributable.
	if !strings.HasSuffix(oldS, "3201") || !strings.HasSuffix(newS, "1111") {
		t.Fatalf("IBAN tail lost, change no longer attributable: old=%q new=%q", oldS, newS)
	}
	// A too-short value cannot be masked meaningfully → redacted wholesale.
	short := redacted(t, diffOf("bic", "AB", "CD"))
	if short["bic"].(map[string]any)["old"] != redactedMark {
		t.Errorf("short BIC should be redacted wholesale, got %v", short["bic"])
	}
}

func TestRedactKeepsOrdinaryBusinessFields(t *testing.T) {
	// An audit trail that redacts everything is useless — normal fields must survive.
	got := redacted(t, map[string]any{
		"name":   map[string]any{"old": "Hermann 1", "new": "Hermann 2"},
		"amount": map[string]any{"old": 100, "new": 250},
		"status": map[string]any{"old": "stored", "new": "collected"},
	})
	if got["name"].(map[string]any)["new"] != "Hermann 2" {
		t.Errorf("name must stay readable, got %v", got["name"])
	}
	if got["amount"].(map[string]any)["new"].(float64) != 250 {
		t.Errorf("amount must stay readable, got %v", got["amount"])
	}
	if got["status"].(map[string]any)["old"] != "stored" {
		t.Errorf("status must stay readable, got %v", got["status"])
	}
}

func TestRedactNilStaysNil(t *testing.T) {
	// "was not set" must remain distinguishable from "was set and hidden".
	got := redacted(t, diffOf("password", nil, "x"))
	inner := got["password"].(map[string]any)
	if inner["old"] != nil {
		t.Errorf("nil old should stay nil, got %v", inner["old"])
	}
	if inner["new"] != redactedMark {
		t.Errorf("set new should be redacted, got %v", inner["new"])
	}
}

func TestRedactStructInput(t *testing.T) {
	// Call sites may pass a struct rather than a diff map; the walk must still apply.
	type payload struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		IBAN     string `json:"iban"`
	}
	got := redacted(t, payload{Name: "Max", Password: "s3cret", IBAN: "AT611904300234573201"})
	if got["password"] != redactedMark {
		t.Errorf("struct password not redacted: %v", got["password"])
	}
	if got["name"] != "Max" {
		t.Errorf("struct name should survive: %v", got["name"])
	}
	if s, _ := got["iban"].(string); strings.Contains(s, "1904300234") {
		t.Errorf("struct IBAN not masked: %v", got["iban"])
	}
}
