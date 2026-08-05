package handlers

import (
	"strings"
	"testing"
)

// TestLengthPredicates pins each input-length cap at its boundary and documents
// that counting is by BYTE, not code point.
func TestLengthPredicates(t *testing.T) {
	rep := func(n int) string { return strings.Repeat("a", n) }

	cases := []struct {
		name string
		fn   func(string) bool
		in   string
		want bool
	}{
		// username: non-empty, 1..maxUsernameLen
		{"username empty", validUsernameLength, "", false},
		{"username 1", validUsernameLength, "a", true},
		{"username max", validUsernameLength, rep(maxUsernameLen), true},
		{"username max+1", validUsernameLength, rep(maxUsernameLen + 1), false},

		// name/label: 0..maxNameLen (empty allowed; required-ness checked elsewhere)
		{"name empty", validNameLength, "", true},
		{"name max", validNameLength, rep(maxNameLen), true},
		{"name max+1", validNameLength, rep(maxNameLen + 1), false},

		{"email max", validEmailLength, rep(maxEmailLen), true},
		{"email max+1", validEmailLength, rep(maxEmailLen + 1), false},

		{"note max", validNoteLength, rep(maxNoteLen), true},
		{"note max+1", validNoteLength, rep(maxNoteLen + 1), false},

		{"phone max", validPhoneLength, rep(maxPhoneLen), true},
		{"phone max+1", validPhoneLength, rep(maxPhoneLen + 1), false},

		{"address max", validAddressLength, rep(maxAddressLen), true},
		{"address max+1", validAddressLength, rep(maxAddressLen + 1), false},

		// TOTP code: non-empty, 1..maxTOTPCodeLen
		{"totp empty", validTOTPCodeLength, "", false},
		{"totp max", validTOTPCodeLength, rep(maxTOTPCodeLen), true},
		{"totp max+1", validTOTPCodeLength, rep(maxTOTPCodeLen + 1), false},

		// password: minPasswordLen..maxPasswordLen
		{"password min-1", validPasswordLength, rep(minPasswordLen - 1), false},
		{"password min", validPasswordLength, rep(minPasswordLen), true},
		{"password max", validPasswordLength, rep(maxPasswordLen), true},
		{"password max+1", validPasswordLength, rep(maxPasswordLen + 1), false},

		// cron length: 0..maxCronLen
		{"cron empty", validCronLength, "", true},
		{"cron short", validCronLength, "0 0 * * *", true},
		{"cron max", validCronLength, rep(maxCronLen), true},
		{"cron max+1", validCronLength, rep(maxCronLen + 1), false},

		// backup key length: 0..maxBackupKeyLen
		{"backup key empty", validBackupKeyLength, "", true},
		{"backup key short", validBackupKeyLength, "my-secret-key", true},
		{"backup key max", validBackupKeyLength, rep(maxBackupKeyLen), true},
		{"backup key max+1", validBackupKeyLength, rep(maxBackupKeyLen + 1), false},
	}
	for _, tc := range cases {
		if got := tc.fn(tc.in); got != tc.want {
			t.Errorf("%s: got %v, want %v (len=%d)", tc.name, got, tc.want, len(tc.in))
		}
	}

	// Counting is by byte, not code point: maxNameLen multi-byte runes exceed the
	// byte cap even though they are only maxNameLen characters. This is intentional
	// (bcrypt/storage/rate-limiter are byte-denominated).
	umlauts := strings.Repeat("ä", maxNameLen) // maxNameLen runes, 2*maxNameLen bytes
	if validNameLength(umlauts) {
		t.Errorf("expected %d-rune (%d-byte) umlaut string to exceed the byte cap", maxNameLen, len(umlauts))
	}
}
