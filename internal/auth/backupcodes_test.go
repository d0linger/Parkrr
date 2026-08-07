package auth

import (
	"regexp"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

var codeRe = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}$`)

func TestRandomCodeFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := randomCode()
		if err != nil {
			t.Fatalf("randomCode failed: %v", err)
		}
		if !codeRe.MatchString(c) {
			t.Fatalf("bad code format: %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 190 {
		t.Fatalf("codes not sufficiently random: %d unique of 200", len(seen))
	}
}

func TestHashCode(t *testing.T) {
	h, err := hashCode("ABCD-EFGH-IJKL")
	if err != nil {
		t.Fatalf("hashCode failed: %v", err)
	}
	// Matching codes compare true, regardless of case and surrounding space.
	if bcrypt.CompareHashAndPassword([]byte(h), []byte("ABCD-EFGH-IJKL")) != nil {
		t.Fatal("hash should compare true for the original code")
	}
	if bcrypt.CompareHashAndPassword([]byte(h), []byte(normalizeCode("  abcd-efgh-ijkl  "))) != nil {
		t.Fatal("hash should compare true for a case/space-normalized code")
	}
	// A wrong code compares false.
	if bcrypt.CompareHashAndPassword([]byte(h), []byte("ABCD-EFGH-IJKX")) == nil {
		t.Fatal("hash should compare false for a different code")
	}
}
