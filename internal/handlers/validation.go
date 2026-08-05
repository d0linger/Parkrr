package handlers

// Input length policy — the single source of truth for how much user-supplied
// text each endpoint accepts. Caps are enforced up front to protect against
// resource exhaustion (DoS) and database bloat, and to keep passwords within
// bcrypt's 72-byte limit (x/crypto rejects longer inputs outright).
//
// Counting is in BYTES (len), not code points: the policy is byte-denominated
// (bcrypt, storage, rate-limiter keys), and keeping every field on the same
// unit avoids a mixed len()/RuneCountInString foot-gun. Limits are generous
// enough that the extra strictness for multi-byte input (e.g. umlauts) is
// immaterial in practice.
//
// Over-length errors follow one convention: "<field> is too long" (English, to
// match the API-wide error strings). Callers emit it via writeError or by
// returning the message string, per each handler's existing style.
const (
	maxUsernameLen = 100
	maxNameLen     = 100
	maxEmailLen    = 255
	maxNoteLen     = 2000
	maxPhoneLen    = 50
	maxAddressLen  = 500
	maxTOTPCodeLen = 20
	minPasswordLen = 8
	maxPasswordLen = 72
	maxCronLen     = 100
	maxBackupKeyLen = 100
)

// validCronLength reports whether s is within the cron expression length cap.
func validCronLength(s string) bool {
	return len(s) <= maxCronLen
}

// validBackupKeyLength reports whether s is within the backup key length cap.
func validBackupKeyLength(s string) bool {
	return len(s) <= maxBackupKeyLen
}

// validUsernameLength reports whether s is a non-empty username within the
// length cap. Bounding it protects the rate limiter (which keys on the
// username) from memory pressure via over-long keys.
func validUsernameLength(s string) bool {
	return len(s) > 0 && len(s) <= maxUsernameLen
}

// validNameLength reports whether s is within the name/label length cap.
func validNameLength(s string) bool {
	return len(s) <= maxNameLen
}

// validEmailLength reports whether s is within the email length cap.
func validEmailLength(s string) bool {
	return len(s) <= maxEmailLen
}

// validNoteLength reports whether s is within the note length cap.
func validNoteLength(s string) bool {
	return len(s) <= maxNoteLen
}

// validPhoneLength reports whether s is within the phone length cap.
func validPhoneLength(s string) bool {
	return len(s) <= maxPhoneLen
}

// validAddressLength reports whether s is within the address length cap.
func validAddressLength(s string) bool {
	return len(s) <= maxAddressLen
}

// validTOTPCodeLength reports whether s is a non-empty TOTP code within the
// length cap.
func validTOTPCodeLength(s string) bool {
	return len(s) > 0 && len(s) <= maxTOTPCodeLen
}

// validPasswordLength reports whether pw satisfies the length policy (between
// minPasswordLen and maxPasswordLen bytes).
func validPasswordLength(pw string) bool {
	return len(pw) >= minPasswordLen && len(pw) <= maxPasswordLen
}
