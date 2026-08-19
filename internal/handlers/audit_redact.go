package handlers

import (
	"encoding/json"
	"strings"
)

// normalizeChanges converts whatever a call site passed (a diffFields map, a
// struct, a slice) into plain JSON values, so the redaction walk sees field names
// as map keys. Round-tripping through JSON also guarantees the redacted result
// marshals identically to the unredacted one would have.
func normalizeChanges(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

// Audit-log redaction (Data Compliance).
//
// Every value that reaches the audit_log `changes` column passes through
// redactChanges, which is called from auditExec — the single INSERT shared by the
// best-effort and transactional paths. Putting the filter at that choke point (and
// not at the ~90 call sites) means a new handler cannot leak a secret through
// `changes` by forgetting a skip list: the default for a matching field is redaction.
//
// SCOPE — this covers `changes` ONLY. The `summary` column is free text assembled
// by string concatenation at the call site, so there are no field names to key a
// policy on and no automatic masking is possible. Summaries are therefore a
// deliberate exception: they must be written to be safe by construction (name the
// object and the operation, never a credential), which is why e.g. the admin
// bootstrap logs a control-flow-selected mode literal rather than the password
// flag. That convention is enforced at build time by
// TestAuditSummariesCarryNoSecretIdentifiers in audit_coverage_test.go.
//
// Policy:
//   - Secrets (passwords, tokens, keys, TOTP/backup codes, hashes) are replaced
//     wholesale — the trail records THAT the field changed, never its value.
//   - Bank identifiers (IBAN/BIC) are PARTIALLY masked: enough tail remains to
//     prove which account a change refers to, without storing the identifier.
//   - Everything else is kept: an audit trail whose values are all redacted is
//     useless for its actual purpose (proving what a user changed).

const redactedMark = "***REDACTED***"

// secretFieldTokens are matched as SUBSTRINGS of the (lower-cased) field name, so
// `password`, `current_password`, `new_password_hash` and `s3_secret_key` all hit.
// Substring matching is deliberate: it fails safe for fields added later.
var secretFieldTokens = []string{
	"password", "passwort", "secret", "token", "apikey", "api_key",
	"privatekey", "private_key", "accesskey", "access_key", "credential",
	"hash", "salt", "totp", "otp", "backup_code", "recovery",
}

// bankFieldNames are masked partially rather than fully (see maskTail).
var bankFieldNames = map[string]bool{"iban": true, "bic": true}

// isSecretField reports whether a field name must be redacted wholesale.
func isSecretField(name string) bool {
	n := strings.ToLower(name)
	// "code" alone is ambiguous (period_key, status codes...), so only treat the
	// explicit auth codes as secret; the token list above covers totp_code etc.
	if n == "code" || n == "codes" {
		return true
	}
	for _, t := range secretFieldTokens {
		if strings.Contains(n, t) {
			return true
		}
	}
	return false
}

// maskTail keeps the last `keep` characters of a bank identifier and replaces the
// rest with dots, e.g. "AT611904300234573201" -> "…3201". A value too short to
// mask meaningfully is redacted wholesale.
func maskTail(s string, keep int) string {
	t := strings.TrimSpace(s)
	// Count and slice by RUNES, not bytes. An IBAN is ASCII in practice, but the value
	// is whatever the user typed: byte slicing can cut a multibyte character in half,
	// and the broken rune then reaches the trail as U+FFFD. Runes also make "keep the
	// last 4 characters" true rather than "the last 4 bytes".
	r := []rune(t)
	if len(r) <= keep {
		return redactedMark
	}
	return "…" + string(r[len(r)-keep:])
}

// redactValue applies the policy to one field value. Nested maps/slices are walked
// so a struct logged as a whole (not just a diff) is covered too.
func redactValue(field string, v any) any {
	if isSecretField(field) {
		if v == nil {
			return nil // nothing was set; keep the distinction
		}
		return redactedMark
	}
	if bankFieldNames[strings.ToLower(field)] {
		if s, ok := v.(string); ok {
			if strings.TrimSpace(s) == "" {
				return s
			}
			return maskTail(s, 4)
		}
		if v == nil {
			return nil
		}
		return redactedMark
	}
	switch t := v.(type) {
	case map[string]any:
		return redactMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = redactValue(field, e) // keep the parent's field name for nested entries
		}
		return out
	}
	return v
}

// redactMap walks a map, applying the policy per key. The {"old":…, "new":…}
// wrapper that diffFields produces is transparent: its keys are not field names,
// so the surrounding field name is carried down by redactChanges.
func redactMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = redactValue(k, v)
	}
	return out
}

// redactChanges is the entry point used by auditExec. It understands the shape
// diffFields emits — {"field": {"old": x, "new": y}} — and redacts by the OUTER
// key (the real field name), so both old and new are masked consistently. Any
// other shape falls back to a generic walk.
func redactChanges(changes any) any {
	if arr, ok := changes.([]any); ok { // a list of change records — walk each
		out := make([]any, len(arr))
		for i, e := range arr {
			out[i] = redactChanges(e)
		}
		return out
	}
	m, ok := changes.(map[string]any)
	if !ok {
		return changes // scalar — no field name to key a policy on
	}
	out := make(map[string]any, len(m))
	for field, v := range m {
		inner, isDiff := v.(map[string]any)
		if !isDiff {
			out[field] = redactValue(field, v)
			continue
		}
		// {"old":…, "new":…} — redact both sides under the OUTER field name.
		red := make(map[string]any, len(inner))
		for side, sv := range inner {
			if side == "old" || side == "new" {
				red[side] = redactValue(field, sv)
			} else {
				red[side] = redactValue(side, sv)
			}
		}
		out[field] = red
	}
	return out
}
